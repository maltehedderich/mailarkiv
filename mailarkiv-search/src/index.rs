//! Opening an index generation and writing documents to it. Each open index
//! has one writer thread, which owns its `IndexWriter`.

use std::fs::{self, DirBuilder, File};
use std::io::{self, Write as _};
use std::os::unix::fs::DirBuilderExt;
use std::path::{Path, PathBuf};
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::{Arc, Condvar, Mutex};
use std::thread;

use tantivy::directory::error::{LockError, OpenDirectoryError, OpenWriteError};
use tantivy::indexer::NoMergePolicy;
use tantivy::{
    Index, IndexReader, IndexSettings, IndexWriter, ReloadPolicy, TantivyDocument, TantivyError,
    Term,
};

use crate::analysis;
use crate::directory::RecordingDirectory;
use crate::faults::{self, Point};
use crate::maintain::{self, Merged};
use crate::protocol::{Address, Answer, Cause, Document, Done, ErrorCode, Failure, Outgoing};
use crate::schema::{self, Fields, SCHEMA_VERSION};

/// The writer's memory budget, shared by its indexing threads.
const WRITER_MEMORY: usize = 128_000_000;
const WRITER_THREADS: usize = 2;

/// Beside Tantivy's files. Tantivy only removes files it wrote, so it leaves
/// this one alone.
const SCHEMA_VERSION_FILE: &str = "schema_version";

/// An index the search process has open.
pub struct OpenIndex {
    pub reader: IndexReader,
    pub fields: Fields,
    jobs: Sender<Job>,
    searches: Searches,
}

/// Work for an index's writer thread, which owns its `IndexWriter`.
pub enum Job {
    Write {
        id: u64,
        documents: Vec<Document>,
        commit: bool,
    },
    /// Starts a maintenance run. `writer` is this thread's own queue, for the
    /// maintenance thread to send the run back once its merges end.
    Maintain { id: u64, writer: Sender<Job> },
    /// A run's merges ended. The writer collects garbage and answers.
    CollectGarbage(Merged),
    /// Stops the writer. Later jobs answer `unknown_generation`.
    Close { done: Sender<()> },
}

/// Counts the searches sent to an index that haven't answered yet, so
/// closing it can wait for them.
#[derive(Default)]
struct Searches {
    count: Mutex<usize>,
    idle: Condvar,
}

/// A search in progress on an index. Dropping it, after its answer is sent,
/// ends it.
pub struct Searching(Arc<OpenIndex>);

impl Searching {
    pub fn index(&self) -> &OpenIndex {
        &self.0
    }
}

impl Drop for Searching {
    fn drop(&mut self) {
        let searches = &self.0.searches;
        let mut count = searches.count.lock().unwrap();
        *count -= 1;
        if *count == 0 {
            searches.idle.notify_all();
        }
    }
}

impl OpenIndex {
    /// Queues documents for the writer thread, which answers on its own.
    pub fn write(&self, id: u64, documents: Vec<Document>, commit: bool) {
        // The writer thread only stops once every sender is gone.
        let _ = self.jobs.send(Job::Write {
            id,
            documents,
            commit,
        });
    }

    /// Queues a maintenance run for the writer thread, which answers once
    /// its merges and garbage collection are done.
    pub fn maintain(&self, id: u64) {
        let _ = self.jobs.send(Job::Maintain {
            id,
            writer: self.jobs.clone(),
        });
    }

    /// Counts a search as in progress until the returned value is dropped.
    pub fn start_search(self: &Arc<Self>) -> Searching {
        *self.searches.count.lock().unwrap() += 1;
        Searching(Arc::clone(self))
    }

    /// Waits for the searches in progress, then stops the writer. Indexing
    /// requests queued before this are answered first.
    pub fn close(&self) {
        let mut count = self.searches.count.lock().unwrap();
        while *count > 0 {
            count = self.searches.idle.wait(count).unwrap();
        }
        drop(count);
        let (done, closed) = mpsc::channel();
        if self.jobs.send(Job::Close { done }).is_ok() {
            let _ = closed.recv();
        }
    }
}

fn directory(generation: u64) -> PathBuf {
    // The working directory is the data directory.
    Path::new("index").join(generation.to_string())
}

/// Opens `index/<generation>/`, or creates it when create is true, and
/// starts its writer thread, which sends its answers to answers and its
/// maintenance runs to maintenance.
pub fn open(
    generation: u64,
    create: bool,
    answers: Sender<Outgoing>,
    maintenance: Sender<maintain::Run>,
) -> Result<OpenIndex, Failure> {
    if faults::at(Point::OnOpen) {
        return Err(Failure::code(ErrorCode::IndexDamaged));
    }
    let dir = directory(generation);
    let (index, directory) = if dir.join("meta.json").is_file() {
        open_existing(&dir)?
    } else if create {
        create_new(&dir)?
    } else {
        return Err(Failure::code(ErrorCode::IndexMissing));
    };
    analysis::register(&index);
    let fields = Fields::of(&index.schema());
    let writer = new_writer(&index)
        .map_err(|err| Failure::write_failed(cause_of(&err, Some(&directory))))?;
    let reader = index
        .reader_builder()
        .reload_policy(ReloadPolicy::Manual)
        .try_into()
        .map_err(|_| Failure::code(ErrorCode::IndexDamaged))?;

    let (writes, jobs) = mpsc::channel();
    let mut thread = Writer {
        index,
        directory,
        fields,
        reader: IndexReader::clone(&reader),
        writer: Some(writer),
        closed: false,
    };
    thread::Builder::new()
        .name(format!("writer-{generation}"))
        .spawn(move || thread.run(jobs, &answers, &maintenance))
        .map_err(|_| Failure::write_failed(Cause::Other))?;
    Ok(OpenIndex {
        reader,
        fields,
        jobs: writes,
        searches: Searches::default(),
    })
}

/// The sum of the sizes of the files in `index/<generation>/`.
pub fn size(generation: u64) -> Result<u64, Failure> {
    let unreadable = |err: io::Error| match err.kind() {
        io::ErrorKind::NotFound => Failure::code(ErrorCode::IndexMissing),
        _ => Failure::code(ErrorCode::IndexDamaged),
    };
    let mut bytes = 0;
    for entry in fs::read_dir(directory(generation)).map_err(unreadable)? {
        match entry.and_then(|entry| entry.metadata()) {
            Ok(metadata) if metadata.is_file() => bytes += metadata.len(),
            Ok(_) => {}
            // A merge or garbage collection can remove a file meanwhile.
            Err(err) if err.kind() == io::ErrorKind::NotFound => {}
            Err(err) => return Err(unreadable(err)),
        }
    }
    Ok(bytes)
}

/// Removes `index/<generation>/`. One that is already gone counts as removed.
pub fn remove(generation: u64) -> Result<(), Failure> {
    match fs::remove_dir_all(directory(generation)) {
        Err(err) if err.kind() != io::ErrorKind::NotFound => {
            Err(Failure::write_failed(io_cause(&err)))
        }
        _ => Ok(()),
    }
}

fn open_existing(dir: &Path) -> Result<(Index, RecordingDirectory), Failure> {
    let version = fs::read_to_string(dir.join(SCHEMA_VERSION_FILE))
        .ok()
        .and_then(|text| text.trim().parse::<u32>().ok())
        .ok_or(Failure::code(ErrorCode::IndexDamaged))?;
    if version != SCHEMA_VERSION {
        return Err(Failure::code(ErrorCode::SchemaMismatch));
    }
    let damaged = |_| Failure::code(ErrorCode::IndexDamaged);
    let directory = RecordingDirectory::open(dir).map_err(damaged)?;
    let index =
        Index::open(directory.clone()).map_err(|_| Failure::code(ErrorCode::IndexDamaged))?;
    if index.schema() != schema::schema() {
        return Err(Failure::code(ErrorCode::SchemaMismatch));
    }
    Ok((index, directory))
}

/// Creates the directory and the index in it. The schema version is written
/// before `meta.json`, so an index that exists always has it.
fn create_new(dir: &Path) -> Result<(Index, RecordingDirectory), Failure> {
    let failed = |err: &io::Error| Failure::write_failed(io_cause(err));
    match DirBuilder::new().mode(0o700).create(dir) {
        Err(err) if err.kind() != io::ErrorKind::AlreadyExists => return Err(failed(&err)),
        _ => {}
    }
    let mut file = File::create(dir.join(SCHEMA_VERSION_FILE)).map_err(|err| failed(&err))?;
    file.write_all(format!("{SCHEMA_VERSION}\n").as_bytes())
        .and_then(|()| file.sync_all())
        .map_err(|err| failed(&err))?;
    let directory = RecordingDirectory::open(dir)
        .map_err(|err| Failure::write_failed(cause_of(&err.into(), None)))?;
    let index = Index::create(
        directory.clone(),
        schema::schema(),
        IndexSettings::default(),
    )
    .map_err(|err| Failure::write_failed(cause_of(&err, Some(&directory))))?;
    Ok((index, directory))
}

fn new_writer(index: &Index) -> tantivy::Result<IndexWriter> {
    let writer = index.writer_with_num_threads(WRITER_THREADS, WRITER_MEMORY)?;
    // Merges run only when the server asks for maintenance, so their
    // failures come back as results.
    writer.set_merge_policy(Box::new(NoMergePolicy));
    Ok(writer)
}

struct Writer {
    index: Index,
    directory: RecordingDirectory,
    fields: Fields,
    reader: IndexReader,
    /// None after a failure the writer couldn't roll back from, or after a
    /// close. The next request makes a new one, unless the index is closed.
    writer: Option<IndexWriter>,
    closed: bool,
}

impl Writer {
    fn run(
        &mut self,
        jobs: Receiver<Job>,
        answers: &Sender<Outgoing>,
        maintenance: &Sender<maintain::Run>,
    ) {
        // The loop ends once the index and every maintenance run holding
        // this queue are gone.
        for job in jobs {
            let answer = match job {
                Job::Write { id, .. } | Job::Maintain { id, .. } if self.closed => {
                    Some(Answer::error(id, ErrorCode::UnknownGeneration))
                }
                Job::Write {
                    id,
                    documents,
                    commit,
                } => Some(match self.write(&documents, commit) {
                    Ok(()) => Answer::ok(id, Done::Indexed { committed: commit }),
                    Err(cause) => Answer::failed(id, Failure::write_failed(cause)),
                }),
                Job::Maintain { id, writer } => self.maintain(id, writer, maintenance),
                Job::CollectGarbage(merged) => Some(self.collect_garbage(merged)),
                Job::Close { done } => {
                    self.closed = true;
                    // Waiting for running merges means none writes into a
                    // directory that close then removes.
                    if let Some(writer) = self.writer.take() {
                        let _ = writer.wait_merging_threads();
                    }
                    let _ = done.send(());
                    None
                }
            };
            if let Some(answer) = answer
                && answers.send(answer.into()).is_err()
            {
                return;
            }
        }
    }

    /// The writer, made again after a failure took it.
    fn writer(&mut self) -> tantivy::Result<&mut IndexWriter> {
        if self.writer.is_none() {
            self.writer = Some(new_writer(&self.index)?);
        }
        Ok(self.writer.as_mut().expect("set above"))
    }

    fn write(&mut self, documents: &[Document], commit: bool) -> Result<(), Cause> {
        // A failure from an earlier request isn't this one's cause.
        self.directory.take_failure();
        if let Err(err) = self.writer() {
            return Err(cause_of(&err, Some(&self.directory)));
        }
        let writer = self.writer.as_mut().expect("made above");
        let result = add(writer, &self.fields, documents).and_then(|()| {
            if commit {
                writer.commit()?;
                // The answer comes after the reload, so the next search sees
                // the batch.
                self.reader.reload()?;
            }
            Ok(())
        });
        if result.is_ok() && commit && faults::at(Point::AfterCommit) {
            return Err(Cause::Other);
        }
        if let Err(err) = result {
            let cause = cause_of(&err, Some(&self.directory));
            // Uncommitted documents are discarded.
            if writer.rollback().is_err() {
                self.writer = None;
            }
            return Err(cause);
        }
        Ok(())
    }

    /// Starts the run's merges and hands them to the maintenance thread, or
    /// answers at once when the run can't start.
    fn maintain(
        &mut self,
        id: u64,
        queue: Sender<Job>,
        maintenance: &Sender<maintain::Run>,
    ) -> Option<Answer> {
        let failed = Some(maintain::answer(id, 1, 1));
        if faults::at(Point::DuringMerge) {
            return failed;
        }
        let index = self.index.clone();
        let Ok(writer) = self.writer() else {
            return failed;
        };
        let Ok(merges) = maintain::start(&index, writer) else {
            return failed;
        };
        let count = merges.len() as u64;
        match maintenance.send(maintain::Run::new(id, merges, queue)) {
            Ok(()) => None,
            Err(_) => Some(maintain::answer(id, count, count)),
        }
    }

    fn collect_garbage(&mut self, merged: Merged) -> Answer {
        let done = maintain::answer(merged.id, merged.merges, merged.failed);
        // A closed index's directory is removed or left as it is.
        if self.closed {
            return done;
        }
        let collected = self
            .writer()
            .and_then(|writer| writer.garbage_collect_files().wait());
        // A failed merge is the more specific answer. Either one counts as a
        // failed run.
        match collected {
            Err(_) if merged.failed == 0 => Answer::error(merged.id, ErrorCode::GcFailed),
            _ => done,
        }
    }
}

/// Adds documents in order, each after deleting its message's ID, so a batch
/// sent again replaces its entries instead of adding more.
fn add(writer: &IndexWriter, fields: &Fields, documents: &[Document]) -> tantivy::Result<()> {
    for document in documents {
        writer.delete_term(Term::from_field_u64(fields.id, document.id));
        writer.add_document(to_tantivy(fields, document))?;
    }
    Ok(())
}

fn to_tantivy(fields: &Fields, document: &Document) -> TantivyDocument {
    let mut out = TantivyDocument::default();
    out.add_u64(fields.id, document.id);
    out.add_i64(fields.list_date, document.list_date);
    out.add_i64(fields.first_archived_at, document.first_archived_at);
    out.add_u64(fields.has_attachments, u64::from(document.has_attachments));
    if let Some(subject) = &document.subject {
        out.add_text(fields.subject, subject);
        out.add_text(fields.all, subject);
    }
    for address in &document.from {
        add_address(
            &mut out,
            fields,
            address,
            [fields.from, fields.from_address, fields.from_domain],
        );
    }
    for address in &document.to {
        add_address(
            &mut out,
            fields,
            address,
            [fields.to, fields.to_address, fields.to_domain],
        );
    }
    for name in &document.attachment_names {
        out.add_text(fields.attachment, name);
        out.add_text(fields.all, name);
    }
    for text in &document.text {
        out.add_text(fields.all, text);
    }
    out
}

/// Adds one mailbox to its words, address, and domain fields, and to `all`.
fn add_address(
    out: &mut TantivyDocument,
    fields: &Fields,
    address: &Address,
    [words, whole, domain]: [tantivy::schema::Field; 3],
) {
    if let Some(name) = address.name.as_deref().filter(|name| !name.is_empty()) {
        out.add_text(words, name);
        out.add_text(fields.all, name);
    }
    if address.address.is_empty() {
        return;
    }
    out.add_text(words, &address.address);
    out.add_text(fields.all, &address.address);
    out.add_text(whole, &address.address);
    if let Some((_, after)) = address.address.rsplit_once('@')
        && !after.is_empty()
    {
        out.add_text(domain, after);
    }
}

/// The cause of a failed write, from the I/O error inside it or else from the
/// directory's latest failed write.
fn cause_of(err: &TantivyError, directory: Option<&RecordingDirectory>) -> Cause {
    let io = match err {
        TantivyError::IoError(err) => Some(err.as_ref()),
        TantivyError::OpenWriteError(OpenWriteError::IoError { io_error, .. }) => {
            Some(io_error.as_ref())
        }
        TantivyError::OpenDirectoryError(OpenDirectoryError::IoError { io_error, .. }) => {
            Some(io_error.as_ref())
        }
        TantivyError::LockFailure(LockError::IoError(err), _) => Some(err.as_ref()),
        _ => None,
    };
    io.map(|err| err.kind())
        .or_else(|| directory.and_then(RecordingDirectory::take_failure))
        .map_or(Cause::Other, kind_cause)
}

fn io_cause(err: &io::Error) -> Cause {
    kind_cause(err.kind())
}

fn kind_cause(kind: io::ErrorKind) -> Cause {
    match kind {
        io::ErrorKind::StorageFull | io::ErrorKind::QuotaExceeded => Cause::NoSpace,
        io::ErrorKind::PermissionDenied | io::ErrorKind::ReadOnlyFilesystem => {
            Cause::PermissionDenied
        }
        _ => Cause::Other,
    }
}

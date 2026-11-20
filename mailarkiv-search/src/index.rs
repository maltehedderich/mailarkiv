//! Opening an index generation and writing documents to it. Each open index
//! has one writer thread, which owns its `IndexWriter`.

use std::fs::{self, DirBuilder, File};
use std::io::{self, Write as _};
use std::os::unix::fs::DirBuilderExt;
use std::path::{Path, PathBuf};
use std::sync::mpsc::{self, Receiver, Sender};
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
use crate::protocol::{Address, Answer, Cause, Document, Done, ErrorCode, Failure};
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
    writes: Sender<Write>,
}

/// One `index` request, for the writer thread.
struct Write {
    id: u64,
    documents: Vec<Document>,
    commit: bool,
}

impl OpenIndex {
    /// Queues documents for the writer thread, which answers on its own.
    pub fn write(&self, id: u64, documents: Vec<Document>, commit: bool) {
        // The writer thread only stops when the process does.
        let _ = self.writes.send(Write {
            id,
            documents,
            commit,
        });
    }
}

fn directory(generation: u64) -> PathBuf {
    // The working directory is the data directory.
    Path::new("index").join(generation.to_string())
}

/// Opens `index/<generation>/`, or creates it when create is true, and
/// starts its writer thread, which sends its answers to answers.
pub fn open(generation: u64, create: bool, answers: Sender<Answer>) -> Result<OpenIndex, Failure> {
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
    };
    thread::Builder::new()
        .name(format!("writer-{generation}"))
        .spawn(move || thread.run(jobs, answers))
        .map_err(|_| Failure::write_failed(Cause::Other))?;
    Ok(OpenIndex {
        reader,
        fields,
        writes,
    })
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
    /// None after a failure the writer couldn't roll back from. The next
    /// request makes a new one.
    writer: Option<IndexWriter>,
}

impl Writer {
    fn run(&mut self, jobs: Receiver<Write>, answers: Sender<Answer>) {
        for job in jobs {
            let answer = match self.write(&job.documents, job.commit) {
                Ok(()) => Answer::ok(
                    job.id,
                    Done::Indexed {
                        committed: job.commit,
                    },
                ),
                Err(cause) => Answer::failed(job.id, Failure::write_failed(cause)),
            };
            if answers.send(answer).is_err() {
                return;
            }
        }
    }

    fn write(&mut self, documents: &[Document], commit: bool) -> Result<(), Cause> {
        // A failure from an earlier request isn't this one's cause.
        self.directory.take_failure();
        if self.writer.is_none() {
            let writer =
                new_writer(&self.index).map_err(|err| cause_of(&err, Some(&self.directory)))?;
            self.writer = Some(writer);
        }
        let writer = self.writer.as_mut().expect("set above");
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

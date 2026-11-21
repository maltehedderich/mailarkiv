//! `mailarkiv-search` holds MailArkiv's search indexes. Only the server starts
//! it, and the two talk over its standard input and output in frames.
//!
//! One thread reads frames and dispatches them, one writes answers in any
//! order, two run searches, one opens, closes, and measures indexes, one
//! waits for maintenance merges, and one writer thread runs per open index.

mod analysis;
mod collector;
mod directory;
mod faults;
mod frame;
mod index;
mod maintain;
mod protocol;
mod query;
mod schema;

use std::collections::HashMap;
use std::io::{self, BufWriter};
use std::process::ExitCode;
use std::sync::mpsc::{self, Receiver, Sender};
use std::sync::{Arc, Mutex, RwLock};
use std::thread;
use std::time::{Duration, Instant};

use index::{OpenIndex, Searching};
use protocol::{Answer, Done, ErrorCode, Hello, PROTOCOL, Request, Search};

// musl's own allocator built the index 2.4 times slower.
#[global_allocator]
static GLOBAL: mimalloc::MiMalloc = mimalloc::MiMalloc;

const SEARCH_THREADS: usize = 2;

/// Exit status for a frame that broke the protocol's rules.
const BROKEN_FRAME: u8 = 3;

type Indexes = Arc<RwLock<HashMap<u64, Arc<OpenIndex>>>>;

fn main() -> ExitCode {
    if std::env::args_os().len() > 1 {
        eprintln!("usage: mailarkiv-search (started by the MailArkiv server, takes no arguments)");
        return ExitCode::from(2);
    }
    if faults::init().is_err() {
        eprintln!("mailarkiv-search: MAILARKIV_SEARCH_FAULTS isn't a list of point=action");
        return ExitCode::from(2);
    }

    let (answers, outgoing) = mpsc::channel();
    thread::spawn(move || write_answers(outgoing));

    let indexes = Indexes::default();
    let (searches, search_jobs) = mpsc::channel();
    let search_jobs = Arc::new(Mutex::new(search_jobs));
    for _ in 0..SEARCH_THREADS {
        let jobs = Arc::clone(&search_jobs);
        let answers = answers.clone();
        thread::spawn(move || run_searches(&jobs, &answers));
    }
    let (maintenance, runs) = mpsc::channel();
    {
        let answers = answers.clone();
        thread::spawn(move || maintain::run(runs, &answers));
    }
    let (control, control_jobs) = mpsc::channel();
    {
        let indexes = Arc::clone(&indexes);
        let answers = answers.clone();
        thread::spawn(move || run_control(control_jobs, &indexes, &answers, &maintenance));
    }

    let mut input = io::stdin().lock();
    loop {
        let request: Request = match frame::read(&mut input) {
            Ok(request) => request,
            // Standard input closes when the server is gone or stopping.
            // Committed batches are safe on disk, and the server sends again
            // whatever it didn't see answered.
            Err(frame::ReadError::Closed) => return ExitCode::SUCCESS,
            Err(frame::ReadError::Broken) => return ExitCode::from(BROKEN_FRAME),
        };
        if !request.is_valid() {
            return ExitCode::from(BROKEN_FRAME);
        }
        let id = request.id();
        let open_index = |generation| indexes.read().unwrap().get(&generation).cloned();
        let unknown = || Answer::error(id, ErrorCode::UnknownGeneration);
        match request {
            Request::Open {
                generation, create, ..
            } => {
                let _ = control.send(Control::Open {
                    id,
                    generation,
                    create,
                });
            }
            Request::Close {
                generation, delete, ..
            } => {
                let _ = control.send(Control::Close {
                    id,
                    generation,
                    delete,
                });
            }
            Request::Size { generation, .. } => {
                let _ = control.send(Control::Size { id, generation });
            }
            Request::Maintain { generation, .. } => match open_index(generation) {
                Some(index) => index.maintain(id),
                None => {
                    let _ = answers.send(unknown());
                }
            },
            Request::Index {
                generation,
                documents,
                commit,
                ..
            } => match open_index(generation) {
                Some(index) => index.write(id, documents, commit),
                None => {
                    let _ = answers.send(unknown());
                }
            },
            Request::Search(search) => match open_index(search.generation) {
                Some(index) => {
                    // Time waiting for a search thread counts.
                    let deadline = Instant::now() + Duration::from_millis(search.deadline_ms);
                    let _ = searches.send(SearchJob {
                        search,
                        index: index.start_search(),
                        deadline,
                    });
                }
                None => {
                    let _ = answers.send(unknown());
                }
            },
        }
    }
}

/// Sends the hello, then every answer as it comes.
fn write_answers(answers: Receiver<Answer>) {
    let mut output = BufWriter::new(io::stdout().lock());
    let hello = Hello {
        kind: "hello",
        protocol: PROTOCOL,
        tantivy: tantivy_version(),
        schema_version: schema::SCHEMA_VERSION,
    };
    if frame::write(&mut output, &hello).is_err() {
        return;
    }
    for answer in answers {
        // A failed write means the server is gone, and standard input
        // closes next.
        if frame::write(&mut output, &answer).is_err() {
            return;
        }
    }
}

/// The version of the Tantivy built in, from "tantivy v0.27.0, index_format v7".
fn tantivy_version() -> String {
    let full = tantivy::version_string();
    full.strip_prefix("tantivy v")
        .and_then(|rest| rest.split(',').next())
        .unwrap_or(full)
        .to_string()
}

struct SearchJob {
    search: Search,
    /// Counts as in progress until the job is dropped, after its answer.
    index: Searching,
    deadline: Instant,
}

fn run_searches(jobs: &Mutex<Receiver<SearchJob>>, answers: &Sender<Answer>) {
    loop {
        let job = jobs.lock().unwrap().recv();
        let Ok(job) = job else { return };
        let id = job.search.id;
        let answer = match query::search(job.index.index(), &job.search, job.deadline) {
            Ok(done) => Answer::ok(id, done),
            Err(error) => Answer::error(id, error),
        };
        if answers.send(answer).is_err() {
            return;
        }
    }
}

enum Control {
    Open {
        id: u64,
        generation: u64,
        create: bool,
    },
    Close {
        id: u64,
        generation: u64,
        delete: bool,
    },
    Size {
        id: u64,
        generation: u64,
    },
}

/// Opens, closes, and measures indexes, one request at a time, so a close
/// can't pass an open of the same index.
fn run_control(
    jobs: Receiver<Control>,
    indexes: &Indexes,
    answers: &Sender<Answer>,
    maintenance: &Sender<maintain::Run>,
) {
    for job in jobs {
        let answer = match job {
            Control::Open {
                id,
                generation,
                create,
            } => {
                if indexes.read().unwrap().contains_key(&generation) {
                    Answer::ok(id, Done::Empty {})
                } else {
                    match index::open(generation, create, answers.clone(), maintenance.clone()) {
                        Ok(index) => {
                            indexes.write().unwrap().insert(generation, Arc::new(index));
                            Answer::ok(id, Done::Empty {})
                        }
                        Err(failure) => Answer::failed(id, failure),
                    }
                }
            }
            Control::Close {
                id,
                generation,
                delete,
            } => {
                // Requests that arrive from now on find no index.
                let open = indexes.write().unwrap().remove(&generation);
                if let Some(index) = open {
                    index.close();
                }
                match delete.then(|| index::remove(generation)) {
                    Some(Err(failure)) => Answer::failed(id, failure),
                    _ => Answer::ok(id, Done::Empty {}),
                }
            }
            Control::Size { id, generation } => match index::size(generation) {
                Ok(bytes) => Answer::ok(id, Done::Sized { bytes }),
                Err(failure) => Answer::failed(id, failure),
            },
        };
        if answers.send(answer).is_err() {
            return;
        }
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn tantivy_version_reads_the_version_string() {
        assert_eq!(super::tantivy_version(), "0.27.0");
    }
}

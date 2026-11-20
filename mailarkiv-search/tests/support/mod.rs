//! Runs the real `mailarkiv-search` program in a temporary data directory and
//! talks to it in frames, as the server does.

// Each test file uses a different part of this module.
#![allow(dead_code)]

use std::fs;
use std::io::{self, BufReader, Read, Write};
use std::path::{Path, PathBuf};
use std::process::{Child, ChildStdin, Command, ExitStatus, Stdio};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::mpsc::{self, Receiver, RecvTimeoutError};
use std::thread;
use std::time::{Duration, Instant};

use serde_json::{Value, json};

pub const PROGRAM: &str = env!("CARGO_BIN_EXE_mailarkiv-search");

/// How long a test waits for any one frame or exit.
pub const WAIT: Duration = Duration::from_secs(10);

/// A data directory under the system's temporary directory, removed on drop.
pub struct DataDir {
    path: PathBuf,
}

impl DataDir {
    /// Makes a data directory holding an empty `index/`, as the server does
    /// before it starts the search process.
    pub fn new() -> DataDir {
        static NEXT: AtomicU64 = AtomicU64::new(0);
        let path = std::env::temp_dir().join(format!(
            "mailarkiv-search-test-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        fs::create_dir_all(path.join("index")).unwrap();
        DataDir { path }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn generation(&self, generation: u64) -> PathBuf {
        self.path.join("index").join(generation.to_string())
    }
}

impl Drop for DataDir {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.path);
    }
}

/// What the reading thread got from the program's standard output.
enum Output {
    Frame(Value),
    End,
    Broken(String),
}

/// One running search process.
pub struct Search {
    child: Child,
    stdin: Option<ChildStdin>,
    frames: Receiver<Output>,
    next_id: u64,
}

impl Search {
    /// Starts the program in dir, with no arguments and an empty environment,
    /// and checks its hello.
    pub fn start(dir: &DataDir) -> Search {
        let mut search = Search::start_without_hello(dir);
        let hello = search.frame();
        assert_eq!(hello["type"], "hello", "first frame {hello}");
        search
    }

    /// Starts the program with MAILARKIV_SEARCH_FAULTS set to faults, and
    /// checks its hello.
    pub fn start_with_faults(dir: &DataDir, faults: &str) -> Search {
        let mut search = Search::spawn(dir, Some(faults));
        let hello = search.frame();
        assert_eq!(hello["type"], "hello", "first frame {hello}");
        search
    }

    pub fn start_without_hello(dir: &DataDir) -> Search {
        Search::spawn(dir, None)
    }

    fn spawn(dir: &DataDir, faults: Option<&str>) -> Search {
        let mut command = Command::new(PROGRAM);
        command.env_clear();
        if let Some(faults) = faults {
            command.env("MAILARKIV_SEARCH_FAULTS", faults);
        }
        let mut child = command
            .current_dir(dir.path())
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        let stdout = BufReader::new(child.stdout.take().unwrap());
        let (sender, frames) = mpsc::channel();
        thread::spawn(move || read_frames(stdout, sender));
        Search {
            stdin: child.stdin.take(),
            child,
            frames,
            next_id: 1,
        }
    }

    /// The next frame, within WAIT.
    pub fn frame(&mut self) -> Value {
        match self.frames.recv_timeout(WAIT) {
            Ok(Output::Frame(value)) => value,
            Ok(Output::End) => panic!("the search process closed its output"),
            Ok(Output::Broken(why)) => panic!("broken frame: {why}"),
            Err(RecvTimeoutError::Timeout) => panic!("no frame within {WAIT:?}"),
            Err(RecvTimeoutError::Disconnected) => panic!("the reader stopped"),
        }
    }

    /// Writes raw bytes to standard input. A write can fail once the program
    /// has exited, which some tests expect.
    pub fn send_raw(&mut self, bytes: &[u8]) -> io::Result<()> {
        let stdin = self.stdin.as_mut().unwrap();
        stdin.write_all(bytes)?;
        stdin.flush()
    }

    pub fn send(&mut self, request: &Value) {
        self.send_raw(&frame(request)).unwrap();
    }

    /// Sends request with the next id and returns the answer with that id.
    pub fn call(&mut self, mut request: Value) -> Value {
        let id = self.next_id;
        self.next_id += 1;
        request["id"] = json!(id);
        self.send(&request);
        let answer = self.frame();
        assert_eq!(
            answer["id"],
            json!(id),
            "answer {answer} to request {request}"
        );
        answer
    }

    pub fn close_stdin(&mut self) {
        self.stdin = None;
    }

    /// Waits up to WAIT for the program to exit.
    pub fn wait(&mut self) -> ExitStatus {
        let deadline = Instant::now() + WAIT;
        loop {
            if let Some(status) = self.child.try_wait().unwrap() {
                return status;
            }
            assert!(Instant::now() < deadline, "still running after {WAIT:?}");
            thread::sleep(Duration::from_millis(10));
        }
    }

    /// Closes standard input and checks for a clean exit.
    pub fn stop(mut self) {
        self.close_stdin();
        assert_eq!(self.wait().code(), Some(0));
    }

    pub fn open(&mut self, generation: u64, create: bool) -> Value {
        self.call(json!({"op": "open", "generation": generation, "create": create}))
    }

    pub fn index(&mut self, generation: u64, documents: Vec<Value>, commit: bool) -> Value {
        self.call(json!({"op": "index", "generation": generation, "documents": documents, "commit": commit}))
    }

    /// Searches generation for words and returns the answer.
    pub fn search(&mut self, generation: u64, words: &str, offset: u64, limit: u64) -> Value {
        self.query(
            generation,
            json!({"words": words, "offset": offset, "limit": limit}),
        )
    }

    /// Sends a search on generation with every field empty, its first page of
    /// 50, and a 10 s deadline, except for the fields in changes.
    pub fn query(&mut self, generation: u64, changes: Value) -> Value {
        let mut request = search_request(0, generation, "");
        for (field, value) in changes.as_object().expect("changes is an object") {
            request[field] = value.clone();
        }
        self.call(request)
    }

    /// The IDs a search for words finds on its first page of 50.
    pub fn ids(&mut self, generation: u64, words: &str) -> Vec<u64> {
        self.found(generation, json!({"words": words}))
    }

    /// The IDs a search with changes finds, as query sends it.
    pub fn found(&mut self, generation: u64, changes: Value) -> Vec<u64> {
        let answer = self.query(generation, changes.clone());
        answer["ok"]["ids"]
            .as_array()
            .unwrap_or_else(|| panic!("search {changes} answered {answer}"))
            .iter()
            .map(|id| id.as_u64().unwrap())
            .collect()
    }
}

impl Drop for Search {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

/// Starts the program with args and returns how it exited.
pub fn run_with_args(args: &[&str]) -> ExitStatus {
    let mut command = Command::new(PROGRAM);
    command.args(args).env_clear();
    run(command)
}

/// Starts the program with MAILARKIV_SEARCH_FAULTS set to faults and returns
/// how it exited.
pub fn run_with_faults(faults: &str) -> ExitStatus {
    let mut command = Command::new(PROGRAM);
    command.env_clear().env("MAILARKIV_SEARCH_FAULTS", faults);
    run(command)
}

/// Runs command in a new data directory, with standard input open, until it
/// exits.
fn run(mut command: Command) -> ExitStatus {
    let dir = DataDir::new();
    let mut child = command
        .current_dir(dir.path())
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + WAIT;
    loop {
        if let Some(status) = child.try_wait().unwrap() {
            return status;
        }
        if Instant::now() > deadline {
            let _ = child.kill();
            panic!("still running after {WAIT:?}");
        }
        thread::sleep(Duration::from_millis(10));
    }
}

/// A complete search request for words, with every filter empty, the first
/// page of 50, and a 10 s deadline.
pub fn search_request(id: u64, generation: u64, words: &str) -> Value {
    json!({
        "id": id,
        "op": "search",
        "generation": generation,
        "words": words,
        "from": "",
        "to": "",
        "subject": "",
        "attachment": "",
        "list_date_from": null,
        "list_date_before": null,
        "has_attachments": false,
        "offset": 0,
        "limit": 50,
        "deadline_ms": 10000,
    })
}

/// Encodes value as a frame: a 4-byte big-endian length, then the JSON.
pub fn frame(value: &Value) -> Vec<u8> {
    let body = serde_json::to_vec(value).unwrap();
    let mut out = (body.len() as u32).to_be_bytes().to_vec();
    out.extend(body);
    out
}

fn read_frames(mut stdout: impl Read, sender: mpsc::Sender<Output>) {
    loop {
        let mut length = [0u8; 4];
        if stdout.read_exact(&mut length).is_err() {
            let _ = sender.send(Output::End);
            return;
        }
        let mut body = vec![0u8; u32::from_be_bytes(length) as usize];
        let read = match stdout.read_exact(&mut body) {
            Ok(()) => match serde_json::from_slice(&body) {
                Ok(value) => Output::Frame(value),
                Err(err) => Output::Broken(err.to_string()),
            },
            Err(err) => Output::Broken(err.to_string()),
        };
        if sender.send(read).is_err() {
            return;
        }
    }
}

/// A complete index document with a few words in its subject.
pub fn document(id: u64, subject: &str) -> Value {
    json!({
        "id": id,
        "list_date": 1_793_603_695_000i64,
        "first_archived_at": 1_794_100_000_000i64,
        "has_attachments": false,
        "subject": subject,
        "from": [],
        "to": [],
        "attachment_names": [],
        "text": [],
    })
}

/// A document whose subject is "Gemeinsam", from one address to others.
pub fn addressed(id: u64, from: &str, to: &[&str]) -> Value {
    let mut document = document(id, "Gemeinsam");
    document["from"] = json!([{ "name": null, "address": from }]);
    document["to"] = to
        .iter()
        .map(|address| json!({ "address": address }))
        .collect();
    document
}

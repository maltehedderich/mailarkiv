//! What the program writes besides its answers: log frames, and one line on
//! standard error when it panics. None of it may hold text from mail or from
//! a search, whatever the operation and whatever fault fires.
#![cfg(feature = "faults")]

mod support;

use std::os::unix::process::ExitStatusExt;
use std::process::ExitStatus;

use serde_json::{Value, json};
use support::{DataDir, Search, document, search_request};

/// The signal `std::process::abort` raises on Linux.
const SIGABRT: i32 = 6;

/// Every marker holds this, so a marker found anywhere shows a leak.
const MARK: &str = "zq7mark";

/// Tantivy drops a token longer than this and logs a warning.
const MAX_TOKEN_LEN: usize = 65_530;

/// A document with a marker in every field. Its sender's address is one
/// token too long for Tantivy, which makes Tantivy warn.
fn marked_document(id: u64) -> Value {
    let long_address = format!("zq7markfrom{}@example.test", "a".repeat(MAX_TOKEN_LEN));
    let mut marked = document(id, "Zq7marksubject Betreff");
    marked["from"] = json!([{ "name": "Zq7markfromname", "address": long_address }]);
    marked["to"] = json!([{ "name": "Zq7marktoname", "address": "zq7markto@example.test" }]);
    marked["attachment_names"] = json!(["zq7markattachment.pdf"]);
    marked["text"] = json!(["Zq7marktext und mehr"]);
    marked
}

/// A search with a marker in every field.
fn marked_search(generation: u64) -> Value {
    let mut request = search_request(0, generation, "zq7markwords");
    request["from"] = json!("zq7markfromsearch@example.test");
    request["to"] = json!("zq7marktosearch.example.test");
    request["subject"] = json!("zq7marksubjectsearch");
    request["attachment"] = json!("zq7markattachmentsearch");
    request
}

/// What one run wrote, once the program ended.
struct Run {
    frames: Vec<Value>,
    logs: Vec<Value>,
    stderr: String,
    status: ExitStatus,
}

/// Runs every operation in turn with the fault list, until the last one or
/// until a fault ends the program, then ends it and collects its output.
fn run_every_operation(faults: &str) -> Run {
    let dir = DataDir::new();
    let mut search = Search::start_with_faults(&dir, faults);
    let operations = [
        json!({"op": "open", "generation": 1, "create": true}),
        json!({"op": "index", "generation": 1, "documents": [marked_document(1)], "commit": true}),
        json!({"op": "index", "generation": 1, "documents": [marked_document(2)], "commit": true}),
        marked_search(1),
        json!({"op": "maintain", "generation": 1}),
        json!({"op": "size", "generation": 1}),
        json!({"op": "close", "generation": 1, "delete": false}),
        json!({"op": "open", "generation": 1, "create": false}),
        json!({"op": "close", "generation": 1, "delete": true}),
    ];
    for operation in operations {
        if search.try_call(operation).is_none() {
            break;
        }
    }
    search.close_stdin();
    let status = search.wait();
    // Log frames that came after the last answer.
    while let Some(frame) = search.next_frame() {
        if frame["type"] == "log" {
            search.logs.push(frame);
        }
    }
    Run {
        frames: search.seen.clone(),
        logs: search.logs.clone(),
        stderr: search.stderr(),
        status,
    }
}

/// Whether line is exactly the panic line the server reads:
/// `^mailarkiv-search panic at [A-Za-z0-9_/.-]+:[0-9]+$`.
fn is_panic_line(line: &str) -> bool {
    let Some(location) = line.strip_prefix("mailarkiv-search panic at ") else {
        return false;
    };
    let Some((file, number)) = location.rsplit_once(':') else {
        return false;
    };
    !file.is_empty()
        && file
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || "_/.-".contains(c))
        && !number.is_empty()
        && number.chars().all(|c| c.is_ascii_digit())
}

#[test]
fn reports_no_text() {
    let mut fault_lists = vec![String::new()];
    for point in ["after-commit", "during-search", "during-merge", "on-open"] {
        for action in ["fail", "exit", "panic"] {
            fault_lists.push(format!("{point}={action}"));
        }
    }
    for faults in fault_lists {
        let run = run_every_operation(&faults);
        for frame in &run.frames {
            assert!(
                !frame.to_string().to_lowercase().contains(MARK),
                "with faults {faults:?} a frame holds a marker: {frame}"
            );
        }
        assert!(
            !run.stderr.to_lowercase().contains(MARK),
            "with faults {faults:?} standard error holds a marker: {:?}",
            run.stderr
        );
        if faults.ends_with("=panic") {
            let lines: Vec<&str> = run.stderr.lines().collect();
            assert!(
                lines.len() == 1 && is_panic_line(lines[0]),
                "with faults {faults:?} standard error is {:?}, want one panic line",
                run.stderr
            );
            assert_eq!(run.status.signal(), Some(SIGABRT), "with faults {faults:?}");
        } else {
            assert_eq!(run.stderr, "", "with faults {faults:?}");
        }
        // Each frame is an answer or a log frame with only its three fields.
        for log in &run.logs {
            let keys: Vec<&String> = log.as_object().unwrap().keys().collect();
            assert_eq!(keys.len(), 3, "with faults {faults:?} log frame {log}");
            assert!(
                log["level"] == "warn" || log["level"] == "error",
                "with faults {faults:?} log frame {log}"
            );
        }
    }
}

#[test]
fn a_panic_writes_only_its_location_and_aborts() {
    let dir = DataDir::new();
    let mut search = Search::start_with_faults(&dir, "during-search=panic");
    search.open(1, true);
    search.index(1, vec![document(1, "Apfel")], true);
    search.send(&search_request(100, 1, "apfel"));
    let status = search.wait();
    assert_eq!(status.signal(), Some(SIGABRT));
    let stderr = search.stderr();
    let lines: Vec<&str> = stderr.lines().collect();
    assert!(
        lines.len() == 1 && is_panic_line(lines[0]) && stderr.ends_with('\n'),
        "standard error is {stderr:?}, want one panic line"
    );
}

#[test]
fn a_tantivy_warning_arrives_as_a_log_frame_without_text() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    search.open(1, true);
    let answer = search.index(1, vec![marked_document(1)], true);
    assert_eq!(answer["ok"], json!({"committed": true}), "answer {answer}");
    assert!(
        search.logs.contains(
            &json!({"type": "log", "level": "warn", "target": "tantivy::postings::postings_writer"})
        ),
        "log frames {:?}",
        search.logs
    );
    for log in &search.logs {
        assert_eq!(
            log.as_object().unwrap().len(),
            3,
            "log frame {log} has more than type, level, and target"
        );
    }
    search.stop();
}

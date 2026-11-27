//! Fault points, which only builds with the `faults` feature have.
#![cfg(feature = "faults")]

mod support;

use std::os::unix::process::ExitStatusExt;

use serde_json::json;
use support::{DataDir, Search, document, run_with_faults, search_request};

/// The signal `std::process::abort` raises on Linux.
const SIGABRT: i32 = 6;

#[test]
fn a_fault_list_with_a_mistake_stops_at_startup() {
    for faults in [
        "during-search",
        "during-search=crash",
        "nowhere=exit",
        "during-search=fail,",
        "during-search=fail,during-search=hang",
    ] {
        assert_eq!(
            run_with_faults(faults).code(),
            Some(2),
            "MAILARKIV_SEARCH_FAULTS={faults}"
        );
    }
}

#[test]
fn during_search_fail_answers_timed_out() {
    let dir = DataDir::new();
    let mut search = Search::start_with_faults(&dir, "during-search=fail");
    search.open(1, true);
    search.index(1, vec![document(1, "Apfel")], true);
    assert_eq!(search.search(1, "apfel", 0, 50)["error"], "timed_out");
    // Words without terms build no query, so the point isn't reached.
    assert_eq!(
        search.search(1, "?!", 0, 50)["ok"],
        json!({"no_terms": true})
    );
    search.stop();
}

#[test]
fn during_search_hang_leaves_other_requests_answered() {
    let dir = DataDir::new();
    let mut search = Search::start_with_faults(&dir, "during-search=hang");
    search.open(1, true);
    search.send(&search_request(100, 1, "apfel"));
    assert_eq!(search.open(2, true)["ok"], json!({}));
    search.stop();
}

#[test]
fn during_search_exit_aborts() {
    let dir = DataDir::new();
    let mut search = Search::start_with_faults(&dir, "during-search=exit");
    search.open(1, true);
    search.send(&search_request(100, 1, "apfel"));
    assert_eq!(search.wait().signal(), Some(SIGABRT));
}

#[test]
fn on_open_fail_answers_index_damaged() {
    let dir = DataDir::new();
    let mut search = Search::start_with_faults(&dir, "on-open=fail");
    assert_eq!(search.open(1, true)["error"], "index_damaged");
    search.stop();
}

#[test]
fn during_merge_fail_answers_a_failed_merge() {
    let dir = DataDir::new();
    let mut search = Search::start_with_faults(&dir, "during-merge=fail");
    search.open(1, true);
    search.index(1, vec![document(1, "Apfel")], true);
    let answer = search.maintain(1);
    assert_eq!(
        answer["ok"],
        json!({"merges": 1, "failed": 1}),
        "answer {answer}"
    );
    // The index still answers.
    assert_eq!(search.ids(1, "apfel"), vec![1]);
    search.stop();
}

#[test]
fn during_merge_exit_aborts() {
    let dir = DataDir::new();
    let mut search = Search::start_with_faults(&dir, "during-merge=exit");
    search.open(1, true);
    search.send(&json!({"id": 100, "op": "maintain", "generation": 1}));
    assert_eq!(search.wait().signal(), Some(SIGABRT));
}

#[test]
fn after_commit_fail_answers_write_failed() {
    let dir = DataDir::new();
    let mut search = Search::start_with_faults(&dir, "after-commit=fail");
    search.open(1, true);
    let answer = search.index(1, vec![document(1, "Apfel")], true);
    assert_eq!(answer["error"], "write_failed", "answer {answer}");
    assert_eq!(answer["cause"], "other", "answer {answer}");
    // Without a commit, the point isn't reached.
    let answer = search.index(1, vec![document(2, "Birne")], false);
    assert_eq!(answer["ok"], json!({"committed": false}), "answer {answer}");
    search.stop();
}

//! A search stops at its deadline, and its total counts every match. How
//! often the deadline is checked is tested in `src/collector.rs`, which can
//! set the clock.

mod support;

use serde_json::json;
use support::{DataDir, Search, document};

/// The most documents one index request may carry.
const BATCH: u64 = 500;

#[test]
fn deadline_stops_search() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    search.open(1, true);
    search.index(1, vec![document(1, "Apfel"), document(2, "Apfel")], true);

    let answer = search.query(1, json!({ "words": "apfel", "deadline_ms": 0 }));
    assert_eq!(answer["error"], "timed_out", "answer {answer}");
    assert!(answer.get("ok").is_none(), "answer {answer}");

    // The process still answers the next search.
    assert_eq!(search.ids(1, "apfel"), [2, 1]);
    search.stop();
}

#[test]
fn total_counts_every_match_past_the_page() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    search.open(1, true);
    for first in (1..=10_000).step_by(BATCH as usize) {
        let documents = (first..first + BATCH)
            .map(|id| document(id, "Gemeinsam"))
            .collect();
        let answer = search.index(1, documents, first + BATCH > 10_000);
        assert!(answer["ok"].is_object(), "answer {answer}");
    }

    let first = search.query(1, json!({ "words": "gemeinsam" }));
    assert_eq!(first["ok"]["total"], 10_000);
    let ids = first["ok"]["ids"].as_array().unwrap();
    assert_eq!(ids.len(), 50);
    assert_eq!(ids[0], 10_000);

    let last = search.query(
        1,
        json!({ "words": "gemeinsam", "offset": 9_990, "limit": 50 }),
    );
    assert_eq!(
        last["ok"],
        json!({ "total": 10_000, "ids": [10, 9, 8, 7, 6, 5, 4, 3, 2, 1] })
    );
    search.stop();
}

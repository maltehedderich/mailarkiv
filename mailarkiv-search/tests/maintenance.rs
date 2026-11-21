//! Maintenance by request, closing an index, and its size.

mod support;

use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;

use serde_json::{Value, json};
use support::{DataDir, Search, document, search_request};

/// LogMergePolicy's default merges a level once it has this many segments.
const SEGMENTS_PER_MERGE: u64 = 8;

/// The segment IDs meta.json lists, in the form Tantivy's file names use.
fn segments(index: &Path) -> Vec<String> {
    let meta: Value = serde_json::from_slice(&fs::read(index.join("meta.json")).unwrap()).unwrap();
    meta["segments"]
        .as_array()
        .unwrap()
        .iter()
        .map(|segment| segment["segment_id"].as_str().unwrap().replace('-', ""))
        .collect()
}

/// The names of the files in index that belong to segment.
fn files_of(index: &Path, segment: &str) -> Vec<String> {
    fs::read_dir(index)
        .unwrap()
        .map(|entry| entry.unwrap().file_name().into_string().unwrap())
        .filter(|name| name.starts_with(segment))
        .collect()
}

/// Opens generation 1 and commits one document at a time, so each gets its
/// own segment.
fn index_in_segments(search: &mut Search, count: u64) {
    assert_eq!(search.open(1, true)["ok"], json!({}));
    for id in 1..=count {
        let answer = search.index(1, vec![document(id, "Apfel")], true);
        assert_eq!(answer["ok"], json!({"committed": true}), "answer {answer}");
    }
}

#[test]
fn maintenance_reports_failed_merge_and_retries() {
    let dir = DataDir::new();
    let index = dir.generation(1);
    let mut search = Search::start(&dir);
    index_in_segments(&mut search, SEGMENTS_PER_MERGE);
    let before = segments(&index);
    assert_eq!(before.len() as u64, SEGMENTS_PER_MERGE);

    // A merge writes a new segment, which a read-only directory refuses.
    // Garbage collection fails there too, but the failed merge is what the
    // answer reports.
    fs::set_permissions(&index, fs::Permissions::from_mode(0o500)).unwrap();
    let failed = search.maintain(1);
    fs::set_permissions(&index, fs::Permissions::from_mode(0o700)).unwrap();
    assert_eq!(
        failed["ok"],
        json!({"merges": 1, "failed": 1}),
        "answer {failed}"
    );
    assert_eq!(
        segments(&index),
        before,
        "a failed merge changed the segments"
    );

    // The next run tries the same segments again.
    let retried = search.maintain(1);
    assert_eq!(
        retried["ok"],
        json!({"merges": 1, "failed": 0}),
        "answer {retried}"
    );
    let after = segments(&index);
    assert_eq!(after.len(), 1, "segments after the merge: {after:?}");
    assert!(!before.contains(&after[0]));
    // Garbage collection ran after the merge and removed the old segments'
    // files.
    for segment in &before {
        assert_eq!(files_of(&index, segment), Vec::<String>::new());
    }
    let mut found = search.ids(1, "apfel");
    found.sort_unstable();
    assert_eq!(found, (1..=SEGMENTS_PER_MERGE).collect::<Vec<_>>());
    search.stop();
}

#[test]
fn maintenance_with_nothing_to_merge_reports_no_merges() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    index_in_segments(&mut search, 2);
    assert_eq!(search.maintain(1)["ok"], json!({"merges": 0, "failed": 0}));
    assert_eq!(search.maintain(7)["error"], "unknown_generation");
    search.stop();
}

#[test]
fn close_waits_for_a_search_and_removes_the_directory() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    index_in_segments(&mut search, 1);

    // The search is sent first, so its answer comes before the close's.
    search.send(&search_request(100, 1, "apfel"));
    search.send(&json!({"id": 101, "op": "close", "generation": 1, "delete": true}));
    let first = search.frame();
    assert_eq!(first["id"], 100, "first answer {first}");
    assert_eq!(first["ok"]["ids"], json!([1]), "first answer {first}");
    let second = search.frame();
    assert_eq!(second, json!({"id": 101, "ok": {}}));
    assert!(!dir.generation(1).exists());

    assert_eq!(
        search.search(1, "apfel", 0, 50)["error"],
        "unknown_generation"
    );
    assert_eq!(
        search.index(1, vec![document(2, "Birne")], true)["error"],
        "unknown_generation"
    );
    search.stop();
}

#[test]
fn close_without_delete_keeps_the_directory() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    index_in_segments(&mut search, 1);
    assert_eq!(search.close(1, false)["ok"], json!({}));
    assert!(dir.generation(1).join("meta.json").is_file());
    assert_eq!(
        search.search(1, "apfel", 0, 50)["error"],
        "unknown_generation"
    );

    // Closing frees the index, so it opens again with what was committed.
    assert_eq!(search.open(1, false)["ok"], json!({}));
    assert_eq!(search.ids(1, "apfel"), vec![1]);
    // Closing an index that isn't open still removes its directory.
    assert_eq!(search.close(1, false)["ok"], json!({}));
    assert_eq!(search.close(1, true)["ok"], json!({}));
    assert!(!dir.generation(1).exists());
    search.stop();
}

#[test]
fn size_answers_the_sum_of_the_file_sizes() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    index_in_segments(&mut search, 3);

    let answer = search.size(1);
    let mut expected = 0;
    for entry in fs::read_dir(dir.generation(1)).unwrap() {
        let metadata = entry.unwrap().metadata().unwrap();
        if metadata.is_file() {
            expected += metadata.len();
        }
    }
    assert!(expected > 0);
    assert_eq!(answer["ok"], json!({"bytes": expected}), "answer {answer}");
    assert_eq!(search.size(4)["error"], "index_missing");
    search.stop();
}

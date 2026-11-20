//! Opening indexes and adding documents.

mod support;

use std::fs;
use std::os::unix::fs::PermissionsExt;

use serde_json::json;
use support::{DataDir, Search, document};

#[test]
fn open_with_create_makes_the_directory() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    assert_eq!(search.open(3, true)["ok"], json!({}));
    assert!(dir.generation(3).join("meta.json").is_file());
    let mode = fs::metadata(dir.generation(3))
        .unwrap()
        .permissions()
        .mode();
    assert_eq!(mode & 0o777, 0o700, "index/3 has mode {mode:o}");
    // Opening it again, with or without create, changes nothing.
    assert_eq!(search.open(3, true)["ok"], json!({}));
    assert_eq!(search.open(3, false)["ok"], json!({}));
    search.stop();

    let mut search = Search::start(&dir);
    assert_eq!(search.open(3, false)["ok"], json!({}));
    search.stop();
}

#[test]
fn open_without_create_answers_index_missing() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    assert_eq!(search.open(5, false)["error"], "index_missing");
    assert!(!dir.generation(5).exists());

    // A directory without meta.json is missing too.
    fs::create_dir(dir.generation(6)).unwrap();
    assert_eq!(search.open(6, false)["error"], "index_missing");
    search.stop();
}

#[test]
fn another_schema_version_answers_schema_mismatch() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    assert_eq!(search.open(1, true)["ok"], json!({}));
    search.stop();

    fs::write(dir.generation(1).join("schema_version"), "2\n").unwrap();
    let mut search = Search::start(&dir);
    assert_eq!(search.open(1, false)["error"], "schema_mismatch");
    assert_eq!(search.open(1, true)["error"], "schema_mismatch");
    search.stop();
}

#[test]
fn unreadable_index_answers_index_damaged() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    assert_eq!(search.open(1, true)["ok"], json!({}));
    search.stop();

    fs::write(dir.generation(1).join("meta.json"), "not json").unwrap();
    let mut search = Search::start(&dir);
    assert_eq!(search.open(1, false)["error"], "index_damaged");
    search.stop();
}

#[test]
fn unopened_generation_answers_unknown_generation() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    assert_eq!(
        search.index(9, vec![document(1, "Hallo")], true)["error"],
        "unknown_generation"
    );
    assert_eq!(
        search.search(9, "hallo", 0, 50)["error"],
        "unknown_generation"
    );
    search.stop();
}

#[test]
fn creating_in_a_missing_index_directory_answers_write_failed() {
    let dir = DataDir::new();
    fs::remove_dir(dir.path().join("index")).unwrap();
    let mut search = Search::start(&dir);
    assert_eq!(
        search.open(1, true),
        json!({"id": 1, "error": "write_failed", "cause": "other"})
    );
    search.stop();
}

#[test]
fn delete_before_add_keeps_one() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    search.open(1, true);
    let answer = search.index(
        1,
        vec![
            document(17, "Bericht Entwurf"),
            document(17, "Bericht Zwischenstand"),
        ],
        true,
    );
    assert_eq!(answer["ok"], json!({"committed": true}));
    assert_eq!(search.ids(1, "bericht"), [17]);
    assert_eq!(search.ids(1, "zwischenstand"), [17]);
    assert_eq!(search.ids(1, "entwurf"), Vec::<u64>::new());

    search.index(1, vec![document(17, "Bericht Endfassung")], true);
    let answer = search.search(1, "bericht", 0, 50);
    assert_eq!(answer["ok"], json!({"total": 1, "ids": [17]}));
    assert_eq!(search.ids(1, "endfassung"), [17]);
    assert_eq!(search.ids(1, "zwischenstand"), Vec::<u64>::new());
    search.stop();
}

#[test]
fn documents_wait_for_a_commit() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    search.open(1, true);
    assert_eq!(
        search.index(1, vec![document(1, "Apfel")], false)["ok"],
        json!({"committed": false})
    );
    assert_eq!(search.ids(1, "apfel"), Vec::<u64>::new());
    assert_eq!(
        search.index(1, vec![], true)["ok"],
        json!({"committed": true})
    );
    assert_eq!(search.ids(1, "apfel"), [1]);
    search.stop();
}

#[test]
fn committed_documents_survive_a_restart() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    search.open(1, true);
    search.index(1, vec![document(1, "Apfel")], true);
    search.index(1, vec![document(2, "Apfel")], false);
    search.stop();

    let mut search = Search::start(&dir);
    search.open(1, false);
    assert_eq!(search.ids(1, "apfel"), [1]);
    search.stop();
}

#[test]
fn read_only_directory_answers_write_failed_then_recovers() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    search.open(1, true);
    search.index(1, vec![document(1, "Apfel")], true);

    let generation = dir.generation(1);
    fs::set_permissions(&generation, fs::Permissions::from_mode(0o500)).unwrap();
    // The server sends a failed batch again, so every try fails the same way.
    let answers: Vec<_> = (0..3)
        .map(|_| search.index(1, vec![document(2, "Birne")], true))
        .collect();
    fs::set_permissions(&generation, fs::Permissions::from_mode(0o700)).unwrap();
    for answer in answers {
        assert_eq!(answer["error"], "write_failed", "answer {answer}");
        assert_eq!(answer["cause"], "permission_denied", "answer {answer}");
    }

    // The failed batch is gone, and the same batch commits now.
    assert_eq!(search.ids(1, "birne"), Vec::<u64>::new());
    assert_eq!(
        search.index(1, vec![document(2, "Birne")], true)["ok"],
        json!({"committed": true})
    );
    assert_eq!(search.ids(1, "birne"), [2]);
    assert_eq!(search.ids(1, "apfel"), [1]);
    search.stop();
}

// An indexing thread that fails stops the writer, so the batch's later
// documents fail differently. The cause still comes from the I/O error.
#[test]
fn many_documents_in_a_read_only_directory_answer_permission_denied() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    search.open(1, true);
    let generation = dir.generation(1);
    fs::set_permissions(&generation, fs::Permissions::from_mode(0o500)).unwrap();
    let text = "Quartalsbericht ".repeat(20_000);
    let answers: Vec<_> = (0..100)
        .map(|_| {
            let documents = (1..=2)
                .map(|id| {
                    let mut document = document(id, "Apfel");
                    document["text"] = json!([text]);
                    document
                })
                .collect();
            search.index(1, documents, true)
        })
        .collect();
    fs::set_permissions(&generation, fs::Permissions::from_mode(0o700)).unwrap();
    for answer in answers {
        assert_eq!(answer["error"], "write_failed", "answer {answer}");
        assert_eq!(answer["cause"], "permission_denied", "answer {answer}");
    }
    search.stop();
}

#[test]
fn more_than_500_documents_exits_3() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    search.open(1, true);
    let documents: Vec<_> = (1..=501).map(|id| document(id, "Apfel")).collect();
    search.send(
        &json!({"id": 99, "op": "index", "generation": 1, "documents": documents, "commit": true}),
    );
    assert_eq!(search.wait().code(), Some(3));
}

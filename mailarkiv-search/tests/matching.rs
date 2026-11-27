//! Which documents a search finds, and in what order.

mod support;

use serde_json::{Value, json};
use support::{DataDir, Search, document};

/// A search process with one open generation, 1, holding documents.
fn indexed(documents: Vec<Value>) -> (DataDir, Search) {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    assert_eq!(search.open(1, true)["ok"], json!({}));
    assert_eq!(
        search.index(1, documents, true)["ok"],
        json!({"committed": true})
    );
    (dir, search)
}

#[test]
fn a_word_matches_the_words_it_starts() {
    let (_dir, mut search) = indexed(vec![
        document(1, "Quartalsbericht Q3"),
        document(2, "Quartett am Abend"),
        document(3, "Monatsbericht"),
    ]);
    assert_eq!(search.ids(1, "quartals"), [1]);
    assert_eq!(search.ids(1, "QUARTALS"), [1]);
    assert_eq!(search.ids(1, "Quartalsbericht"), [1]);
    assert_eq!(search.ids(1, "quarta"), [1]);
    assert_eq!(search.ids(1, "quart"), [2, 1]);
    // Only the start of a word matches.
    assert_eq!(search.ids(1, "bericht"), Vec::<u64>::new());
    search.stop();
}

#[test]
fn every_word_must_match() {
    let (_dir, mut search) = indexed(vec![
        document(1, "Apfel Birne"),
        document(2, "Apfel"),
        document(3, "Birne"),
    ]);
    assert_eq!(search.ids(1, "apfel birne"), [1]);
    assert_eq!(search.ids(1, "birne, apfel!"), [1]);
    assert_eq!(search.ids(1, "apfel"), [2, 1]);
    assert_eq!(search.ids(1, "apfel kirsche"), Vec::<u64>::new());
    search.stop();
}

#[test]
fn words_come_from_every_part_of_the_document() {
    let (_dir, mut search) = indexed(vec![json!({
        "id": 5,
        "list_date": 0,
        "first_archived_at": 0,
        "has_attachments": true,
        "subject": "Betreffwort",
        "from": [{"name": "Absendername", "address": "absender@example.test"}],
        "to": [
            {"name": null, "address": "empfaenger@example.test"},
            {"address": "kopie@example.org"},
            {"name": "Nur Text", "address": ""},
        ],
        "attachment_names": ["Anhangname.pdf"],
        "text": ["Klartextwort", "Htmlwort"],
    })]);
    for words in [
        "betreffwort",
        "absendername",
        "absender",
        "example",
        "empfaenger",
        "kopie",
        "org",
        "nur text",
        "anhangname",
        "pdf",
        "klartextwort",
        "htmlwort",
    ] {
        assert_eq!(search.ids(1, words), [5], "searching {words:?}");
    }
    search.stop();
}

#[test]
fn a_document_may_lack_a_subject_and_addresses() {
    let (_dir, mut search) = indexed(vec![json!({
        "id": 6,
        "list_date": -86_400_000i64,
        "first_archived_at": 1_794_100_000_000i64,
        "has_attachments": false,
        "subject": null,
        "from": [],
        "to": [],
        "attachment_names": [],
        "text": ["Nur ein Text"],
    })]);
    assert_eq!(search.ids(1, "text"), [6]);
    search.stop();
}

#[test]
fn words_over_100_bytes_are_left_out() {
    let long = "x".repeat(101);
    let longest = "y".repeat(100);
    let (_dir, mut search) = indexed(vec![json!({
        "id": 1,
        "list_date": 0,
        "first_archived_at": 0,
        "has_attachments": false,
        "subject": "Lang",
        "from": [],
        "to": [],
        "attachment_names": [],
        "text": [format!("{long} {longest}")],
    })]);
    assert_eq!(search.ids(1, &longest), [1]);
    assert_eq!(search.ids(1, "yy"), [1]);
    assert_eq!(search.ids(1, "xx"), Vec::<u64>::new());
    search.stop();
}

#[test]
fn words_without_letters_or_digits_give_no_terms() {
    let (_dir, mut search) = indexed(vec![document(1, "Apfel")]);
    assert_eq!(search.search(1, "", 0, 50)["ok"], json!({"no_terms": true}));
    assert_eq!(
        search.search(1, " -- !? ", 0, 50)["ok"],
        json!({"no_terms": true})
    );
    search.stop();
}

fn dated(id: u64, list_date: i64, first_archived_at: i64) -> Value {
    let mut document = document(id, "Gemeinsam");
    document["list_date"] = json!(list_date);
    document["first_archived_at"] = json!(first_archived_at);
    document
}

#[test]
fn results_sort_newest_first_by_list_date_then_archive_time_then_id() {
    let (_dir, mut search) = indexed(vec![
        dated(1, 300, 10),
        dated(2, 300, 20),
        dated(3, 100, 50),
    ]);
    // A second commit puts the rest in another segment.
    search.index(
        1,
        vec![dated(4, 300, 20), dated(5, 500, 1), dated(6, -100, 90)],
        true,
    );
    assert_eq!(search.ids(1, "gemeinsam"), [5, 4, 2, 1, 3, 6]);

    let answer = search.search(1, "gemeinsam", 1, 2);
    assert_eq!(answer["ok"], json!({"total": 6, "ids": [4, 2]}));
    let answer = search.search(1, "gemeinsam", 5, 50);
    assert_eq!(answer["ok"], json!({"total": 6, "ids": [6]}));
    let answer = search.search(1, "gemeinsam", 50, 50);
    assert_eq!(answer["ok"], json!({"total": 6, "ids": []}));
    search.stop();
}

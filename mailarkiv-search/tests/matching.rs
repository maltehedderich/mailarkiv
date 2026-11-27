//! Which documents a search finds, and in what order.

mod support;

use serde_json::{Value, json};
use support::{DataDir, Search, addressed, document};

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

/// A document whose text is words.
fn texted(id: u64, words: &str) -> Value {
    let mut document = document(id, "Gemeinsam");
    document["text"] = json!([words]);
    document
}

#[test]
fn matching_one_character_exact() {
    let (_dir, mut search) = indexed(vec![
        texted(1, "Punkt a der Liste"),
        texted(2, "Ein Apfel"),
        texted(3, "Punkt A"),
        texted(4, "ä"),
        texted(5, "Äpfel"),
    ]);
    assert_eq!(search.ids(1, "a"), [3, 1]);
    assert_eq!(search.ids(1, "A"), [3, 1]);
    // One character is one letter, however many bytes it takes.
    assert_eq!(search.ids(1, "ä"), [4]);
    // Two characters are a prefix again.
    assert_eq!(search.ids(1, "ap"), [2]);
    assert_eq!(search.ids(1, "äp"), [5]);
    search.stop();
}

#[test]
fn matching_accents_kept() {
    let (_dir, mut search) = indexed(vec![
        texted(1, "Herr Müller"),
        texted(2, "HERR MÜLLER"),
        texted(3, "Herr Muller"),
    ]);
    assert_eq!(search.ids(1, "müller"), [2, 1]);
    assert_eq!(search.ids(1, "MÜLLER"), [2, 1]);
    assert_eq!(search.ids(1, "mü"), [2, 1]);
    assert_eq!(search.ids(1, "muller"), [3]);
    search.stop();
}

#[test]
fn matching_address_exact() {
    let (_dir, mut search) = indexed(vec![
        addressed(1, "anna@example.com", &["team@example.test"]),
        addressed(2, "anna@example.org", &["team@example.test"]),
        addressed(3, "Anna@Example.COM", &["team@example.test"]),
        addressed(4, "hanna@example.com", &["team@example.test"]),
        addressed(5, "anna@example.com.au", &["team@example.test"]),
        addressed(6, "bob@example.com", &["anna@example.com"]),
    ]);
    let from = |address: &str| json!({ "from": address });
    assert_eq!(search.found(1, from("anna@example.com")), [3, 1]);
    assert_eq!(search.found(1, from("ANNA@example.com")), [3, 1]);
    assert_eq!(search.found(1, from(" anna@example.com ")), [3, 1]);
    assert_eq!(search.found(1, from("anna@example.org")), [2]);
    assert_eq!(search.found(1, json!({ "to": "anna@example.com" })), [6]);
    search.stop();
}

#[test]
fn matching_domain() {
    let (_dir, mut search) = indexed(vec![
        addressed(1, "anna@example.com", &["team@example.test"]),
        addressed(2, "bob@example.com", &["team@example.test"]),
        addressed(3, "carol@mail.example.com", &["team@example.test"]),
        addressed(4, "x@attacker-example.com", &["team@example.test"]),
        addressed(5, "anna@example.org", &["team@example.test"]),
        addressed(6, "dora@example.test", &["erik@example.com"]),
    ]);
    let from = |domain: &str| json!({ "from": domain });
    assert_eq!(search.found(1, from("example.com")), [2, 1]);
    assert_eq!(search.found(1, from("@example.com")), [2, 1]);
    assert_eq!(search.found(1, from("EXAMPLE.COM")), [2, 1]);
    assert_eq!(search.found(1, from("mail.example.com")), [3]);
    assert_eq!(search.found(1, json!({ "to": "example.com" })), [6]);
    search.stop();
}

#[test]
fn matching_field_words() {
    let mut named_from = document(1, "Gemeinsam");
    named_from["from"] = json!([{ "name": "Anna Schmidt", "address": "as@example.test" }]);
    let mut named_to = document(3, "Gemeinsam");
    named_to["to"] = json!([{ "name": "Anna Weber", "address": "aw@example.test" }]);
    let (_dir, mut search) = indexed(vec![named_from, document(2, "Anna"), named_to]);
    assert_eq!(search.found(1, json!({ "from": "anna" })), [1]);
    assert_eq!(search.found(1, json!({ "from": "schm" })), [1]);
    assert_eq!(search.found(1, json!({ "from": "Anna Schmidt" })), [1]);
    // The address's words are words of the field too.
    assert_eq!(search.found(1, json!({ "from": "as" })), [1]);
    assert_eq!(search.found(1, json!({ "subject": "anna" })), [2]);
    assert_eq!(search.found(1, json!({ "to": "anna" })), [3]);
    assert_eq!(search.ids(1, "anna"), [3, 2, 1]);
    search.stop();
}

#[test]
fn matching_to_covers_cc_bcc() {
    // The server puts To, Cc, and Bcc in one list.
    let (_dir, mut search) = indexed(vec![
        addressed(
            1,
            "anna@example.test",
            &["team@example.com", "kopie@example.org", "blind@example.net"],
        ),
        addressed(2, "kopie@example.org", &["team@example.test"]),
    ]);
    assert_eq!(search.found(1, json!({ "to": "kopie@example.org" })), [1]);
    assert_eq!(search.found(1, json!({ "to": "blind@example.net" })), [1]);
    assert_eq!(search.found(1, json!({ "to": "example.net" })), [1]);
    assert_eq!(search.found(1, json!({ "from": "kopie@example.org" })), [2]);
    search.stop();
}

#[test]
fn matching_attachment_name() {
    let mut attached = document(1, "Gemeinsam");
    attached["attachment_names"] = json!(["Vertrag_2026.pdf"]);
    let mut other = document(3, "Gemeinsam");
    other["attachment_names"] = json!(["Rechnung.pdf"]);
    let (_dir, mut search) = indexed(vec![attached, document(2, "Vertrag"), other]);
    assert_eq!(search.found(1, json!({ "attachment": "vertrag" })), [1]);
    assert_eq!(search.found(1, json!({ "attachment": "2026" })), [1]);
    assert_eq!(search.found(1, json!({ "attachment": "pdf" })), [3, 1]);
    assert_eq!(search.ids(1, "vertrag"), [2, 1]);
    search.stop();
}

#[test]
fn matching_date_range() {
    let (_dir, mut search) = indexed(vec![
        dated(1, 999, 0),
        dated(2, 1000, 0),
        dated(3, 1500, 0),
        dated(4, 1999, 0),
        dated(5, 2000, 0),
        dated(6, -5, 0),
    ]);
    let range = |from: Value, before: Value| json!({ "words": "gemeinsam", "list_date_from": from, "list_date_before": before });
    // The first bound is included, the second isn't.
    assert_eq!(search.found(1, range(json!(1000), json!(2000))), [4, 3, 2]);
    assert_eq!(search.found(1, range(json!(1500), Value::Null)), [5, 4, 3]);
    assert_eq!(search.found(1, range(Value::Null, json!(1000))), [1, 6]);
    assert_eq!(search.found(1, range(json!(-5), json!(0))), [6]);
    assert_eq!(
        search.found(1, range(json!(1000), json!(1000))),
        Vec::<u64>::new()
    );
    search.stop();
}

#[test]
fn matching_has_attachments() {
    let mut attached = document(1, "Gemeinsam");
    attached["has_attachments"] = json!(true);
    let (_dir, mut search) = indexed(vec![attached, document(2, "Gemeinsam")]);
    assert_eq!(
        search.found(1, json!({ "words": "gemeinsam", "has_attachments": true })),
        [1]
    );
    // False is no filter.
    assert_eq!(
        search.found(1, json!({ "words": "gemeinsam", "has_attachments": false })),
        [2, 1]
    );
    search.stop();
}

#[test]
fn matching_no_terms() {
    let mut attached = addressed(1, "anna@example.com", &[]);
    attached["has_attachments"] = json!(true);
    attached["list_date"] = json!(5000);
    let mut other = addressed(2, "bob@example.com", &[]);
    other["list_date"] = json!(100);
    let (_dir, mut search) = indexed(vec![attached, other]);
    let no_terms = json!({ "no_terms": true });
    assert_eq!(search.query(1, json!({ "words": "?!" }))["ok"], no_terms);
    // A filter without letters or digits is empty, like the words.
    assert_eq!(
        search.query(1, json!({ "words": "?!", "from": "--", "subject": " " }))["ok"],
        no_terms
    );
    assert_eq!(search.query(1, json!({ "from": "@" }))["ok"], no_terms);

    // With a filter, the words are left out and the filter applies.
    let only = json!({ "total": 1, "ids": [1] });
    for filter in [
        json!({ "has_attachments": true }),
        json!({ "from": "anna@example.com" }),
        json!({ "from": "anna" }),
        json!({ "list_date_from": 5000 }),
        json!({ "list_date_from": 4000, "list_date_before": 5001 }),
    ] {
        let mut changes = filter.clone();
        changes["words"] = json!("?!");
        assert_eq!(search.query(1, changes)["ok"], only, "filter {filter}");
    }
    search.stop();
}

#[test]
fn matching_operators_are_text() {
    let (_dir, mut search) = indexed(vec![
        texted(1, "Apfel Birne"),
        texted(2, "Apfel"),
        texted(3, "Birne"),
        texted(4, "Apfel Orange Birne"),
    ]);
    // OR is a word, which "Orange" starts with.
    assert_eq!(search.ids(1, "apfel OR birne"), [4]);
    // A minus separates words, so it excludes nothing.
    assert_eq!(search.ids(1, "apfel -birne"), [4, 1]);
    // Quotes don't make a phrase.
    assert_eq!(search.ids(1, "\"birne apfel\""), [4, 1]);
    assert_eq!(search.ids(1, "apfel AND birne"), Vec::<u64>::new());
    search.stop();
}

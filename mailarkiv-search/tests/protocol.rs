//! Frames, the hello, and exit statuses, as the search process contract sets
//! them.

mod support;

use std::time::Instant;

use serde_json::json;
use support::{DataDir, Search, frame, search_request};

#[test]
fn hello_comes_first() {
    let dir = DataDir::new();
    let started = Instant::now();
    let mut search = Search::start_without_hello(&dir);
    let hello = search.frame();
    assert!(started.elapsed() < support::WAIT);
    assert_eq!(
        hello,
        json!({"type": "hello", "protocol": 1, "tantivy": "0.27.0", "schema_version": 1})
    );
    search.stop();
}

#[test]
fn any_argument_exits_2() {
    assert_eq!(support::run_with_args(&["--help"]).code(), Some(2));
    assert_eq!(support::run_with_args(&["serve"]).code(), Some(2));
}

#[test]
fn closing_input_exits_0() {
    let dir = DataDir::new();
    Search::start(&dir).stop();
}

/// Sends bytes after the hello and expects exit status 3.
fn exits_3(bytes: &[u8]) {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    // The program may exit before it has read everything.
    let _ = search.send_raw(bytes);
    assert_eq!(
        search.wait().code(),
        Some(3),
        "after {:?}",
        &bytes[..bytes.len().min(16)]
    );
}

#[test]
fn zero_length_exits_3() {
    exits_3(&[0, 0, 0, 0]);
}

#[test]
fn frame_over_64_mib_exits_3() {
    // Only the length: the program must refuse it before reading a body.
    exits_3(&(64 * 1024 * 1024 + 1u32).to_be_bytes());
}

#[test]
fn json_that_does_not_parse_exits_3() {
    let body = b"{\"id\": 1, \"op\": ";
    let mut bytes = (body.len() as u32).to_be_bytes().to_vec();
    bytes.extend(body);
    exits_3(&bytes);
}

#[test]
fn unknown_operation_exits_3() {
    exits_3(&frame(&json!({"id": 1, "op": "format_disk"})));
}

#[test]
fn frame_cut_short_exits_3() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    let whole = frame(&json!({"id": 1, "op": "open", "generation": 1, "create": true}));
    search.send_raw(&whole[..whole.len() - 3]).unwrap();
    search.close_stdin();
    assert_eq!(search.wait().code(), Some(3));
}

#[test]
fn frame_of_exactly_64_mib_is_read() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    let request = search_request(9, 4, "w");
    let mut body = serde_json::to_vec(&request).unwrap();
    // JSON allows spaces after the object.
    body.resize(64 * 1024 * 1024, b' ');
    let mut bytes = (body.len() as u32).to_be_bytes().to_vec();
    bytes.extend(body);
    search.send_raw(&bytes).unwrap();
    assert_eq!(
        search.frame(),
        json!({"id": 9, "error": "unknown_generation"})
    );
    search.stop();
}

#[test]
fn answers_carry_their_request_id() {
    let dir = DataDir::new();
    let mut search = Search::start(&dir);
    search.send(&json!({"id": 41, "op": "open", "generation": 1, "create": true}));
    let created = search.frame();
    search.send(&json!({"id": 7, "op": "open", "generation": 2, "create": false}));
    search.send(&search_request(1000, 3, "w"));
    let mut later = [search.frame(), search.frame()];
    later.sort_by_key(|answer| answer["id"].as_u64());
    assert_eq!(created, json!({"id": 41, "ok": {}}));
    assert_eq!(later[0], json!({"id": 7, "error": "index_missing"}));
    assert_eq!(later[1], json!({"id": 1000, "error": "unknown_generation"}));
    search.stop();
}

#[test]
fn id_zero_exits_3() {
    exits_3(&frame(
        &json!({"id": 0, "op": "open", "generation": 1, "create": true}),
    ));
}

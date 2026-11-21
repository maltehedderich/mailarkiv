# Search process protocol

`mailarkiv-search` holds the Tantivy indexes. Only the MailArkiv server starts it, and only the server talks to it. Both sides are built from this repository and change together, so the protocol has one version and no negotiation. [research.md](../research.md#search-process) has the reasons for each choice.

## Start and stop

| Rule | Value |
| ---- | ----- |
| Program | `mailarkiv-search`, in the same directory as the server's executable |
| Arguments | None. Any argument makes it exit with status 2 |
| Environment | Empty, except `MAILARKIV_SEARCH_FAULTS` in builds with the `faults` feature |
| Working directory | The data directory |
| Standard input and output | Pipes to the server, carrying frames |
| Standard error | Read by the server. See [Output](#output) |
| Parent death | Started with `Pdeathsig: SIGKILL`. It also exits with status 0 when standard input closes |
| Threads | One writer thread per open index, two search threads, one maintenance thread |
| Memory | Each writer gets a 128 MB budget across 2 indexing threads |

## Frames

A frame is a 4-byte unsigned big-endian length, then that many bytes of UTF-8 JSON holding one object. A frame over 64 MiB, a length of 0, or JSON that doesn't parse ends the connection: the search process exits with status 3, and the server kills it and counts a crash.

Within 10 s of starting, the search process sends:

```json
{ "type": "hello", "protocol": 1, "tantivy": "0.27.0", "schema_version": 1 }
```

A missing or different `hello` is Search process didn't start, with cause `bad_hello`.

Every request carries a unique `id`, a positive integer the server chooses. The answer carries the same `id` and either `ok` or `error`. Answers can arrive in any order.

```json
{ "id": 7, "op": "search", "generation": 3, "…": "…" }
{ "id": 7, "ok": { "…": "…" } }
{ "id": 8, "error": "timed_out" }
```

## Operations

The server's time limit for each call comes first. A call that passes it is Search process didn't answer (FR-041). The search process may still finish the work; the server ignores a late answer.

### `open`, 60 s

```json
{ "id": 1, "op": "open", "generation": 3, "create": false }
```

Opens `index/<generation>/`, or creates it with the schema when `create` is true. Sets `NoMergePolicy` on the writer.

| Answer | When |
| ------ | ---- |
| `ok: {}` | Opened or created |
| `error: "index_missing"` | `create` is false and the directory or its `meta.json` doesn't exist |
| `error: "index_damaged"` | The files exist but can't be opened |
| `error: "schema_mismatch"` | The index was made with another schema version |
| `error: "write_failed"` | The directory couldn't be created. Comes with `"cause"` |

### `index`, 120 s

```json
{
  "id": 2,
  "op": "index",
  "generation": 3,
  "documents": [
    {
      "id": 17,
      "list_date": 1793603695000,
      "first_archived_at": 1794100000000,
      "has_attachments": true,
      "subject": "Quartalsbericht Q3",
      "from": [{ "name": "Anna Schmidt", "address": "anna@example.com" }],
      "to": [{ "name": null, "address": "team@example.com" }],
      "attachment_names": ["Vertrag_2026.pdf"],
      "text": ["…plain body…", "…words from the HTML body…"]
    }
  ],
  "commit": true
}
```

At most 500 documents and 32 MiB per frame. For each document, in order, the writer deletes the term `id` and adds the document. `to` holds To, Cc, and Bcc. `null` and missing names are the same. With `commit: true`, the answer comes after `IndexWriter::commit` returns and the reader reloads, so the next search sees the batch.

| Answer | When |
| ------ | ---- |
| `ok: { "committed": true }` | Added, and committed if asked |
| `error: "write_failed"` | Adding or committing failed. Comes with `"cause": "no_space" \| "permission_denied" \| "other"`. Uncommitted documents are discarded with `rollback` |
| `error: "unknown_generation"` | That index isn't open |

### `search`, 12 s

```json
{
  "id": 3,
  "op": "search",
  "generation": 3,
  "words": "quartals müller",
  "from": "anna@example.com",
  "to": "",
  "subject": "",
  "attachment": "",
  "list_date_from": 1767222000000,
  "list_date_before": null,
  "has_attachments": false,
  "offset": 0,
  "limit": 50,
  "deadline_ms": 10000
}
```

The search process builds the query as [research.md](../research.md#matching) describes and stops it at `deadline_ms`. `offset` plus `limit` is at most 100,050.

| Answer | When |
| ------ | ---- |
| `ok: { "total": 132, "ids": [17, 12, 9] }` | Finished. `ids` in list order |
| `ok: { "no_terms": true }` | `words` gave no terms and every filter is empty. The server shows the full list |
| `error: "timed_out"` | The deadline passed |
| `error: "unknown_generation"` | |

### `maintain`, 30 minutes

```json
{ "id": 4, "op": "maintain", "generation": 3 }
```

Computes candidates with `LogMergePolicy::compute_merge_candidates`, runs and waits for each merge, then runs `garbage_collect_files`.

| Answer | When |
| ------ | ---- |
| `ok: { "merges": 2, "failed": 0 }` | Every merge finished. `failed` is 0 |
| `ok: { "merges": 2, "failed": 1 }` | At least one merge failed. The server records Index maintenance failed |
| `error: "gc_failed"` | Removing unused files failed. Counted as a failed run |

### `close`, 60 s

```json
{ "id": 5, "op": "close", "generation": 2, "delete": true }
```

Waits for searches in progress on that index, closes its reader and writer, and, with `delete: true`, removes its directory. Searches that arrive after `close` get `unknown_generation`.

### `size`, 10 s

```json
{ "id": 6, "op": "size", "generation": 3 }
```

Answers `ok: { "bytes": 6012345678 }`, the sum of the sizes of the index's files. Used for the rebuild's space check.

## Output

The search process never sends message content, search words, or Tantivy's error text, in any frame or on standard error (FR-048). Errors are the closed codes above.

Besides answers, it may send one kind of unsolicited frame:

```json
{ "type": "log", "level": "warn", "target": "tantivy::directory::managed_directory" }
```

`level` is `warn` or `error`. `target` is the Rust module path of the record, or `unknown` when the record's target isn't one: at most 128 characters of letters, digits, `_`, and `:`. The record's text is dropped. The server logs these at warn level and doesn't record failure events for them.

On standard error, the only line the search process writes is its panic line, after which it aborts:

```text
mailarkiv-search panic at src/index.rs:214
```

The server logs a line that matches `^mailarkiv-search panic at [A-Za-z0-9_/.-]+:[0-9]+$` with the exit that follows. It counts every other line and logs only the count.

## Exit statuses

| Status | Meaning |
| ------ | ------- |
| 0 | Standard input closed |
| 2 | Started with arguments |
| 3 | A frame broke the rules above |
| Killed by `SIGABRT` | Panic, after the panic line |
| Anything else, or a signal | Crash |

The server records every exit it didn't ask for as Search process stopped, with the status or signal in the event's detail.

## Faults

Builds with the cargo feature `faults` read `MAILARKIV_SEARCH_FAULTS`, a comma-separated list of `point=action`. Release builds don't contain the code.

| Point | Where |
| ----- | ----- |
| `after-commit` | After `commit` returns, before the answer is sent |
| `during-search` | After the query is built, before collection |
| `during-merge` | Inside a maintenance run, before the first merge |
| `on-open` | Before an index opens |

| Action | Effect |
| ------ | ------ |
| `exit` | `std::process::abort()` |
| `fail` | The operation answers its failure code: `write_failed` with cause `other`, `timed_out`, a failed merge, or `index_damaged` |
| `hang` | Sleeps forever, so the server's time limit fires |
| `panic` | Panics, so the panic line goes to standard error before the abort. Tests use it to check that the line holds only the location |

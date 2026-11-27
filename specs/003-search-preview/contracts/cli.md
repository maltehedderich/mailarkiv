# Command line changes

This adds to [the Slice 1 command line](../../001-import-browse-retrieve/contracts/cli.md) and [the Slice 2 changes](../../002-imap-incremental-archive/contracts/cli.md). No option changes, and the exit codes keep their meaning.

## Files

A build produces two programs, and they must stay in the same directory:

| File | Built by |
| ---- | -------- |
| `bin/mailarkiv` | `mise run server:build`, as before |
| `bin/mailarkiv-search` | `mise run search:build`. A static musl binary |

`mailarkiv` without `mailarkiv-search` still serves. Search is then Unavailable with Search process didn't start, cause `missing`, and capture, the list, previews, and downloads work.

## `mailarkiv serve`

```text
mailarkiv serve --data-dir DIR [--listen ADDR] [--key-file PATH]
```

Startup order, with the new steps in bold:

1. Create the data directory if it's missing, with mode 0700.
2. If `--key-file` is given, load the key.
3. Take the lock.
4. Check storage.
5. Open the catalog and apply migrations, now including [0003](../data-model.md#migration-0003).
6. Run recovery.
7. **Create `index/` with mode 0700 if it's missing, and remove any directory in it that no `search_generation` row names.**
8. Load sources.
9. **Start the two worker pools.**
10. Bind the port.
11. Start syncs.
12. **Start processing, and start the search process. Each runs in the background, so a slow or failing search process doesn't delay serving.**
13. Serve.

Neither new step can stop startup. A failure to create `index/` makes search Unavailable with Index write failed, cause `permission_denied` or `other`. Migration 0003 can fail, with Slice 1's cause `catalog`.

On SIGTERM or SIGINT, the server stops processing and indexing, closes the search process's standard input, and waits for it inside the same 5 second grace, then kills what still runs. A batch that wasn't committed is sent again after the next start.

### Log entries

All are JSON lines from `log/slog` on stderr, as before. None contains a search word, filter, subject, address, name, body text, or attachment name (FR-048). `message_id`, `generation_id`, and `rebuild_id` are internal IDs.

| `level` | `msg` | Other fields | When |
| ------- | ----- | ------------ | ---- |
| ERROR | `search failure` | `category` and the [event's detail](../data-model.md#failure-categories) | Each failure in the eight new categories, including each repeat |
| INFO | `search process started` | `restarts` | Each start that sent a valid `hello` |
| INFO | `search state` | `state`, `indexing` | Each change of the search or indexing state |
| INFO | `rebuild started` | `rebuild_id`, `generation_id` | An administrator started a rebuild |
| INFO | `rebuild finished` | `rebuild_id`, `generation_id`, `messages` | The swap committed |
| WARN | `search process log` | `level`, `target` | A `log` frame from the search process |
| ERROR | `search process panic` | `location` | Its panic line |
| WARN | `search process output ignored` | `lines` | Lines on its standard error that weren't a panic line |
| ERROR | `processing_worker_stopped` | `message_id`, `exit` | A worker died or passed 60 s on a message |
| WARN | `processing skipped` | `message_id`, `category` | The original was missing or damaged. Slice 1's event is recorded too |

Processing results and processing problems aren't logged. They are on the message and on the status screen's counts.

## `mailarkiv parse-worker`

Internal. The server starts it; operators never run it, and the usage text doesn't list it.

```text
mailarkiv parse-worker
```

It reads one request at a time on standard input and writes the result on standard output, in the length-prefixed frames of the [search process protocol](search-process.md#frames), with its own operations: `read`, `html`, `text`, and `attachment`. An original can be 150,000,000 bytes and a frame at most 64 MiB, so a request names the original's size in its JSON frame and the original's raw bytes follow the frame. An `attachment` answer likewise names the decoded size, and the decoded raw bytes follow it. It opens no files and no network connections. Started from a terminal, it exits with status 2 and the usage text. The server and the worker are the same binary, so the format has no version.

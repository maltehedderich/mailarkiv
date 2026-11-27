# Data Model: Find Messages Quickly and Safely

Slice 3 adds four columns to `message` and two tables, all owned by `archive`, plus three tables owned by the new `search` module. It adds an `index/` directory to the data directory, and search and processing state in server memory. Message bodies, recipients, and attachment lists aren't stored. The view, the preview, attachment downloads, and indexing read them from the original. [research.md](research.md#what-the-catalog-keeps) explains why.

Times are integer Unix milliseconds in UTC, and every table is `STRICT`, as in [Slice 1](../001-import-browse-retrieve/data-model.md). Only a table's owner writes it. Other modules read it only through the owner's functions.

## Data directory

| Path | Holds | Rules |
| ---- | ----- | ----- |
| `index/` | One directory per index generation | Created at start with mode 0700. Holds copies of message text, so it needs the same protection as the rest of the data directory |
| `index/<generation id>/` | One Tantivy index | Written only by the search process. Removed when its generation is discarded. At start, any directory here that names no row in `search_generation` is removed |

Backups can skip `index/`. A missing generation directory at start leads to an automatic build, as below.

## Migration 0003

`internal/sqlitedb/migrations/0003_search_preview.sql` runs in one transaction, through the runner Slice 2 added, with foreign keys off and `foreign_key_check` before commit.

1. Rebuild `message` as `message_new` with the new columns. Copy every row. Set `first_archived_at` to the earliest `observed_at` among the message's entries, `list_date` to the same value, and `processing_state` to `not_processed`. Drop the old table, rename the new one, and create its indexes.
2. Drop `message_by_latest_observation`. The list now uses `message_list_order`.
3. Create the other new tables.

Every message archived before Slice 3 starts Not processed yet and is processed in the background (FR-001).

## Tables

### `message` (owner: `archive`), new columns

| Column | Type | Rules |
| ------ | ---- | ----- |
| `first_archived_at` | INTEGER NOT NULL | Time of the message's first entry. Set at capture and never changed |
| `list_date` | INTEGER NOT NULL | Set to `first_archived_at` at capture. Processing sets it once, by the [list date rule](#list-date) |
| `processing_state` | TEXT NOT NULL | One of the [processing states](#processing-states). `not_processed` at capture |
| `processed_seq` | INTEGER | NULL exactly when `processing_state` is `not_processed`. Assigned as the highest existing value plus 1 in the transaction that records the result, so values have no gaps. Unique |

Indexes:

| Name | On | Serves |
| ---- | -- | ------ |
| `message_list_order` | `(list_date DESC, first_archived_at DESC, id DESC)` | The message list (FR-012) |
| `message_with_problems` | The same columns, `WHERE processing_state IN ('processed_with_problems', 'unreadable')` | The problem list (FR-013) and its counts |
| `message_by_processed_seq` | `processed_seq`, unique, `WHERE processed_seq IS NOT NULL` | The indexer's reads after a cursor |
| `message_not_processed` | `id DESC`, `WHERE processing_state = 'not_processed'` | Processing's next messages, newest capture first |

The capture commit sets `first_archived_at` and `list_date` in its existing `INSERT INTO message`. A message captured again keeps both.

Counts on the status screen:

| Count | How |
| ----- | --- |
| Not processed yet | `COUNT(*)` of `message` minus the highest `processed_seq`. Both come from the same read transaction |
| Processed with problems, Couldn't be read | `COUNT(*)` through `message_with_problems`, grouped by state |
| Waiting to be indexed | The highest `processed_seq` minus the active or building generation's `cursor` |

### `message_summary` (owner: `archive`)

One row for each message that is `processed` or `processed_with_problems`, written in the same transaction as its state. A message that Couldn't be read has no row.

| Column | Type | Rules |
| ------ | ---- | ----- |
| `message_id` | INTEGER PRIMARY KEY | REFERENCES `message` |
| `sender_name` | TEXT | The first From address's display name, decoded. NULL when there is none, or when it equals the address ignoring case (FR-008). At most 500 characters |
| `sender_address` | TEXT | The first From address. NULL when the message has no From header. At most 320 characters. When From can't be parsed as addresses, NULL, with the decoded header text in `sender_name` and a malformed header problem |
| `more_senders` | INTEGER NOT NULL | Count of further From addresses, for "and N more" (FR-010) |
| `subject` | TEXT | Decoded. NULL when the message has no Subject header. At most 1,000 characters |
| `date_header` | TEXT | The Date header's decoded text, at most 200 characters. NULL when there is none |
| `date_reason` | TEXT | NULL when `list_date` is the Date header's time. Otherwise `missing`, `unreadable`, or `ahead` |
| `has_attachments` | INTEGER NOT NULL | 1 when the message lists an attachment that isn't an [embedded image](#attachments), else 0 |

The character limits apply to rows. The view and preview read the full values from the original (FR-011).

### `message_problem` (owner: `archive`)

| Column | Type | Rules |
| ------ | ---- | ----- |
| `message_id` | INTEGER NOT NULL | REFERENCES `message` |
| `position` | TEXT NOT NULL | The [part number](#part-numbers), or an empty string for the whole message |
| `declared_type` | TEXT | The part's declared media type, lowercased, without parameters. At most 255 characters. NULL when the part declares none |
| `kind` | TEXT NOT NULL | One of the [problem kinds](#problem-kinds) |

Primary key `(message_id, position, kind)`, so one problem appears once per part. Rows are written with the message's result and never change.

### `search_generation` (owner: `search`)

One row for each index that exists or is being built.

| Column | Type | Rules |
| ------ | ---- | ----- |
| `id` | INTEGER PRIMARY KEY | Also the directory name under `index/`. Never reused |
| `role` | TEXT NOT NULL | `active` or `building`. A partial unique index allows at most one of each |
| `schema_version` | INTEGER NOT NULL | 1 in this slice. The search process refuses to open another version |
| `cursor` | INTEGER NOT NULL DEFAULT 0 | The highest `processed_seq` saved in this index. Written only after the search process confirms a commit |
| `rebuild_id` | INTEGER | REFERENCES `search_rebuild`. NULL for a build that started because no index existed |
| `created_at` | INTEGER NOT NULL | |

Transitions, each one SQLite transaction:

| Event | Change |
| ----- | ------ |
| Start with no row, or the active generation's directory is missing | Delete any rows. Insert a `building` row with no rebuild. Search shows Building |
| An administrator starts a rebuild | Insert `search_rebuild` and a `building` row that references it |
| A building generation's cursor reaches the highest `processed_seq` | Delete the `active` row, if any. Set the `building` row to `active`. End its rebuild as `finished`, and delete `search_needs_rebuild`. Then the search process closes the old index and its directory is removed |
| A rebuild fails, as defined under [Bounds](#bounds) | Delete its `building` row and end its rebuild as `failed`. The directory is removed |

### `search_rebuild` (owner: `search`)

| Column | Type | Rules |
| ------ | ---- | ----- |
| `id` | INTEGER PRIMARY KEY | |
| `started_by` | INTEGER NOT NULL | REFERENCES `account` |
| `started_at` | INTEGER NOT NULL | |
| `ended_at` | INTEGER | NULL while it runs |
| `outcome` | TEXT | NULL while it runs, then `finished` or `failed`. A restart doesn't end a rebuild (FR-036) |

### `search_needs_rebuild` (owner: `search`)

At most one row, present while the index is marked Needs rebuild. It survives restarts, and only a finished rebuild deletes it (FR-040).

| Column | Type | Rules |
| ------ | ---- | ----- |
| `id` | INTEGER PRIMARY KEY | `CHECK (id = 1)` |
| `category` | TEXT NOT NULL | `search_process_stopped` after 5 crashes in 30 minutes, `index_maintenance_failed` after 3 failed runs in a row, or `index_damaged` when the search process can't open the active index |
| `since` | INTEGER NOT NULL | |

## Processing states

| Wire word | State | Set when |
| --------- | ----- | -------- |
| `not_processed` | Not processed yet | At capture. Also while the stored original is missing or damaged |
| `processed` | Processed | Every part was read, with no problem |
| `processed_with_problems` | Processed with problems | The header was read, and at least one problem was found |
| `unreadable` | Couldn't be read | The header couldn't be read, processing passed 60 s, or two workers in a row died on the message |

The only transition is from `not_processed` to one of the other three, once (FR-006). The update checks `processing_state = 'not_processed'`, so a duplicate result changes nothing.

When the original is missing or damaged, `archive.OpenVerified` records the Slice 1 failure event, and processing leaves the message `not_processed`. Processing keeps such IDs in memory and skips them until the next start, or until a capture restores an original, which clears the whole set.

## List date

Processing sets `list_date` and `date_reason`:

| Date header | `list_date` | `date_reason` |
| ----------- | ----------- | ------------- |
| Missing | `first_archived_at` | `missing` |
| Present, but `net/mail.ParseDate` fails | `first_archived_at` | `unreadable` |
| More than 24 h after `first_archived_at` | `first_archived_at` | `ahead` |
| Anything else, however far in the past | The header's time | NULL |

Messages that are Not processed yet or Couldn't be read keep `first_archived_at`. A row marks the date as the archive time whenever `date_reason` is set or the message has no summary.

## Problem kinds

| Wire word | Kind | From |
| --------- | ---- | ---- |
| `unknown_charset` | Unknown character set | enmime, see [research](research.md#problem-kinds) |
| `invalid_encoding` | Invalid encoding | enmime |
| `malformed_header` | Malformed header | enmime, or From that isn't an address list |
| `malformed_structure` | Malformed structure | enmime, or a worker that died twice |
| `cut_short` | Message cut short | The boundary scanner: the end came with a boundary still open |
| `too_many_parts` | Too many parts | The boundary scanner, at the 1,001st part |
| `nested_too_deeply` | Nested too deeply | The boundary scanner, at depth 21 |
| `body_cut` | Body cut at the size limit | A text or HTML body over 2,000,000 bytes |
| `took_too_long` | Processing took too long | The worker passed 60 s |

## Part numbers

Positions follow IMAP section numbers (RFC 3501, section 6.4.5): the parts of a multipart are `1`, `2`, `3`, a part inside part 2 is `2.1`, and a message that isn't multipart has one part, `1`. The worker numbers the parts as it walks enmime's tree, so the numbers don't depend on enmime's own IDs.

## Attachments

Read from the original each time; not stored.

| Field | Rule |
| ----- | ---- |
| `position` | Part number. Names the part in the download address |
| `name` | Decoded from `Content-Disposition` `filename`, or else `Content-Type` `name`. NULL when neither is given |
| `declared_type` | Lowercased media type without parameters, as declared |
| `size` | Decoded bytes |
| `damaged` | True when the part has an encoding error |
| `kind` | `message` for `message/rfc822`, `embedded_image` for an `image/*` part whose `Content-ID` the HTML body references through `cid:`, otherwise `file` |

Every part other than the two displayed bodies is listed, including multipart containers' children that are signatures, encrypted parts, and TNEF files (FR-031). Containers themselves aren't listed.

A download's file name:

1. Start with `name`.
2. Remove C0 and C1 control characters, the bidirectional controls U+061C, U+200E, U+200F, U+202A to U+202E, and U+2066 to U+2069, and `/` and `\`.
3. Trim spaces and dots from both ends.
4. If nothing is left, use `attachment-<position>`.
5. For `kind` `message`, append `.eml` unless the name already ends in it, ignoring case.
6. Shorten to 200 bytes on a character boundary, keeping the extension.

## Index documents

One document per message that is `processed` or `processed_with_problems`. [research.md](research.md#matching) lists its fields. A message that Couldn't be read gets no document and is skipped when the cursor passes it. If its original is missing or damaged at indexing time, the document is built from `message_summary` alone, without text or recipients, and the Slice 1 failure event is recorded.

## Search and indexing states

Derived in memory from the search process's state, the generation rows, `search_needs_rebuild`, and free space. The first matching row wins.

| Search state | Wire word | When |
| ------------ | --------- | ---- |
| Needs rebuild | `needs_rebuild` | `search_needs_rebuild` has a row |
| Unavailable | `unavailable` | The search process isn't running |
| Available | `available` | Otherwise |

| Indexing state | Wire word | When |
| -------------- | --------- | ---- |
| Stopped | `stopped` | Search is Unavailable, or Needs rebuild with no rebuild running |
| Paused | `paused` | Free space fell below 2 GiB and hasn't reached 3 GiB since |
| Rebuilding | `rebuilding` | A `building` generation has a rebuild |
| Building | `building` | A `building` generation has no rebuild |
| Catching up | `catching_up` | The highest `processed_seq` is above the active cursor |
| Up to date | `up_to_date` | Otherwise |

Searches go to the active generation, or to the building one when no active one exists. While search is Needs rebuild, only a rebuild's generation receives batches, so a rebuild started from that state shows Rebuilding with its progress. FR-044's table doesn't say which state applies then, and Rebuilding is the one that shows progress.

## Failure categories

New categories in `opstatus`. Each goes to the log at error level with its time, category, and the IDs below, and to the status screen.

| Wire word | Category | IDs in the event |
| --------- | -------- | ---------------- |
| `search_process_stopped` | Search process stopped | Exit status or signal number |
| `search_process_did_not_start` | Search process didn't start | A closed cause: `missing`, `not_executable`, `bad_hello` |
| `search_process_did_not_answer` | Search process didn't answer | The operation name |
| `index_write_failed` | Index write failed | Generation ID, the first and last message ID of the batch, and a closed cause: `no_space`, `permission_denied`, `other` |
| `index_maintenance_failed` | Index maintenance failed | Generation ID |
| `index_needs_rebuild` | Index needs rebuild | The `search_needs_rebuild` category |
| `low_disk_space` | Low disk space | Free bytes |
| `rebuild_failed` | Rebuild failed | Rebuild ID, generation ID |

A failure event gains `repeats` and `last_at`. For these eight categories, a failure of the same category as the newest event, with no success of that kind in between, increments `repeats` and sets `last_at` instead of adding an event. A success is a started process, an answered call, a committed batch, a finished maintenance run, or free space at 3 GiB, depending on the category: they end runs of `search_process_did_not_start`, `search_process_did_not_answer`, `index_write_failed`, `index_maintenance_failed`, and `low_disk_space` in that order. `search_process_stopped`, `index_needs_rebuild`, and `rebuild_failed` have no success, so only another category ends their runs. A process that crashes at every start then stays one event. Slice 1 and 2 categories are unchanged.

The log line `processing_worker_stopped`, with the message ID and exit status, is not a failure event. Processing problems aren't system problems (spec, Assumptions).

## Problem count

The count on every signed-in page is:

```text
the sum of repeats over all failure events (1 for an event that never repeated)
+ unreferenced originals
+ problem sources                                   (Slices 1 and 2)
+ 1 when search is Unavailable or Needs rebuild
+ 1 when indexing is Paused
```

Messages that are Processed with problems or Couldn't be read don't count (FR-047).

## State in memory

| Owner | State |
| ----- | ----- |
| `processing` | The two worker pools. The set of message IDs skipped for a missing or damaged original |
| `search` | The search process's state, its start time, its restart count since the server started, the times of its crashes in the last 30 minutes, the current restart wait, failed maintenance runs in a row, commits since the last maintenance, the paused flag, and the last free space reading |

A restart clears all of it. The count of maintenance failures starts again at 0, while Needs rebuild survives in `search_needs_rebuild`.

## Bounds

| Bound | Value | Requirement |
| ----- | ----- | ----------- |
| Processing time per message | 60 s, then `SIGKILL` | FR-005 |
| Parts per message | 1,000 | FR-005 |
| Nesting depth | 20 | FR-005 |
| Bytes per text or HTML body | 2,000,000, cut on a character boundary | FR-005, FR-029 |
| Embedded image bytes shown per preview | 10,000,000 decoded | FR-027 |
| Background workers, interactive workers | 2 and 2 | Plan |
| Wait for an interactive worker | 30 s, then 503 `busy` | Plan |
| Results per page | 50 | FR-018 |
| Search time in the search process | 10 s | FR-021 |
| Index batch | 500 messages or 32 MiB, whichever comes first | FR-036 |
| Commit | When the queue is empty, after 10,000 messages, or 20 s after the first uncommitted one | FR-020 |
| Maintenance | After 50 commits or 10 minutes | Plan |
| Free space | Pause below 2 GiB, resume at 3 GiB, rebuild needs over 2 GiB plus the active index | FR-037, FR-038 |
| Restart wait | 1 s, doubling to 60 s, reset after 10 minutes running | FR-039 |
| Wait before a failed batch is sent again | 1 s, doubling to 60 s, reset by a committed batch | FR-046 |
| Rebuild failed | Its index can't be created, or one batch for it fails 5 times in a row. Low disk space and restarts don't count | FR-046 |
| Needs rebuild | 5 crashes in 30 minutes, or 3 failed maintenance runs in a row | FR-040 |
| Indexed word length | 100 bytes | Plan |
| Frame size | 64 MiB | Plan |

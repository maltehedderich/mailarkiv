# Data Model: Import, Browse, and Retrieve One Email

Slice 1 keeps data in three places: SQLite for the catalog and accounts, plain files for originals, and server memory for what the status screen covers since the latest start.

## Data directory

```text
<data-dir>/            mode 0700. The binary sets umask 077 at startup.
├── lock               flock held by `serve`
├── catalog.db         plus catalog.db-wal and catalog.db-shm
├── tmp/<random>       uploads in progress
└── blobs/ab/cd/<hash> stored originals, named by lowercase hex SHA-256
```

Tested: without the umask, SQLite creates `catalog.db` world-readable (`0644`).

`tmp/` and `blobs/` must be on one filesystem, so a rename is atomic. Files in `blobs/` whose names aren't a valid hash at the matching path are logged and otherwise ignored, because no supplied name ever reaches this directory.

## Connections

- One writer connection: `journal_mode=WAL`, `synchronous=FULL`, `foreign_keys=ON`, `busy_timeout=5000`, `_txlock=immediate`.
- A read pool: the same settings plus `query_only=ON`.
- The CLI runs as a separate process against the same file. `busy_timeout` covers the overlap.

Migrations are numbered SQL files in `internal/sqlitedb/migrations/`, applied in one transaction each and tracked with `PRAGMA user_version`. A database newer than the binary refuses to open (FR-026).

## Tables

Times are integer Unix milliseconds in UTC. Table ownership follows constitution principle II: only the owner writes.

### `account` (owner: `identity`)

| Column | Type | Rules |
| ------ | ---- | ----- |
| `id` | INTEGER PRIMARY KEY | |
| `email` | TEXT NOT NULL UNIQUE | `strings.ToLower` of the address given at creation. The only stored form (FR-044) |
| `password_hash` | TEXT NOT NULL | Argon2id in PHC format (FR-002) |
| `created_at` | INTEGER NOT NULL | |

Address rule at creation (FR-044): exactly one `@`, text on both sides, and no whitespace. Sign-in, the sign-in limit, and `set-password` lowercase the submitted address before using it. Password rule (FR-002): at least 12 characters, counted as Unicode code points.

### `session` (owner: `identity`)

| Column | Type | Rules |
| ------ | ---- | ----- |
| `token_hash` | BLOB PRIMARY KEY | SHA-256 of the cookie token |
| `account_id` | INTEGER NOT NULL REFERENCES `account` | |
| `created_at` | INTEGER NOT NULL | |
| `expires_at` | INTEGER NOT NULL | `created_at` plus 12 hours, never extended |

A session is valid only if its row exists and `expires_at` is in the future. Sign-out deletes the row. Setting a password deletes all of that account's rows in the same transaction (FR-005). Each sign-in also deletes expired rows.

### `sign_in_failure` and `sign_in_block` (owner: `identity`)

| Table | Column | Type | Rules |
| ----- | ------ | ---- | ----- |
| `sign_in_failure` | `address_key` | BLOB NOT NULL | SHA-256 of the lowercased submitted address, indexed with `failed_at` |
| `sign_in_failure` | `failed_at` | INTEGER NOT NULL | |
| `sign_in_block` | `address_key` | BLOB PRIMARY KEY | |
| `sign_in_block` | `blocked_until` | INTEGER NOT NULL | |

Sign-in follows these steps, the same for any address (FR-004):

1. If an unexpired block exists, refuse without checking the password. The attempt doesn't count and doesn't extend the block.
2. Check the password, against a dummy hash if the account doesn't exist.
3. On success, delete the address's failures and create a session.
4. On failure, delete failures older than 15 minutes, add one, and count. At 10, write a block for 15 minutes from now and delete the address's failures.

Setting a password deletes the address's failures and block in its transaction.

### `message` (owner: `archive`)

| Column | Type | Rules |
| ------ | ---- | ----- |
| `id` | INTEGER PRIMARY KEY AUTOINCREMENT | The internal message ID in URLs, failure reports, and logs. Never reused, so a stale reference can't point to a different message after a deletion in Slice 6 |
| `content_hash` | TEXT NOT NULL UNIQUE | Lowercase hex SHA-256. Identifies the message (FR-016) |
| `size` | INTEGER NOT NULL | Bytes, from 1 to 150,000,000 |
| `latest_observation_id` | INTEGER REFERENCES `source_observation` | Set in the same transaction that creates the row, so committed rows are never NULL. Indexed descending for the list |

`message` and `source_observation` reference each other, which forces the column to be nullable while the row is being created. The list's inner join would hide a row with a NULL, and the capture test asserts none exists.

### `source_observation` (owner: `archive`)

| Column | Type | Rules |
| ------ | ---- | ----- |
| `id` | INTEGER PRIMARY KEY | Rising order gives a stable "latest" even within one millisecond |
| `message_id` | INTEGER NOT NULL REFERENCES `message` | Indexed with `id` |
| `source_type` | TEXT NOT NULL | `CHECK (source_type IN ('manual_import'))`. Slices 2 and 4 widen it by migration |
| `observed_at` | INTEGER NOT NULL | |
| `account_email` | TEXT NOT NULL | The account's lowercase address, copied when the import is recorded |
| `file_name` | TEXT | NULL means no name was supplied. Stored as received, in valid UTF-8 |

Observations are append-only in this slice. Copying `account_email` keeps `archive` out of `identity`'s tables, and the record stays correct if accounts change in a later slice. Slices 2 and 4 make the column nullable for sources with no account.

### List query

```sql
SELECT m.id, m.content_hash, m.size, o.file_name, o.observed_at,
       (SELECT COUNT(*) FROM source_observation c WHERE c.message_id = m.id)
FROM message m
JOIN source_observation o ON o.id = m.latest_observation_id
ORDER BY m.latest_observation_id DESC
LIMIT 50 OFFSET ?;
```

## In memory (owner: `opstatus`)

These reset at every start, which is what FR-027 and User Story 5 scenario 7 specify. Everything here also goes to the server log, so a restart loses nothing an operator can't still read.

| Entity | Fields |
| ------ | ------ |
| `Start` | Start time |
| `RecoveryResult` | Finish time, temp files removed, temp files not removed, unreferenced originals found, duration |
| `UnreferencedOriginal` | Content hash, size. Removed when an import of those bytes commits (FR-018) |
| `FailureEvent` | Time, category, optional message ID, optional content hash, `restored` flag |

`restored` is true when an import has already replaced the stored original. The status screen then says the copy was restored, in place of the next step.

The problem count is the number of failure events plus the number of unreferenced originals (FR-039). Failure events can only come from signed-in actions or startup, so the list has no cap.

`FailureEvent.category` is one of `storage_full`, `storage_write_failed`, `catalog_write_failed`, `stored_original_missing`, `stored_original_damaged`, or `temp_file_not_removed` (FR-038). The category's words and next step live in the frontend.

None of these structures has a field for a subject, a message address, a body, a file name, or an account address. That is how FR-029 holds at the type level. The marker test in SC-006 checks it from outside.

## Import outcome

Capture reads whether the `message` row exists, then `ensureBlob` checks the blob. The order matters: two imports of the same new bytes can both find no blob, and the later commit then finds the other's row. Reading the row first separates that race from a blob that really went missing. The transaction then reports whether the row exists at commit.

| Row before the blob check | Row at commit | Blob at the check | Outcome | Failure event |
| ------------------------- | ------------- | ----------------- | ------- | ------------- |
| No | No | Absent | Archived | None |
| No | No | Present and matching (unreferenced) | Archived, and it leaves the unreferenced list | None |
| No | No | Present and not matching, or unreadable | Archived, blob replaced | `stored_original_damaged`, restored |
| No | Yes | Absent | Already archived | None |
| Either | Yes | Present and matching | Already archived | None |
| Yes | Yes | Absent | Restored | `stored_original_missing`, restored |
| Either | Yes | Present and not matching, or unreadable | Restored | `stored_original_damaged`, restored |

Every row adds exactly one source observation.

## Capture states

```text
receiving ──body ended early──────────► dropped: temp removed, no event, client sees Unconfirmed
    │
    ├─empty, no header line───────────► refused: nothing written, rest of body discarded
    ├─over the limit──────────────────► refused: temp removed
    ├─write or fsync failed───────────► failed (storage_full or storage_write_failed): temp removed
    ▼
temp fsynced
    │
    ├─catalog read failed─────────────► 500 internal_error: temp removed, no event, client sees Unconfirmed
    ├─rename or directory fsync failed► failed (storage_write_failed): temp removed
    ▼
blob durable
    │
    ├─commit failed───────────────────► failed (catalog_write_failed): blob kept, temp removed
    ▼
committed ──► response sent
```

A crash at any point before `committed` leaves at most a temp file, which recovery removes, or an unreferenced blob, which recovery lists. A crash between `committed` and the response leaves a listed message and an Unconfirmed result, and importing again shows Already archived.

## Recovery

Runs in `serve` after the catalog opens and before the listener starts. Running it again gives the same result (FR-025).

1. Remove each entry in `tmp/`. A failure adds a `temp_file_not_removed` event, and startup continues (User Story 5 scenario 5).
2. Walk `blobs/`. For each valid blob, look up the hash in `message`. A miss adds an `UnreferencedOriginal`. Blobs are never hashed here, and none is ever deleted (FR-024).
3. Log the result with its duration.

Unusable storage or an unusable catalog stops the start with a log entry naming which one failed (FR-026). A failed write probe in `tmp/` counts as unusable storage.

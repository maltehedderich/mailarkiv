# Data Model: Archive an IMAP Mailbox Incrementally

Slice 2 adds three tables owned by `collect`, one owned by `archive`, and three columns on `source_observation`. It adds a key file outside the data directory, and sync state in server memory. The Slice 1 layout of the data directory doesn't change.

Times are integer Unix milliseconds in UTC, and every table is `STRICT`, as in [Slice 1](../001-import-browse-retrieve/data-model.md). Only a table's owner writes it. Other modules read it only through the owner's functions.

## Key file

One per installation, at a path the operator chooses outside the data directory. `serve` reads it once at start.

| Property | Rule |
| -------- | ---- |
| Content | `mailarkiv-key-v1:`, then 32 random bytes in standard padded base64, then one newline. Exactly this, at most 1 KiB |
| Mode at creation | 0600, owned by whoever ran `key create` |
| Location | Not equal to or under the data directory, after both paths are made absolute and symbolic links are resolved |

A missing, unreadable, invalid, or misplaced key stops `serve` with cause `key`. With no `--key-file`, `serve` runs with collection unavailable: see [source states](#source-states).

### Stored passwords

`imap_source.sealed_password` holds AES-256-GCM output from `cipher.NewGCMWithRandomNonce`: a 12-byte nonce, the ciphertext, and a 16-byte tag. The additional data is:

```text
"mailarkiv-imap-password-v1" 0x00 lowercase(host) 0x00 lowercase(user_name)
```

A password that fails to open, for any reason, is unreadable. The plaintext exists only in memory, during a sign-in or while a request that carries it is handled.

## Migration 0002

`internal/sqlitedb/migrations/0002_imap_collection.sql` creates the new tables and rebuilds `source_observation` in one transaction: create `source_observation_new`, copy every row with `source_type = 'manual_import'`, drop the old table, rename the new one, and create its indexes. Slice 1 catalogs hold only imports, so nothing needs filling in.

The runner changes for every migration:

1. `PRAGMA foreign_keys = OFF`, outside the transaction, since SQLite ignores it inside one.
2. Begin, run the file, and set `user_version`.
3. `PRAGMA foreign_key_check`. Any row rolls the migration back, and the start fails with cause `catalog`.
4. Commit, then `PRAGMA foreign_keys = ON`, and read it back as 1.

`message.latest_observation_id` keeps its reference text, `REFERENCES source_observation`, which then names the rebuilt table. [research.md](research.md#widening-source_observation) has the tests.

## Tables

### `imap_source` (owner: `collect`)

| Column | Type | Rules |
| ------ | ---- | ----- |
| `id` | INTEGER PRIMARY KEY | The source's internal ID in URLs and logs. Rows are never deleted, so IDs are never reused |
| `name` | TEXT NOT NULL | 1 to 100 characters, counted as Unicode code points, after trimming spaces |
| `host` | TEXT NOT NULL | Lowercase. Fixed after saving (FR-005) |
| `port` | INTEGER NOT NULL | 1 to 65535 |
| `security` | TEXT NOT NULL | `tls` or `starttls` |
| `user_name` | TEXT NOT NULL | As entered, because servers may compare it exactly. Fixed after saving |
| `user_key` | TEXT NOT NULL | `strings.ToLower(user_name)`, for the duplicate rule |
| `sealed_password` | BLOB | NULL exactly when `removed_at` is set |
| `created_at` | INTEGER NOT NULL | |
| `paused_at` | INTEGER | Set while paused |
| `sign_in_rejected_at` | INTEGER | Set when the server rejected sign-in. Cleared by a saved password or by Sync now |
| `failed_attempts` | INTEGER NOT NULL DEFAULT 0 | Failed syncs in a row. Sets the retry wait |
| `failing_since` | INTEGER | The first failure in the current run. NULL exactly when `failed_attempts` is 0 |
| `last_failure_category` | TEXT | One of the [sync categories](#failure-categories). NULL exactly when `failed_attempts` is 0 |
| `last_failure_at` | INTEGER | NULL exactly when `failed_attempts` is 0 |
| `last_server_reply` | TEXT | The last failure's `NO` or `BAD` text, at most 1,000 characters, keeping the start and end. NULL when there was none |
| `last_success_at` | INTEGER | End of the latest successful sync |
| `last_success_archived` | INTEGER | Messages that sync stored |
| `removed_at` | INTEGER | Set when an administrator removed the source |

A partial unique index on `(host, user_key) WHERE removed_at IS NULL` refuses a second active source for the same mailbox (FR-004). A removed source keeps its row, so history entries still name it (FR-041), and the same mailbox can be added again as a new source.

Removing a source sets `removed_at`, sets `sealed_password` to NULL, and deletes its rows in `imap_message_problem`, in one transaction (FR-029).

### `imap_folder` (owner: `collect`)

| Column | Type | Rules |
| ------ | ---- | ----- |
| `id` | INTEGER PRIMARY KEY | Collection entries reference it |
| `source_id` | INTEGER NOT NULL REFERENCES `imap_source` | |
| `name` | TEXT NOT NULL | Decoded from modified UTF-7, as mail clients show it. Valid UTF-8, 1 to 1,000 bytes. Unique with `source_id` |
| `chosen` | INTEGER NOT NULL | 1 while the source collects it, 0 after an administrator removes it |
| `uid_validity` | INTEGER | NULL until the folder is first opened |
| `last_uid` | INTEGER NOT NULL DEFAULT 0 | The checkpoint: every UID up to this one, under `uid_validity`, is archived or recorded in `imap_message_problem`. 0 while `uid_validity` is NULL |
| `renumbered_at` | INTEGER | The latest change of `UIDVALIDITY` |
| `unavailable_since` | INTEGER | Set when `EXAMINE` was refused. Cleared when it succeeds |

Folder rows are never deleted. Removing a folder sets `chosen` to 0 and keeps its checkpoint. Adding the same name again sets it back to 1, and collection continues from the checkpoint, which still collects every message in the folder. A source syncs its chosen folders in `id` order.

### `imap_message_problem` (owner: `collect`)

A message waiting for retry or not archived. It holds no message content.

| Column | Type | Rules |
| ------ | ---- | ----- |
| `folder_id` | INTEGER NOT NULL REFERENCES `imap_folder` | |
| `uid_validity` | INTEGER NOT NULL | The folder's numbering when this was recorded |
| `uid` | INTEGER NOT NULL | |
| `kind` | TEXT NOT NULL | `waiting_for_retry`, `too_large`, or `removed` |
| `size` | INTEGER | Bytes from `RFC822.SIZE` or the literal. NULL when the server reported neither |
| `received_at` | INTEGER | From `INTERNALDATE`. NULL when the server reported none |
| `recorded_at` | INTEGER NOT NULL | When this kind was recorded |

Primary key `(folder_id, uid_validity, uid)`. A `waiting_for_retry` row becomes `removed`, or is deleted once the message is stored.

### `source_observation` (owner: `archive`), widened

| Column | Type | Rules |
| ------ | ---- | ----- |
| `id` | INTEGER PRIMARY KEY | Unchanged |
| `message_id` | INTEGER NOT NULL REFERENCES `message` | Unchanged |
| `source_type` | TEXT NOT NULL | `manual_import` or `imap_collection` |
| `observed_at` | INTEGER NOT NULL | Import time, or collection time |
| `account_email` | TEXT | Imports only, and required for them. Was NOT NULL |
| `file_name` | TEXT | Imports only. NULL means no name was supplied |
| `imap_source_id` | INTEGER REFERENCES `imap_source` | New. Collections only, and required for them |
| `imap_folder_id` | INTEGER REFERENCES `imap_folder` | New. Collections only, and required for them |
| `received_at` | INTEGER | New. Collections only. NULL when the server reported no time |

`CHECK` constraints tie each column to its type: `(source_type = 'manual_import') = (account_email IS NOT NULL)`, the same for `imap_source_id` and for `imap_folder_id` with `imap_collection`, `file_name IS NULL OR source_type = 'manual_import'`, and `received_at IS NULL OR source_type = 'imap_collection'`.

| Index | Columns | Purpose |
| ----- | ------- | ------- |
| `source_observation_by_message` | `(message_id, id)` | History and the entry count. Unchanged |
| `source_observation_once_per_folder` | UNIQUE `(imap_folder_id, message_id) WHERE imap_folder_id IS NOT NULL` | At most one entry per folder and message (FR-014) |
| `source_observation_by_source` | `(imap_source_id, message_id) WHERE imap_source_id IS NOT NULL` | Whether an entry is the source's first for a message |

`collect` passes `imap_source_id` and `imap_folder_id` to archive. Archive can't check that the folder belongs to the source, so a test in `collect` does.

### `collected_message_count` (owner: `archive`), new

| Column | Type | Rules |
| ------ | ---- | ----- |
| `imap_source_id` | INTEGER PRIMARY KEY REFERENCES `imap_source` | |
| `messages` | INTEGER NOT NULL | Distinct messages with at least one entry from this source |

### Collection commit

`StoreCollected` runs the Slice 1 capture up to the blob, then commits in one transaction:

1. `INSERT INTO message … ON CONFLICT (content_hash) DO NOTHING`, then read the message's ID.
2. Read whether `source_observation_by_source` has an entry for this source and message.
3. `INSERT INTO source_observation … ON CONFLICT (imap_folder_id, message_id) WHERE imap_folder_id IS NOT NULL DO NOTHING`.
4. If step 3 added a row, set `message.latest_observation_id` to it. If step 2 found none, add 1 to `collected_message_count`.

The [Slice 1 outcome table](../001-import-browse-retrieve/data-model.md#import-outcome) applies unchanged, except that an entry is added only when step 3 adds one. A collected message's outcome isn't shown to anyone. Restored outcomes still record a `restored` failure event.

### List and history queries

Both queries also return `source_type`, `account_email`, `file_name`, `imap_source_id`, `imap_folder_id`, and `received_at` for each entry. The web adapter then asks `collect` for the labels of the folders on the page, in one query: folder name, source name, host, user name, and whether the source was removed. The entry count counts every entry, imports and collections alike (FR-040).

## Source states

`collect` computes a source's state from its row and its memory, taking the first that applies (FR-032):

| State | When | Shown with |
| ----- | ---- | ---------- |
| Paused | `paused_at` is set | `paused_at` |
| Syncing | A sync is running | Archived and found counts for this sync |
| Needs attention | `sign_in_rejected_at` is set, or the password is unreadable | The category: `sign_in_rejected` or `password_unreadable` |
| Retrying | `failed_attempts` > 0 | `last_failure_category` and the next attempt's time |
| Waiting | Otherwise | The next sync's time |

The password is unreadable when the server has no key, or when opening it failed at this start or at a later sign-in. That fact lives in memory and is checked again at each start (FR-027).

| Event | Effect |
| ----- | ------ |
| Source saved | Sync at once |
| Server start, after recovery | Every source that isn't paused, removed, or in Needs attention syncs at once |
| Next sync due | Sync, for a Waiting or Retrying source |
| Sync now | Clears `sign_in_rejected_at`, then syncs at once. Refused while syncing or paused |
| Pause | Stops a running sync, then sets `paused_at` |
| Resume | Clears `paused_at`, then syncs at once |
| Password saved | Clears `sign_in_rejected_at` and the unreadable mark, then syncs at once unless paused |
| Folder added | Syncs at once unless paused |
| Remove | Stops a running sync, then removes the source |
| Sync succeeded | `failed_attempts` 0. Failure columns cleared. `last_success_at` and `last_success_archived` set. Next sync in 5 minutes |
| Sync failed | `failed_attempts` + 1. `failing_since` kept or set. Category, time, and reply set. Next attempt after min(2^(n−1), 60) minutes, where n is `failed_attempts` |
| Sign-in rejected | As a failed sync, and `sign_in_rejected_at` set. No next attempt |
| Sync stopped by pause, removal, or shutdown | Nothing recorded. It isn't a failure |

The due time isn't stored. A sync started by Sync now that fails counts as an attempt like any other.

## Failure categories

One closed set in `collect`, named as on the wire. Sync categories end a sync and go in `last_failure_category`. The others are recorded on a folder or a message, and the sync continues.

| Category | Kind | Comes from |
| -------- | ---- | ---------- |
| `server_not_reachable` | Sync | DNS failure, connection refused, no route, or another dial error |
| `secure_connection_failed` | Sync | TLS handshake or certificate error, or a STARTTLS step that failed for any reason except a timeout |
| `sign_in_rejected` | Sync | `NO` to `LOGIN`, except `NO [UNAVAILABLE]` |
| `timed_out` | Sync | The 30 second connect deadline or the 60 second idle deadline passed, or a check reached its 120 second limit |
| `server_error` | Sync | `NO` or `BAD` outside the cases in this table, `NO [UNAVAILABLE]` to `LOGIN`, `BYE`, a closed connection, a reply the client can't parse, a reply over its byte bound, or a literal announced over 150,000,000 bytes |
| `storage_full` | Sync | Capture returned this category |
| `storage_write_failed` | Sync | Capture returned this category |
| `catalog_write_failed` | Sync | Capture returned this category, the catalog couldn't be read, or the checkpoint commit failed |
| `password_unreadable` | State | No key, or the stored password didn't open. Shown as Needs attention |
| `folder_unavailable` | Folder | `NO` to `EXAMINE` |
| `message_unavailable` | Message | `UID FETCH` returned no data or a NIL body for a message still in the folder, an empty body, or a literal announced over the limit |
| `message_too_large` | Message | `RFC822.SIZE` over 150,000,000 |
| `message_removed` | Message | A message waiting for retry is no longer in the folder |

The meaning and next step of each category live in the frontend, as in Slice 1.

## Sync

A sync handles one source over one connection.

1. Open the password. On failure, mark it unreadable and stop, with a log entry and no attempt counted.
2. Connect, upgrade or handshake, and wait for the greeting, all within 30 seconds. Then `LOGIN`.
3. For each chosen folder in `id` order:
   1. `EXAMINE`. A `NO` marks the folder unavailable, and the sync moves to the next folder. Success clears `unavailable_since`.
   2. If `UIDVALIDITY` differs from the stored value, in one transaction: store the new value, set `last_uid` to 0, set `renumbered_at`, and delete the folder's `waiting_for_retry` and `too_large` rows. On first open, store the value with `last_uid` 0.
   3. If `UIDNEXT` is greater than `last_uid` + 1, run `UID SEARCH UID <last_uid+1>:*` and keep UIDs above `last_uid`.
   4. Fetch `UID RFC822.SIZE INTERNALDATE` for those UIDs in batches of 500, then handle each UID in rising order as the table below says.
   5. Do the same for the folder's `waiting_for_retry` rows under the current `UIDVALIDITY`.
4. `LOGOUT`, then record the result on the source.

Found counts the UIDs from step 3.3 plus the retry rows, added as each folder opens. Archived counts the messages that archive stored in this sync, at the moment `StoreCollected` returns. Neither is stored until the sync ends.

| Per message | Record | Checkpoint | Archived count | Sync |
| ----------- | ------ | ---------- | -------------- | ---- |
| `StoreCollected` returned Stored | Delete its retry row, if any | Moves to its UID | + 1 | Continues |
| `RFC822.SIZE` over the limit | `too_large`, a `message_too_large` failure event, and a log entry | Moves | | Continues |
| No data or NIL body, still in the folder | `waiting_for_retry`, if not already, and a log entry | Moves | | Continues |
| No data or NIL body, gone, not a retry | None | Moves | | Continues |
| No data or NIL body, gone, a retry | Becomes `removed`, with a `message_removed` failure event and a log entry | | | Continues |
| Refused, an empty body | `waiting_for_retry` | Moves | | Continues |
| Literal announced over the limit | `waiting_for_retry`, after closing the connection | Moves | | Fails with `server_error` |
| Dropped, the literal ended early | None | Stays | | Fails with `timed_out` or `server_error` |
| Failed with a storage or catalog category | None | Stays | | Fails with that category |
| The checkpoint commit fails | Rolled back | Stays | Already counted, since the message is stored | Fails with `catalog_write_failed` |

The checkpoint and any record for one message commit in one transaction. "Moves" applies to new messages only. A retry doesn't change `last_uid`.

The sync succeeds when every chosen folder was opened or found unavailable and no sync category ended it. Folder and message problems don't make it fail, so they don't slow the source's schedule.

### Server reply

When a sync fails with a `NO` or `BAD`, `last_server_reply` keeps the code and text as received, such as `[AUTHENTICATIONFAILED] Invalid credentials`. It is shown only on the source's page and in the check's answer, never in the log or on the status screen (FR-036, FR-037).

## In memory (owner: `collect`)

Per active source: whether a sync is running, its found and archived counts, the next due time, the unreadable mark with its time, the running sync's cancel function, and a wake-up channel. These reset at each start, when every source syncs at once.

## In memory (owner: `opstatus`), changed

`Category` gains `message_too_large` and `message_removed`. `FailureEvent` gains an optional source ID, set for those two categories. Their status entries name the source, never the folder (FR-037). Storage and catalog failures during collection record no event, because the source shows them.

The problem count adds one for each source that `collect` reports as a problem: Retrying, Needs attention, or with an unavailable folder or a message waiting for retry (FR-038). `opstatus` doesn't know about sources. The web adapter adds the two numbers.

## Input rules

| Field | Rule |
| ----- | ---- |
| `name` | Valid UTF-8, 1 to 100 code points after trimming spaces |
| `host` | 1 to 253 bytes: a DNS name of ASCII letters, digits, hyphens, and dots, or an IPv4 or IPv6 address without brackets. A non-ASCII name must be entered in its `xn--` form |
| `port` | Integer from 1 to 65535. The form fills 993 for TLS and 143 for STARTTLS (FR-001) |
| `security` | `tls` or `starttls` |
| `user_name` | Valid UTF-8, 1 to 1,024 bytes, no control characters |
| `password` | Valid UTF-8, 1 to 1,024 bytes (FR-030) |
| `folders` | 1 to 1,000 names, no repeats, each valid UTF-8 and 1 to 1,000 bytes |

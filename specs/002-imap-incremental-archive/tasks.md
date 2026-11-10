---

description: "Task list for Slice 2: archive an IMAP mailbox incrementally"
---

# Tasks: Archive an IMAP Mailbox Incrementally

**Input**: Design documents from `specs/002-imap-incremental-archive/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/http-api.md](contracts/http-api.md), [contracts/cli.md](contracts/cli.md), [quickstart.md](quickstart.md)

**Tests**: Included. Each story has an independent test, SC-002 to SC-011 need checks, and the plan names a check and a layer for each of its 22 credible failures.

**Organization**: Phases follow the plan's [delivery order](plan.md#delivery-order). Each user story phase is one plan step. All six stories are required before this slice is done.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: The user story the task serves (US1 to US6)

## Rules for every task

- Read [the constitution](../../.specify/memory/constitution.md) and [AGENTS.md](../../AGENTS.md) first. Read [frontend/DESIGN.md](../../frontend/DESIGN.md) before touching a screen.
- Work on branch `002-imap-incremental-archive`. Open a pull request at each checkpoint, and merge it when CI passes.
- `main` stays releasable. Until T120 removes it, collection runs and the source routes exist only under `serve --preview-collection`.
- Write each phase's test tasks first and watch them fail. Expected values come from the spec, the data model, or a contract, never from running the code (constitution V).
- Run `mise run check` at each checkpoint. It must exit 0 before the next phase starts.
- Use real SQLite, real files, and real modules. Use doubles only for the mail server (`internal/imaptest`), the disk's errors (the `faults` build), and the clock. Never skip a test or add a retry.
- Migration 0002 is final once merged. A catalog at version 2 never runs it again, so a later schema change needs migration 0003.
- `collect` tests shorten the time limits, byte bounds, and schedule through unexported options. A unit test asserts each production value. Process tests can't shorten them.
- Fixtures hold synthetic mail only, with addresses under `example.test`. Leak tests use marker strings such as `MARKER-PW-4f1c`.
- Go tests send the session cookie by hand, as in Slice 1.
- Code comments never cite spec, plan, or task IDs (AGENTS.md).
- "Extend" means add to a file that exists or that an earlier task created. "Change" means the specified behavior changed. Say so in the commit message.

## How this list relates to the plan

| Phase | Plan step | Finishes | Shown by |
| ----- | --------- | -------- | -------- |
| 1 Setup | Part of 1 | ADR 0008, the `ARCHITECTURE.md` line | Link check |
| 2 Foundational | Part of 1 | Migration 0002, the key package, the test double, module rules | `TestMigrateSlice1Catalog`, `TestSealBoundToSource`, `archtest` |
| 3 US1 | 1 Walking skeleton | US1 scenarios 3, 5, 6, and 9. Scenario 2 through the API | US1's independent test without the 6 minute part, `TestCollectionChangesNothing` |
| 4 US2 | 2 Exactly once | US2 scenarios 1 to 8 and 10 | `TestCollectCrashMatrix`, `TestEntryAndCopyCounts` (SC-002, SC-003) |
| 5 US3 | 3 Failures and timeouts | US3, except Sync now in scenario 4. US1 scenarios 4 and 7 through the API. US2 scenario 9 | `TestStallAtEachStep`, `TestSchedule`, `TestShutdownDuringSync` (SC-005) |
| 6 US4 | 4 Passwords and the key | US4 scenarios 1, 4, 5, 9, and 10, and scenario 6 up to saving the password again | `TestNoPasswordInDataDir` (SC-006) |
| 7 US6 | 5 Management | US6. US4 scenarios 2, 3, 6, 7, and 8. US3 scenario 4. US1 scenario 1 through the API | `TestEveryRouteDeniesWithoutSession` (SC-007) |
| 8 US5 | 6 Visibility | US5 | `TestProgressCountsDurable`, `TestNoMarkersInLogOrStatus` (SC-004, SC-010) |
| 9 Interface | 7 Interface completion | US1 scenarios 1, 2, 7, and 8 on screen. FR-007, FR-039, FR-042 | `sources.spec.ts` (SC-011) |
| 10 Release | 8 Release | FR-043, FR-044, and collection without the flag | Quickstart scenarios 1 to 9 (SC-001, SC-008, SC-009) |

US6 comes before US5, as in the plan. Both are P2, and building management first means the visibility screens show every state, including Paused.

Writing these tasks exposed ten places where the plan's order or contracts don't hold. T004 records them.

| Change | Why |
| ------ | --- |
| The connection wrapper's deadlines land in Phase 3, with the first dial. Phase 5 adds the byte bounds, the stall test at every step, and the full error classification | Constitution III requires a timeout on every network call, and Phase 3 already signs in to mail servers, even if only under the hidden flag |
| The display rule for any text lands in Phase 3, not step 7 | List rows and the history drawer show source and folder names from Phase 3 |
| `frontend/DESIGN.md` gains the list row and history drawer changes in Phase 3, not step 6 | The components change in Phase 3, and AGENTS.md puts the design guide before the screen |
| `GET /api/sources` and `GET /api/sources/{id}` land in Phase 3 | The walking skeleton's process test watches the first sync through them. Fields whose behavior arrives later read null or zero until then, which is true at that point |
| The per-source counter is written in Phase 3, with the rest of the collection commit | The data model defines one commit. Phase 4 verifies the counter with `TestCollectedCounts` |
| The two new status categories reach the client in Phase 4, when the server first records them | `parseFailure` in `frontend/src/lib/api.ts` refuses an unknown category, so the status screen would fail to load under `--preview-collection`. Constitution III changes consumers together |
| `POST /api/imports` answers its `message` in the new list item shape | The answer shares the list's encoder in `internal/web/messages.go` and the client's `parseMessage`. The contract named only `GET /api/messages` |
| The source page's refresh while syncing lands in Phase 8, not step 7 | T098 chooses the interval and T100 builds the page that refreshes. Phase 9's browser test watches it |
| Each log entry lands with the record or failure it reports, not in step 6 | Phases 4, 5, and 6 write the entries for their events. Phase 8's marker test checks them all |
| `frontend/DESIGN.md` also changes in step 8 | T121 adds the Sources navigation item. The plan's table of document changes named only steps 6 and 7 |

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: The branch and the documents that step 1 changes.

- [X] T001 Create branch `002-imap-incremental-archive`, renamed from the planning branch `docs/imap-first-slice-order` so it keeps the roadmap commit. Commit `specs/002-imap-incremental-archive/` on it as `docs(spec): add Slice 2 spec, plan, and tasks`
- [X] T002 [P] Write `docs/adr/0008-imap-password-encryption.md` in the style of `docs/adr/0005-password-hashing.md`. Cover AES-256-GCM through `cipher.NewGCMWithRandomNonce`, the additional data `"mailarkiv-imap-password-v1" 0x00 lowercase(host) 0x00 lowercase(user_name)`, the key line `mailarkiv-key-v1:` plus 32 bytes in standard padded base64 plus one newline, at most 1 KiB, creation with `O_CREATE|O_EXCL` and mode 0600, refusal of a key path equal to or under the data directory after resolving symbolic links, no mode check at `serve`, and no key rotation in this slice. Name the rejected alternatives: `nacl/secretbox`, a key in an environment variable, a raw 32-byte file, and a key derived from an administrator's password. Say that old ciphertext can stay in SQLite's free pages or WAL until reused. [research.md](research.md#passwords-and-the-key) has the facts
- [X] T003 [P] In `docs/ARCHITECTURE.md` under "Technology direction", replace the sentence that says `x/net` v0.62.0 and `x/text` v0.43.0 arrive with the mail libraries in Slices 2, 3, and 7. `go-imap` v2.0.0-beta.8 brings `go-message` v0.18.2 and `go-sasl` and needs neither, so `x/net` and `x/text` arrive with Slices 3 and 7, if at all
- [X] T004 [P] Record the changes from "How this list relates to the plan" in `specs/002-imap-incremental-archive/plan.md`: the Delivery order rows for steps 1, 2, 3, 6, and 7, and the `frontend/DESIGN.md` row of the document changes. In `specs/002-imap-incremental-archive/contracts/http-api.md`, say that `POST /api/imports` answers `message` in the list item shape of `GET /api/messages`

**Checkpoint**: Documentation only. Check local links and that the plan, contracts, and this list agree, as AGENTS.md requires for documentation changes.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The schema, the key package, the IMAP test double, the failure categories, and the module rules. Every story needs these.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T005 Structural change, committed alone as `refactor(sqlitedb): …`: make `migrate` and `migrateOnce` in `internal/sqlitedb/sqlitedb.go` read from an `fs.FS` argument, with `Open` passing the embedded `migrations`. Behavior doesn't change, and the existing tests pass unchanged

### Tests for Phase 2

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [X] T006 [P] Extend `internal/sqlitedb/sqlitedb_test.go`
  - Runner: a migration set from `fstest.MapFS` whose second file inserts a `source_observation` row pointing at message 999 fails `Open`, leaves `user_version` at 1, and leaves no row behind. A second file that rebuilds a referenced table with create, copy, drop, and rename commits, and `foreign_keys` then reads back 1 on the writer
  - Change `TestSchemaHasTheSixTables` to expect ten tables, adding `imap_source`, `imap_folder`, `imap_message_problem`, and `collected_message_count`. Change `TestSourceTypeAcceptsOnlyManualImport` to accept `manual_import` and `imap_collection` and refuse anything else
  - `source_observation` checks: an import without `account_email` is refused. A collection without `imap_source_id` or `imap_folder_id` is refused. `file_name` on a collection is refused. `received_at` on an import is refused. A second collection entry for the same `imap_folder_id` and `message_id` is refused, while two import entries for one message are accepted
  - `imap_source` checks: port 0 and 65536 refused, `security = 'ssl'` refused, `sealed_password` NULL with `removed_at` NULL refused, `failing_since` set with `failed_attempts` 0 refused, `last_failure_category = 'folder_unavailable'` refused. A second row with the same `host` and `user_key` is refused while the first has `removed_at` NULL, and accepted once it is set
  - `imap_folder` checks: `last_uid` 5 with `uid_validity` NULL refused, `chosen = 2` refused, a repeated `(source_id, name)` refused. `imap_message_problem`: `kind = 'other'` refused
- [X] T007 [P] Write `TestMigrateSlice1Catalog` in `internal/archive/migrate_test.go`. Build a Slice 1 catalog by running `internal/sqlitedb/migrations/0001_initial.sql` on a raw connection and setting `user_version` to 1. Insert three messages and five import entries by hand: one message imported twice with different file names, one with a NULL file name. Set `latest_observation_id` and write each blob under `blobs/`. Then `sqlitedb.Open` and `archive.Open`. Expected values come from the inserted rows: `List` returns the three messages in the same order with the same counts, `History` returns each entry with its address and file name, every blob is byte-identical, `latest_observation_id` still points at the right rows, `PRAGMA foreign_key_check` returns nothing, and inserting an entry for message 999 is refused
- [X] T008 [P] Write `internal/passwordkey/passwordkey_test.go`
  - `Create` writes exactly `mailarkiv-key-v1:`, 44 characters of standard padded base64, and `\n`, 62 bytes in all, with mode 0600. It refuses an existing file and leaves it unchanged, and refuses a missing parent directory
  - `Load` accepts that line. It returns a missing error for no file, an unreadable error for a directory at the path, and an invalid error for 1,025 bytes, no final newline, `\r\n`, a wrong prefix, 31 or 33 key bytes, unpadded base64, the URL-safe alphabet, and a second line. No error text contains the file's content
  - `TestKeyLocation`: `Inside(dataDir, path)` is true for the data directory itself, a file under it, `data/../data/key`, a symbolic link that points into it, and a path under a symbolic link to it. It resolves a path that doesn't exist yet through its longest existing parent. It is false for `data-other/key` beside `data/`, and works when the data directory doesn't exist yet
  - `TestSealBoundToSource`: seal and open round-trip. A 6-byte password seals to 34 bytes. Opening fails with another key, another host, another user name, or one flipped byte. `IMAP.Example.Test` and `Anna@Example.Test` seal what `imap.example.test` and `anna@example.test` open. Two seals of one password differ
- [ ] T009 [P] Extend `internal/archtest/archtest_test.go`
  - Imports: `cmd/mailarkiv` adds `collect` and `passwordkey`. `web` adds `collect`. `collect` uses `archive`, `opstatus`, `sqlitedb`, `fault`, and `passwordkey`. `passwordkey` and `imaptest` import no module. No non-test package outside `internal/imaptest/cmd/` imports `imaptest`
  - Table writes: `imap_source`, `imap_folder`, and `imap_message_problem` belong to `collect`. `collected_message_count` belongs to `archive`, beside `message` and `source_observation`
  - Add one bad in-memory source per new rule, so the checker can't pass on an empty tree

### Implementation for Phase 2

- [X] T010 Change the runner in `internal/sqlitedb/sqlitedb.go` to pass T006's runner cases, as [the data model](data-model.md#migration-0002) says. On a pinned `*sql.Conn` from the writer: `PRAGMA foreign_keys = OFF` outside the transaction, begin, run the file, set `user_version`, run `PRAGMA foreign_key_check` and roll back on any row, commit, then `PRAGMA foreign_keys = ON` and read it back as 1. A rollback fails `Open`, which `serve` already reports with cause `catalog`
- [X] T011 Write `internal/sqlitedb/migrations/0002_imap_collection.sql` to pass T006. Every table is `STRICT`, and times are integer Unix milliseconds in UTC
  - `imap_source`: `id` INTEGER PRIMARY KEY ("Rows are never deleted, so IDs are never reused"), `name` TEXT NOT NULL ("1 to 100 characters, counted as Unicode code points, after trimming spaces", enforced in `collect`), `host` TEXT NOT NULL ("Lowercase. Fixed after saving"), `port` INTEGER NOT NULL `CHECK (port BETWEEN 1 AND 65535)`, `security` TEXT NOT NULL `CHECK (security IN ('tls', 'starttls'))`, `user_name` TEXT NOT NULL ("As entered, because servers may compare it exactly"), `user_key` TEXT NOT NULL ("`strings.ToLower(user_name)`"), `sealed_password` BLOB ("NULL exactly when `removed_at` is set"), `created_at` INTEGER NOT NULL, `paused_at` INTEGER, `sign_in_rejected_at` INTEGER, `failed_attempts` INTEGER NOT NULL DEFAULT 0, `failing_since` INTEGER, `last_failure_category` TEXT (one of `server_not_reachable`, `secure_connection_failed`, `sign_in_rejected`, `timed_out`, `server_error`, `storage_full`, `storage_write_failed`, `catalog_write_failed`), `last_failure_at` INTEGER ("`failing_since`, `last_failure_category`, and `last_failure_at` are NULL exactly when `failed_attempts` is 0"), `last_server_reply` TEXT ("at most 1,000 characters, keeping the start and end. NULL when there was none"), `last_success_at` INTEGER, `last_success_archived` INTEGER, `removed_at` INTEGER. Partial unique index on `(host, user_key) WHERE removed_at IS NULL`
  - `imap_folder`: `id` INTEGER PRIMARY KEY, `source_id` INTEGER NOT NULL REFERENCES `imap_source`, `name` TEXT NOT NULL ("Valid UTF-8, 1 to 1,000 bytes. Unique with `source_id`"), `chosen` INTEGER NOT NULL `CHECK (chosen IN (0, 1))`, `uid_validity` INTEGER ("NULL until the folder is first opened"), `last_uid` INTEGER NOT NULL DEFAULT 0 ("0 while `uid_validity` is NULL"), `renumbered_at` INTEGER, `unavailable_since` INTEGER. `UNIQUE (source_id, name)`
  - `imap_message_problem`: `folder_id` INTEGER NOT NULL REFERENCES `imap_folder`, `uid_validity` INTEGER NOT NULL, `uid` INTEGER NOT NULL, `kind` TEXT NOT NULL `CHECK (kind IN ('waiting_for_retry', 'too_large', 'removed'))`, `size` INTEGER ("NULL when the server reported neither"), `received_at` INTEGER ("NULL when the server reported none"), `recorded_at` INTEGER NOT NULL. Primary key `(folder_id, uid_validity, uid)`
  - `collected_message_count`: `imap_source_id` INTEGER PRIMARY KEY REFERENCES `imap_source`, `messages` INTEGER NOT NULL ("Distinct messages with at least one entry from this source")
  - Rebuild `source_observation` last: create `source_observation_new` with `source_type` `CHECK (source_type IN ('manual_import', 'imap_collection'))`, `account_email` now nullable, and new `imap_source_id` INTEGER REFERENCES `imap_source`, `imap_folder_id` INTEGER REFERENCES `imap_folder`, and `received_at` INTEGER. Add the checks `(source_type = 'manual_import') = (account_email IS NOT NULL)`, `(source_type = 'imap_collection') = (imap_source_id IS NOT NULL)`, `(source_type = 'imap_collection') = (imap_folder_id IS NOT NULL)`, `file_name IS NULL OR source_type = 'manual_import'`, and `received_at IS NULL OR source_type = 'imap_collection'`. Copy every row with `source_type = 'manual_import'`, drop the old table, rename the new one, and create `source_observation_by_message (message_id, id)`, the unique `source_observation_once_per_folder (imap_folder_id, message_id) WHERE imap_folder_id IS NOT NULL`, and `source_observation_by_source (imap_source_id, message_id) WHERE imap_source_id IS NOT NULL`
- [X] T012 [P] Implement `internal/passwordkey/passwordkey.go` to pass T008, importing no module. `Create(path)` writes with `O_CREATE|O_EXCL` and mode 0600, fsyncs the file and its directory, and removes a partly written file. `Load(path)` reads at most 1 KiB plus one byte and returns a `*Key` or an error of a closed kind: missing, unreadable, or invalid. `Inside(dataDir, path)` follows [the location rule](research.md#where-the-key-may-live). `Seal(password, host, userName)` and `Open(sealed, host, userName)` use AES-256-GCM through `cipher.NewGCMWithRandomNonce`, with the additional data from T002
- [X] T013 [P] Write `internal/imaptest/imaptest_test.go`, then `internal/imaptest/imaptest.go`, the IMAP test double from [research.md](research.md#imap-test-double). Run `go get github.com/emersion/go-imap/v2@v2.0.0-beta.8` and `go mod tidy`. Confirm `go.mod` gains `github.com/emersion/go-message` v0.18.2 and `github.com/emersion/go-sasl` v0.0.0-20241020182733-b788ff22d5a6 as indirect, and neither `golang.org/x/net` nor `golang.org/x/text`. Run `govulncheck ./...`
  - `New(Options)` runs `imapserver/imapmemserver` on `127.0.0.1` with implicit TLS or STARTTLS, one user and password, and a test CA made in memory at each run, with a server certificate for `localhost` and `127.0.0.1`. `New` needs no `*testing.T`, because T102's process uses it too
  - Methods: `Addr`, `CAPEM`, `WriteCA(path)`, `AddFolder`, `DeleteFolder`, `AddMessage(folder, body, received)` returning the UID, `DeleteMessage`, `SetPassword`, a snapshot of every message's flags, and a log of the commands each session received, in order
  - Wrap each session through `imapserver.Options.NewSession`, so a test hook sees every command before the in-memory server does. This phase's hook can fail a command with `NO`. Later phases add the other faults
  - The test drives the double with `imapclient`: TLS and STARTTLS connect with a pool holding `CAPEM()`, the right password signs in, a wrong one gets `NO [AUTHENTICATIONFAILED]`, `LIST` returns `Entwürfe` decoded, `BODY.PEEK[]` returns the exact bytes added, and the command log and the hook see each command
- [X] T014 [P] Write `internal/collect/category_test.go`, then `internal/collect/category.go`. `Category` is a closed type with exactly the 13 names in [the data model's table](data-model.md#failure-categories): `server_not_reachable`, `secure_connection_failed`, `sign_in_rejected`, `password_unreadable`, `timed_out`, `server_error`, `folder_unavailable`, `storage_full`, `storage_write_failed`, `catalog_write_failed`, `message_unavailable`, `message_too_large`, `message_removed`. `String()` returns the wire name. `Sync()` is true for exactly the eight sync categories

**Checkpoint**: `mise run check` passes. A Slice 1 catalog migrates to version 2 with its list, history, and blobs unchanged. Nothing visible changes.

---

## Phase 3: User Story 1 - Connect a mailbox and see its mail archived (Priority: P1) 🎯 MVP

**Goal**: The walking skeleton, plan step 1. Under `--preview-collection`, an administrator adds a source over the API. The first sync starts at once, stores every message in the chosen folders through the Slice 1 capture path, and moves each folder's checkpoint after each message. Collected messages appear in the list and the history drawer. This phase finishes scenarios 3, 5, 6, and 9, and scenario 2 through the API. Phase 5 adds scenarios 4 and 7, Phase 7 adds scenario 1's check route, and Phase 9 builds the screens for scenarios 1, 2, 7, and 8.

**Independent Test**: Against the test double holding synthetic messages in two folders, add a source with `POST /api/sources`, wait for the first sync to finish, and check that every message is listed and downloads byte for byte. The 6 minute part waits for Phase 5.

### Tests for User Story 1

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T015 [P] [US1] Write `internal/archive/collected_test.go` with real files and SQLite, following [the collection commit](data-model.md#collection-commit)
  - New bytes return Stored with Archived, store the blob, add one `message` and one `imap_collection` entry with the given source, folder, and `received_at`, set `latest_observation_id` to it, and set `collected_message_count` to 1
  - The same bytes and folder again add no entry, leave `latest_observation_id` and the counter alone, and store no second blob. The same bytes in another folder of that source add an entry and leave the counter at 1. The same bytes from another source set that source's counter to 1
  - Bytes already imported gain a collection entry and no second blob. `%PDF-1.7\n` is stored, because collection skips the mail header check. An empty body is refused with `empty`
  - With a plain file in place of `tmp/`, the result is Failed with `storage_write_failed`, no failure event reaches `opstatus`, and the log has archive's line. A stored original found missing is restored and still records its `restored` event
- [ ] T016 [P] [US1] Write `internal/collect/helpers_test.go` and `internal/collect/sync_test.go`. Helpers build a collector on a temp data directory with real SQLite, a real archive, a key from `passwordkey.Create` outside the data directory, an injected clock, and short limits. Sources use host `127.0.0.1` and the double's port
  - `TestFirstSync`: `INBOX` holds three messages, one with CRLF endings, one with LF, and one with no final line break. `Entwürfe` holds two, and `Leer` is empty. Adding the source with all three folders starts a sync at once. When it ends, the archive lists five messages, each byte-identical to what the double holds, each with one entry naming the source and folder, `received_at` equal to the double's received time in milliseconds, and `observed_at` from the clock. Each folder row stores the double's `UIDVALIDITY` and the highest UID as `last_uid`, and `Leer` keeps 0. `last_success_at` is set and `last_success_archived` is 5. A new collector on the same catalog syncs at start, and the double's log shows a second `LOGIN`
  - A message whose bytes were imported first keeps one list entry and one blob, and its history has the import and the collection (scenario 5)
  - The stored `sealed_password` doesn't contain the password and opens with the key for that host and user name
  - `TestCollectionChangesNothing`: a hook fails the test on `SELECT`, `STORE`, `COPY`, `MOVE`, `EXPUNGE`, `APPEND`, `CREATE`, `DELETE`, `RENAME`, `SUBSCRIBE`, or `UNSUBSCRIBE`. The flag snapshot after the sync equals the one before, and no message has `\Seen`
- [ ] T017 [P] [US1] Write `internal/collect/conn_test.go` for the connection wrapper's deadlines, from [research.md](research.md#timeouts)
  - `TestTimeoutValues` asserts the production values: 30 seconds to connect, 60 seconds idle, and 120 seconds for a check
  - A listener that accepts and never sends a greeting fails after the short connect limit. A double that stalls on `LOGIN` fails after the short idle limit. The wrapper's record classifies both as `timed_out`
  - A literal sent one byte every half idle limit, for three idle limits, arrives whole, because a slow transfer that keeps sending isn't cut off
  - A gap between commands longer than the idle limit doesn't fail the next command. After a timeout, every later read returns the same error at once
- [ ] T018 [P] [US1] Write `internal/web/sources_test.go` against [the API](contracts/http-api.md), with a real collector and the double
  - `POST /api/sources` answers 201 with the list entry shape, and the first sync has started. `GET /api/sources` lists it with `"collection": "available"`. `GET /api/sources/{id}` has the folder objects and the `messages` object. `abc` and an unknown ID answer 404 `not_found`
  - A body that isn't JSON, has an unknown field, or is over 1 MiB answers 400 `invalid_fields`. Each [input rule](data-model.md#input-rules) gets a refused and an accepted case, and a body breaking several rules names each field: `name` empty, 101 code points refused, 100 `ä` accepted. `host` with a space, 254 bytes, `[::1]`, or `bücher.example` refused, `::1` and `xn--bcher-kva.example` accepted. `port` 0, 65536, and `"993"` refused. `security` `ssl` refused. `user_name` with U+0001 or 1,025 bytes refused. `password` empty or 1,025 bytes refused. `folders` empty, 1,001 names, a repeat, or an empty name refused
  - With no collector in `web.Deps`, `/api/sources` answers 404 `not_found` with a session and 401 without, like any unknown path. With a collector and no key, `POST /api/sources` answers 409 `no_key` and saves nothing
- [ ] T019 [P] [US1] Change `internal/web/list_test.go`, `internal/web/messages_test.go`, and `internal/web/imports_test.go` for the renamed fields in [the API](contracts/http-api.md#changed-get-apimessages). List items carry `observed_at`, `observation_count`, and `latest`. An import's entry is `{source_type, observed_at, account_email, file_name}`. A collection's entry has `source` with `id`, `name`, `host`, `user_name`, and `removed: false`, plus `folder` and `received_at`, which is null when the server reported none. `observation_count` counts imports and collections. The order is still the most recent entry first. The 201 import answer's `message` has the same shape
- [ ] T020 [P] [US1] Write `cmd/mailarkiv/collect_helpers_test.go` and `TestCollectFirstSync` in `cmd/mailarkiv/collect_test.go`. Helpers run the double in the test process, write its CA to a file, run `key create` outside the data directory, and start the binary with `SSL_CERT_FILE` set to the CA, `--key-file`, and `--preview-collection`
  - `key create` exits 0 and prints [the contract's line](contracts/cli.md#mailarkiv-key-create) with the path. Sign in, `POST /api/sources` with two folders, and poll `GET /api/sources/{id}` until `last_success` is set. `GET /api/messages` lists every message with `latest.folder`, and each download's SHA-256 equals its fixture's. A restart syncs at start, and the double's log shows a new `LOGIN`
  - Without `--preview-collection`, `POST /api/sources` answers 404 and the double sees no connection. The usage text mentions neither `key`, `--key-file`, nor `--preview-collection`
- [ ] T021 [P] [US1] Extend `frontend/src/lib/api.test.ts` and write `frontend/src/lib/format/untrusted.test.ts`
  - `parseMessage` reads `observed_at`, `observation_count`, and `latest` for both entry kinds. History parses both kinds, with `received_at` null and `removed: true`. An unknown `source_type` makes the result `unavailable`
  - `displayText` turns U+202E, U+0000, and U+2028 into visible codes, keeps `Entwürfe` and Hebrew text as they are, and shortens long text in the middle, keeping its end. `frontend/src/lib/format/file-name.test.ts` passes unchanged

### Implementation for User Story 1

- [ ] T022 [US1] In `internal/archive/archive.go`, `internal/archive/catalog.go`, and `internal/archive/retrieve.go`, replace `Message`'s `FileName`, `ImportedAt`, and `ImportCount` with `Latest` and `ObservationCount`. An entry is a closed type with two kinds: an import with account address and optional file name, and a collection with source ID, folder ID, and optional received time. Both [queries](data-model.md#list-and-history-queries) return every entry column, and the count includes both kinds
- [ ] T023 [US1] Implement `StoreCollected(ctx, body, CollectedMeta)` in `internal/archive/collected.go` to pass T015, sharing the capture in `internal/archive/capture.go` with `Import`. `CollectedMeta` holds the source ID, the folder ID, and an optional received time. Skip the mail header check, and refuse an empty body. Commit the [four steps](data-model.md#collection-commit) in one writer transaction. A storage or catalog failure logs archive's line with the category and the cause, records no failure event, and returns Failed. Restored outcomes still record their event. Add `CollectedCounts(ctx, sourceIDs)` reading `collected_message_count`
- [ ] T024 [P] [US1] Implement the connection wrapper in `internal/collect/conn.go` to pass T017. It wraps the TCP connection below TLS. One absolute deadline 30 seconds after the dial starts covers DNS, TCP, the handshake, the greeting, and for STARTTLS the plain greeting and the upgrade. After that, while a command is outstanding, each read and write sets a fresh 60 second deadline. With none outstanding, reads have no deadline. Ignore the deadlines the library sets, keep the first read error and return it for every later read, and record whether an error was a timeout. The sync's context closes the connection. Limits come from an unexported option
- [ ] T025 [US1] Implement `internal/collect/dial.go`. TLS 1.2 or later with the system's trusted certificates and `ServerName` set to the host. Implicit TLS hands the TLS connection to `imapclient.New` and waits for the greeting. STARTTLS uses `imapclient.NewStartTLS`. Then `LOGIN`. In this phase a wrapper timeout is `timed_out` and anything else is `server_error`. T064 completes the classification
- [ ] T026 [US1] Implement `internal/collect/input.go` and `internal/collect/sources.go` to pass T018's collector cases. Parse each [input rule](data-model.md#input-rules) once into domain types and return every broken field by name. `Add` lowercases the host, sets `user_key` to `strings.ToLower(user_name)`, seals the password, signs in, and only then inserts the source and its chosen folders in one transaction and wakes the source. Without a key, `Add` returns a no-key error before connecting. `List` and `Get` return [the list entry](contracts/http-api.md#get-apisources) with `state` `waiting` or `syncing`, `progress`, `last_success`, and `collected` from archive's `CollectedCounts`, ordered by name, then ID. `next_sync_at` stays null until Phase 5
- [ ] T027 [US1] Implement `internal/collect/sync.go` and `internal/collect/checkpoint.go` to pass T016, following [the sync steps](data-model.md#sync) for new messages. Open the password, connect, and sign in. For each chosen folder in `id` order: `EXAMINE` through `Select` with `ReadOnly: true`. On first open, store `UIDVALIDITY` with `last_uid` 0. Skip the search when `UIDNEXT` is at most `last_uid` + 1. Otherwise run `UID SEARCH UID <last_uid+1>:*`, keep UIDs above `last_uid`, and fetch `UID RFC822.SIZE INTERNALDATE` in batches of 500. For each UID in rising order, fetch `BODY.PEEK[]` alone, pass the literal to `StoreCollected`, and on Stored commit the new `last_uid` in collect's own transaction. Then `LOGOUT` and record the success. Count found and archived in memory, archived when `StoreCollected` returns Stored. In this phase any other per-message result ends the sync with the checkpoint before that message
- [ ] T028 [US1] Implement `internal/collect/collect.go` and `internal/collect/labels.go`. `Open` loads sources. `Start` runs one goroutine per source that isn't paused, removed, or rejected, and each syncs at once. Each loop waits on a wake-up channel and runs one sync at a time under its own context. `Stop` cancels every sync, which closes its connection, and waits. `Labels(ctx, folderIDs)` returns each folder's name and its source's ID, name, host, user name, and whether it was removed, in one query
- [ ] T029 [US1] Implement `internal/web/sources.go` and extend `internal/web/web.go` to pass T018. `web.Deps` gains `Collect`. The source routes are marked in the route table and registered only when `Collect` is set, so `Routes()` still lists every route for the access tests. Add `POST /api/sources`, `GET /api/sources`, and `GET /api/sources/{id}`, all `SignedIn`. Decode bodies of at most 1 MiB with unknown fields refused. A request that signs in has 120 seconds from its start and raises its write deadline to 130 seconds through `http.ResponseController`. Map collector errors to 400 `invalid_fields`, 404 `not_found`, and 409 `no_key`
- [ ] T030 [US1] Change the encoders in `internal/web/messages.go` and `internal/web/imports.go` to pass T019. Collection entries take their labels from one `collect.Labels` call per page or history
- [ ] T031 [US1] Implement `cmd/mailarkiv/key.go` for `key create --data-dir DIR PATH`, dispatched from `cmd/mailarkiv/main.go`, and extend `cmd/mailarkiv/serve.go` with `--key-file` and `--preview-collection`, to pass T020. Keep all three out of the usage text. Load the key after creating the data directory and before the lock, as [startup step 2](contracts/cli.md#mailarkiv-serve) says. With `--preview-collection`, open the collector after recovery, pass it to `web.New`, start it after the port is bound, and stop it during shutdown. In this phase a key error exits 1 with the error. T075 and T076 set the exact lines
- [ ] T032 [P] [US1] Change `frontend/src/lib/api.ts` to pass T021's API cases. `SourceType` becomes `"manual_import" | "imap_collection"`, and `Observation` becomes a union of the two entry kinds. `Message` carries `observedAt`, `observationCount`, and `latest`
- [ ] T033 [P] [US1] Write `frontend/src/lib/format/untrusted.ts` to pass T021's display cases, moving the neutralizing and middle-shortening rules out of `frontend/src/lib/format/file-name.ts`, which then calls `displayText` and keeps its missing-name case. Add `frontend/src/lib/components/UntrustedText.svelte`, and make `frontend/src/lib/components/FileName.svelte` render through it. Neither uses `{@html}`
- [ ] T034 [US1] Update `frontend/DESIGN.md` for list rows and the history drawer: a collection row shows the source name and folder where an import shows its file name, the drawer's labels say entries instead of imports, and a collection entry shows the source name, server, user name, folder, received time or "Unknown", and collection time. Then change `frontend/src/lib/components/MessageRow.svelte` and `frontend/src/lib/components/HistoryDrawer.svelte` to match, rendering source and folder names through `UntrustedText`, and update `frontend/tests/e2e/history.spec.ts` for the new labels

**Checkpoint**: `mise run check` passes. `TestCollectFirstSync` and `TestCollectionChangesNothing` pass. Under `--preview-collection`, US1's independent test passes without the 6 minute part. Without the flag, nothing changes for users except the history drawer's wording.

---

## Phase 4: User Story 2 - Never skip or double-count a message (Priority: P1)

**Goal**: Plan step 2. Renumbered folders, messages the server doesn't return, size limits, the per-source counter, and the collection crash matrix. This phase finishes scenarios 1 to 8 and 10. Phase 5 finishes scenario 9, graceful shutdown.

**Independent Test**: `TestCollectCrashMatrix` stops the server at each step of collecting a message, restarts it, and compares the archive with the double's mailbox.

### Test support for User Story 2

- [ ] T035 [US2] Extend `internal/imaptest/imaptest.go` and its test. A test can renumber a folder, giving it a new `UIDVALIDITY` and new UIDs for the same messages. For one UID it can return a NIL body, return no data, omit `INTERNALDATE`, report a given `RFC822.SIZE`, announce a literal of a given size and then send fewer bytes and close, or hold the body fetch until the test releases it. It counts body fetches per UID

### Tests for User Story 2

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T036 [P] [US2] Write `TestEntryAndCopyCounts` and `TestCollectedCounts` in `internal/collect/counts_test.go`, one case per row of SC-003 with expected counts written into the test. After each case, the counter equals `COUNT(DISTINCT message_id)` of that source's entries
  - A second sync with no new mail fetches no body and adds no entry (scenario 3)
  - A folder with three archived messages is renumbered and gains a fourth. The next sync fetches all four, adds one entry and one blob, sets `renumbered_at`, and stores the new `UIDVALIDITY` and `last_uid` (scenario 4)
  - Two copies of the same bytes in one folder give one message, one blob, and one entry (scenario 5). The same bytes in `INBOX` and `Archiv` give one message, one blob, and two entries (scenario 6). Bytes imported by hand first give one blob and two entries
  - A message moved between two chosen folders gains an entry for the new one and no blob. Messages deleted from the mailbox stay listed after the next sync (scenario 10). A message with no `INTERNALDATE` has `received_at` NULL
  - Two sources sharing bytes each count 1
- [ ] T037 [P] [US2] Extend `internal/archive/concurrent_test.go` with `TestCollectWhileImport`: `StoreCollected` and `Import` store the same new bytes at once, 50 times under `-race`. Each round leaves one blob, one message, two entries, and an empty `tmp/`
- [ ] T038 [P] [US2] Write `TestMessageUnavailable` in `internal/collect/problems_test.go`, from [the per-message table](data-model.md#sync)
  - UID 2 of 3 returns a NIL body. UIDs 1 and 3 are archived, UID 2 is `waiting_for_retry` with its size and received time, `last_uid` is 3, the sync succeeds, and the log has `message unavailable` with `source_id`, `folder_id`, and `category` (scenario 7). No data at all behaves the same, and so does an empty body
  - The next sync fetches UID 2 again. When it returns, it is stored and its row is deleted. A retry doesn't change `last_uid`
  - A UID still failing and then deleted from the double becomes `removed`, records one `message_removed` failure event with the source ID, logs `message not archived`, and is never fetched again (scenario 8)
  - A UID the search found but that is gone at fetch and wasn't waiting gets no record
  - Renumbering a folder deletes its `waiting_for_retry` and `too_large` rows
- [ ] T039 [P] [US2] Write `TestMessageSizes` in `internal/collect/sizes_test.go`
  - `RFC822.SIZE` 150,000,001 records `too_large`, fetches no body, records one `message_too_large` event, logs `message not archived`, moves the checkpoint, and isn't tried again at the next sync
  - A literal announced as 150,000,001 bytes closes the connection without reading it, records `waiting_for_retry`, moves the checkpoint, and fails the sync with `server_error`
  - A literal that ends early stores nothing, leaves `tmp/` empty, leaves the checkpoint before the message, and fails the sync with `server_error`
  - A literal whose length differs from `RFC822.SIZE` is stored
- [ ] T040 [P] [US2] Extend `internal/opstatus/opstatus_test.go`: `message_too_large` and `message_removed` exist, their events carry a source ID, the log line has `source_id`, and `ProblemCount` counts them
- [ ] T041 [P] [US2] Extend `internal/web/status_test.go`: both events appear in `failures` with `source_id` and `source_name`, and no folder name
- [ ] T042 [P] [US2] Extend `frontend/src/lib/api.test.ts`: status failures with `message_too_large` and `message_removed` parse with their source ID and name. Extend `frontend/src/lib/status/groups.test.ts` so both group like other failure categories
- [ ] T043 [US2] Write `TestCollectCrashMatrix` in `cmd/mailarkiv/collect_crash_test.go` from [the plan's table](plan.md#crash-matrix). The double serves three messages in one folder. Start the `faults` binary with `--preview-collection` and `MAILARKIV_FAULTS` set, add the source over HTTP, let message 1 reach the point, then restart with the default binary and wait for the sync at start. Check each row's two columns, reading checkpoints from the catalog after the process exits and body fetches from the double's count. The `checkpoint-commit=EIO` row needs no kill. All rows end with three messages listed, byte-identical, one entry each, and the collected count 3

### Implementation for User Story 2

- [ ] T044 [US2] Extend `internal/collect/sync.go` and `internal/collect/checkpoint.go` to pass T036 and T038. A changed `UIDVALIDITY` stores the new value, sets `last_uid` to 0, sets `renumbered_at`, and deletes the folder's `waiting_for_retry` and `too_large` rows, in one transaction. After the new messages, retry the folder's `waiting_for_retry` rows under the current `UIDVALIDITY`. Handle each row of [the per-message table](data-model.md#sync), committing the checkpoint and any record in one transaction. A NIL body or no data sends `UID SEARCH UID <uid>` to tell a failure from a deletion. Log `message unavailable` and `message not archived` with the fields in [the CLI contract](contracts/cli.md#log-entries)
- [ ] T045 [US2] Extend `internal/collect/sync.go` to pass T039: skip a message whose `RFC822.SIZE` is over 150,000,000. When a literal is announced over that limit, close the connection without reading it, record `waiting_for_retry`, and fail the sync with `server_error`
- [ ] T046 [US2] Add the fault points to the checkpoint commit in `internal/collect/checkpoint.go`: `checkpoint-commit` takes an injected error, which rolls back and fails the sync with `catalog_write_failed`, and `after-checkpoint-commit` is a stop point. `internal/fault` doesn't change
- [ ] T047 [P] [US2] Extend `internal/opstatus/opstatus.go` to pass T040: add `MessageTooLarge` and `MessageRemoved`, and an optional source ID on `FailureEvent`, set only for those two and logged as `source_id`
- [ ] T048 [US2] Extend `internal/web/status.go` to pass T041, taking source names from `collect` in one call
- [ ] T049 [P] [US2] Extend `FAILURE_CATEGORIES`, `FailureEvent`, and `parseFailure` in `frontend/src/lib/api.ts`, and add both categories to `frontend/src/lib/status/categories.ts` with their label, meaning, and next step from FR-035's table. The status screen in `frontend/src/routes/(app)/status/+page.svelte` names the source of these events through `UntrustedText`

**Checkpoint**: `mise run check` passes. `TestCollectCrashMatrix`, `TestEntryAndCopyCounts`, and `TestCollectedCounts` pass (SC-002, SC-003).

---

## Phase 5: User Story 3 - Recover from network and server failures on its own (Priority: P1)

**Goal**: Plan step 3. Bounded replies, secure connections, sign-in results, the 5 minute schedule with growing retry waits, rejected sign-in kept across restarts, unavailable folders, storage failures, and shutdown. This finishes US3 except Sync now in scenario 4, which Phase 7 adds. It also finishes US1 scenario 4 and scenario 7 through the API, and US2 scenario 9.

**Independent Test**: The double stalls or disconnects at each step. Each stall times out, shows as Timed out, and leaves the stalled message for the next sync.

### Test support for User Story 3

- [ ] T050 [US3] Extend `internal/imaptest/imaptest.go` and its test. A test can withhold the greeting, stall the TLS handshake, stall any command until the connection closes, stall partway through a literal, close the connection at a command, answer `NO` or `BAD` with a given code and text, send a `LIST` reply larger than a given size, serve a certificate from another CA, an expired one, or one for another name, accept only TLS 1.1, leave STARTTLS out of its capabilities, and stop accepting connections. It counts `LOGIN` commands

### Tests for User Story 3

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T051 [P] [US3] Extend `internal/collect/conn_test.go` with `TestStallAtEachStep`, with short limits. Stall points: the dial, through an unexported dial option, the TLS handshake, the greeting, STARTTLS, `LOGIN`, `EXAMINE`, `UID SEARCH`, the metadata fetch, and partway through a literal. Each sync ends as `timed_out` within the limit plus one second, records `last_failure_at`, stores nothing new, leaves `tmp/` empty, and leaves every checkpoint unchanged. With the stall removed, the next sync archives the stalled message (SC-005, scaled)
- [ ] T052 [P] [US3] Write `TestReplyBounds` and `TestLimitValues` in `internal/collect/bounds_test.go`. `TestLimitValues` asserts 16 MiB per command and 150,000,000 bytes plus 1 MiB for a body fetch. With bounds lowered through the unexported option, a folder list past the command bound and a body past the body bound each fail the sync as `server_error` and store nothing. Bytes the server sends between commands count toward the next command
- [ ] T053 [P] [US3] Write `TestSecureConnection` in `internal/collect/secure_test.go`. A certificate from an untrusted CA, an expired one, one for another name, a server offering only TLS 1.1, and a STARTTLS server that doesn't offer it each give `secure_connection_failed`, through `Add` and through a sync. In every case the double saw no `LOGIN` (scenarios 6 and 7)
- [ ] T054 [P] [US3] Write `TestSignInResults` in `internal/collect/signin_test.go` as a unit table from [research.md](research.md#sign-in-results): `NO [UNAVAILABLE]` gives `server_error`. `NO` with no code, `[AUTHENTICATIONFAILED]`, `[AUTHORIZATIONFAILED]`, `[EXPIRED]`, and `[CONTACTADMIN]` give `sign_in_rejected`. `BAD` and a closed connection give `server_error`. A `*net.DNSError`, a refused connection to a closed port on `127.0.0.1`, and `EHOSTUNREACH` give `server_not_reachable`. The reply kept for each is the code and text as received
- [ ] T055 [P] [US3] Write `TestSignInRejectedStops` in `internal/collect/rejected_test.go`, with the real double and a fake clock through the unexported timer option. A wrong password gives one `LOGIN`, state `needs_attention` with `sign_in_rejected`, `sign_in_rejected_at` set, and no next sync. After the clock passes three retry waits there is still one `LOGIN`. A new collector on the same catalog doesn't sync at start, and the count stays at one. `NO [UNAVAILABLE]` instead gives `retrying` (scenario 5)
- [ ] T056 [P] [US3] Write `TestSchedule` in `internal/collect/schedule_test.go` with `testing/synctest` and a fake sync function. A success makes the next sync due 5 minutes after it ends. Failures in a row wait 1, 2, 4, 8, 16, 32, 60, and 60 minutes. A success returns to 5 minutes. A new loop on the same catalog continues the waits from the stored `failed_attempts`. A wake-up during a wait syncs at once, and one during a sync starts no second sync. `Stop` during a wait returns at once
- [ ] T057 [P] [US3] Write `TestSourcesIndependent` in `internal/collect/independent_test.go`: one source's double stalls on `EXAMINE` and another's is healthy. The healthy source finishes its sync while the stalled one is still running (scenario 8)
- [ ] T058 [P] [US3] Write `TestFolderUnavailable` in `internal/collect/folders_test.go`. A chosen folder deleted on the double gets `NO` to `EXAMINE`. The other folders are collected, `unavailable_since` is set, the sync succeeds with `failed_attempts` 0, and each sync logs `folder unavailable` with `source_id`, `folder_id`, and `category`. Re-creating the folder clears `unavailable_since` (scenario 10)
- [ ] T059 [P] [US3] Write `TestFailureRecord` in `internal/collect/failures_test.go`. After a failed sync, `failed_attempts` is 1 and `failing_since`, `last_failure_category`, `last_failure_at`, and `last_server_reply` are set, such as `[AUTHENTICATIONFAILED] Invalid credentials`. A 5,000-character reply is stored as at most 1,000 characters, keeping its start and end. A second failure keeps `failing_since`. A success clears all four and sets `last_success_at` and `last_success_archived` (scenario 3). Each failure logs `sync failed` with `source_id`, `category`, `failed_attempts`, and `next_attempt_at` when one is scheduled. `GET /api/sources` shows `next_sync_at` while Waiting or Retrying, and null otherwise
- [ ] T060 [P] [US3] Extend `internal/web/sources_test.go`: `POST /api/sources` with a failing sign-in answers 422 `check_failed` with `category` and `reply` for `server_not_reachable`, `secure_connection_failed`, `sign_in_rejected`, `timed_out`, and `server_error`, and saves nothing (US1 scenario 7). With the check limit lowered, a stalled check ends as `timed_out`
- [ ] T061 [P] [US3] Write `TestCollectStorageFailures` in `cmd/mailarkiv/collect_faults_test.go` with the `faults` binary. `temp-write=ENOSPC` gives `storage_full`, `temp-write=EACCES` gives `storage_write_failed`, and `commit=EIO` gives `catalog_write_failed`. Each time the source is Retrying with that category, the checkpoint stays before the message, `GET /api/status` lists no failure event for it, and the log has archive's line. A restart without faults archives the message (scenario 9)
- [ ] T062 [P] [US3] Extend `cmd/mailarkiv/shutdown_test.go` with `TestShutdownDuringSync`. The double holds a literal partway through. SIGTERM ends the process with exit 0 within the 5 second grace plus one second, `tmp/` is empty, and the message isn't listed. After a restart it is archived with one entry (US2 scenario 9)

### Implementation for User Story 3

- [ ] T063 [US3] Extend `internal/collect/conn.go` to pass T052. Count bytes read since the current command started, reset the count when a command starts, and fail the connection past 16 MiB, or past 150,000,000 bytes plus 1 MiB during a body fetch. Bounds come from an unexported option
- [ ] T064 [US3] Write `internal/collect/classify.go` to pass T051, T053, and T054. Map dial errors to `server_not_reachable`, handshake and certificate errors and any non-timeout STARTTLS failure to `secure_connection_failed`, the wrapper's timeout record to `timed_out`, and `LOGIN` answers as research says. Everything else is `server_error`. Add the unexported dial option that T051 uses to `internal/collect/dial.go`. Keep the server's `NO` or `BAD` code and text as the reply, shortened to 1,000 characters keeping the start and end
- [ ] T065 [US3] Write `internal/collect/schedule.go` to pass T055, T056, and T059, and use it from `internal/collect/collect.go`. The next sync is due 5 minutes after a success ends, and after the nth failure in a row after min(2^(n−1), 60) minutes. The due time lives in memory. Record a success or failure on the source row as [the events table](data-model.md#source-states) says, including `sign_in_rejected_at` for a rejection, which ends the loop. `Start` skips rejected sources. Timers come from an unexported option
- [ ] T066 [US3] Extend `internal/collect/sync.go` to pass T057, T058, and T061. `NO` to `EXAMINE` sets `unavailable_since` and moves to the next folder, and success clears it. A Failed result from `StoreCollected` ends the sync with its category. Log `sync failed` and `folder unavailable` with the fields in [the CLI contract](contracts/cli.md#log-entries)
- [ ] T067 [US3] Change shutdown in `cmd/mailarkiv/serve.go` to pass T062: stop every sync while `http.Server.Shutdown` runs, inside the same 5 second grace
- [ ] T068 [US3] Extend `internal/web/sources.go` to pass T060: a failed sign-in answers 422 `check_failed` with `category` and `reply`, which is omitted when the server sent none

**Checkpoint**: `mise run check` passes. Every stall point ends as `timed_out` with checkpoints unchanged (SC-005). A new message is listed by the next scheduled sync, which `TestSchedule` puts 5 minutes after the previous one ended.

---

## Phase 6: User Story 4 - Keep mailbox passwords secret (Priority: P1)

**Goal**: Plan step 4. Key refusals, running without a key, unreadable passwords, and the data directory search. This finishes scenarios 1, 4, 5, 9, and 10, and scenario 6 up to saving the password again. Phase 7 adds scenarios 2, 3, 7, and 8, and the rest of 6.

**Independent Test**: Add sources whose passwords are marker strings. Copy the data directory, then search the copy, the log, the status screen, and every response for each marker.

### Tests for User Story 4

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T069 [P] [US4] Write `TestKeyCreate` in `cmd/mailarkiv/key_test.go` against [the contract](contracts/cli.md#mailarkiv-key-create). Exit 0 prints the created line with the path, and `stat` shows mode 0600 (scenario 9). A path inside the data directory, and one through a symbolic link into it, exit 1 with `The key must be outside the data directory.` (scenario 4). An existing file exits 1 with `A file already exists at <path>. MailArkiv never replaces a key.` and is unchanged. A missing parent directory exits 1 with `The key couldn't be written: <cause>` and leaves no file. A missing path argument exits 2 with the usage text
- [ ] T070 [P] [US4] Write `TestServeKeyRefusals` in `cmd/mailarkiv/keyfile_test.go`. Each case exits 1 with a log line `startup failed`, `cause` `key`, and the `reason`: `inside_data_dir` for a key in the data directory (scenario 4), `missing`, `unreadable` for a directory at the path, and `invalid` for 1,025 bytes, a wrong prefix, and 31 key bytes. The log never contains the file's content, checked with a marker inside an invalid file. The data directory holds nothing but itself after a refusal
- [ ] T071 [P] [US4] Write `TestNoKey` and `TestWrongKey` in `cmd/mailarkiv/nokey_test.go`
  - `TestNoKey`: with `--preview-collection` and no `--key-file`, the log has `collection unavailable` with `reason` `no key file given` (scenario 10). `GET /api/sources` has `"collection": "no_key"`, and a source saved under an earlier start shows `needs_attention` with `password_unreadable`, `since` set, and `failed_attempts`, `last_at`, and `reply` null. `POST /api/sources` answers 409 `no_key`. An import and a download still work (scenario 5)
  - `TestWrongKey`: a restart with another key shows each source as `needs_attention` with `password_unreadable`, logs `password unreadable` with `source_id` once per source, and the double sees no `LOGIN` (scenario 6)
- [ ] T072 [P] [US4] Write `TestNoPasswordInDataDir` in `cmd/mailarkiv/passwords_test.go`. Add two sources with marker passwords. While the server runs, copy the data directory, including the WAL and any `tmp/` files. Search every file in the copy and the log for each marker, its standard and URL-safe base64, and its lowercase and uppercase hex. Expected: no match. Restart with another key and check that no source's password opens (scenario 1, SC-006)
- [ ] T073 [P] [US4] Write `TestSourceResponsesHaveNoPassword` in `internal/web/secrets_test.go`. For every route in `Routes()` whose path starts with `/api/sources`, send a request carrying a marker password where the route accepts one. Fail when such a route has no request case, so later routes must add one. Search every response's body and headers for the marker and its base64 and hex forms, and do the same for `GET /api/status`, `GET /api/session`, and `GET /api/messages`
- [ ] T074 [P] [US4] Write `internal/collect/nokey_test.go`. A collector opened with no key marks every source unreadable with the start time, starts no sync, and logs `password unreadable` once per source. `Add` returns the no-key error without connecting. A source whose password fails to open at a sign-in becomes unreadable and counts no failed attempt

### Implementation for User Story 4

- [ ] T075 [US4] Change `cmd/mailarkiv/key.go` to pass T069 with the exact lines and exit codes from [the contract](contracts/cli.md#mailarkiv-key-create)
- [ ] T076 [US4] Change the key load in `cmd/mailarkiv/serve.go` to pass T070. Refuse a path inside the data directory before reading it. Log `startup failed` with `cause` `key` and the `reason`, and leave out the error text for `invalid`, so no file content can reach the log
- [ ] T077 [US4] Extend `internal/collect/collect.go` and `internal/collect/sync.go` to pass T071 and T074. At `Open`, try to open each stored password, as [startup step 7](contracts/cli.md#mailarkiv-serve) says. Keep the unreadable mark and its time in memory, and compute `needs_attention` with `password_unreadable` from it. With `--preview-collection` and no `--key-file`, `serve` logs `collection unavailable` with `reason` `no key file given` at start
- [ ] T078 [US4] Extend `internal/web/sources.go` to pass T071's API cases: `collection` is `no_key` without a key, and any request that would sign in or store a password answers 409 `no_key`

**Checkpoint**: `mise run check` passes. No marker password appears in a copy of the data directory, the log, or any response (SC-006).

---

## Phase 7: User Story 6 - Manage sources (Priority: P2)

**Goal**: Plan step 5. The check routes, edit with a sign-in check, pause, resume, Sync now, removal, duplicates, and adding and removing folders. This finishes US6, US4 scenarios 2, 3, 6, 7, and 8, US3 scenario 4, and US1 scenario 1 through the API.

**Independent Test**: Run each action on a source during a sync and while it waits, and check its state, its checkpoints, and the archive afterward.

### Tests for User Story 6

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T079 [P] [US6] Write `internal/collect/manage_test.go`
  - Pause during a held literal: the sync stops at once, the message isn't stored, `tmp/` is empty, checkpoints don't move, no sync runs over three schedule periods, and a new collector on the same catalog keeps it paused. Pausing twice changes nothing (scenario 1)
  - Resume syncs at once. Resuming an active source starts nothing (scenario 2)
  - Sync now on a Waiting, Retrying, or rejected source starts a sync at once. On a rejected source it clears `sign_in_rejected_at` and signs in once (scenario 3, US3 scenario 4). While syncing it returns an already-syncing error and starts nothing (scenario 4). While paused it returns a paused error
  - Adding a folder syncs at once unless paused and collects every message in it (scenario 5). Removing a folder sets `chosen` to 0, stops collecting it, and keeps its messages listed (scenario 6). Adding it back continues from its checkpoint. A folder renamed on the server and added under its new name gains entries and no blob. A folder on the server that was never chosen gets no `EXAMINE`
  - Removing a source during a sync stops the sync, then in one transaction sets `removed_at`, clears `sealed_password`, and deletes its `imap_message_problem` rows. Its messages' history still names the source, server, user name, and folder with `removed: true` (scenario 7, US4 scenario 8)
  - A second source with the same host and user name in other capital letters is refused with the existing source's ID, before any connection. After removal, the same mailbox can be added as a new source (scenario 8)
- [ ] T080 [P] [US6] Write `internal/collect/edit_test.go`. Renaming alone doesn't sign in. A change to port, security, or password signs in first with the new values. A failed check returns its category, and nothing changes, including a new name in the same request (US4 scenario 3). An empty or missing password keeps the stored one. A saved password clears `sign_in_rejected_at` and the unreadable mark and syncs at once unless paused (US4 scenario 6). Host and user name can't change. A password changed during a sync leaves that sync running, and the next sign-in uses the new one
- [ ] T081 [P] [US6] Write `internal/collect/check_test.go` against [the check contract](contracts/http-api.md#post-apisourcescheck). `INBOX` comes first, then the server's order. Folders marked `\Noselect` or `\NonExistent` aren't listed. Counts come from `STATUS (MESSAGES)`, because the double doesn't offer `LIST-STATUS`, and `messages` is null for a folder whose `STATUS` fails. `Entwürfe` comes back decoded. A duplicate is refused before connecting. A check for a stored source uses the stored port, security, and password for fields the request leaves out
- [ ] T082 [P] [US6] Extend `internal/web/sources_test.go` for `POST /api/sources/check`, `POST /api/sources/{id}/check`, `PATCH /api/sources/{id}`, `POST /api/sources/{id}/pause`, `/resume`, `/sync`, and `DELETE /api/sources/{id}`, with every status in [the contract](contracts/http-api.md). Also: 409 `source_exists` with `source_id`, 400 `invalid_fields` naming `host` and `user_name` in a `PATCH`, 409 `already_syncing`, `paused`, and `no_key` from `/sync`, 204 from `DELETE`, and 404 for a removed source afterward
- [ ] T083 [P] [US6] Extend `internal/web/access_test.go` and `internal/web/web_test.go`
  - The harness passes a collector, so the source routes are registered. `requestBody` gives each source route a valid body, so a request that got through would change something. `TestEveryRouteDeniesWithoutSession` also compares the sources, folders, and sealed passwords before and after
  - The cross-origin test runs for every non-GET route in `Routes()` with a valid session and `Origin: https://evil.example`, answering 403 `cross_origin` with nothing changed (SC-007, US4 scenario 7)
  - `TestEverySignedInRouteFailsClosedWhenTheCatalogFails` covers the source routes
- [ ] T084 [P] [US6] Extend `internal/web/secrets_test.go` with request cases for the routes this phase adds, including a `PATCH` that saves a marker password (US4 scenario 2)

### Implementation for User Story 6

- [ ] T085 [US6] Write `internal/collect/manage.go` to pass T079: `Pause`, `Resume`, `SyncNow`, `Remove`, and folder add and remove, as [the events table](data-model.md#source-states) says. Pause and removal cancel a running sync's context and wait for it to stop. `SyncNow` returns an already-syncing or paused error
- [ ] T086 [US6] Write `internal/collect/edit.go` to pass T080: `Edit` applies a name, port, security, password, and full folder list together. It signs in first when port, security, or password change, and changes nothing if that fails. Refuse host and user name
- [ ] T087 [US6] Write `internal/collect/check.go` to pass T081: `Check` and `CheckSource` refuse a duplicate before connecting, sign in, list with `LIST "" "*"`, skip `\Noselect` and `\NonExistent`, read counts with `LIST-STATUS` when offered and `STATUS (MESSAGES)` otherwise, and put `INBOX` first. Map a duplicate found at insert by the unique index to the same `source_exists` error
- [ ] T088 [US6] Extend `internal/web/sources.go` with the seven routes to pass T082, T083, and T084. The check routes and a `PATCH` that signs in get the same 120 second limit and 130 second write deadline as `POST /api/sources`

**Checkpoint**: `mise run check` passes. No request without a valid session or from another site reads or changes a source (SC-007).

---

## Phase 8: User Story 5 - See progress and problems without seeing mail (Priority: P2)

**Goal**: Plan step 6. The sources list and source pages, problem sources on the status screen and in the problem count, and the marker test across every category.

**Independent Test**: With marker strings as the password, user name, folder names, message content, and the double's replies, force each failure category. Each appears on the sources screen, the status screen, and in the log, and no marker appears on the status screen or in the log.

### Tests for User Story 5

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T089 [P] [US5] Write `TestProgressCountsDurable` in `internal/collect/progress_test.go`. The double holds before each body fetch. At each hold, `progress.archived` equals the number of this sync's entries in the catalog, and `found` equals the UIDs found so far plus retry rows. A message held partway through its literal doesn't count. A sync that times out partway through a message leaves `last_failure_*` set and the archived count equal to the messages it stored (scenarios 1 and 2, SC-004)
- [ ] T090 [P] [US5] Extend `TestNoMarkersInLogOrStatus` in `cmd/mailarkiv/markers_test.go` with a collection case. Markers go in the password, the user name, the source name, folder names, message content, and the double's `NO` and `BAD` texts. Force every category in FR-035: change the password on the double, restart with another key, stop the double, serve a misnamed certificate, accept and never greet, which waits the real 30 second connect limit and runs in parallel with the other subcases, answer `BAD` to `UID SEARCH`, delete a folder, use `faults` points for the three storage categories, return a NIL body, report a size over the limit, and remove a message waiting for retry. After each, `GET /api/sources/{id}` shows the category, `GET /api/status` lists the source or the event, `problem_count` includes it, and the log has the category and `source_id`. No marker appears in `GET /api/status` or the log. The server's reply appears only in `GET /api/sources/{id}` (SC-010, scenarios 4 to 7)
- [ ] T091 [P] [US5] Write `internal/collect/report_test.go` for `ProblemSources`: a Retrying or Needs attention source reports its sync category with `failing_since` and `failed_attempts`. Otherwise a source with an unavailable folder reports `folder_unavailable` from the earliest `unavailable_since`, and otherwise one with a message waiting for retry reports `message_unavailable` from the earliest `recorded_at`. Both have `failed_attempts` null. Each source appears once, and a paused source with no other problem doesn't appear
- [ ] T092 [P] [US5] Extend `internal/web/status_test.go` and `internal/web/session_test.go`. `GET /api/status` has `sources` in [the contract's shape](contracts/http-api.md#changed-get-apistatus), with no user name, folder name, or reply. `problem_count` in `GET /api/session` adds one per source in `sources` (FR-038)
- [ ] T093 [P] [US5] Extend `frontend/src/lib/api.test.ts`: parse the source list, a source's details, and status `sources`. An unknown `state` or `category` makes the result `unavailable`. `progress`, `last_success`, `failure`, and `next_sync_at` accept null
- [ ] T094 [P] [US5] Write `frontend/src/lib/sources/words.test.ts`. Every state from FR-032 has its word: Waiting, Syncing, Retrying, Needs attention, Paused. Every category in FR-035 has its label and its "What happened", "What MailArkiv does", and "Next step" text exactly as in the spec's table

### Implementation for User Story 5

- [ ] T095 [US5] Write `internal/collect/report.go` to pass T091, and extend `internal/web/status.go` and `internal/web/session.go` to pass T092. `opstatus` doesn't learn about sources: the web adapter adds the two counts
- [ ] T096 [P] [US5] Extend `frontend/src/lib/api.ts` to pass T093 with `Source`, `SourceDetail`, `SourceState`, `CollectionCategory`, and `listSources` and `readSource`, parsed once at this boundary
- [ ] T097 [P] [US5] Write `frontend/src/lib/sources/words.ts` to pass T094
- [ ] T098 [US5] Add the sources screens to `frontend/DESIGN.md`: the list row, the state words with their dots, the source page's sections for folders, failure, messages waiting for retry, and messages not archived, the no-key notice, and the refresh interval while a source syncs. [research.md](research.md#counting-collected-messages) says the screen refreshes every few seconds, so choose the value here
- [ ] T099 [US5] Build `frontend/src/routes/(app)/sources/+page.ts` and `+page.svelte` from T098: each source with its name, server, user name, state, folders, last successful sync with the messages it archived, the total collected, and the next sync. With `no_key`, say that collection needs the key and link to the operator guide's key section. Failed loads use `FailedLoad`, never an empty list. Times use `formatTime`. Names go through `UntrustedText`
- [ ] T100 [US5] Build `frontend/src/routes/(app)/sources/[id]/+page.ts` and `+page.svelte` from T098: the list fields, plus for a failure the category with its meaning and next step, when the failures began, the failed attempts, and the server's reply as plain text through `UntrustedText`. List unavailable folders, renumbered folders, messages waiting for retry, and messages not archived, each with folder, size, server time, and reason, and show the totals when more than 100. Refresh at T098's interval while the state is `syncing`
- [ ] T101 [US5] Extend `frontend/src/routes/(app)/status/+page.svelte` with a section for problem sources: name, category label, since, and failed attempts when present, each linking to its source page

**Checkpoint**: `mise run check` passes. `TestProgressCountsDurable` and the collection case of `TestNoMarkersInLogOrStatus` pass (SC-004, SC-010).

---

## Phase 9: Complete the Interface

**Goal**: Plan step 7. The two-step add, the edit and remove flows, source actions, the removal warning, and the browser checks for keyboard, 360 px, and axe. This finishes US1 scenarios 1, 2, 7, and 8 on screen, FR-007, FR-039, and FR-042.

**Independent Test**: Add a source and follow its first sync with the keyboard alone, at 360 px wide, with no horizontal page scrolling.

### Test support

- [ ] T102 Write `internal/imaptest/cmd/imaptestserver/main.go`: run the double as a process with `--listen`, `--ca-out PATH`, `--user`, `--password`, a repeatable `--folder NAME=COUNT` that fills a folder with synthetic messages, and `--fetch-delay DURATION` to slow body fetches. Print the listening address on stdout. In `mise.toml`, make `test:e2e` run `go build -o bin/imaptestserver ./internal/imaptest/cmd/imaptestserver` before `bunx playwright test`. Update the `mise run test:e2e` row in `AGENTS.md` in the same commit
- [ ] T103 Extend `frontend/tests/e2e/support/server.ts` so `startServer` can create a key, pass `--key-file` and `--preview-collection`, and set `SSL_CERT_FILE`. Write `frontend/tests/e2e/support/imap.ts` to start `../bin/imaptestserver` on a free port with given folders and stop it after the test

### Tests

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T104 [P] Write `frontend/tests/e2e/sources.spec.ts`, using the keyboard alone at 360 px. Open `/sources` directly, since the navigation item comes in T121. Add a source: the port fills 993 for TLS and 143 for STARTTLS. Connect lists each folder with its count and `INBOX` checked. The removal warning is visible before Save. Save, and watch the source go from Syncing with archived out of found to Waiting. The message list shows the source and folder, and the history shows the collection entry. Connect with a wrong password names Sign-in rejected, shows the server's reply, and saves nothing. The page never scrolls sideways (SC-011)
- [ ] T105 [P] Write `frontend/tests/e2e/sources-manage.spec.ts`: Pause, Resume, and Sync now change the state shown. Sync now during a sync says one is running. Edit shows an empty password field, and saving it empty keeps collection working. Adding a folder shows the removal warning. Remove asks for confirmation naming the source, and afterward the source is gone from the list while its messages' history still names it. Adding the same server and user name again links to the existing source
- [ ] T106 [P] Write `frontend/tests/e2e/sources-text.spec.ts`: folders named `<img src=x onerror=alert(1)>`, one holding U+202E, and one 300 characters long show as plain text with codes for the controls and the middle shortened, on the add form, the source page, the list rows, and the history. No script runs and no dialog opens (FR-039)
- [ ] T107 [P] Extend `frontend/tests/e2e/screens.spec.ts` with `/sources`, `/sources/new`, and a source page in both themes, at 360 px and at full width: axe finds no violation, and the page doesn't scroll sideways (FR-042)
- [ ] T108 [P] Write `frontend/src/lib/sources/form.test.ts`. Switching security sets the port to 993 or 143 unless the administrator typed one. The folder choice starts with `INBOX` checked. A check answer maps to a closed result: folders, `check_failed` with category and reply, `source_exists` with the ID, `no_key`, `invalid_fields` with names, timed out after 150 seconds, or unavailable

### Implementation

- [ ] T109 [P] Write `frontend/src/lib/sources/form.ts` to pass T108, and extend `frontend/src/lib/api.ts` with `checkNewSource`, `addSource`, `editSource`, `checkSource`, `pauseSource`, `resumeSource`, `syncSource`, and `removeSource`. Calls that sign in wait 150 seconds
- [ ] T110 Add the two-step add form, the folder checklist, the edit form, and the remove confirmation to `frontend/DESIGN.md`, with FR-007's line that archived mail can't be removed in this version
- [ ] T111 Build `frontend/src/routes/(app)/sources/new/+page.svelte` from T110. Step one asks for name, server, port, security, user name, and password. Connect shows a busy state, then either the folder checklist or the failed step with its category words and the server's reply. Step two shows FR-007's line before Save
- [ ] T112 Extend `frontend/src/routes/(app)/sources/[id]/+page.svelte` with Sync now, Pause, and Resume, enabled to match the state. Show the already-syncing and paused answers in words, and announce state changes without moving focus
- [ ] T113 Extend `frontend/src/routes/(app)/sources/[id]/+page.svelte` with the edit form from T110: the password field starts empty, adding folders runs `checkSource` and shows FR-007's line, and a failed sign-in check shows why and leaves the source as it was
- [ ] T114 Extend `frontend/src/routes/(app)/sources/[id]/+page.svelte` with removal: a confirmation naming the source, then a return to `/sources`

**Checkpoint**: `mise run check` passes, including the new browser tests. Quickstart scenario 9's screen reader pass can run.

---

## Phase 10: Polish & Release

**Purpose**: Plan step 8. Remove the flag, finish the documentation, and run the manual scenarios against a real mail server.

- [ ] T115 Run quickstart scenarios 1 to 8 against Dovecot in Docker, untimed, with `--preview-collection`. Record the image tag and each result in `specs/002-imap-incremental-archive/quickstart.md`. Turn any repeatable finding into a test in the phase that owns it before continuing
- [ ] T116 Run quickstart scenario 9's 15-minute screen reader pass on adding a source and following its first sync. Fix what it finds in `frontend/src/routes/(app)/sources/`, and record the pass in `specs/002-imap-incremental-archive/quickstart.md`
- [ ] T117 [P] Update `docs/adr/0003-mail-libraries.md` Consequences: the client's deadlines miss a silent server and its decoder has no size bound, so `collect` wraps the connection. `imapmemserver` is the test double. Link [research.md](research.md#timeouts)
- [ ] T118 [P] Add the key to `docs/OPERATIONS.md` (FR-043): how to create it, where to keep it, which user must own it, how to start the server with it, that it needs its own backup apart from the data directory, that a lost key means saving every IMAP password again, that anyone who can read the key and the data directory can read the passwords, how to replace it, and that a hard link into the data directory isn't detected
- [ ] T119 Add collection to `docs/OPERATIONS.md` (FR-044): it never changes the mailbox. It collects only the chosen folders, and a folder created later needs adding. Removed messages stay archived. Messages deleted before a sync saw them aren't archived. Microsoft 365 isn't supported, because it requires OAuth 2.0. A server certificate must be trusted by the host, with how to add a private issuer and the `SSL_CERT_FILE` and `SSL_CERT_DIR` variables. Give the 5 minute schedule, the 30, 60, and 120 second limits, the retry waits, the new log entries from [the CLI contract](contracts/cli.md#log-entries), startup cause `key` with its reasons, and the `collection unavailable` entry. List only the servers T115 ran
- [ ] T120 Remove `--preview-collection` from `cmd/mailarkiv/serve.go`, the route marks from `internal/web/web.go`, and the flag from every Go and Playwright test. Collection then always runs, and without a key `serve` logs `collection unavailable`. Add `key create` and `--key-file` to the usage text in `cmd/mailarkiv/main.go`. Update `TestCollectFirstSync`'s usage check, and update the hidden flag sections of `contracts/cli.md` and `contracts/http-api.md` to say the flag is gone
- [ ] T121 Add the Sources item to the sidebar in `frontend/src/routes/(app)/+layout.svelte`, with the navigation item in `frontend/DESIGN.md`, and make `frontend/tests/e2e/sources.spec.ts` reach `/sources` through it
- [ ] T122 [P] Update the status in `README.md`: Slice 2 of 7 works, with IMAP collection
- [ ] T123 Run quickstart scenario 1 timed, following only `docs/OPERATIONS.md` from an empty data directory, from `key create` to the first collected message in the list, with TLS and again with STARTTLS. It must take under 10 minutes (SC-001). Then scenario 2's five trials (SC-009) and scenario 3's `doveadm` comparison (SC-008). Record the times, the tag, and each result in `specs/002-imap-incremental-archive/quickstart.md`
- [ ] T124 Run `mise run check` until it exits 0, using the tasks in `mise.toml`. Report every check run, any that couldn't run with the reason and the remaining risk, and the plan's [remaining risks](plan.md#remaining-risks)

**Checkpoint**: The slice is done. Collection runs without the flag, and every Issue #4 criterion has its evidence from the quickstart's table.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup. Blocks every story.
- **US1 (Phase 3)**: Depends on Foundational.
- **US2 (Phase 4)**: Depends on US1, for `StoreCollected`, the sync, and the checkpoint commit.
- **US3 (Phase 5)**: Depends on US1 for the wrapper and the sync, and on US2 for the per-message records that stalls must leave alone.
- **US4 (Phase 6)**: Depends on US3, for the schedule that a no-key start must not run and the failure columns it must not count.
- **US6 (Phase 7)**: Depends on US4, for `no_key` answers and the password rules that edit applies.
- **US5 (Phase 8)**: Depends on US6, so the screens and the marker test cover Paused, Sync now, and the check routes.
- **Interface (Phase 9)**: Depends on US5, for the list and source pages it extends.
- **Polish (Phase 10)**: Depends on Phase 9.

### User Story Dependencies

```text
Setup → Foundational → US1 → US2 → US3 → US4 → US6 → US5 → Interface → Polish
```

The plan builds every story on one walking skeleton, so the stories aren't independent of US1. US4's tests T069, T070, and T072 need only US1 and could start beside US2. Its implementation waits for US3.

### Within Each Phase

- Tests come first and fail until the phase's code is done.
- Test support tasks (T035, T050, T102, T103) come before that phase's tests.
- Schema before modules, `archive` before `collect`, `collect` before `web`, `web` before screens.
- T005 lands alone, before T006 and T010.

### Parallel Opportunities

- Setup: T002, T003, T004.
- Foundational: T006 to T009, then T012 to T014. T010 and T011 run in sequence.
- Each story's test tasks are `[P]`, except T043, which needs T035 and the crash points.
- US1 implementation: T024, T032, and T033 touch separate files and can start at once. T022 and T023 come before T027.
- US2: T047 and T049 beside T044 to T046.
- US5: T096 and T097 beside T095.
- Polish: T117, T118, and T122. T119 follows T118 in the same file.

---

## Parallel Examples

```bash
# Foundational tests:
Task: "Extend internal/sqlitedb/sqlitedb_test.go"
Task: "Write TestMigrateSlice1Catalog in internal/archive/migrate_test.go"
Task: "Write internal/passwordkey/passwordkey_test.go"
Task: "Extend internal/archtest/archtest_test.go"

# User Story 1 tests:
Task: "Write internal/archive/collected_test.go"
Task: "Write internal/collect/sync_test.go"
Task: "Write internal/collect/conn_test.go"
Task: "Write internal/web/sources_test.go"
Task: "Write TestCollectFirstSync in cmd/mailarkiv/collect_test.go"
Task: "Write frontend/src/lib/format/untrusted.test.ts"

# User Story 3 tests, after T050:
Task: "Write TestSecureConnection in internal/collect/secure_test.go"
Task: "Write TestSchedule in internal/collect/schedule_test.go"
Task: "Write TestCollectStorageFailures in cmd/mailarkiv/collect_faults_test.go"
Task: "Write TestShutdownDuringSync in cmd/mailarkiv/shutdown_test.go"

# User Story 4 tests:
Task: "Write TestKeyCreate in cmd/mailarkiv/key_test.go"
Task: "Write TestServeKeyRefusals in cmd/mailarkiv/keyfile_test.go"
Task: "Write TestNoPasswordInDataDir in cmd/mailarkiv/passwords_test.go"
Task: "Write TestSourceResponsesHaveNoPassword in internal/web/secrets_test.go"

# Phase 9 browser tests, after T102 and T103:
Task: "Write frontend/tests/e2e/sources.spec.ts"
Task: "Write frontend/tests/e2e/sources-manage.spec.ts"
Task: "Write frontend/tests/e2e/sources-text.spec.ts"
```

---

## Implementation Strategy

### MVP First

Phases 1 to 3 give the walking skeleton: add a source over the API, archive its folders through the Slice 1 capture path, and list and download the result, all behind `--preview-collection`. Stop there and run US1's independent test. It proves the path. Don't point it at a real mailbox yet: without Phase 4 a failed message ends the sync, and without Phase 5 there is no schedule.

### Incremental Delivery

Merge to `main` at each checkpoint, with `mise run check` passing. Users see nothing new until T120 removes the flag, apart from the history drawer's wording in Phase 3. Each phase's tests protect what it added from the next.

### Parallel Team Strategy

1. Finish Setup, Foundational, and US1 together.
2. Developer A takes US2, then US3. Developer B writes US4's process tests, then the Phase 8 frontend parsers and words, which depend only on the contract.
3. Developer B takes US4's implementation once US3 lands. Developer A takes US6. Both finish US5 and Phase 9 together.

---

## Notes

- `[P]` tasks touch different files and depend on no unfinished task.
- `[Story]` labels map each task to its story. Setup, Foundational, Interface, and Polish tasks have none.
- Commit after each task or logical group, with a Conventional Commits scope such as `feat(collect): …`.
- Stop at any checkpoint to validate the phase on its own.

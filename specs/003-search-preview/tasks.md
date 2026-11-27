---

description: "Task list for Slice 3: find messages quickly and safely"
---

# Tasks: Find Messages Quickly and Safely

**Input**: Design documents from `specs/003-search-preview/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/http-api.md](contracts/http-api.md), [contracts/search-process.md](contracts/search-process.md), [contracts/cli.md](contracts/cli.md), [quickstart.md](quickstart.md)

**Tests**: Included. Each story has an independent test, SC-001 to SC-013 need checks, and the plan names a check and a layer for each of its 22 credible failures.

**Organization**: Phases follow the plan's [delivery order](plan.md#delivery-order). Each user story phase is one plan step. All seven stories are required before this slice is done.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: The user story the task serves (US1 to US7)

## Rules for every task

- Read [the constitution](../../.specify/memory/constitution.md) and [AGENTS.md](../../AGENTS.md) first. Read [frontend/DESIGN.md](../../frontend/DESIGN.md) before touching a screen.
- Work on branch `003-search-preview`. Open a pull request at each checkpoint, and merge it when CI passes.
- `main` stays buildable and checked. Processing and search run on normal startup; see the [greenfield exception](plan.md#complexity-tracking).
- Write each phase's test tasks first and watch them fail. Expected values come from the spec, the data model, or a contract, never from running the code (constitution V).
- Run `mise run check` at each checkpoint. It must exit 0 before the next phase starts.
- Use real SQLite, real files, real worker processes, and the real search process. Use doubles only for the clock, the free space reader, and the internet's image hosts (`internal/imagetest`). Never skip a test or add a retry.
- Migration 0003 is final once merged. A later schema change needs migration 0004.
- Processing runs once per message (FR-006). A development data directory keeps whatever an earlier phase stored, so start a new one after Phase 4 lands.
- Go tests find the search program at `bin/mailarkiv-search` and its fault build at `bin/faults/mailarkiv-search`, and fail with "run mise run search:build" when either is missing. `mise run server:test` builds both first.
- Packages whose tests start parse workers run the test binary as the worker: `TestMain` calls `processing.RunWorker` when `MAILARKIV_TEST_PARSE_WORKER` is set. Tests shorten the processing limit to 2 s and other waits through unexported options or an injected clock. A unit test asserts each production value. Process tests can't shorten them.
- Fixtures hold synthetic mail only. Addresses use `example.test`, or the `example.com`, `example.org`, `mail.example.com`, and `attacker.example` names the spec's scenarios need. Leak tests use marker strings such as `MARKER-SUBJ-7d2e`.
- Search words and filters never appear in a page or request address, the log, or the status screen. Mail content never reaches the log or the status screen.
- Go tests send the session cookie by hand, as in Slices 1 and 2.
- Code comments never cite spec, plan, or task IDs (AGENTS.md).
- "Extend" means add to a file that exists or that an earlier task created. "Change" means the specified behavior changed. Say so in the commit message.

## How this list relates to the plan

| Phase | Plan step | Finishes | Shown by |
| ----- | --------- | -------- | -------- |
| 1 Setup | Part of 1 | Rust toolchain and checks, ADR 0007 amended, ADR 0009, `ARCHITECTURE.md` | `mise run check` with an empty crate, link check |
| 2 Foundational | Part of 1 | Migration 0003, list order, module rules, the frame codec | `TestMigrateSlice2Catalog`, `archtest` |
| 3 US1 | 1 Walking skeleton | US1 scenarios 1, 2, 9 (one trial), 13, 14 | `TestSearchFirstMessage`, `TestServeWithoutSearchProgram` |
| 4 US3 | 2 Processing | US3 scenarios 1 to 6, 9, 10. FR-013's list | `TestMalformedSet` (SC-003), `TestWorkerKilledAtLimit` |
| 5 US1 | 3 Search | US1 scenarios 3 to 8, 10 to 12 | `TestSearchFixedSet` (SC-001), `TestSearchableWithin60s` (SC-010) |
| 6 US2 | 4 Message view | US2 | `preview.spec.ts` (SC-002), `images.spec.ts` (SC-013) |
| 7 US5 | 5 Index reliability | US5. US3 scenarios 7 and 8 | `TestIndexCrashMatrix`, `TestSearchProcessKilled` (SC-005 to SC-008) |
| 8 US6 | 6 Visibility | US6 | `TestNoMarkersInLogOrStatus` (SC-009) |
| 9 US4 | 7 Access and interface completion | US4. FR-050 | `TestPreviewWithoutSession` (SC-004), `screens.spec.ts` (SC-012) |
| 10 US7 | 8 Sizing | US7 | The sizing run (SC-011) |
| 11 Release | 8 Release | FR-051, FR-052, the README | Quickstart scenarios 1 to 7 |

US1 spans Phases 3 and 5, because the plan delivers the walking skeleton before processing's bounds. US5 comes before US6, as in the plan, so the status screen can show every state.

Writing these tasks exposed places where the plan's order or contracts don't hold. T002 records them.

| Change | Why |
| ------ | --- |
| Everything processing stores lands in Phase 3: the list date rule, `has_attachments` with the embedded image rule, enmime's problem kinds, and the body cut at 2,000,000 bytes. Phase 4 keeps the boundary scanner, the bounds it reports, the worker limits, and the malformed set | Processing runs once per message (FR-006), so a later phase couldn't correct a stored result. The body cut must also come first, because an uncut body can pass the 64 MiB frame limit |
| Index documents are complete in Phase 3: recipients, attachment names, and the words of both bodies | The worker reads them anyway for the summary, and the index's schema is version 1 from the start |
| The restart wait doubling from 1 to 60 s, and the wait before a failed batch is sent again, land in Phase 3. The crash window and Needs rebuild stay in Phase 7 | With a fixed wait, a missing search program records a failure event every second, and repeats merge only in Phase 8 |
| Four failure categories reach `opstatus` and the client in Phase 3: `search_process_stopped`, `search_process_did_not_start`, `search_process_did_not_answer`, `index_write_failed`. The other four land in Phase 7. Repeats, `detail`, and the status section land in Phase 8 | `parseFailure` in `frontend/src/lib/api.ts` refuses an unknown category, and constitution III changes consumers together, as in Slice 2 |
| The list order switches to `message_list_order` in Phase 2, with migration 0003 | The migration drops `message_by_latest_observation`, which the old order uses |
| Worker requests carry the original as raw bytes after their JSON frame, and an `attachment` answer carries the decoded bytes the same way | An original can be 150,000,000 bytes, and a frame is at most 64 MiB. JSON would add a third through base64 |
| The Go frame codec lives in `internal/processing/frame.go`, and `search` uses it | Both need the same frames. `search` already imports `processing`, and the plan's import rules name no shared package |
| Process tests start each server binary from its own directory: the default build beside `bin/mailarkiv-search`, the `faults` build beside `bin/faults/mailarkiv-search` | The server finds the search program in its own directory, and `TestMain` now puts both server builds in one directory |
| `TestSearchFixedSet`'s messages live in `internal/web/testdata/searchset/`, and `sizing seed` imports them | The quickstart's seed includes "the fixed set the automated tests search" |
| The Go fault action `hang` serves a `parse-worker` point, used by `TestHungWorkers` | The plan adds the action without naming its use. Hung workers must not stop capture or shutdown (FR-002) |
| ADR 0003 changes in Phase 4, as the plan's document table says. The delivery order also lists it under step 8 | The boundary scanner and enmime's options land in step 2 |
| `ARCHITECTURE.md` and ADR 0007 link to `docs/research/search-benchmark/README.md`, which commit `696b3a0` removed on purpose. Both links point to that file at commit `f61d6a1` | The plan says to restore the file or change the link. The removal was deliberate |
| `low_disk_space` is forced for the marker check in `internal/search` with the injected free space reader, not in the process test | A process test can't lower free space without `sudo`. Quickstart scenario 7 covers a real file system |

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: The branch, the documents step 1 changes, and the Rust toolchain with its checks.

- [X] T001 Create branch `003-search-preview` from `main`. Commit `specs/003-search-preview/`, the uncommitted sender rule in `frontend/DESIGN.md`, and the preview rules in `docs/ARCHITECTURE.md` as `docs(spec): add Slice 3 spec, plan, and tasks`
- [X] T002 [P] Record the changes from "How this list relates to the plan" in `specs/003-search-preview/plan.md`: the Delivery order rows for steps 1, 2, 3, 5, and 8, and the Required checks row for `mise run search:test`. In `specs/003-search-preview/contracts/cli.md`, under `mailarkiv parse-worker`, say that a request names the original's size in its JSON frame and the raw bytes follow, and that an `attachment` answer names the decoded size and the raw bytes follow
- [X] T003 [P] Amend `docs/adr/0007-tantivy-search-process.md` with the facts in [research.md](research.md#tantivy-version), [Connection](research.md#connection), and [Index maintenance](research.md#index-maintenance): Tantivy 0.27.0 with the release dates 0.26.0 on 2026-03-31, 0.26.1 on 2026-04-21, 0.26.2 on 2026-09-08, and 0.27.0 on 2026-10-17; RUSTSEC-2026-0253 in `lru` 0.16, fixed by `lru` 0.18.2, which 0.27.0 requires; no advisory and no principle IV exception; pipes instead of a socket; a second file beside the server; `NoMergePolicy` with maintenance by request; and the finding that automatic merges retry after each commit but report failures only as log text. Point the benchmark link to `https://github.com/maltehedderich/mailarkiv/blob/f61d6a1/docs/research/search-benchmark/README.md`
- [X] T004 [P] Write `docs/adr/0009-parse-worker-processes.md` in the style of `docs/adr/0005-password-hashing.md`, from [research.md](research.md#time-limit-and-isolation). Cover why: Go can't stop a goroutine, fatal runtime errors escape `recover`, and originals reach 150,000,000 bytes. Cover the design: the hidden `parse-worker` subcommand, two background and two interactive workers, the 60 s `SIGKILL`, `Pdeathsig: SIGKILL`, one retry in a fresh worker after a death, and that a worker reads standard input, writes standard output, and opens no file or connection. Name the rejected alternatives: parsing in the server with `recover`, a process per message, and a Rust parser in the search process
- [X] T005 [P] Change `docs/ARCHITECTURE.md`: the search index rules say pipes instead of "listens only on a Unix socket" and maintenance by request; the technology table names Tantivy 0.27.0; the `go.mod` line says `golang.org/x/net` v0.62.0 and `golang.org/x/text` v0.43.0 arrive with this slice, pinned. Point the benchmark link to the file at commit `f61d6a1`, as in T003
- [X] T006 Create the crate `mailarkiv-search/`
  - `Cargo.toml`: package `mailarkiv-search`, edition 2024, `rust-version = "1.100"`. Dependencies `tantivy = { version = "=0.27.0", default-features = false, features = ["mmap", "lz4-compression"] }`, `mimalloc = "=0.1.52"`, `serde` with `derive`, `serde_json`, and `log`, all of which Tantivy already pulls in. Feature `faults = []`. `panic = "abort"` in the release and dev profiles, and `lto = true` and `codegen-units = 1` in release
  - `src/main.rs`: mimalloc as the global allocator, exit 2 when given any argument, read standard input until it closes, then exit 0
  - Run `cargo generate-lockfile` and commit `Cargo.lock`. Confirm `cargo tree --locked --target x86_64-unknown-linux-musl -e normal --prefix none | sort -u | wc -l` counts 113 crates and that `lru` resolves to 0.18.x. Add `/mailarkiv-search/target/` to `.gitignore`
- [X] T007 Change `mise.toml`
  - Tools: `rust = { version = "1.100.0", targets = "x86_64-unknown-linux-musl", profile = "minimal", components = "rustfmt,clippy" }` and `"cargo:cargo-audit" = "0.23.0"`. Run `mise install` and commit `mise.lock`
  - `search:build`: in `mailarkiv-search/`, `cargo build --release --locked --target x86_64-unknown-linux-musl`, then copy the binary to `bin/mailarkiv-search`. `search:build:faults`: `cargo build --locked --features faults --target x86_64-unknown-linux-musl`, then copy to `bin/faults/mailarkiv-search`
  - `search:test`: `cargo test --locked`, then `cargo test --locked --features faults`
  - `lint` adds `cargo fmt --check` and `cargo clippy --locked --all-targets -- -D warnings`, and the clippy run again with `--features faults`. `format` adds `cargo fmt`. `audit` adds `cargo audit` in `mailarkiv-search/`
  - `server:test` depends on `search:build` and `search:build:faults`. `test:e2e` and `build` depend on `search:build`. `test` adds `search:test`
  - `setup` stops with "Install musl-gcc: sudo apt-get install musl-tools" when `command -v musl-gcc` fails, and runs `cargo fetch --locked`
  - `dev:server` depends on `server:build` and `search:build` and runs `bin/mailarkiv serve` instead of `go run`, so the search program sits beside it. `clean` also removes `mailarkiv-search/target`
- [ ] T008 Change `.github/workflows/check.yml`: install `musl-tools` with `strace`, and cache the cargo registry and `mailarkiv-search/target` keyed on `mailarkiv-search/Cargo.lock` and `mise.lock`. mise runs rustup with its own `CARGO_HOME`, so cache the registry under the path `mise env` prints, not `~/.cargo`, if they differ. Note the first run's duration in the pull request
- [X] T009 Change the required checks table in `AGENTS.md`: `lint` adds `cargo fmt --check` and `cargo clippy`, `audit` adds `cargo audit`, a new `search:test` row (Unit, integration), `server:test` builds `bin/mailarkiv-search` and `bin/faults/mailarkiv-search` first, `test:e2e` builds `bin/mailarkiv-search`, and `test` runs four tasks. Add `search:build` and `search:build:faults` to the other tasks, and say that `mise run setup` needs `musl-gcc` from `musl-tools`

**Checkpoint**: `mise run check` passes with the empty crate, locally and in CI. Check local links and that the plan, contracts, and this list agree, as AGENTS.md requires for documentation changes.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The schema, the new list order, the module rules, and the frame codec. Every story needs these.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

### Tests for Phase 2

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [X] T010 [P] Extend `internal/sqlitedb/sqlitedb_test.go`
  - Change the schema test to expect fifteen tables, adding `message_summary`, `message_problem`, `search_generation`, `search_rebuild`, and `search_needs_rebuild`
  - `message` checks: `processing_state = 'other'` refused. `processed` with `processed_seq` NULL refused, and `not_processed` with `processed_seq` set refused. A second message with the same `processed_seq` refused
  - `message_summary`: `has_attachments = 2` refused, `date_reason = 'late'` refused, `more_senders = -1` refused, a row for message 999 refused
  - `message_problem`: `kind = 'other'` refused. A repeat of `(message_id, position, kind)` refused, and the same kind at two positions accepted
  - `search_generation`: `role = 'old'` refused, a second `active` refused, a second `building` refused, one of each accepted. After deleting the row with the highest `id`, the next insert gets a higher one
  - `search_rebuild`: `outcome = 'stopped'` refused, and an outcome with `ended_at` NULL refused. `search_needs_rebuild`: `id = 2` refused, `category = 'other'` refused
- [X] T011 [P] Write `TestMigrateSlice2Catalog` in `internal/archive/migrate_test.go`, following `TestMigrateSlice1Catalog`. Build a Slice 2 catalog by running `0001_initial.sql` and `0002_imap_collection.sql` on a raw connection and setting `user_version` to 2. Insert by hand: a message with two imports at different times and one collection entry, a collection-only message, an import-only message, the source and folder rows, `collected_message_count`, and each blob. Then `sqlitedb.Open` and `archive.Open`. Expected values come from the inserted rows: every message is `not_processed` with `processed_seq` NULL, and `first_archived_at` and `list_date` both equal its earliest `observed_at`. `History` returns every entry unchanged, every blob is byte-identical, `PRAGMA foreign_key_check` returns nothing, `message_by_latest_observation` is gone, the four new indexes exist, and a new capture gets an ID above every old one
- [X] T012 [P] Extend `internal/archive/capture_test.go` and `internal/archive/collected_test.go`: a new message has `first_archived_at` and `list_date` equal to its entry's `observed_at`, `processing_state` `not_processed`, and `processed_seq` NULL. An import of the same bytes an hour later, and a collection of them, leave both times unchanged
- [X] T013 [P] Write `internal/archive/catalog_test.go`: `List` orders by `list_date`, then `first_archived_at`, then `id`, all newest first. Set `list_date` through SQL in the test. Two messages with equal list dates and first archive times order by ID. Across 120 messages with equal dates, pages of 50 never repeat or skip a message. A message imported again keeps its place (US1 scenario 14)
- [X] T014 [P] Extend `internal/archtest/archtest_test.go`
  - Imports, from [the plan](plan.md#project-structure): `cmd/mailarkiv` adds `internal/processing` and `internal/search`. `internal/web` adds both. `internal/processing` uses `archive`, `mailparse`, and `fault`. `internal/search` uses `archive`, `processing`, `opstatus`, `sqlitedb`, and `fault`. `internal/mailparse` and `internal/imagetest` import no module. `internal/imagetest/cmd/imagehost` uses `imagetest`. `internal/sizing/cmd/sizing` uses `archive`, `processing`, `search`, `opstatus`, and `sqlitedb`. No package imports `sizing`, and only `imagetest/cmd/imagehost` imports `imagetest`
  - Table writes: `message_summary` and `message_problem` belong to `archive`. `search_generation`, `search_rebuild`, and `search_needs_rebuild` belong to `search`
  - Add one bad in-memory source per new rule, so the checker can't pass on an empty tree
- [X] T015 [P] Write `internal/processing/frame_test.go`: a value round-trips. The length is 4 bytes, unsigned, big-endian. A length of 0 is an error. A length over 64 MiB is an error before any body byte is read. A short body is an error, and so is JSON that doesn't parse

### Implementation for Phase 2

- [X] T016 Write `internal/sqlitedb/migrations/0003_search_preview.sql` to pass T010 and T011, as [the data model](data-model.md#migration-0003) says. Every table is `STRICT`, and times are integer Unix milliseconds in UTC
  - Rebuild `message` as `message_new` with `id` INTEGER PRIMARY KEY AUTOINCREMENT, `content_hash`, `size`, and `latest_observation_id` as before, and the new columns: `first_archived_at` INTEGER NOT NULL ("Time of the message's first entry. Set at capture and never changed"), `list_date` INTEGER NOT NULL ("Set to `first_archived_at` at capture. Processing sets it once"), `processing_state` TEXT NOT NULL `CHECK (processing_state IN ('not_processed', 'processed', 'processed_with_problems', 'unreadable'))`, and `processed_seq` INTEGER ("NULL exactly when `processing_state` is `not_processed`"), with `CHECK ((processing_state = 'not_processed') = (processed_seq IS NULL))`. Copy every row, setting `first_archived_at` and `list_date` to the earliest `observed_at` among its entries and `processing_state` to `not_processed`. Carry `sqlite_sequence`'s value for `message` over, so IDs are never reused. Drop the old table, rename the new one, and don't recreate `message_by_latest_observation`
  - Indexes: `message_list_order` on `(list_date DESC, first_archived_at DESC, id DESC)`. `message_with_problems` on the same columns `WHERE processing_state IN ('processed_with_problems', 'unreadable')`. `message_by_processed_seq` UNIQUE on `processed_seq` `WHERE processed_seq IS NOT NULL`. `message_not_processed` on `id DESC` `WHERE processing_state = 'not_processed'`
  - `message_summary`: `message_id` INTEGER PRIMARY KEY REFERENCES `message`, `sender_name` TEXT ("At most 500 characters"), `sender_address` TEXT ("At most 320 characters"), `more_senders` INTEGER NOT NULL `CHECK (more_senders >= 0)`, `subject` TEXT ("At most 1,000 characters"), `date_header` TEXT ("at most 200 characters. NULL when there is none"), `date_reason` TEXT `CHECK (date_reason IN ('missing', 'unreadable', 'ahead'))` ("NULL when `list_date` is the Date header's time"), `has_attachments` INTEGER NOT NULL `CHECK (has_attachments IN (0, 1))`. `mailparse` enforces the character limits
  - `message_problem`: `message_id` INTEGER NOT NULL REFERENCES `message`, `position` TEXT NOT NULL ("The part number, or an empty string for the whole message"), `declared_type` TEXT ("lowercased, without parameters. At most 255 characters. NULL when the part declares none"), `kind` TEXT NOT NULL `CHECK (kind IN ('unknown_charset', 'invalid_encoding', 'malformed_header', 'malformed_structure', 'cut_short', 'too_many_parts', 'nested_too_deeply', 'body_cut', 'took_too_long'))`. PRIMARY KEY `(message_id, position, kind)`
  - `search_rebuild`: `id` INTEGER PRIMARY KEY, `started_by` INTEGER NOT NULL REFERENCES `account`, `started_at` INTEGER NOT NULL, `ended_at` INTEGER ("NULL while it runs"), `outcome` TEXT `CHECK (outcome IN ('finished', 'failed'))` ("NULL while it runs"), with `CHECK ((ended_at IS NULL) = (outcome IS NULL))`
  - `search_generation`: `id` INTEGER PRIMARY KEY AUTOINCREMENT ("Also the directory name under `index/`. Never reused"), `role` TEXT NOT NULL `CHECK (role IN ('active', 'building'))` with a unique index on `role` ("allows at most one of each"), `schema_version` INTEGER NOT NULL ("1 in this slice"), `cursor` INTEGER NOT NULL DEFAULT 0 ("Written only after the search process confirms a commit"), `rebuild_id` INTEGER REFERENCES `search_rebuild` ("NULL for a build that started because no index existed"), `created_at` INTEGER NOT NULL
  - `search_needs_rebuild`: `id` INTEGER PRIMARY KEY `CHECK (id = 1)`, `category` TEXT NOT NULL `CHECK (category IN ('search_process_stopped', 'index_maintenance_failed', 'index_damaged'))`, `since` INTEGER NOT NULL
- [X] T017 Change the capture commit in `internal/archive/capture.go` to pass T012: the `INSERT INTO message` sets `first_archived_at` and `list_date` to the entry's `observed_at` and `processing_state` to `not_processed`. A message captured again keeps both times
- [X] T018 Change `List` in `internal/archive/catalog.go` to pass T013, ordering by `list_date DESC, first_archived_at DESC, id DESC` through `message_list_order`. This replaces "most recent entry first". Change the tests in `internal/web/list_test.go` that assumed the old order, and say so in the commit message
- [X] T019 [P] Write `internal/processing/frame.go` to pass T015: `WriteFrame(w, v)`, `ReadFrame(r, v)`, and `MaxFrame = 64 << 20`. A package comment says what `processing` owns

**Checkpoint**: `mise run check` passes. A Slice 2 catalog migrates to version 3 with its history and blobs unchanged. The list is in list date order, which equals first archive time for every message so far.

---

## Phase 3: User Story 1 - Find a message by its fields and text (Priority: P1) 🎯 MVP

**Goal**: The walking skeleton, plan step 1. Background processing reads each message in a worker process and stores its summary, list date, and problems. Rows show sender, subject, and list date. The search process indexes processed messages through a cursor with delete before add, an index is built automatically at start, and a words field searches it. This phase finishes US1 scenarios 1, 2, 9 (one trial), 13, and 14. Phase 5 finishes US1.

**Independent Test**: Import the messages in `internal/mailparse/testdata/plain/`, wait until indexing is Up to date, search for "quartals", and check that the message whose body says "Quartalsbericht" is the only result and that its row shows its sender and subject.

### Test support for User Story 1

- [X] T020 [US1] Write synthetic fixtures in `internal/mailparse/testdata/plain/`, one `.eml` each: plain text only; HTML only, with "Quartalsbericht" only in the HTML's text and `<script>Geheim</script>` in its markup; `multipart/alternative` with different words in each body; a subject in UTF-8 encoded words and a body in ISO-8859-1; a sender whose name equals the address in another case; three From addresses; no From; no Subject; no Date; an unreadable Date; a Date three days in the future; a Date in 1970; an attachment named `Vertrag_2026.pdf`; a signature logo referenced through `cid:`; an attached `message/rfc822`; To, Cc, and Bcc; and bodies with "Müller", "MÜLLER", and "Muller". Each fixture's comment-free expected values live in the tests

### Tests for User Story 1

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [X] T021 [P] [US1] Write `TestRead` in `internal/mailparse/parse_test.go` against T020's fixtures. Expected values come from [the data model](data-model.md) and each fixture
  - Fields decoded from encoded words, encoded parameters, and declared character sets (FR-007): the sender's name and address, `more_senders`, subject, Date header text, To, Cc, and Bcc in full
  - Part numbers follow IMAP section numbers: the parts of a multipart are `1`, `2`, `3`, a part inside part 2 is `2.1`, and a message that isn't multipart has one part, `1`
  - Bodies: the first `text/plain` and the first `text/html` part that aren't attachments, decoded to UTF-8
  - The HTML's words exclude `script`, `style`, and `head`, with a space at each block element, so "Geheim" isn't there and words don't run together
  - Attachments in part order with position, name from `Content-Disposition` `filename` or else `Content-Type` `name`, lowercased declared type without parameters, decoded size, and kind: `message` for `message/rfc822`, `embedded_image` for an `image/*` part whose `Content-ID` the HTML references through `cid:`, else `file`
  - The row summary: `sender_name` NULL when it equals the address ignoring case. Values cut on a character boundary at 500, 320, 1,000, and 200 characters for name, address, subject, and Date header text. `has_attachments` false for a message whose only attachment is an embedded image
- [X] T022 [P] [US1] Write `TestListDateRule` in `internal/mailparse/listdate_test.go` from [the list date table](data-model.md#list-date): missing gives the first archive time with `missing`. An unreadable header gives `unreadable`. Exactly 24 hours after the first archive time keeps the header's time, and 24 hours plus one millisecond gives `ahead`. 1970 keeps the header's time. A `+0100` and a `-0700` offset give the same instant as the equivalent UTC time
- [X] T023 [P] [US1] Write `TestProblemKinds` in `internal/mailparse/problems_test.go`
  - List every error name enmime v2.5.0 defines and assert its kind from [research.md](research.md#problem-kinds). An unknown name gives `malformed_structure`, so an enmime upgrade that adds a name fails this test
  - A problem holds the part's position and declared type and never the error's `Detail`. A header enmime can't read makes the message `unreadable` with `malformed_header` at position "". A From header that isn't an address list gives `malformed_header`, `sender_address` NULL, and the decoded header text as `sender_name`. An unknown character set gives replacement characters and `unknown_charset`
  - A text body of 2,000,001 bytes whose last character is 3 bytes long is cut to the last whole character at or below 2,000,000 bytes, with `body_cut` on that part
- [X] T024 [P] [US1] Write `internal/archive/processed_test.go` with real files and SQLite
  - `RecordProcessed` changes only a `not_processed` message. It sets the state, `processed_seq` as the highest existing value plus 1, `list_date`, the summary for `processed` and `processed_with_problems` only, and the problem rows, in one transaction. A second result for the same message changes nothing
  - `NotProcessed` returns IDs newest capture first and leaves out the IDs it is told to skip. `ProcessedAfter(cursor, n)` returns messages in `processed_seq` order with their state, summary, `first_archived_at`, and `list_date`. `Rows(ids)` returns rows in the order given. `HighestProcessedSeq` matches
  - The capture signal fires after a commit that adds a message, and the restore signal after one that restores an original
- [X] T025 [P] [US1] Write `internal/processing/main_test.go`, `internal/processing/worker_test.go`, and `internal/processing/background_test.go`
  - `TestMain` runs `RunWorker` on standard input and output when `MAILARKIV_TEST_PARSE_WORKER=1`
  - `read` through a real worker returns the sender, subject, and attachment names of three T020 fixtures, with the original sent as raw bytes after its frame. A worker opens no file: `/proc/<pid>/fd` holds only descriptors 0, 1, and 2
  - Processing with real SQLite, files, and two workers: importing five fixtures wakes processing, each ends in its expected state, and `processed_seq` runs 1 to 5 with no gap, newest capture first. A new processor on the same catalog processes nothing again. A message whose original was deleted stays `not_processed`, records the Slice 1 event once, and logs `processing skipped` with `message_id` and `category`. Importing that file again restores it, and processing then reads it
- [X] T026 [P] [US1] Write `mailarkiv-search/tests/support/mod.rs` and `mailarkiv-search/tests/protocol.rs`. The support module starts `env!("CARGO_BIN_EXE_mailarkiv-search")` in a temporary data directory and reads and writes frames. Tests, from [the contract](contracts/search-process.md): the first frame is `{"type":"hello","protocol":1,"tantivy":"0.27.0","schema_version":1}` within 10 s. Any argument exits 2. Closing standard input exits 0. A length of 0, a frame over 64 MiB, or JSON that doesn't parse exits 3. Answers carry their request's `id`
- [X] T027 [P] [US1] Write `mailarkiv-search/tests/index.rs` and `mailarkiv-search/tests/matching.rs`
  - `open` with `create: true` makes `index/<generation>/`, and without it answers `index_missing` for a missing directory. An index made with schema version 2 answers `schema_mismatch`. Operations on an unopened generation answer `unknown_generation`
  - `delete_before_add_keeps_one`: message 17 indexed twice in one commit and again in a later one is found once, with the latest document's subject
  - Words: "quartals" and "QUARTALS" find "Quartalsbericht". Every word must match. Results sort by `list_date`, then `first_archived_at`, then `id`, all newest first
- [X] T028 [P] [US1] Write `internal/search/helpers_test.go` and `internal/search/supervisor_test.go`. Helpers find `bin/mailarkiv-search` from the repository root, build a module on a temporary data directory with real SQLite, archive, and test-binary workers, and inject the clock
  - The search process starts with no arguments, an empty environment, the data directory as working directory, and `Pdeathsig: SIGKILL`, and sends `hello`
  - A missing program records `search_process_did_not_start` with cause `missing`, a file without execute permission `not_executable`, and a wrong `hello` from a test-binary mode `bad_hello`
  - Killing the process records `search_process_stopped` and starts it again. `TestRestartWait` with `testing/synctest`: waits of 1, 2, 4, 8, 16, 32, 60, and 60 s, and back to 1 s after the process ran for 10 minutes
  - Lines on its standard error aren't logged, only their count as `search process output ignored` with `lines`
- [X] T029 [P] [US1] Write `internal/search/indexer_test.go` and `internal/search/generation_test.go`
  - Start creates `index/` with mode 0700 and removes directories no `search_generation` row names. With no row, or with an active row whose directory is missing, it deletes the rows, inserts a `building` row with no rebuild, and builds. When the cursor reaches the highest `processed_seq`, that row becomes `active`
  - Batches hold at most 500 messages or 32 MiB, checked with both bounds lowered. A batch commits when the queue is empty. The cursor is written only after the commit's answer. An `unreadable` message gets no document and the cursor passes it. A message captured after the build is searchable once processing wakes the indexer
  - A message whose original was deleted after processing is indexed from its summary alone, and the Slice 1 event is recorded
  - With `index/<id>/` made read-only, a batch records `index_write_failed` with the generation ID, the batch's first and last message ID, and cause `permission_denied`, and is sent again after 1, 2, 4 s. After the directory is writable again, the batch commits and the next failure waits 1 s again
  - `TestCallLimitValues` asserts each operation's limit: `open` 60 s, `index` 120 s, `search` 12 s, `maintain` 30 minutes, `close` 60 s, `size` 10 s
- [X] T030 [P] [US1] Extend `internal/opstatus/opstatus_test.go`: `search_process_stopped`, `search_process_did_not_start`, `search_process_did_not_answer`, and `index_write_failed` exist with those wire names, their events log at error level, and `ProblemCount` counts them
- [X] T031 [P] [US1] Write `internal/web/search_test.go` and change `internal/web/messages_test.go`, `internal/web/list_test.go`, and `internal/web/imports_test.go` for [the new row](contracts/http-api.md#changed-message-rows)
  - Rows carry `processing`, `list_date`, `list_date_is_archive_time`, `sender` with `name`, `address`, and `more`, `subject`, `has_attachments`, `observation_count`, and `latest`. A `not_processed` row has `sender` and `subject` null and `list_date_is_archive_time` true. The 201 import answer's `message` has the same shape
  - `POST /api/search` with `words` answers `items` in list order, `page`, `page_size` 50, `total`, `not_included`, `full_list` false, and `may_be_incomplete` false. A body that isn't JSON, has an unknown field, or is over 64 KiB answers 400 `invalid_fields`. Page 0 answers `invalid_fields` and page 2,001 `page_too_deep`. With the search process stopped, 503 `search_unavailable` with `cause` and `next_start_at`
- [X] T032 [P] [US1] Change `TestMain` in `cmd/mailarkiv/process_test.go` to build each server binary into its own directory, copying `bin/mailarkiv-search` beside the default build and `bin/faults/mailarkiv-search` beside the `faults` build. Then write `cmd/mailarkiv/search_test.go`
  - `TestSearchFirstMessage`: start the server, import a T020 fixture, and poll `POST /api/search` with a body word every 500 ms until it is found, within 60 s. Its row shows the fixture's sender and subject. After a restart, the same search finds it once
  - `TestServeWithoutSearchProgram`: a server directory without `mailarkiv-search` serves. Import, list, and download work, and search answers 503 `search_unavailable` with cause `search_process_did_not_start`
  - `mailarkiv parse-worker` with standard input on a pseudo-terminal, opened through `golang.org/x/sys/unix`, exits 2 with the usage text, and the usage text doesn't list `parse-worker`
  - Extend `cmd/mailarkiv/shutdown_test.go`: SIGTERM with the search process and the workers running exits within the grace, and no child process remains
- [X] T033 [P] [US1] Extend `frontend/src/lib/api.test.ts` and write `frontend/src/lib/format/sender.test.ts` and `frontend/src/lib/search/form.test.ts`
  - `parseMessage` reads the new row for each `processing` word. An unknown word makes the result `unavailable`. The search answer parses, and so do 400, 422, and both 503 bodies
  - Sender display: a name and an address are separate parts; the address splits at its last `@` into the part that may shorten and the domain that stays; no name shows the address once; control and bidirectional characters become badges through `displayText`
  - The words-only form builds a body with every filter empty, `has_attachments` false, and `page` 1
- [X] T034 [US1] Change the browser tests that read file names from rows in `frontend/tests/e2e/` (`file-name.spec.ts`, `paging.spec.ts`, `import-flow.spec.ts`, `journey.spec.ts`, `history.spec.ts`) to find them in the history drawer, and say so in the commit message. Write `frontend/tests/e2e/search.spec.ts`: import a fixture, search for a body word, and see its row. Neither the page address nor any request address contains the word

### Implementation for User Story 1

- [X] T035 [US1] Run `go get github.com/jhillyerd/enmime/v2@v2.5.0 golang.org/x/net@v0.62.0 golang.org/x/text@v0.43.0 golang.org/x/sys@v0.49.0` and `go mod tidy`. Confirm `go.mod` gains the 20 modules [research.md](research.md#parser-and-its-dependencies) lists and that `govulncheck ./...` reports nothing
- [X] T036 [US1] Implement `internal/mailparse/parse.go`, `internal/mailparse/parts.go`, and `internal/mailparse/fields.go` to pass T021's field and part cases, importing no module. Parse with `SkipMalformedParts(true)`, `DisableTextConversion(true)`, `DisableCharacterDetection(true)`, and `MaxMIMEParts(1000)`. Number parts while walking enmime's tree. Read addresses with `net/mail`, keeping the decoded text when From isn't an address list
- [X] T037 [US1] Implement `internal/mailparse/listdate.go` and `internal/mailparse/problems.go` to pass T022 and T023. Read the Date header with `net/mail.ParseDate`. Map only enmime's error `Name`
- [X] T038 [US1] Implement `internal/mailparse/attachments.go`, `internal/mailparse/text.go`, and `internal/mailparse/summary.go` to pass the rest of T021 and the body cut in T023. Read HTML words with the `x/net/html` tokenizer. Cut on a character boundary
- [X] T039 [US1] Implement `internal/archive/processed.go` to pass T024: `RecordProcessed`, `NotProcessed`, `ProcessedAfter`, `Rows`, `HighestProcessedSeq`, and the capture and restore signals, each a channel with a buffer of 1 that a commit fills without blocking. Change `Page` and `Message` in `internal/archive/catalog.go` and `internal/archive/archive.go` to carry the row's state, list date, whether it is the archive time, and the summary, read through a `LEFT JOIN` on `message_summary`
- [X] T040 [US1] Implement `internal/processing/worker.go` to pass T025's worker cases: `RunWorker(in, out) int` reads one request at a time. `read` takes the original's size and, after the frame, its bytes, and answers the parsed message, with the bodies' words when the request asks for text. It opens no file or connection
- [X] T041 [US1] Implement the background pool in `internal/processing/pool.go`: two workers, started by a function that returns the command (production: the server's own executable with `parse-worker`; tests: the test binary with `MAILARKIV_TEST_PARSE_WORKER=1`), with `Pdeathsig: SIGKILL` and an empty environment except `MAILARKIV_FAULTS`. A worker that exits is started again. Start workers from goroutines that don't lock their OS thread, because `Pdeathsig` follows the thread that forked
- [X] T042 [US1] Implement `internal/processing/background.go` and `internal/processing/read.go` to pass T025's processing cases. The loop takes `NotProcessed` newest capture first, opens each original through `archive.OpenVerified`, sends it to the background pool, and calls `RecordProcessed`. It wakes on the capture signal. It keeps a set of IDs skipped for a missing or damaged original, which the restore signal clears, and logs `processing skipped`. It signals `search` after each result. `Read(ctx, id, withText)` serves the indexer
- [X] T043 [US1] Implement `mailarkiv-search/src/frame.rs`, `mailarkiv-search/src/protocol.rs`, and the request loop in `mailarkiv-search/src/main.rs` to pass T026. Requests and answers are serde types with closed error codes. One thread reads frames and dispatches, one writes answers in any order, two run searches, and one writer thread runs per open index. Closing standard input exits 0
- [X] T044 [US1] Implement `mailarkiv-search/src/schema.rs`, `mailarkiv-search/src/analysis.rs`, and `mailarkiv-search/src/index.rs` to pass T027's index cases, with every field in [the matching table](research.md#matching): `all`, `subject`, `from`, `to`, `attachment` as words; `from_address`, `to_address`, `from_domain`, `to_domain` lowercased whole; `id`, `list_date`, `first_archived_at`, and `has_attachments` as numbers. Words use `SimpleTokenizer`, a filter dropping words over 100 bytes, and `LowerCaser`, and record only which documents hold a word. The writer uses `NoMergePolicy` and a 128 MB budget across 2 threads. Each document deletes the term `id`, then adds. With `commit: true`, the answer comes after the commit and the reader's reload
- [X] T045 [US1] Implement `mailarkiv-search/src/query.rs` to pass T027's word cases: each word becomes a `RangeQuery` on `all` from the word to the first term after every word that starts with it, every word required, sorted by `TopDocs::order_by` on the three keys, newest first
- [X] T046 [US1] Implement `internal/search/client.go` and `internal/search/supervisor.go` to pass T028 and T029's limit values. The client numbers requests, ends each call at its limit, ignores a late answer, and records `search_process_did_not_answer` with the operation name. The supervisor starts the program from a directory its caller gives, reads `hello` within 10 s, counts standard error lines, restarts with the wait in T028, and records the Phase 3 categories with the [log line](contracts/cli.md#log-entries) `search failure`
- [X] T047 [US1] Implement `internal/search/generation.go` and `internal/search/indexer.go` to pass T029. Follow [the transitions](data-model.md#search_generation-owner-search), each in one SQLite transaction. The indexer reads `ProcessedAfter` from the cursor, builds each document through `processing.Read` with text, sends batches, commits when the queue is empty, after 10,000 messages, or 20 s after the first uncommitted one, and writes the cursor after the answer. After the search process restarts, it starts again from the stored cursor. A failed batch waits 1 s, doubling to 60 s, and a committed batch resets the wait
- [X] T048 [US1] Implement `internal/search/search.go` and `internal/search/state.go`: `Search` sends words to the active generation, or to the building one when none is active, and returns rows from `archive.Rows` with `total` and `not_included`. When the search process isn't running, it returns the cause and the next start time. The state is Available or Unavailable, and indexing is Building, Catching up, Up to date, or Stopped
- [X] T049 [P] [US1] Add `SearchProcessStopped`, `SearchProcessDidNotStart`, `SearchProcessDidNotAnswer`, and `IndexWriteFailed` to `internal/opstatus/opstatus.go` to pass T030
- [X] T050 [US1] Change `cmd/mailarkiv/main.go` and `cmd/mailarkiv/serve.go` to pass T032. `parse-worker` is hidden and exits 2 when standard input is a terminal. `serve` follows [the startup order](contracts/cli.md#mailarkiv-serve): step 7 prepares `index/`, step 9 starts the pool, and step 12 starts processing and the search module in the background, with the search program looked up beside `os.Executable()`. On SIGTERM it stops processing and indexing, closes the search process's standard input, and waits inside the 5 s grace before killing what still runs
- [X] T051 [US1] Change the row encoder in `internal/web/messages.go` and `internal/web/imports.go`, and write `internal/web/search.go`, to pass T031. `web.Deps` gains `Search`. Register `POST /api/search` as `SignedIn`. Decode bodies of at most 64 KiB with unknown fields refused, and never log the body
- [X] T052 [P] [US1] Change `frontend/src/lib/api.ts` to pass T033's API cases: the row's new fields, a `Processing` union, `searchMessages`, and the four new categories in `FAILURE_CATEGORIES`. Add their name, meaning, and next step to `frontend/src/lib/status/categories.ts` from FR-046's table
- [X] T053 [P] [US1] Write `frontend/src/lib/format/sender.ts` and `frontend/src/lib/components/Sender.svelte` to pass T033's sender cases, following the sender rule in `frontend/DESIGN.md`. Each part renders through `UntrustedText`, in its own `<bdi>`, and neither uses `{@html}`
- [X] T054 [US1] Update `frontend/DESIGN.md` for rows with sender, subject, and list date, the archive time mark, and the words field. Then change `frontend/src/lib/components/MessageRow.svelte` and `frontend/src/routes/(app)/+page.svelte` to match, with a words field that searches and shows results in the list's rows, to pass T034. Write `frontend/src/lib/search/form.ts` to pass T033's form case

**Checkpoint**: `mise run check` passes. `TestSearchFirstMessage` and `TestServeWithoutSearchProgram` pass. US1's independent test passes.

---

## Phase 4: User Story 3 - Keep originals and capture safe when processing fails (Priority: P1)

**Goal**: Plan step 2. The boundary scanner and its bounds, the 60 s worker limit, a retry after a worker dies, all four states, the malformed set, the processing problems list, and rows for each state. This finishes US3 scenarios 1 to 6, 9, and 10. Phase 7 adds scenarios 7 and 8.

**Independent Test**: Import the malformed set, wait until nothing waits to be processed, and check that every original downloads byte for byte, every message has a row, and each message's state and problems match the expected ones.

### Test support for User Story 3

- [ ] T055 [US3] Write synthetic fixtures in `internal/mailparse/testdata/malformed/`: a broken base64 part between two good ones, an unknown character set, a missing boundary, a file cut off mid-part, a header enmime can't read, no headers but a body, a sender `ceo@example.com` on mail from `x@attacker.example`, a 500-character name, U+202E and U+0000 in a name and a subject, and HTML in a subject. Tests generate the 1,200-part, 12,000-part, 25-level, and 800-level messages

### Tests for User Story 3

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T056 [P] [US3] Write `TestBoundaryScanner` and `FuzzBoundaryScanner` in `internal/mailparse/scanner_test.go`, from [research.md](research.md#part-and-nesting-bounds)
  - 1,000 flat parts aren't cut. 1,001 parts are cut before part `1001`, with closing delimiters for every open boundary, and `too_many_parts` at position `1001`. 21 levels are cut at the first part that deep, with `nested_too_deeply` at its position. An input ending with a boundary open gives `cut_short`
  - A `message/rfc822` part holding a multipart isn't entered. LF and CRLF endings, a quoted boundary parameter, a preamble, and an epilogue all count the same. The input slice is unchanged
  - The fuzz target never panics, and enmime parses every output
- [ ] T057 [P] [US3] Write `TestParseBounds` in `internal/mailparse/bounds_test.go`: an 800-level message parses within 1 s and reports `nested_too_deeply`. A 12,000-part message keeps the text of the parts before the bound and reports `too_many_parts`
- [ ] T058 [P] [US3] Write `internal/processing/limits_test.go`. `TestMain`'s worker gains the modes `hang`, `die`, and `die-once`, the last deciding through a marker file
  - `TestWorkerKilledAtLimit`: with a 2 s limit and `hang`, the worker is killed within 3 s. The message is `unreadable` with `took_too_long`, `processing_worker_stopped` is logged with `message_id` and `exit`, a new worker starts, and the next message is processed
  - `TestWorkerDiesTwice`: with `die`, the message is tried in a second worker, then becomes `unreadable` with `malformed_structure`. With `die-once`, the second try succeeds
  - `TestLimitValues`: 60 s per message, 2 background and 2 interactive workers, and 30 s to wait for an interactive one
- [ ] T059 [P] [US3] Write `TestMalformedSet` in `internal/processing/malformed_test.go` (SC-003). Import T055's fixtures and the generated ones through `archive.Import`. Hash every blob and every `source_observation` row, process everything, and hash again. Expected states and problems per message come from the spec's edge cases and FR-004, written into the test. Every message has a list row, every original downloads byte for byte through `OpenVerified`, and the hashes before and after are equal
- [ ] T060 [P] [US3] Extend `internal/archive/processed_test.go`: `ProblemList(page)` returns only `processed_with_problems` and `unreadable` rows, in list order, with the list's page rules. `ProcessingCounts` returns Not processed yet as `COUNT(*)` minus the highest `processed_seq` in one read transaction, and the two problem states through `message_with_problems`
- [ ] T061 [P] [US3] Write `internal/web/problems_test.go`: `GET /api/processing-problems?page=N` answers [the list's shape](contracts/http-api.md#get-apiprocessing-problemspagen) with only problem rows, and `abc` as a page answers 400 as `GET /api/messages` does. Extend `internal/web/messages_test.go`: an `unreadable` row has `sender` and `subject` null, `latest` set, and `list_date_is_archive_time` true
- [ ] T062 [P] [US3] Write `TestHungWorkers` in `cmd/mailarkiv/processing_test.go` with the `faults` binary and `MAILARKIV_FAULTS=parse-worker=hang`: imports and an IMAP sync from the test double succeed, the list shows them Not processed yet, and SIGTERM exits within the grace with no worker left
- [ ] T063 [P] [US3] Write `frontend/src/lib/format/row.test.ts` and extend `frontend/src/lib/format/sender.test.ts`: a summary row shows sender, subject, list date, and a problem mark for `processed_with_problems`. A `not_processed` or `unreadable` row shows its capture data and its state. "(no sender)" and "(no subject)" appear only in summary rows. Three senders show the first and "and 2 more". A name `ceo@example.com` from `x@attacker.example` keeps both parts apart. A 500-character name never shortens the domain
- [ ] T064 [US3] Write `frontend/tests/e2e/rows.spec.ts` and `frontend/tests/e2e/problems.spec.ts` with T055's fixtures. At 1280 and 360 px, the row for `x@attacker.example` shows `attacker.example` in view beside the name. U+202E shows as a badge. Each state shows its mark. `/problems` lists exactly the Processed with problems and Couldn't be read messages

### Implementation for User Story 3

- [ ] T065 [US3] Implement `internal/mailparse/scanner.go` to pass T056 and T057, and run it before enmime in `internal/mailparse/parse.go`. It reads only header blocks and delimiter lines and works on a copy
- [ ] T066 [US3] Extend `internal/processing/pool.go` to pass T058: a timer per request kills the worker with `SIGKILL` at the limit, a dead worker's message is tried once in a fresh worker, and a second death gives `unreadable` with `malformed_structure`. Log `processing_worker_stopped` with `message_id` and `exit`. The limit is a parameter, and production passes 60 s
- [ ] T067 [US3] Add `hang` to `internal/fault/fault_faults.go`, which blocks forever, and the point `parse-worker` before each parse in `internal/processing/worker.go`, to pass T062. Update the package comment in `internal/fault/fault.go` and `internal/fault/fault_faults.go` to name processing
- [ ] T068 [US3] Implement `ProblemList` and `ProcessingCounts` in `internal/archive/processed.go` to pass T060
- [ ] T069 [US3] Register `GET /api/processing-problems` as `SignedIn` in `internal/web/web.go`, with its handler in `internal/web/messages.go`, to pass T061
- [ ] T070 [US3] Change `server:test` in `mise.toml` to also run `go test -run '^$' -fuzz FuzzBoundaryScanner -fuzztime 10s ./internal/mailparse`, and update its row in `AGENTS.md`
- [ ] T071 [P] [US3] Write `frontend/src/lib/format/row.ts` to pass T063, add `listProcessingProblems` to `frontend/src/lib/api.ts`, and change `frontend/src/lib/components/MessageRow.svelte` to show each state
- [ ] T072 [US3] Update `frontend/DESIGN.md` for state marks and the problem list. Then write `frontend/src/routes/(app)/problems/+page.svelte` and `+page.ts` to pass T064
- [ ] T073 [P] [US3] Update `docs/adr/0003-mail-libraries.md`: enmime's four options, the boundary scanner and why `MaxMIMEParts` is only a backstop, the 20 modules enmime brings, and the `x/net` pin for GO-2026-6860

**Checkpoint**: `mise run check` passes. `TestMalformedSet` (SC-003), `TestWorkerKilledAtLimit`, `TestWorkerDiesTwice`, and `TestHungWorkers` pass.

---

## Phase 5: User Story 1 - Find a message by its fields and text, continued (Priority: P1)

**Goal**: Plan step 3. Every filter, complete addresses and domains, one-character words, accents, the date range, Has attachments, totals, `not_included`, the page cap, `full_list`, the 10 s deadline, and Back to results. This finishes US1.

**Independent Test**: `TestSearchFixedSet` runs SC-001's searches over the fixed set through `POST /api/search` and compares each result with the expected messages in the expected order.

### Test support for User Story 1

- [ ] T074 [US1] Write the fixed set in `internal/web/testdata/searchset/`: three messages from `anna@example.com`, two from `anna@example.org`, one each from `bob@example.com`, `carol@mail.example.com`, and `x@attacker-example.com`; "Quartalsbericht" in a body; `Vertrag_2026.pdf` attached; "Müller", "MÜLLER", and "Muller" in separate messages; the word "a" alone and "apfel"; recipients only in Cc and only in Bcc; Date headers spread across 2026-03-28 to 2026-03-30 and 2026-10-24 to 2026-10-26 in Europe/Berlin; a missing, an unreadable, and a future Date; a message whose only attachment is an embedded image; and one with a header enmime can't read

### Tests for User Story 1

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T075 [P] [US1] Extend `mailarkiv-search/tests/matching.rs`: `matching_one_character_exact` ("a" finds "a" and not "apfel"), `matching_accents_kept` ("müller" finds "Müller" and "MÜLLER", not "Muller"), `matching_address_exact`, `matching_domain` (`example.com` finds `anna@` and `bob@example.com`, not `carol@mail.example.com` or `x@attacker-example.com`; `@example.com` the same), `matching_field_words` ("anna" in From matches words), `matching_to_covers_cc_bcc`, `matching_attachment_name`, `matching_date_range` (from inclusive, before exclusive), `matching_has_attachments`, `matching_no_terms` (`?!` with no filter answers `no_terms`; `?!` with a filter applies the filter), and `matching_operators_are_text` (`OR`, `-`, and quotes)
- [ ] T076 [P] [US1] Write `mailarkiv-search/tests/deadline.rs`: `deadline_stops_search` with `deadline_ms` 0 answers `timed_out`. A collector test over 10,000 documents stops between blocks of 4,096. `total` counts every match beyond `offset` plus `limit`
- [ ] T077 [P] [US1] Write `TestSearchFixedSet` in `internal/web/search_test.go` (SC-001), through `POST /api/search` against the real search process, with expected IDs in order written into the test for each case in [the quickstart's table](quickstart.md#2-search) and each filter. Date ranges are instants for Europe/Berlin, including both days around each daylight saving change. The message enmime couldn't read matches nothing. With processing stopped and one new import, `not_included` is 1
- [ ] T078 [P] [US1] Extend `internal/web/search_test.go` for each [request rule](contracts/http-api.md#post-apisearch): 1,001 characters in a field refused and 1,000 `ä` accepted, `list_date_from` that isn't RFC 3339 refused, `has_attachments` as a string refused, several broken fields named together
- [ ] T079 [P] [US1] Write `TestSearchCallLimit` in `internal/search/limit_test.go` with `bin/faults/mailarkiv-search`: `during-search=hang` answers 503 `search_did_not_answer` within 13 s and records `search_process_did_not_answer` with operation `search`. `during-search=fail` answers 422 `search_took_too_long`
- [ ] T080 [P] [US1] Write `TestSearchableWithin60s` in `cmd/mailarkiv/search_test.go` (SC-010): 10 trials, each importing a new message with nothing else waiting and polling search every 500 ms until it is found, each within 60 s
- [ ] T081 [P] [US1] Write `frontend/src/lib/search/dates.test.ts` with `TZ=Europe/Berlin`: a range from 2026-03-29 to 2026-03-29 gives `2026-03-29T00:00:00.000+01:00` and `2026-03-30T00:00:00.000+02:00`. An empty first or last day gives null. Extend `frontend/src/lib/search/form.test.ts`: every filter reaches the body, and a form with no letters or digits and no filter needs no request
- [ ] T082 [US1] Extend `frontend/tests/e2e/search.spec.ts`: each filter through the form. Open a result, go Back, and see the same search, page, and results with no new `POST /api/search`. Marker words in every field appear in no request or page address. The results header shows the total and the count not included

### Implementation for User Story 1

- [ ] T083 [US1] Extend `mailarkiv-search/src/query.rs` to pass T075: a From or To value with an `@` and text on both sides is a complete address, a value without spaces that contains a dot or starts with `@` is a domain, and anything else is words in that field. One-character words match exactly. Date range and Has attachments as range queries. `no_terms` when the words give no terms and every filter is empty
- [ ] T084 [US1] Write `mailarkiv-search/src/collector.rs` to pass T076: override `collect_segment`, check the deadline before each segment and every 4,096 documents while driving the segment's scorer, and pass documents to `TopDocs` with the offset and to `Count`
- [ ] T085 [US1] Write `mailarkiv-search/src/faults.rs` behind `#[cfg(feature = "faults")]`: read `MAILARKIV_SEARCH_FAULTS` as `point=action` pairs and stop at startup on a mistake. Points `during-search`, `after-commit`, and `on-open`, and actions `exit`, `fail`, and `hang`, as [the contract](contracts/search-process.md#faults) says. Change `internal/search/supervisor.go` to pass `MAILARKIV_SEARCH_FAULTS` through when the server's environment sets it
- [ ] T086 [US1] Extend `internal/search/search.go` to pass T077 and T079: map the page to `offset` and `limit`, answer the full list from `archive.List` with `full_list` true on `no_terms`, compute `not_included` as Not processed yet plus the highest `processed_seq` minus the queried generation's cursor, and map `timed_out` and a passed call limit to their errors
- [ ] T087 [US1] Extend `internal/web/search.go` to pass T078: parse the body once into a search type, and answer 422 `search_took_too_long` and 503 `search_did_not_answer`
- [ ] T088 [P] [US1] Write `frontend/src/lib/search/dates.ts` and extend `frontend/src/lib/search/form.ts` to pass T081
- [ ] T089 [US1] Update `frontend/DESIGN.md` for the search form, the results header, and the search messages. Then write `frontend/src/lib/components/SearchForm.svelte` and `frontend/src/lib/components/ResultsHeader.svelte`, and extend `frontend/src/routes/(app)/+page.svelte`, to pass T082: the filters, the total, the count not included, "This search took too long. Add words or filters.", Unavailable with its cause and next start time, and a SvelteKit `snapshot` holding the search, page, and results

**Checkpoint**: `mise run check` passes. `TestSearchFixedSet` (SC-001), `TestSearchableWithin60s` (SC-010), and `TestSearchCallLimit` pass. US1's independent tests pass.

---

## Phase 6: User Story 2 - Read a message and its attachments without risk (Priority: P1)

**Goal**: Plan step 4. The message view, the isolated preview with embedded images and images from the internet on request, the plain text body, and attachment downloads, through the interactive pool. This finishes US2.

**Independent Test**: In Chromium, open the hostile set while recording requests. No script runs and no request goes to another host. Load images in each, and check that only image requests go out, over HTTPS, without the preview's address. Open the image set and see each allowed image in place with no request to another host.

### Test support for User Story 2

- [ ] T090 [P] [US2] Write `internal/imagetest/imagetest.go`, `internal/imagetest/imagetest_test.go`, and `internal/imagetest/cmd/imagehost/main.go`, importing no module. The host serves HTTPS on `127.0.0.1` with a certificate made at start for `localhost` and `127.0.0.1`, answers any `/img/` path with a 1×1 PNG and anything else with 404, records each request's method, path, `Referer`, whether a cookie came, and `Sec-Fetch-Dest`, and serves the record at `/__log`. `imagehost` prints its address on its first line
- [ ] T091 [US2] Write `frontend/tests/e2e/support/imagehost.ts` to start `bin/imagehost`, read its address and log, and fill `{{IMAGE_HOST}}` in fixture templates. Change `test:e2e` in `mise.toml` to build `bin/imagehost`, and add it to `AGENTS.md`
- [ ] T092 [P] [US2] Write the hostile set in `frontend/tests/e2e/fixtures/hostile/`, one `.eml` per attack: scripts and event handlers that set a marker title, call `alert`, read `document.cookie`, change `top.location`, call `window.open`, and `postMessage` to the parent; remote images, style sheets, `@import`, fonts, CSS backgrounds, SVG `<image>`, video posters, iframes, objects, and embeds on `leak.example.test`; forms; meta refresh; `<a href>` with the default target, `_self`, `_top`, and `_blank`; `<area>`; SVG `<a href>` and `xlink:href`; links inside `<noscript>`; `ping`; `<base>`; `<link rel=prefetch>`, `preload`, `preconnect`, and `dns-prefetch`; `<meta name="referrer" content="unsafe-url">` and `referrerpolicy="unsafe-url"`; attachments named `../../etc/passwd`, `<b>x</b>.html`, one with U+202E, and an HTML and an SVG attachment
- [ ] T093 [P] [US2] Write the image set in `frontend/tests/e2e/fixtures/images/`: a pasted PNG screenshot, a JPEG signature logo, a GIF, a WebP, an image shown twice, a `cid:` reference to a missing part, an SVG part, SVG bytes declared `image/png`, a TIFF, a PNG with damaged base64, and templates with `http:` and `https:` images, `srcset`, a CSS `url(` background, and `<link rel=prefetch>` on `{{IMAGE_HOST}}`. The spec generates the message whose images pass 10,000,000 bytes

### Tests for User Story 2

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T094 [P] [US2] Write `TestRewrite` in `internal/mailparse/rewrite_test.go`, from [research.md](research.md#html-rewrite): links inside `<noscript>` are rewritten, because parsing runs with scripting off. `href` is removed from `a` and `area`, and `href` and `xlink:href` from SVG `a`. `link`, `meta`, and `base` elements are removed. `ping` and `referrerpolicy` attributes are removed. Inline styles stay
- [ ] T095 [P] [US2] Write `TestEmbeddedImages` in `internal/mailparse/images_test.go`: PNG, JPEG, GIF, and WebP signatures become `data:` URLs typed by the signature. SVG bytes declared `image/png`, a TIFF, and a damaged part don't show, and their `src` is removed. Images go in document order, and the first that would bring the decoded bytes shown past 10,000,000 is left out, setting `embedded_images_left_out`. An image shown twice counts once. A missing `cid:` loses its `src`, and the rest renders. `remote_images` is true for `http:` or `https:` in `img` `src`, `srcset`, a `background` attribute, or `url(` in a style
- [ ] T096 [P] [US2] Write `TestCleanFileName` in `internal/mailparse/filename_test.go`, following [the file name steps](data-model.md#attachments), with expected names worked out by hand: C0 and C1 controls, U+061C, U+200E, U+200F, U+202A to U+202E, U+2066 to U+2069, `/`, and `\` removed; spaces and dots trimmed from both ends; an empty result gives `attachment-<position>`; `message` kind gains `.eml` unless the name ends in it, ignoring case; a 300-byte name shortens to 200 bytes on a character boundary and keeps its extension. Extend `internal/mailparse/parse_test.go`: `encrypted` is true for `multipart/encrypted` and for `application/pkcs7-mime` with `smime-type=enveloped-data`, and a signed message's signature is listed as an attachment
- [ ] T097 [P] [US2] Extend `internal/processing/worker_test.go` for `html`, `text`, and `attachment`: `attachment` answers the decoded size and then the raw bytes, a 20,000,000-byte attachment arrives whole, and a damaged one sends the bytes that decoded. Write `TestInteractivePoolBusy` in `internal/processing/pool_test.go` with an injected clock: with both interactive workers in `hang`, a third request returns busy after 30 s
- [ ] T098 [P] [US2] Write `TestPreviewHeaders` in `internal/web/preview_test.go`: the answer carries exactly one `Content-Security-Policy`, one `Referrer-Policy`, and `X-DNS-Prefetch-Control: off`, with [the contract's values](contracts/http-api.md#get-apimessagesidhtml) with and without `images=1`. `?images=0`, `?x=1`, and a repeated `images` answer 400 `bad_query`. No HTML body answers 404 `no_body`. Errors are JSON with the preview's headers. Without a session, `Sec-Fetch-Dest: document` gives 303 to sign-in and anything else gives an empty 401 with the preview's headers
- [ ] T099 [P] [US2] Write `TestAttachmentHeaders` in `internal/web/attachments_test.go`: `Content-Type: application/octet-stream`, `Content-Disposition` from `mime.FormatMediaType("attachment", …)` with `filename*` for `Prüfbericht.pdf`, `Content-Security-Policy: sandbox; default-src 'none'; frame-ancestors 'none'`, `Content-Length`, and `X-Content-Type-Options: nosniff`. An unknown position answers 404 `not_found`. A worker that dies after the headers aborts the connection, so the client sees an unexpected end
- [ ] T100 [P] [US2] Write `internal/web/content_test.go`: [the `content` answer](contracts/http-api.md#get-apimessagesidcontent) for each state, with problems ordered by position, a 600-character name in full, and attachments in part order. `not_processed` and `unreadable` have every field from `date_header` on null and `attachments` empty. A deleted original answers 404 `stored_original_missing`, a changed one 500 `stored_original_damaged`, both with their Slice 1 events. Busy workers answer 503 `busy`, and a worker past its limit 500 `could_not_read`. `GET /api/messages/{id}/text` answers `text/plain; charset=utf-8` with its policy, cut at 2,000,000 bytes, or 404 `no_body`
- [ ] T101 [P] [US2] Write `frontend/src/lib/message/view.test.ts` and extend `frontend/src/lib/api.test.ts`: the HTML shows when present, else the text, and the text can be chosen when both exist. Neither gives "This message has no text." Images from the internet start not loaded, load for this view only, and reset when the message ID changes. The preview address gains `?images=1` only after the choice. `content` parses, and an unknown attachment `kind` makes it `unavailable`
- [ ] T102 [US2] Write `frontend/tests/e2e/preview.spec.ts` (SC-002) with T092's set and `ignoreHTTPSErrors`. For each message, record `request` and `requestfailed`: every request to another host fails with a `csp` error, none with a name resolution error. No dialog opens, the marker title never appears, and the page's address doesn't change. Click every link in the frame: the frame's address doesn't change and nothing is requested. Submit every form: nothing is sent. Open the preview's address in a new tab: the same holds. After loading images, the image host's log holds only `/img/` requests with `Sec-Fetch-Dest: image`, no `Referer`, and no cookie, and nothing from the prefetch link, style sheets, fonts, or frames
- [ ] T103 [US2] Write `frontend/tests/e2e/images.spec.ts` (SC-013) with T093's set: each allowed image shows in its place with a natural width above 0, no other one shows, and no request goes to another host. The message past 10,000,000 bytes says some embedded images are only in the attachment list, and each embedded image is listed. The remote image notice says images aren't loaded, that loading them can tell the sender when and from which network address the message was opened, and that they weren't archived. An `http:` image arrives at the host over HTTPS. Opening another message, or the same one again, shows them not loaded
- [ ] T104 [US2] Write `frontend/tests/e2e/attachments.spec.ts`: each attachment shows its name, type, and size. Save gives a download whose suggested name is the cleaned name from T096's rules. Opening the HTML and SVG attachments' addresses in a tab downloads them and displays nothing

### Implementation for User Story 2

- [ ] T105 [US2] Implement `internal/mailparse/rewrite.go` to pass T094, parsing with `html.ParseOptionEnableScripting(false)` and rendering the changed tree
- [ ] T106 [US2] Implement `internal/mailparse/images.go` to pass T095, and run it from the rewrite for `img` `src`
- [ ] T107 [P] [US2] Implement `internal/mailparse/filename.go` and the `encrypted` check to pass T096
- [ ] T108 [US2] Extend `internal/processing/worker.go` with `html`, `text`, and `attachment`, and extend `read` with what `content` needs: `encrypted`, the bodies' `truncated`, `remote_images`, and `embedded_images_left_out`
- [ ] T109 [US2] Extend `internal/processing/pool.go` and `internal/processing/read.go` to pass T097: the interactive pool of 2 with a 30 s wait and a busy error, and `Content`, `Text`, `HTML`, and `Attachment` for `web`, the last streaming decoded bytes to a writer
- [ ] T110 [US2] Change the global middleware in `internal/web/middleware.go` so a response can replace `Content-Security-Policy` and `Referrer-Policy`, and each response still carries exactly one of each
- [ ] T111 [US2] Write `internal/web/view.go` and register the routes in `internal/web/web.go` to pass T098 to T100. `web.Deps` gains `Processing`. The `html` route's `NoSession` gives 303 for a document and an empty 401 with the preview's headers otherwise. The attachment route answers no session as the original download does. `content` raises its write deadline to 100 s, and the attachment route rolls its deadline every 60 s after the headers
- [ ] T112 [US2] Structural change, committed alone as `refactor(frontend): …`: move the reader and failure table from `frontend/src/lib/download/original.ts` into `frontend/src/lib/download/save.ts`, with `original.ts` calling it. Existing tests pass unchanged
- [ ] T113 [P] [US2] Extend `frontend/src/lib/api.ts` with `getMessageContent`, `getMessageText`, and the preview and attachment addresses, and write `frontend/src/lib/message/view.ts` and `frontend/src/lib/download/attachment.ts`, to pass T101
- [ ] T114 [US2] Update `frontend/DESIGN.md` for the message view, the preview frame, the image notice, and the attachment list. Then write `frontend/src/routes/(app)/messages/[id]/+page.svelte` and `+page.ts`, and the components `MessageHeader.svelte`, `PreviewFrame.svelte` (`<iframe sandbox referrerpolicy="no-referrer">` with no `allow-` token), `PlainText.svelte`, `ImageNotice.svelte`, and `AttachmentList.svelte` in `frontend/src/lib/components/`, to pass T102 to T104. Untrusted text goes through `UntrustedText`, plain text renders as text with its line breaks and no links, and no component uses `{@html}`
- [ ] T115 [US2] Change `frontend/src/lib/components/MessageRow.svelte` to open the message view. The view links to the original download and opens the history drawer. Update `frontend/tests/e2e/history.spec.ts` and `frontend/tests/e2e/download.spec.ts` for the new path

**Checkpoint**: `mise run check` passes. `preview.spec.ts` (SC-002), `images.spec.ts` (SC-013), `attachments.spec.ts`, `TestPreviewHeaders`, and `TestAttachmentHeaders` pass.

---

## Phase 7: User Story 5 - Rebuild the index without losing search (Priority: P2)

**Goal**: Plan step 5. The crash window and Needs rebuild, maintenance by request, the disk pause, rebuild with resume and swap, `POST /api/search/rebuild`, and both crash matrices. This finishes US5 and US3 scenarios 7 and 8.

**Independent Test**: Delete the index with the server stopped, start it, and compare a fixed set of searches before and after. Start a rebuild, stop the server at three points, and check that the old index answers until the swap and that the rebuild never starts over.

### Tests for User Story 5

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T116 [P] [US5] Write `mailarkiv-search/tests/maintenance.rs`: `maintenance_reports_failed_merge_and_retries` damages a segment file, `maintain` answers `failed` 1 or more, and the next `maintain` tries the same segments again. `garbage_collect_files` runs after each run. `close` waits for a search in progress and removes the directory with `delete: true`, and later searches answer `unknown_generation`. `size` answers the sum of the files' sizes. With `--features faults`: `during-merge=fail` answers a failed merge, and `on-open=fail` answers `index_damaged`
- [ ] T117 [P] [US5] Write `TestNeedsRebuildAfterCrashes` in `internal/search/needs_rebuild_test.go` with an injected clock and real kills: 5 crashes within 30 minutes write `search_needs_rebuild` with `search_process_stopped`, record `index_needs_rebuild`, and stop indexing, and search answers with `may_be_incomplete` true. A fifth crash 31 minutes after the first doesn't
- [ ] T118 [P] [US5] Extend `internal/search/needs_rebuild_test.go` with `bin/faults/mailarkiv-search`
  - `TestNeedsRebuildAfterMaintenanceFailures`: with `during-merge=fail` and the maintenance trigger lowered, 3 failed runs in a row mark `index_maintenance_failed`, and a success between them resets the count. Each failed run records `index_maintenance_failed` with the generation ID
  - `TestMaintenanceValues`: maintenance after 50 commits or 10 minutes
  - `TestIndexDamaged`: `on-open=fail` marks `index_damaged` and records `index_needs_rebuild`
  - `TestNeedsRebuildSurvivesRestart`: a new module on the same catalog keeps the state until a rebuild finishes
- [ ] T119 [P] [US5] Write `TestLowDiskPausesIndexing` in `internal/search/disk_test.go` with an injected free space reader and real imports (SC-008): below 2 GiB, indexing shows Paused, `low_disk_space` is recorded with the free bytes, imports succeed, and search answers. At 2.5 GiB it stays Paused. At 3 GiB it resumes. `TestDiskValues`: 2 GiB, 3 GiB, and a reading before each batch and every 30 s
- [ ] T120 [P] [US5] Write `internal/search/rebuild_test.go`
  - A rebuild writes `search_rebuild` and a `building` generation in one transaction, reports done out of to do, and leaves the old index answering. Messages captured during it are searchable after. The swap deletes the `active` row, makes the `building` row `active`, ends the rebuild `finished`, and deletes `search_needs_rebuild` in one transaction, then closes the old index and removes its directory
  - Refusals: a running rebuild, free space at or below 2 GiB plus the active index's `size` (reporting both numbers), no active index, and a stopped search process
  - A batch for the rebuild failing 5 times in a row ends it `failed`, records `rebuild_failed` with the rebuild and generation IDs, deletes its row and directory, and leaves the old index answering. Low disk space and a restart don't count
  - `TestSwapWithoutGap`: searches in a loop across the swap never find search unavailable or an unknown generation, and each answer equals the old index's complete result or the new one's
- [ ] T121 [P] [US5] Write `internal/web/rebuild_test.go` for [the route](contracts/http-api.md#post-apisearchrebuild): 202 with the rebuild, 409 `rebuild_running`, 409 `not_enough_space` with `needed_bytes` and `free_bytes`, 409 `no_index`, and 503 `search_unavailable`
- [ ] T122 [P] [US5] Extend `internal/opstatus/opstatus_test.go` and `frontend/src/lib/api.test.ts`: `index_maintenance_failed`, `index_needs_rebuild`, `low_disk_space`, and `rebuild_failed` exist, log, count, and parse
- [ ] T123 [US5] Write `TestIndexCrashMatrix` in `cmd/mailarkiv/index_crash_test.go` from [the plan's table](plan.md#crash-matrix). Import three messages, start the `faults` binary with one stop point, wait for it to die, then start the default binary and wait until indexing is Up to date. Check each row's two columns, reading cursors from the catalog after the process exits. Every row ends with each message found exactly once
- [ ] T124 [US5] Write `TestSearchProcessKilled` and `TestCaptureWhileSearchKilled` in `cmd/mailarkiv/search_kill_test.go` (SC-005, US3 scenarios 7 and 8). Kill with `after-commit=exit`, `during-search=exit`, an external `SIGKILL` during a batch, and one during a rebuild. Each period's imports and an IMAP sync succeed, search answers again within 70 s, and after indexing catches up each message is found once. While search is Unavailable, the list, a message view, a preview, an original download, and an attachment download each work
- [ ] T125 [US5] Write `TestDeletedIndexBuilds` and `TestRebuildInterrupted` in `cmd/mailarkiv/rebuild_test.go`. SC-006: note three searches' results, stop, delete `index/`, start, see Building with progress and `not_included` above 0, and after Up to date the same results. SC-007: stop at `rebuild-after-cursor-commit`, `before-swap-commit`, and `after-swap-commit`. The cursor after each restart is at least the one before, searches during it return every message they returned before it started, and after it finishes the searches return the same messages plus one imported during it

### Implementation for User Story 5

- [ ] T126 [US5] Write `mailarkiv-search/src/maintain.rs` and extend `mailarkiv-search/src/index.rs` and `mailarkiv-search/src/faults.rs` to pass T116: `maintain` computes candidates with `LogMergePolicy::compute_merge_candidates`, runs and waits for each merge, then runs `garbage_collect_files`. `close` and `size` as the contract says. The `during-merge` point. A failed open of existing files answers `index_damaged`
- [ ] T127 [US5] Extend `internal/search/supervisor.go` and write `internal/search/needs_rebuild.go` to pass T117: count crashes in the last 30 minutes, write `search_needs_rebuild`, record `index_needs_rebuild`, and stop indexing except a rebuild's generation. Mark `index_damaged` when `open` answers it for the active index
- [ ] T128 [US5] Write `internal/search/maintain.go` to pass T118: ask for maintenance after 50 commits or 10 minutes, count failed runs in a row, and mark `index_maintenance_failed` at 3
- [ ] T129 [P] [US5] Write `internal/search/disk.go` to pass T119: read free space with `syscall.Statfs` on `<data-dir>/index`, through a reader tests replace
- [ ] T130 [US5] Write `internal/search/rebuild.go` and extend `internal/search/generation.go` and `internal/search/indexer.go` to pass T120: one indexing loop per generation, the swap, the failure rule, and at start a rebuild that continues from its stored cursor. Log `rebuild started` and `rebuild finished`
- [ ] T131 [US5] Add the stop points `after-processing-commit`, `after-index-send`, `after-index-commit`, `after-cursor-commit`, `rebuild-after-cursor-commit`, `before-swap-commit`, and `after-swap-commit` through `fault.At` in `internal/processing/background.go`, `internal/search/indexer.go`, and `internal/search/rebuild.go`, to pass T123 and T125
- [ ] T132 [US5] Extend `internal/search/state.go` with [every state](data-model.md#search-and-indexing-states) in its order, and set `may_be_incomplete` while Needs rebuild
- [ ] T133 [US5] Register `POST /api/search/rebuild` as `SignedIn` in `internal/web/web.go`, with its handler in `internal/web/search.go`, to pass T121
- [ ] T134 [P] [US5] Add `IndexMaintenanceFailed`, `IndexNeedsRebuild`, `LowDiskSpace`, and `RebuildFailed` to `internal/opstatus/opstatus.go`, to `FAILURE_CATEGORIES` in `frontend/src/lib/api.ts`, and to `frontend/src/lib/status/categories.ts` with FR-046's meaning and next step, to pass T122

**Checkpoint**: `mise run check` passes. `TestIndexCrashMatrix`, `TestSearchProcessKilled` (SC-005), `TestDeletedIndexBuilds` (SC-006), `TestRebuildInterrupted` (SC-007), and `TestLowDiskPausesIndexing` (SC-008) pass.

---

## Phase 8: User Story 6 - See search and processing problems without seeing mail (Priority: P2)

**Goal**: Plan step 6. The status screen's search and processing section, the eight categories with repeats, the problem count, the remaining log lines, and the marker test across the log, the status screen, and request addresses. This finishes US6.

**Independent Test**: Put markers in subjects, addresses, names, bodies, attachment names, and search words. Force each failure category, then check that each appears on the status screen, in the problem count, and in the log, and that no marker appears in any of them.

### Tests for User Story 6

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T135 [P] [US6] Extend `internal/opstatus/opstatus_test.go`: for the eight new categories, a failure of the same category as the newest event, with no success of that kind between, increments `repeats` and sets `last_at`. A success of that kind ends the run. Slice 1 and 2 categories never merge. `detail` keys follow [the data model](data-model.md#failure-categories). `ProblemCount` adds every repeat
- [ ] T136 [P] [US6] Write `mailarkiv-search/tests/output.rs` with `--features faults`: `reports_no_text` sends marker strings in every document field and every search field through every operation and every fault point, then finds no marker in any frame or on standard error. A forced panic writes exactly one line matching `^mailarkiv-search panic at [A-Za-z0-9_/.-]+:[0-9]+$` and aborts. A Tantivy warning arrives as `{"type":"log","level":"warn","target":"…"}` without text
- [ ] T137 [P] [US6] Extend `internal/search/supervisor_test.go`: a panic line is logged as `search process panic` with `location`, other lines only as a count, a `log` frame as `search process log` with `level` and `target`, each state change as `search state`, and each start as `search process started` with `restarts`
- [ ] T138 [P] [US6] Extend `internal/web/status_test.go` and `internal/web/session_test.go`: `GET /api/status` has [the `processing` and `search` objects](contracts/http-api.md#changed-get-apistatus) and failures with `repeats`, `last_at`, and `detail`. `problem_count` follows [the formula](data-model.md#problem-count): 1 more while Unavailable or Needs rebuild, 1 more while Paused, and nothing for problem messages
- [ ] T139 [US6] Extend `TestNoMarkersInLogOrStatus` in `cmd/mailarkiv/markers_test.go`: markers in subjects, addresses, names, bodies, attachment names, and every search field. Force `search_process_stopped` by a kill, `search_process_did_not_start` from a directory without the program, `search_process_did_not_answer` with `during-search=hang`, `index_write_failed` and `rebuild_failed` with `after-commit=fail`, `index_maintenance_failed` with `during-merge=fail` after 50 single-message commits, and `index_needs_rebuild` with `on-open=fail`. Each appears in the log and in `GET /api/status`, and no marker appears in the log, the status answer, or a request address. Extend `internal/search/disk_test.go` to check the same for `low_disk_space`
- [ ] T140 [P] [US6] Extend `frontend/src/lib/api.test.ts` and `frontend/src/lib/status/groups.test.ts`: the status additions parse, an unknown state word makes the result `unavailable`, and repeats show as one entry with a count and the last time
- [ ] T141 [US6] Extend `frontend/tests/e2e/status.spec.ts`: the section shows the search and indexing states, the four counts, and the restarts. A killed search process shows Search process stopped with its time, and the problem count includes it. The problem link lists exactly the problem messages. Rebuild asks for confirmation, then shows Rebuilding with progress. Needs rebuild shows its cause, since when, and Rebuild

### Implementation for User Story 6

- [ ] T142 [US6] Extend `internal/opstatus/opstatus.go` to pass T135: `RecordSuccess(category)`, `Repeats`, `LastAt`, and `Detail` on `FailureEvent`. Change the comment on `RecordFailure`, which says events come only from signed-in actions and startup
- [ ] T143 [P] [US6] Write `mailarkiv-search/src/output.rs` to pass T136: the panic hook prints only the location line, and a `log` implementation forwards warnings and errors as frames with level and target
- [ ] T144 [US6] Extend `internal/search/supervisor.go` and `internal/search/state.go` to pass T137, and call `RecordSuccess` for each kind of success the data model names
- [ ] T145 [US6] Extend `internal/web/status.go` and `internal/web/session.go` to pass T138, taking states, counts, and the rebuild from `search` and `archive`
- [ ] T146 [P] [US6] Extend `frontend/src/lib/api.ts` and `frontend/src/lib/status/groups.ts` to pass T140
- [ ] T147 [US6] Update `frontend/DESIGN.md` for the status section and the rebuild confirmation. Then write `frontend/src/lib/components/SearchStatus.svelte` and `frontend/src/lib/components/RebuildConfirm.svelte`, and extend `frontend/src/routes/(app)/status/+page.svelte`, to pass T141

**Checkpoint**: `mise run check` passes. `TestNoMarkersInLogOrStatus` (SC-009) and `reports_no_text` pass.

---

## Phase 9: User Story 4 - Keep search, previews, and attachments closed to anyone not signed in (Priority: P1)

**Goal**: Plan step 7. Direct request and framing tests for the new routes, the search process without an address, keyboard and 360 px use, axe, and the design guide. This finishes US4 and FR-050.

**Independent Test**: Send direct requests to search, the message view, the preview, attachment download, the status screen, and rebuild with no session, an expired one, a signed-out one, and a request forged from another site. Each is denied and returns no mail data.

### Tests for User Story 4

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T148 [P] [US4] Write `TestPreviewWithoutSession` and extend `internal/web/access_test.go` (SC-004): for each new route, no session, an expired session, and a signed-out session are denied, and no answer body contains a subject, address, body word, or attachment byte from the message. `TestEveryRouteDeniesWithoutSession` and `TestEverySignedInRouteFailsClosedWhenTheCatalogFails` cover the seven routes through the route table
- [ ] T149 [P] [US4] Extend `internal/web/access_test.go`: `POST /api/search/rebuild` and `POST /api/search` with `Sec-Fetch-Site: cross-site` and another `Origin` are rejected, and no `search_rebuild` row appears (US4 scenario 3)
- [ ] T150 [P] [US4] Write `TestSearchProcessHasNoAddress` in `cmd/mailarkiv/search_access_test.go`: every link under `/proc/<pid>/fd` of the search process is a pipe, `/dev/null`, or a file under the data directory, and none is a `socket:` (US4 scenario 5)
- [ ] T151 [P] [US4] Extend `frontend/tests/e2e/access.spec.ts`: a page at `http://other.example.test/`, served through `page.route`, frames a preview address and an attachment address. Neither frame shows MailArkiv content (US4 scenario 4)
- [ ] T152 [US4] Extend `frontend/tests/e2e/screens.spec.ts` (SC-012): by keyboard alone, search, open a result, save an attachment, and go back. At 360 px, search, results, the message view, the problem list, and the status screen don't scroll sideways. axe finds no violation on each, in both themes

### Implementation for User Story 4

- [ ] T153 [US4] Fix what T148 to T152 find in `internal/web/` and `frontend/src/`, and review every new screen against `frontend/DESIGN.md`. Each fix comes with the test that found it

**Checkpoint**: `mise run check` passes. SC-004 and SC-012 pass.

---

## Phase 10: User Story 7 - Know how large an archive search supports (Priority: P2)

**Goal**: Plan step 8, sizing. The sizing tool, its run on a host with 4 cores and 8 GiB of memory, and the results page. This finishes US7.

**Independent Test**: Run the sizing test and check that the documentation reports its results and the supported size.

### Tests for User Story 7

- [ ] T154 [P] [US7] Write `internal/sizing/cmd/sizing/sizing_test.go`: the generator gives the same messages for the same seed, averages about 8 KB, attaches a file to one in ten, and includes `internal/web/testdata/searchset/`. `seed` with 50 messages and `measure` finish against the real search program and write a results section with every field FR-049 names

### Implementation for User Story 7

- [ ] T155 [US7] Write `internal/sizing/cmd/sizing/main.go` and `internal/sizing/cmd/sizing/generate.go`: `seed --data-dir DIR --messages N [--seed S]` generates German and English synthetic messages from a fixed seed and imports them through `archive.Import`, with the fixed set. `sizing parse-worker` runs `processing.RunWorker`, so the tool can start its own workers
- [ ] T156 [US7] Write `internal/sizing/cmd/sizing/measure.go`, following [research.md](research.md#sizing-test): run processing and search with `bin/mailarkiv-search` until Up to date, run a fixed set of newest-first searches with and without prefix words and record the median and 95th percentile, start a rebuild and time it, sample `RssAnon` of the search process from `/proc` every 100 ms, record the index directory's size, and append a dated section with the versions to `docs/research/search-sizing/README.md`
- [ ] T157 [US7] Add `mise run sizing` to `mise.toml`, outside `check`, depending on `search:build`, and add its row to `AGENTS.md` (Performance, manual)
- [ ] T158 [US7] Run `mise run sizing` on a host with 4 cores and 8 GiB of memory. Write the method and results in `docs/research/search-sizing/README.md`. If every FR-049 target holds (95th percentile under 1 s, rebuild within 8 hours, under 1 GiB allocated), 5,000,000 is the supported size. Otherwise state the largest size tested that met every target, and record the miss for a review of ADR 0007

**Checkpoint**: The sizing run's results are in the repository (SC-011).

---

## Phase 11: Polish & Cross-Cutting Concerns

**Purpose**: Operator documentation and the release checks.

- [ ] T159 [P] Update `docs/OPERATIONS.md` for FR-051 and FR-052: how search matches (capital letters ignored, accents not, prefixes, one-character words, no phrases or operators), the 100-byte word limit, the 2,000-page limit, what it covers (attachment names, not contents; the first 2,000,000 bytes of each body; no attached messages' contents), and what the preview guarantees, including what loading images reveals and that they show as they are now. Where the index lives, that it holds message text and needs the data directory's protection, that backups can skip it, the automatic build, how to rebuild, stopping the server before deleting it and the recovery for an index that crashes the search process at each start, the disk thresholds, the search process and its restarts, the eight log categories, the processing bounds, `musl-tools` for building, and the supported size from T158
- [ ] T160 [P] Update `README.md`: Slice 3 of 7 works, with search and preview
- [ ] T161 Run quickstart scenarios 1 to 7 in [quickstart.md](quickstart.md) and record each result in the pull request. Scenario 7 needs `sudo`; if it can't run, say so and name the remaining risk
- [ ] T162 Do a manual screen reader pass over search, results, the message view, the problem list, and the status screen, and fix what it finds
- [ ] T163 Update [the plan's remaining risks](plan.md#remaining-risks) with CI's run time and anything T158, T161, or T162 found. Run `mise run check`, and report any check that couldn't run with the reason and remaining risk

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup. Blocks every story.
- **US1 skeleton (Phase 3)**: Depends on Foundational.
- **US3 (Phase 4)**: Depends on Phase 3, for the worker, the pool, and processing.
- **US1 search (Phase 5)**: Depends on Phase 3, for the search process and the route. It can run beside Phase 4.
- **US2 (Phase 6)**: Depends on Phase 4, for the bounds the view must respect, and on Phase 3's rows.
- **US5 (Phase 7)**: Depends on Phase 5, for the faults build and the call limit, and on Phase 6, because `TestCaptureWhileSearchKilled` opens previews and attachments.
- **US6 (Phase 8)**: Depends on Phase 7, so the markers test can force all eight categories.
- **US4 (Phase 9)**: Depends on Phase 8, so the access and screen tests cover every route and screen.
- **US7 (Phase 10)**: Depends on Phase 7. The tool can be written beside Phases 8 and 9, but the run measures the finished search.
- **Polish (Phase 11)**: Depends on Phases 9 and 10.

### User Story Dependencies

```text
Setup → Foundational → US1 skeleton → US3 ─┐
                                └→ US1 search ─┴→ US2 → US5 → US6 → US4 → Polish
                                                         └→ US7 ────────┘
```

The plan builds every story on one walking skeleton, so the stories aren't independent of Phase 3. Phases 4 and 5 touch separate packages, apart from `frontend/src/routes/(app)/+page.svelte` and `MessageRow.svelte`, and can run in parallel with care.

### Within Each Phase

- Test support tasks (T020, T055, T074, T090 to T093) come before that phase's tests.
- Tests come first and fail until the phase's code is done.
- Schema before modules. `mailparse` before `processing`, `processing` before `search`, `archive` and `search` before `web`, `web` before screens. The Rust side of each operation before the Go side that calls it.
- `frontend/DESIGN.md` changes before the screen that follows them.
- T112 lands alone, before T113.

### Parallel Opportunities

- Setup: T002 to T005.
- Foundational: T010 to T015, then T019 beside T016 to T018.
- Each phase's test tasks marked `[P]` can start together.
- US1 skeleton: the Rust tasks T043 to T045 beside the Go tasks T036 to T042. T049, T052, and T053 touch separate files.
- US3: T071 and T073 beside T065 to T069.
- US2: T107 and T113 beside T105, T106, and T108.
- US5: T129 and T134 beside T126 to T128.
- US6: T143 and T146 beside T142.
- Polish: T159 and T160.

---

## Parallel Examples

```bash
# Foundational tests:
Task: "Extend internal/sqlitedb/sqlitedb_test.go"
Task: "Write TestMigrateSlice2Catalog in internal/archive/migrate_test.go"
Task: "Write internal/archive/catalog_test.go"
Task: "Extend internal/archtest/archtest_test.go"
Task: "Write internal/processing/frame_test.go"

# User Story 1 skeleton tests, after T020:
Task: "Write TestRead in internal/mailparse/parse_test.go"
Task: "Write mailarkiv-search/tests/protocol.rs"
Task: "Write internal/search/supervisor_test.go"
Task: "Write TestSearchFirstMessage in cmd/mailarkiv/search_test.go"
Task: "Write frontend/src/lib/format/sender.test.ts"

# User Story 2 tests, after T090 to T093:
Task: "Write TestRewrite in internal/mailparse/rewrite_test.go"
Task: "Write TestPreviewHeaders in internal/web/preview_test.go"
Task: "Write TestAttachmentHeaders in internal/web/attachments_test.go"
Task: "Write frontend/src/lib/message/view.test.ts"

# User Story 5 tests:
Task: "Write mailarkiv-search/tests/maintenance.rs"
Task: "Write TestNeedsRebuildAfterCrashes in internal/search/needs_rebuild_test.go"
Task: "Write TestLowDiskPausesIndexing in internal/search/disk_test.go"
Task: "Write internal/search/rebuild_test.go"
```

---

## Implementation Strategy

### MVP First

Phases 1 to 3 give the walking skeleton: processing in workers, rows with sender and subject, a search process with an index built at start, and a words field. Stop there and run US1's independent test. It proves the path through Go, Rust, and the interface. Don't point it at real mail yet: without Phase 4 a hostile message has no time limit, and without Phase 6 there is no safe way to read a result.

### Incremental Delivery

Merge to `main` at each checkpoint, with `mise run check` passing. Each completed path is available during development. Each phase's tests protect what it added from the next.

### Parallel Team Strategy

1. Finish Setup, Foundational, and Phase 3 together.
2. Developer A takes Phase 4, then Phase 6. Developer B takes Phase 5, then the Rust and Go parts of Phase 7.
3. Developer A writes the sizing tool beside Phase 8. Both finish Phases 8, 9, and 11 together, and run the sizing test once Phase 7 lands.

---

## Notes

- `[P]` tasks touch different files and depend on no unfinished task.
- `[Story]` labels map each task to its story. Setup, Foundational, and Polish tasks have none.
- Commit after each task or logical group, with a Conventional Commits scope such as `feat(search): …`.
- Stop at any checkpoint to validate the phase on its own.

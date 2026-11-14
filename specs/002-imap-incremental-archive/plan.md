# Implementation Plan: Archive an IMAP Mailbox Incrementally

**Branch**: `002-imap-incremental-archive` | **Date**: 2026-11-08 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/002-imap-incremental-archive/spec.md`

## Summary

An administrator adds an IMAP mailbox and chooses folders. MailArkiv archives every message in them, then new ones every 5 minutes. The main risk is a checkpoint that moves past a message that isn't stored: once the user deletes that message, the archive never gets it.

A new module, `collect`, runs one loop per source. It opens folders read-only, fetches each body with `BODY.PEEK[]`, stores it through the Slice 1 capture path, and only then commits the folder's checkpoint. A unique index keeps re-collection from adding history entries. The IMAP library's own deadlines miss a server that goes silent, so a wrapper below TLS enforces the 30 second connect limit and the 60 second idle limit, and bounds reply sizes. Passwords are sealed with AES-256-GCM under a key file kept outside the data directory. Tests drive the library's in-memory IMAP server with injected faults, and the crash matrix gains collection rows. [research.md](research.md) has the tests behind each decision.

## Technical Context

**Language/Version**: Go 1.27.3. TypeScript 6.0.3 and Svelte 5.57.3 for the interface, unchanged.

**Primary Dependencies**:

- New at runtime: `github.com/emersion/go-imap/v2` v2.0.0-beta.8, adopted in [ADR 0003](../../docs/adr/0003-mail-libraries.md), with `github.com/emersion/go-message` v0.18.2 and `github.com/emersion/go-sasl` v0.0.0-20241020182733-b788ff22d5a6. Neither `x/net` nor `x/text` is needed.
- Standard library for the rest: `crypto/tls`, `crypto/aes` and `cipher.NewGCMWithRandomNonce` for passwords, and `testing/synctest` for the scheduler tests.
- No new frontend packages.

**Storage**: SQLite as in Slice 1. Three new tables owned by `collect`, one counter table owned by `archive`, and three new columns on `source_observation`, all in migration 0002. The key is a file outside the data directory. Sync progress and due times stay in memory. [data-model.md](data-model.md) has the details.

**Testing**: `go test -race` with the library's `imapmemserver`, wrapped to stall, fail, or misbehave on any command, against real SQLite, real files, and the real archive. `testing/synctest` for schedule and backoff. The `faults` build for the collection crash matrix, with the binary trusting a test CA through `SSL_CERT_FILE`. Vitest for client parsing and wording. Playwright with axe against the built binary and a test IMAP server process.

**Target Platform**: Linux on amd64, static binary with `CGO_ENABLED=0`, unchanged. Current browsers, tested in Chromium.

**Project Type**: Web service with an embedded single-page interface, a small command line, and now a background collector.

**Performance Goals**:

- A new message is listed within 6 minutes when the previous sync took under 30 seconds (SC-009). Worst case, it arrives just after a sync searched its folder. That sync ends within 30 seconds, the next starts 5 minutes later and stores it within 30 seconds. That uses the whole budget, so the schedule adds no jitter.
- A stalled sync ends within 70 seconds of the last data received (SC-005). The idle limit is 60 seconds.
- The sources screen reads per-source counters. The alternative query took 1.53 seconds over 1,180,000 entries for 50 sources.

**Constraints**: Never change the mailbox. Connect and handshake within 30 seconds, then fail after 60 seconds without data. Each command may read at most 16 MiB, and a body fetch at most 150,000,000 bytes plus 1 MiB. One connection per source. Syncs stop inside the 5 second shutdown grace. Passwords never reach the log, the status screen, a browser response, or the data directory in a form readable without the key.

**Scale/Scope**: Up to 50 sources. A 100,000 message first sync that resumes across restarts. One new module, one key package, and one test double package. Four new tables and three new columns. Ten routes, one command, one `serve` option, and three screens.

## Constitution Check

*Gate result: passes before research and again after design, with no exceptions. Version 1.0.0.*

| Principle | How this plan complies |
| --------- | ---------------------- |
| I. User value, YAGNI | Scope matches the spec's. The only new setting is `--key-file`, which the spec requires. No configurable interval, no IDLE, no provider adapters, no key rotation. `StoreCollected` shares capture with `Import`, and the display rule for untrusted text serves file names, folder names, and server replies. The counter table exists because a measured query was too slow |
| II. Modular architecture | `collect` owns `imap_source`, `imap_folder`, and `imap_message_problem`. `archive` owns `source_observation` and `collected_message_count`. `collect` calls `archive.StoreCollected` and commits its own checkpoint, so no transaction crosses owners. The web adapter asks `collect` for folder and source labels instead of joining its tables. `passwordkey` imports no module. `archtest` enforces the new rules |
| III. Explicit contracts and failures | States, categories, and security modes are closed sets. Input is parsed once at the HTTP and CLI boundaries. Every network step has a limit: 30 seconds to connect, 60 seconds idle, 120 seconds for a check. Re-collection is idempotent through the content hash and a unique index. Every failure reaches the source's page, and every problem source reaches the status screen and the log |
| IV. Security and privacy | Passwords are sealed, bound to their host and user name, and never returned or logged. TLS is required, with certificates the host trusts. Every route is denied by default. The key must sit outside the data directory. Logs carry IDs and categories only. The new dependency was adopted in ADR 0003, is pinned, and `govulncheck` finds nothing. ADR 0008 records the encryption |
| V. Test according to risk | Failures are ranked below, each with a check at the lowest layer that catches it. Real SQLite, files, and modules. Doubles only for the mail server, the disk's errors, and the clock |
| VI. Small batches | The [delivery order](#delivery-order) starts with a walking skeleton through every layer. `main` stays buildable and checked. Collection runs on normal startup; the greenfield exception is recorded in [Complexity Tracking](#complexity-tracking) |
| Governance | The spec defines scope and exclusions. This plan names each check and its layer. ADR 0008 records the key format and encryption, and ADR 0003 gains the connection wrapper |

## Credible failures

Ranked by harm first, then likelihood. Expected values come from the spec.

| # | Failure | Likelihood | Harm | Check | Layer |
| --- | ------- | ---------- | ---- | ----- | ----- |
| 1 | The checkpoint moves past a message that isn't stored, so it's lost once the user deletes it | Medium | Critical | `TestCollectCrashMatrix`, `TestStallAtEachStep` checks every checkpoint after each stall, `TestCollectStorageFailures` | Process, integration |
| 2 | An IMAP password is readable from a copy of the data directory, the log, the status screen, or a browser response | Medium | Critical | `TestNoPasswordInDataDir` searches a copy of the data directory for each marker password and its base64 and hex forms. `TestNoMarkersInLogOrStatus`, `TestSourceResponsesHaveNoPassword` | Process, integration |
| 3 | A source route works without a session or from another site | Medium | Critical | `TestEveryRouteDeniesWithoutSession`, generated from the route table, so the ten new routes are covered | Integration |
| 4 | A sync changes the mailbox: marks mail read, moves, or deletes it | Low | Critical | `TestCollectionChangesNothing` fails on any command that can write, and compares flags before and after. Quickstart scenario 3 on Dovecot | Integration, manual |
| 5 | A password reaches the wrong server or travels unencrypted: untrusted certificate, STARTTLS not offered, or a ciphertext moved to another source's row | Low | Critical | `TestSecureConnection` asserts that no `LOGIN` reached the double for an untrusted, expired, or misnamed certificate or a missing STARTTLS. `TestSealBoundToSource` | Integration, unit |
| 6 | Migration 0002 damages a Slice 1 catalog | Low | Critical | `TestMigrateSlice1Catalog` migrates a populated Slice 1 catalog and compares the list, history, and blobs before and after. A failed `foreign_key_check` rolls back | Integration |
| 7 | A silent server hangs a sync that looks busy forever | High | High | `TestStallAtEachStep` stalls at connect, handshake, greeting, STARTTLS, `LOGIN`, `EXAMINE`, `UID SEARCH`, the metadata fetch, and mid-literal. A unit test pins the 30 and 60 second values | Integration, unit |
| 8 | Retries, renumbering, or repeated bytes add history entries or stored copies | Medium | High | `TestEntryAndCopyCounts` covers each case in SC-003. `TestCollectWhileImport` stores the same bytes from both paths at once under `-race` | Integration |
| 9 | A message the server doesn't return blocks its folder or vanishes without a record | Medium | High | `TestMessageUnavailable` fails one fetch, then removes the message. `TestMessageSizes` covers too large, announced over the limit, and short | Integration |
| 10 | Repeated rejected sign-ins get the mailbox locked by its provider | Medium | High | `TestSignInRejectedStops` counts `LOGIN` attempts over fake time and across a new collector on the same catalog. `TestSignInResults` maps each response code | Integration, unit |
| 11 | User names, folder names, or server replies reach the log or status screen | Medium | High | `TestNoMarkersInLogOrStatus` forces every category with markers in the user name, folder names, message content, and the double's replies | Process |
| 12 | A hostile folder name or server reply runs script or spoofs text | Medium | High | Display rule unit tests, one rendered source page with HTML and bidirectional characters, and the page policy | Unit, end to end |
| 13 | The key lands in the data directory, overwrites a key, or is readable by others | Low | High | `TestKeyCreate` and `TestServeKeyRefusals` cover symbolic links, an existing file, and the 0600 mode. `TestKeyLocation` | Process, unit |
| 14 | An endless or huge reply exhausts memory | Low | High | `TestReplyBounds` sends a folder list and a literal past their bounds | Integration |
| 15 | The archived count includes a message that isn't stored | Medium | Medium | `TestProgressCountsDurable` holds the double between messages and compares the count with the catalog at each point | Integration |
| 16 | One failing or stalled source delays others | Medium | Medium | `TestSchedule` in a synctest bubble, and `TestSourcesIndependent` with one stalled double beside a healthy one | Integration |
| 17 | Shutdown leaves a sync running past the grace, or stores a partial message | Medium | Medium | `TestShutdownDuringSync` sends SIGTERM mid-literal, expects exit 0 within the grace, an empty `tmp/`, and the message archived after restart | Process |
| 18 | A disk failure during a sync moves the checkpoint or hides the cause | Medium | Medium | `TestCollectStorageFailures` runs `temp-write=ENOSPC`, `EACCES`, and `commit=EIO` during a sync, then restarts without faults | Process |
| 19 | A screen fails keyboard, screen reader, or 360 px use | Medium | Medium | Playwright keyboard, width, and axe checks on the sources screens in both themes, plus a manual screen reader pass | End to end, manual |
| 20 | A real server disagrees with the library or the double | Medium | Medium | Quickstart scenario 1 against Dovecot. The operator guide lists only servers that were run | Manual |
| 21 | A dependency has a known vulnerability | Medium | Medium | `mise run audit` | Static |
| 22 | The collected count drifts from the catalog | Low | Low | `TestCollectedCounts` compares the counter with `COUNT(DISTINCT message_id)` after the SC-003 cases | Integration |

### Crash matrix

`TestCollectCrashMatrix` serves three messages in one folder from the double. It starts the `faults` binary, adds a source over HTTP, lets the first message hit the stop point, restarts with the default binary, and waits for the next sync. The capture points are Slice 1's, reached through `StoreCollected`. `after-checkpoint-commit` is new in `collect`, and `checkpoint-commit` takes an injected error.

| Stop point | After restart, before the sync | After the next sync |
| ---------- | ------------------------------ | ------------------- |
| `before-temp-create`, `mid-temp-write`, `after-temp-fsync` | Nothing listed, `tmp/` empty | All three listed and byte-identical, one entry each (User Story 2 scenario 1) |
| `after-rename`, `after-dir-fsync`, `before-commit` | Nothing listed, one unreferenced original | All three listed, one copy each, the unreferenced list empty |
| `after-commit` | Message 1 listed, checkpoint before it | Message 1 fetched again, still one entry and one copy, collected count 3 (scenario 2) |
| `after-checkpoint-commit` | Message 1 listed, checkpoint at it | Messages 2 and 3 archived. The double saw no second body fetch for message 1 |
| `checkpoint-commit=EIO`, no kill | Source in Retrying with Catalog write failed | Message 1 already stored, no second entry |

## Required checks

The commands in `AGENTS.md` stay. One task changes: `test:e2e` also builds `bin/imaptestserver` from `internal/imaptest/cmd/imaptestserver` before Playwright runs. `AGENTS.md` changes with it.

| Command | Runs for this slice | Layer |
| ------- | ------------------- | ----- |
| `mise run lint` | `gofmt -l`, `go vet`, `staticcheck`, `prettier --check` | Static |
| `mise run typecheck` | `svelte-kit sync`, `svelte-check --fail-on-warnings` | Static |
| `mise run audit` | `govulncheck ./...`, including `go-imap`, and `bun audit` | Static, needs network |
| `mise run server:test` | `go test -race ./...`: `collect` against the double, `archive` and migration tests, `archtest`, and the process tests for crashes, storage faults, shutdown, keys, and markers. Needs `strace` | Unit, integration, process |
| `mise run frontend:test` | `vitest run`: API parsing, state and category words, display rules | Unit |
| `mise run test:e2e` | `playwright test` against the built binary and the test IMAP server | End to end |
| `mise run check` | All of the above. CI gates on this | |

Process tests start the double inside the test process, so `server:test` needs no network beyond loopback. The quickstart's Dovecot scenario needs Docker and isn't part of `check`.

## Project Structure

### Documentation (this feature)

```text
specs/002-imap-incremental-archive/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── http-api.md
│   └── cli.md
├── checklists/requirements.md
└── tasks.md             # Created by /speckit-tasks
```

### Source Code (repository root)

```text
cmd/mailarkiv/           # key create, serve --key-file
                         # Process tests: collection crash matrix, storage faults, shutdown, keys, markers
internal/
├── collect/             # New. Sources, folders, problems, schedule, sync, connection wrapper, categories
│                        # Owns imap_source, imap_folder, imap_message_problem
├── passwordkey/         # New. Key file create and load, seal and open. Imports no module
├── imaptest/            # New. Test IMAP server: imapmemserver, a test CA, fault-injecting sessions
│   └── cmd/imaptestserver/   # Runs it as a process for Playwright
├── archive/             # StoreCollected, collection entries in list and history, the per-source counter
├── opstatus/            # Two new categories and an optional source ID on failure events
├── sqlitedb/            # Migration runner change, migrations/0002_imap_collection.sql
├── web/                 # Source routes, status sources, renamed message fields
├── fault/               # Unchanged. New points are names only
└── archtest/            # New import and table ownership rules
frontend/src/
├── lib/                 # api.ts parsers, sources/ state and category words, display rule for any text
└── routes/(app)/
    ├── sources/         # List, new, and [id] pages
    └── status/          # Problem sources section
frontend/tests/e2e/      # sources.spec.ts, support for the test IMAP server
docs/OPERATIONS.md, docs/adr/0008-*.md
```

**Structure Decision**: The same Go module and frontend package. Allowed imports change as follows, and `archtest` enforces them:

- `cmd/mailarkiv` adds `collect` and `passwordkey`.
- `web` adds `collect`.
- `collect` uses `archive`, `opstatus`, `sqlitedb`, `fault`, and `passwordkey`.
- `passwordkey` and `imaptest` import no module.
- No package outside tests and `imaptest/cmd` imports `imaptest`.

## Delivery order

Each step ends in behavior that tests can check and merges to `main` with `mise run check` passing. Collection runs on normal startup, and Sources joins the navigation when the interface is built.

| Step | Delivers | Verified by |
| ---- | -------- | ----------- |
| 1. Walking skeleton | Migration 0002 and the runner change. `passwordkey` with documented `key create` and `--key-file`. `collect` with `POST /api/sources`, a sealed password, and syncs of the chosen folders through `StoreCollected` with checkpoints, at save and at start. `GET /api/sources` and `GET /api/sources/{id}`, whose fields from later steps read null or zero until then. The connection wrapper's 30 and 60 second deadlines, from the first dial. The per-source counter, written in the collection commit. Renamed message fields, also in the `POST /api/imports` answer. Collection entries in the list row and history drawer, with their design guide entries and the display rule for any text. `imaptest` with plain messages. `archtest` rules. ADR 0008 and the ARCHITECTURE line | User Story 1's independent test without the 6 minute part, `TestMigrateSlice1Catalog`, `TestCollectionChangesNothing`, risks 4, 6 |
| 2. Exactly once | Renumbering, retry and not-archived records with their log entries, size rules, collection fault points, and the crash matrix. The `message_too_large` and `message_removed` failure events, on the server and in the client together | User Story 2, SC-002, SC-003, risks 1, 8, 9, 22 |
| 3. Failures and timeouts | The connection wrapper's reply bounds and full error classification, TLS and STARTTLS, sign-in results, the schedule and backoff loop, rejected sign-in kept across restarts, unavailable folders, storage faults, and shutdown, with their log entries | User Story 3, SC-005, risks 5, 7, 10, 14, 16, 17, 18 |
| 4. Passwords and the key | Key refusals, running without a key, unreadable passwords, and the data directory search | User Story 4 scenarios 1, 4, 5, 6, 9, 10, SC-006, risks 2, 13 |
| 5. Management | Check routes, edit with a sign-in check, pause, resume, Sync now, removal, duplicates, adding and removing folders | User Story 6, User Story 4 scenarios 2, 3, 7, 8, SC-007, risk 3 |
| 6. Visibility | Sources list and source pages with refresh while syncing, the status screen's sources, the problem count, and the marker test across the log and the status screen | User Story 5, SC-004, SC-010, risks 11, 15 |
| 7. Interface completion | Sources navigation item. Two-step add with the inbox preselected, the removal warning on adding a source and on adding a folder (FR-007), edit and remove flows, browser checks that hostile folder names show as plain text, keyboard, 360 px, axe, and the test IMAP server process for Playwright | SC-011, risks 12, 19 |
| 8. Release | Operator guide, README, and ADR 0003. Dovecot run and the timed walkthrough | SC-001, SC-008, SC-009, risk 20 |

## Changes to other documents

| Document | Change | When |
| -------- | ------ | ---- |
| `docs/adr/0008` (new) | IMAP password encryption: AES-256-GCM from the standard library, the key line format, binding to host and user name, refusal inside the data directory, and no rotation in this slice | Step 1 |
| `docs/ARCHITECTURE.md` | `go.mod` line: `go-imap` brings `go-message` and `go-sasl` and needs neither `x/net` nor `x/text`, so those arrive with Slices 3 and 7 if at all | Step 1 |
| `AGENTS.md` | The `test:e2e` row also builds the test IMAP server | Step 7 |
| `frontend/DESIGN.md` | Collection entries in list rows and the history drawer, whose labels now say entries instead of imports, in step 1. Sources screens and state words with their dots in step 6. The two-step add form, folder checklist, edit form, and remove confirmation in step 7. The Sources navigation item in step 7 | Steps 1, 6, and 7 |
| `docs/adr/0003` | Consequences: the client's deadlines miss a silent server and its decoder has no size bound, so `collect` wraps the connection. `imapmemserver` is the test double | Step 8 |
| `docs/OPERATIONS.md` | The key (FR-043). What collection guarantees and doesn't, the schedule, timeouts, retry waits, log categories, cause `key`, the no-key log entry, trusting a private issuer, and tested servers (FR-044) | Step 8 |
| `README.md` | Status: Slice 2 of 7 works, with IMAP collection | Step 8 |

## Remaining risks

- Only the in-memory double and one Dovecot version are run. The double shares the library's parser, so it can't reveal a disagreement between the library and a server. Hosted providers aren't tested, and the operator guide says so.
- A server that sends folder names in a legacy encoding fails Connect, so that mailbox can't be added.
- A folder with more than about 2 million new messages passes the 16 MiB search bound and fails as Server error at every sync.
- A replaced or removed password's old ciphertext can stay in SQLite's free pages or WAL until reused. Reading it needs the key.
- Plaintext passwords pass through memory during sign-in and request handling. Go can't reliably wipe them, and swap is covered only by the operator's disk encryption.
- A hard link to the key inside the data directory isn't detected. The operator guide tells operators where to keep it.
- A 100,000 message first sync over a 20 ms link is estimated at about 35 minutes of round trips, not measured.
- As in Slice 1, no check cuts power.

## Complexity Tracking

| Rule | Present need | Alternative rejected |
| ---- | ------------ | -------------------- |
| VI: isolate unreleased behavior | The owner confirmed on 2026-11-13 that MailArkiv is a greenfield application with no users. Collection starts normally, and Sources is reachable while the feature is developed. | A temporary enable switch and hidden navigation add setup work without protecting any current users. Keep required checks and deliver in small batches. |

The changed behavior is verified by the existing first-sync and restart process tests, missing-key tests, API access tests, and the keyboard setup flow through Sources. Remove tests for the superseded hidden-route behavior. Run `mise run check`: lint, typecheck, and audit are static checks; server tests cover unit, integration, and process behavior; frontend tests cover units; browser tests cover the complete flow. Inspect navigation at desktop and 360 px widths in both themes.

# Implementation Plan: Find Messages Quickly and Safely

**Branch**: `003-search-preview` | **Date**: 2026-11-15 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/003-search-preview/spec.md`

## Summary

An administrator searches the archive, opens a result in a preview that can't run code or reach the internet, and saves its attachments. Processing reads each original, and a search process indexes it. The worst failures are a preview that leaks a request or runs script, and a processing or search failure that touches an original or stops capture. Next come index entries that are lost or doubled after a crash, and search words or mail text that reach the log.

Three new Go packages carry the work. `mailparse` turns untrusted bytes into fields, problems, text, and a rewritten preview, and imports no other module. `processing` runs it in worker processes of the same binary, killed at 60 s, so a hostile message can't hang or crash the server. `search` supervises `mailarkiv-search`, a Rust program built on Tantivy 0.27.0, over pipes. Each processed message gets a gapless sequence number. Each index generation keeps a cursor, and each batch deletes before it adds, so a crash at any point repeats work but never doubles it. The preview is isolated by its own sandbox policy and frame. The browser probe in [research.md](research.md#isolation) found two leaks the policy misses: a clicked link navigates the frame, and a prefetch hint goes out once images are allowed. The HTML rewrite closes both.

## Technical Context

**Language/Version**: Go 1.27.3. Rust 1.100.0, edition 2024, for the search process. TypeScript 6.0.3 and Svelte 5.57.3 for the interface, unchanged.

**Primary Dependencies**:

- New in Go: `github.com/jhillyerd/enmime/v2` v2.5.0, adopted in [ADR 0003](../../docs/adr/0003-mail-libraries.md), with 20 modules it brings. `golang.org/x/net` v0.62.0, `golang.org/x/text` v0.43.0, and `golang.org/x/sys` v0.49.0 pinned, because enmime's default `x/net` has GO-2026-6860. `x/net/html` is used directly.
- New in Rust: `tantivy` =0.27.0 with only `mmap` and `lz4-compression`, `mimalloc` =0.1.52, and `serde` and `serde_json`, which Tantivy already needs. 113 crates in all, with no OSV advisory on 2026-11-15.
- New tools: Rust through mise with the `x86_64-unknown-linux-musl` and `aarch64-unknown-linux-musl` targets, `musl-gcc` from the system's `musl-tools` on Linux, and `cargo-audit` 0.23.0.
- No new frontend packages.

**Storage**: SQLite as before. Migration 0003 adds four columns to `message` and two tables, owned by `archive`, and three tables owned by `search`. Index generations live in `<data-dir>/index/<id>/`. Bodies, recipients, and attachment lists aren't stored; they are read from the original when needed. [data-model.md](data-model.md) has the details.

**Testing**: `go test -race` against real SQLite, real files, real worker processes, and the real search process, in its release build and in a build with the `faults` cargo feature. Worker tests re-run the test binary as a worker with a 2 s limit. `testing/synctest` and an injected clock for backoff, the crash window, and the maintenance schedule. An injected free space reader stands in for the disk. The `faults` Go build for the indexing crash matrix. `cargo test` for matching, order, the deadline, delete before add, maintenance, and silent output. Vitest for parsers, date range instants, and words. Playwright with axe against the built binaries, with a recording HTTPS image host.

**Target Platform**: Linux on amd64 and arm64, with macOS for development only, as [ADR 0010](../../docs/adr/0010-platforms.md) decides. The server stays a static `CGO_ENABLED=0` binary. The search process is a static musl binary beside it. Current browsers, tested in Chromium.

**Project Type**: Web service with an embedded single-page interface, a small command line, background collection and processing, and a supervised search process.

**Performance Goals**:

- A new message is searchable within 60 s when nothing else waits (FR-020). Capture wakes processing at once, processing wakes the indexer, and the indexer commits when its queue is empty.
- At 5,000,000 messages: 95th percentile search under 1 s, a full rebuild within 8 hours, and under 1 GiB allocated by the search process (FR-049). The sizing test measures these. ADR 0007 measured 212 / 395 ms median / p95 for its slowest query at that size on 0.26.1.
- A rebuild at 5,000,000 messages within 8 hours means about 174 messages per second. Two background workers at 3.2 ms per typical message parse about 600.

**Constraints**: Never change an original or a history entry. Processing stops at 60 s, 1,000 parts, or 20 levels, and uses 2,000,000 bytes of each body. Searches stop at 10 s, and every call to the search process has a limit. Indexing pauses below 2 GiB free and resumes at 3 GiB. Search words and mail content never reach a request address, the log, or the status screen. The preview loads nothing from another host until an administrator loads one message's images, and then only images.

**Scale/Scope**: 5,000,000 messages. Three new Go packages, one Rust program, a sizing tool, and a test image host. Five new tables and four new columns. Seven new routes and four changed answers. A search form, a message view, a problem list, and a status section.

## Constitution Check

*Gate result: passes before research and again after design. The exceptions and justifications are in [Complexity Tracking](#complexity-tracking). Version 1.0.0.*

| Principle | How this plan complies |
| --------- | ---------------------- |
| I. User value, YAGNI | Scope matches the spec's. No new option: the search program is found beside the server, and every bound is a constant from the spec or this plan. The catalog stores only what rows and states need. One parse path serves processing, indexing, the view, the preview, and attachments. The worker pools have two fixed sizes because the two callers need separate capacity |
| II. Modular architecture | `archive` owns `message`, `message_summary`, and `message_problem`, so the list's order comes from tables it owns. `search` owns its three tables and reads messages through `archive`. `mailparse` imports no module, so the code that reads untrusted bytes can't reach the catalog or the disk. The search process is a separate program; the justification against its cost is in [research](research.md#why-a-separate-process) and Complexity Tracking. `archtest` enforces the new rules |
| III. Explicit contracts and failures | Processing states, problem kinds, search and indexing states, failure categories, and protocol errors are closed sets. Search input is parsed once at the HTTP boundary. Every call to the search process has a limit, and a late answer is ignored. Indexing is idempotent through delete before add. Every failure reaches the status screen and the log, and repeats are counted, not dropped |
| IV. Security and privacy | Every new route is `SignedIn` and deny by default. The preview's policy is the barrier, and the rewrite closes link navigation. Attachments are always saved, never shown. The search process has no address to connect to. Its output is limited to closed codes and module paths. New Go dependencies come from ADR 0003 and are pinned with `govulncheck` clean. Tantivy moves to 0.27.0 so that no crate has a known advisory, and `cargo audit` joins the checks. The 20 Go modules and 113 crates raise the count principle IV asks to keep low; both are justified in research |
| V. Test according to risk | Failures are ranked below, each with a check at the lowest layer that catches it. Real SQLite, files, workers, and search process. Doubles only for time, free disk space, and the internet's image hosts |
| VI. Small batches | The [delivery order](#delivery-order) starts with a walking skeleton through every layer, including the Rust program and its checks. Processing and search run on normal startup; the greenfield exception is recorded in Complexity Tracking |
| Governance | The spec defines scope. This plan names each check and its layer. ADR 0007 is amended for the version, the connection, and maintenance. ADR 0009 records worker processes. `AGENTS.md` gains the Rust commands in step 1 |

## Credible failures

Ranked by harm first, then likelihood. Expected values come from the spec.

| # | Failure | Likelihood | Harm | Check | Layer |
| --- | ------- | ---------- | ---- | ----- | ----- |
| 1 | The preview runs script, or sends a request to another host: a remote resource, a clicked link, a prefetch hint, a `Referer` with the preview's address, or anything but an image after images load | Medium | Critical | `preview.spec.ts` opens the hostile set, clicks every link, and records requests through `requestfailed` and the image host's log. `TestRewrite` covers links, `noscript`, `link`, `meta`, `base`, `ping`, and `referrerpolicy`. `TestPreviewHeaders` asserts exactly one policy header per answer | End to end, unit, integration |
| 2 | Search, a view, a preview, an attachment, or a rebuild works without a session or from another site, or another site frames a preview or attachment | Medium | Critical | `TestEveryRouteDeniesWithoutSession` reads the route table, so the seven new routes are covered from step 1. `TestPreviewWithoutSession`. `access.spec.ts` frames both from another origin | Integration, end to end |
| 3 | A processing or search failure changes an original or history entry, or stops capture | Medium | Critical | `TestMalformedSet` hashes every original and history entry before and after. `TestCaptureWhileSearchKilled` imports and collects while the search process is killed. The indexing crash matrix. `archtest` keeps `mailparse` from importing any module | Process, integration, static |
| 4 | Migration 0003 damages a Slice 2 catalog | Low | Critical | `TestMigrateSlice2Catalog` migrates a populated catalog and compares the list's contents, history, and blobs before and after. A failed `foreign_key_check` rolls back | Integration |
| 5 | Search words or mail content reach the log, the status screen, or a request address | Medium | High | `TestNoMarkersInLogOrStatus` gains markers in subjects, addresses, names, bodies, attachment names, and search words, and forces each new category. Rust `reports_no_text` sends markers through every operation and fault. `search.spec.ts` checks every request address and page address for markers | Process, unit, end to end |
| 6 | A message is lost from the index or indexed twice after a crash or restart | Medium | High | The [crash matrix](#crash-matrix). `TestSearchProcessKilled` kills at the four points in SC-005. Rust `delete_before_add_keeps_one` | Process, integration, unit |
| 7 | A hostile message hangs processing, crashes the server, or exhausts its memory | High | High | `TestWorkerKilledAtLimit` and `TestWorkerDiesTwice` with real worker processes. `TestBoundaryScanner` for parts, depth, cut short, and `message/rfc822`. `FuzzBoundaryScanner` in the default suite for 10 s. `TestMalformedSet` (SC-003) | Integration, unit |
| 8 | Search returns the wrong messages or order: prefixes, one character, accents, addresses, domains, date range, missing or future dates | High | High | `TestSearchFixedSet` runs SC-001's set through the HTTP route against the real search process. Rust `matching_*` tests. Vitest for date range instants | Integration, unit |
| 9 | A rebuild starts over, answers from a mix of indexes, leaves search unavailable at the swap, or loses mail captured during it | Medium | High | `TestRebuildInterrupted` stops it at 3 points (SC-007). `TestSwapWithoutGap` searches in a loop across the swap. `TestDeletedIndexBuilds` (SC-006) | Process, integration |
| 10 | The index fills the disk that capture needs | Medium | High | `TestLowDiskPausesIndexing` with an injected free space reader and real captures (SC-008). Quickstart scenario 7 on a real file system | Integration, manual |
| 11 | Repeated crashes or maintenance failures go unseen, or Needs rebuild doesn't survive a restart | Medium | High | `TestNeedsRebuildAfterCrashes` with an injected clock and real kills. `TestNeedsRebuildAfterMaintenanceFailures` with `during-merge=fail`. Rust `maintenance_reports_failed_merge_and_retries`. `TestNeedsRebuildSurvivesRestart` | Integration, unit |
| 12 | An attachment opens in the browser, or saves under a name with a path, controls, or bidirectional characters | Medium | High | `TestAttachmentHeaders`. `TestCleanFileName` with SC-003's names. `attachments.spec.ts` opens an HTML and an SVG attachment's address in a tab and expects a download | Integration, unit, end to end |
| 13 | A sender name passes for an address, or a hostile name or subject spoofs text | Medium | High | Row display unit tests for `ceo@example.com` from `x@attacker.example`, 500 characters, and bidirectional controls. `rows.spec.ts` renders them | Unit, end to end |
| 14 | A search or the search process hangs, and requests or indexing wait forever | Medium | Medium | Rust `deadline_stops_search`. `TestSearchCallLimit` with `during-search=hang` expects 503 within 13 s and a `search_process_did_not_answer` event | Unit, integration |
| 15 | A new message isn't searchable within 60 s | Medium | Medium | `TestSearchableWithin60s` (SC-010) imports and polls search, 10 times | Integration |
| 16 | Processing gives a wrong list date, state, or problem | Medium | Medium | `TestListDateRule`, `TestProblemKinds` with every enmime error name, `TestMalformedSet` | Unit, integration |
| 17 | The Rust build makes CI miss its 30 minute limit, or isn't reproducible | Medium | Medium | `--locked` everywhere. CI caches the cargo registry and target directory. Step 1 records the first CI run's time | Static |
| 18 | A dependency has a known vulnerability | Medium | Medium | `mise run audit`: `govulncheck`, `cargo audit`, `bun audit` | Static |
| 19 | Search misses the size targets at 5,000,000 messages | Medium | Medium | `mise run sizing`. The documented size follows the result | Manual |
| 20 | A new screen fails keyboard, screen reader, or 360 px use | Medium | Medium | Playwright keyboard, width, and axe checks on search, results, the view, problems, and status in both themes. A manual screen reader pass | End to end, manual |
| 21 | Back from a message loses the search, page, or results | Medium | Low | `search.spec.ts` | End to end |
| 22 | Concurrent views wait too long for an interactive worker | Low | Low | `TestInteractivePoolBusy` expects 503 `busy` after 30 s with an injected clock | Integration |

### Crash matrix

`TestIndexCrashMatrix` imports three messages, starts the `faults` server binary with one stop point, waits for it to die, then starts the default binary and waits until indexing is Up to date. After each row, each of the three is found exactly once.

| Stop point | After restart, before indexing | After indexing catches up |
| ---------- | ------------------------------ | ------------------------- |
| `after-processing-commit` | Messages processed, cursor 0 | All three found once |
| `after-index-send`, before the answer | Cursor 0. The search process died with the server, so its batch wasn't committed | The batch sent again, all found once |
| `after-index-commit`, before the cursor commit | Cursor 0, but the index holds the batch | The batch sent again and replaced, all found once (User Story 5 scenario 9) |
| `after-cursor-commit` | Cursor 3 | Nothing sent again |
| `rebuild-after-cursor-commit` | Rebuilding, cursor at the batch | Continues from the cursor, not from 0 (scenario 4) |
| `before-swap-commit` | Still Rebuilding, old index active | Swaps. Search answered throughout |
| `after-swap-commit` | New index active, old directory still there | Startup removed the old directory |

`TestSearchProcessKilled` covers the search process's side with the `faults` cargo build: `after-commit=exit`, `during-search=exit`, an external `SIGKILL` during a batch, and one during a rebuild. Each expects every capture in the period to succeed and search to answer again within 70 s (SC-005).

## Required checks

The commands in `AGENTS.md` stay, and they change in step 1, with `AGENTS.md` in the same change:

| Command | Runs for this slice | Layer |
| ------- | ------------------- | ----- |
| `mise run lint` | `gofmt -l`, `go vet`, `staticcheck`, `prettier --check`, and new: `cargo fmt --check`, `cargo clippy --locked --all-targets -- -D warnings`, and the same clippy run with `--features faults` | Static |
| `mise run typecheck` | `svelte-kit sync`, `svelte-check --fail-on-warnings` | Static |
| `mise run audit` | `govulncheck ./...`, including enmime and the pins, `bun audit --ignore=GHSA-gcqx-w48m-w2h4`, and new: `cargo audit` | Static, needs network |
| `mise run search:test` | New. `cargo test --locked`, then `cargo test --locked --features faults`, in `mailarkiv-search/` | Unit, integration |
| `mise run server:test` | `go test -race ./...`: `mailparse`, `processing` with real workers, `search` against the real search process, `archive` and migration tests, `archtest`, and the process tests. Depends on new `search:build` and `search:build:faults`. Needs `strace` | Unit, integration, process |
| `mise run frontend:test` | `vitest run` | Unit |
| `mise run test:e2e` | Builds `bin/mailarkiv`, `bin/mailarkiv-search`, `bin/imaptestserver`, and new `bin/imagehost`, then `playwright test` | End to end |
| `mise run test` | `search:test`, `server:test`, `frontend:test`, `test:e2e` | |
| `mise run check` | `lint`, `typecheck`, `test`, `audit`. CI gates on this | |
| `mise run sizing` | New, outside `check`. Seeds 5,000,000 messages and measures | Performance, manual |

`search:build` runs `cargo build --release --locked --target <arch>-unknown-linux-musl` for the machine's architecture, or for the Mac on macOS, and copies the result to `bin/mailarkiv-search`. `search:build:faults` builds with `--features faults` in the dev profile to `bin/faults/mailarkiv-search`, which process tests copy beside the `faults` server binary. `mise run setup` checks for `musl-gcc` and says how to install it. `.github/workflows/check.yml` installs `musl-tools` with `strace`, and caches `~/.cargo/registry` and `mailarkiv-search/target` keyed on `Cargo.lock` and `mise.lock`.

This planning change adds only documents under `specs/003-search-preview/`. Per `AGENTS.md`, the check for it is a review against the constitution and of local links.

## Project Structure

### Documentation (this feature)

```text
specs/003-search-preview/
├── spec.md
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── http-api.md
│   ├── search-process.md
│   └── cli.md
├── checklists/requirements.md
└── tasks.md             # Created by /speckit-tasks
```

### Source Code (repository root)

```text
cmd/mailarkiv/           # parse-worker. serve starts the pools, processing, and search
                         # Process tests: indexing crash matrix, search process kills, markers, migration
internal/
├── mailparse/           # New. Boundary scanner, enmime parse, fields, list date, problems, text,
│                        # HTML rewrite, embedded images, file names. Imports no module
├── processing/          # New. Worker pools, the worker loop, background processing,
│                        # Read, Text, HTML, Attachment for web and search
├── search/              # New. Supervisor, protocol client, indexer, rebuild, disk check, states
│                        # Owns search_generation, search_rebuild, search_needs_rebuild
├── sizing/cmd/sizing/   # New. seed and measure, outside check
├── imagetest/cmd/imagehost/  # New. HTTPS image host that records requests, for Playwright
├── archive/             # Columns on message, message_summary, message_problem, list order,
│                        # RecordProcessed, ProcessedAfter, Rows, problem counts
├── opstatus/            # Eight categories, repeats and last time
├── sqlitedb/            # migrations/0003_search_preview.sql
├── web/                 # Seven routes, preview and attachment headers, status and session changes
├── fault/               # New action: hang
└── archtest/            # New import and table ownership rules
mailarkiv-search/        # New Rust crate: Cargo.toml, Cargo.lock, src/, tests/
frontend/src/
├── lib/                 # api.ts parsers, search/ form state and date range, message/ view state,
│                        # status words for the new categories, a reader shared by both downloads
└── routes/(app)/
    ├── +page.svelte     # List and search
    ├── messages/[id]/   # Message view with the preview frame and attachments
    ├── problems/        # Processing problems
    └── status/          # Search and processing section, Rebuild
frontend/tests/e2e/      # search, preview, images, attachments, rows, problems, status specs
                         # fixtures/hostile/, fixtures/images/
docs/OPERATIONS.md, docs/adr/0009-*.md, docs/research/search-sizing/
```

**Structure Decision**: The same Go module and frontend package, plus one Rust crate. Allowed imports change as follows, and `archtest` enforces them:

- `cmd/mailarkiv` adds `processing` and `search`.
- `web` adds `processing` and `search`.
- `processing` uses `archive`, `mailparse`, and `fault`.
- `search` uses `archive`, `processing`, `opstatus`, `sqlitedb`, and `fault`.
- `mailparse` and `imagetest` import no module.
- `sizing/cmd/sizing` uses `archive`, `processing`, `search`, `opstatus`, and `sqlitedb`. No package imports `sizing` or `imagetest`.
- `archive` is unchanged: `opstatus`, `sqlitedb`, `fault`.

`tableOwners` adds `message_summary` and `message_problem` for `archive`, and the three search tables for `search`.

## Delivery order

Each step ends in behavior that tests can check and merges to `main` with `mise run check` passing. Processing and search run on normal startup from step 1.

| Step | Delivers | Verified by |
| ---- | -------- | ----------- |
| 1. Walking skeleton | Toolchain: Rust, `cargo-audit`, `musl-tools` in CI, the new tasks and lint, audit, and test commands, and `AGENTS.md`. Migration 0003 with every column and table, and the list order through `message_list_order`. `mailparse` reading everything processing stores, because processing runs once per message: From, Subject, Date, recipients, attachments, and both bodies, the list date rule, `has_attachments` with the embedded image rule, enmime's problem kinds, and the body cut at 2,000,000 bytes. `processing` with the background pool and the worker, which take the original and answer attachments as raw bytes after a frame. Rows with sender, subject, and list date in the new order. `mailarkiv-search` with `hello`, `open`, `index`, and `search` for words with prefixes, and complete index documents. The `search` supervisor with its restart wait from 1 to 60 s, the indexer with its cursor, delete before add, and the same wait before a failed batch is sent again, and an automatic first build. Four failure categories in `opstatus` and the client: `search_process_stopped`, `search_process_did_not_start`, `search_process_did_not_answer`, and `index_write_failed`. `POST /api/search` and a words field. ADR 0007 amended, ADR 0009, the ARCHITECTURE lines | User Story 1 scenarios 1, 2, 9. `TestMigrateSlice2Catalog`. Risks 4, 17, 18 |
| 2. Processing | The boundary scanner and the bounds it reports, worker limits and the retry after a death, the Go fault action `hang` for hung workers, the malformed set, `GET /api/processing-problems` and its page, rows for each state, and ADR 0003 | User Story 3 scenarios 1 to 6, 9, 10. SC-003. Risks 3, 7, 13, 16 |
| 3. Search | Every filter, addresses and domains, one-character words, accents, the date range, Has attachments, totals, `not_included`, the page cap, `full_list`, the 10 s deadline, and Back to results. The fixed set lives in `internal/web/testdata/searchset/` | User Story 1. SC-001, SC-010. Risks 8, 14, 15, 21 |
| 4. Message view | `content`, `text`, `html`, and attachment routes, the interactive pool, the rewrite, embedded and remote images, attachment names, the image host, and the hostile and image sets | User Story 2. SC-002, SC-013. Risks 1, 12, 22 |
| 5. Index reliability | The crash window, Needs rebuild and its row, maintenance by request, the disk pause, rebuild with resume and swap, `POST /api/search/rebuild`, both crash matrices, and the other four failure categories | User Story 3 scenarios 7, 8, User Story 5. SC-005 to SC-008. Risks 6, 9, 10, 11 |
| 6. Visibility | The status screen's search and processing section, the eight categories with repeats, the problem count, the log lines, and the marker test across the log, status, and addresses | User Story 6. SC-009. Risk 5 |
| 7. Access and interface completion | Frame and direct request tests for the new routes, `TestSearchProcessHasNoAddress`, keyboard, 360 px, axe, and the design guide | User Story 4. SC-004, SC-012. Risks 2, 20 |
| 8. Sizing and release | The sizing tool, which seeds the fixed set with its generated mail, and its run, the results page, the operator guide, and the README | User Story 7. SC-011. Risk 19 |

`TestSearchProcessHasNoAddress` checks that the search process's open file descriptors include no socket, which is how User Story 4 scenario 5 holds: there is no address for another account to connect to.

## Changes to other documents

| Document | Change | When |
| -------- | ------ | ---- |
| `docs/adr/0007` | Tantivy 0.27.0 with corrected release dates, no advisory and no exception, pipes instead of a socket, a second file beside the server, `NoMergePolicy` with maintenance by request, and the finding that automatic merges retry after each commit | Step 1 |
| `docs/adr/0009` (new) | Untrusted mail is parsed in worker processes of the server binary: why, the two pools, the 60 s kill, and what a worker may do | Step 1 |
| `docs/ARCHITECTURE.md` | Search index rules: pipes instead of a socket, maintenance by request. The technology table names 0.27.0. The `go.mod` line: `x/net` and `x/text` arrive with this slice at the pinned versions. The link to `docs/research/search-benchmark/README.md` points to a file that commit `696b3a0` removed on purpose, so it and ADR 0007's link point to that file at commit `f61d6a1` | Step 1 |
| `AGENTS.md` | The required checks table: Rust lint and audit, `search:test`, the helper builds that `server:test` and `test:e2e` need, and `musl-gcc` | Step 1 |
| `mise.toml`, `.github/workflows/check.yml` | Tools, tasks, `musl-tools`, and cargo caches. `dev:server` builds `bin/mailarkiv` instead of `go run`, so the search program is beside it | Step 1 |
| `frontend/DESIGN.md` | Patterns that other screens will share, such as the preview frame, each added before the screen that first needs it. Each screen's own layout stays in its code | Steps 1 to 4 and 6 |
| `docs/adr/0003` | enmime's options, the boundary scanner, the 20 modules it brings, and the `x/net` pin | Step 2 |
| `docs/OPERATIONS.md` | How search matches and what it covers, the 100-byte word limit, the 2,000-page limit, what the preview guarantees (FR-051). Where the index lives, its protection, backups, the automatic build, rebuilding, stopping before deleting, the thresholds, the search process and its restarts, the log categories, the processing bounds, `musl-tools` for building, and the supported size (FR-052) | Step 8 |
| `docs/research/search-sizing/README.md` (new) | The sizing run: method, versions, results | Step 8 |
| `README.md` | Status: Slice 3 of 7 works, with search and preview | Step 8 |

## Remaining risks

- The end-to-end suite runs Chromium only. Firefox and Safari implement the sandbox and policy separately and aren't tested.
- DNS prefetching was checked once with Chromium's network log. The end-to-end tests see requests, not DNS lookups.
- If a damaged active index crashes the search process at every start, a rebuild can't run. The operator guide's recovery is to stop the server and delete the index, which starts an automatic build.
- A message whose original goes missing between processing and indexing is indexed without text or recipients until a rebuild after the original is restored.
- The remote image notice comes from scanning `img`, `srcset`, `background`, and `url(` in styles. A reference it misses still can't load, but the notice and the load button may not appear for that message.
- Workers have no memory cap. A 150,000,000-byte original can make one allocate several times that. The kernel's out-of-memory killer normally picks the largest process, the worker, but that isn't guaranteed.
- The view parses the original each time. If an upgrade changes enmime, a view can show parts differently from the problems stored when the message was processed. Reprocessing is out of scope.
- Search answers only the first 100,000 results of a query, a limit the spec doesn't set.
- Repeated failures raise the problem count by each repeat, so a search process that fails for days shows a large count.
- The last search and its results sit in the browser's session storage and history state until the tab closes.
- The Rust release build took 6 minutes on 3 cores for a probe. CI's 30 minute limit may need raising once the crate is real.
- Tantivy 0.27.0 hasn't been measured at size; the sizing test is the first measurement.
- As in Slices 1 and 2, no check cuts power.

## Complexity Tracking

| Rule | Present need | Alternative rejected |
| ---- | ------------ | -------------------- |
| II: a separate service needs a justification against its operational cost | Tantivy is Rust. In the server through cgo, it would end the static build that ADR 0002 depends on, and a crash would stop capture, which User Story 3 forbids. The cost is a second toolchain, a second file, a protocol, supervision, and 113 crates, each bounded as [research](research.md#why-a-separate-process) lists. ADR 0007 made the decision; this plan carries its justification | Tantivy through cgo, Bleve, SQLite FTS5, and a search server, as ADR 0007 records |
| II and I: worker processes are a new pattern | A hostile message can need minutes or gigabytes to parse, Go can't stop a goroutine, and a fatal runtime error in the server would stop capture. The spec requires a stop at 60 s. Workers run the same binary, so nothing new ships. ADR 0009 records the pattern | Parsing in the server with `recover`, which can't stop a parse or catch fatal errors. A process per message, which would start 5,000,000 processes per pass |
| VI: isolate unreleased behavior | The owner confirmed on 2026-11-13 that MailArkiv is a greenfield application with no users. Processing and search start normally, and the new screens are reachable while the slice is built | A flag and hidden routes add a code path to test without protecting any current users. Keep the required checks and deliver in small steps |

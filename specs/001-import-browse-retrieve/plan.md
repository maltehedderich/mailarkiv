# Implementation Plan: Import, Browse, and Retrieve One Email

**Branch**: `001-import-browse-retrieve` | **Date**: 2026-11-06 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `specs/001-import-browse-retrieve/spec.md`

## Summary

An administrator signs in, imports one `.eml` file, sees it listed, and downloads a byte-identical copy. A confirmation means the bytes and the catalog entry survive a power loss, and that guarantee is the slice's main risk.

We build one Go binary with an embedded SvelteKit interface. It stores originals as plain files named by SHA-256 and keeps the catalog in SQLite. Capture follows one fixed order: temp file, fsync, rename, directory fsyncs, SQLite commit, response. Startup recovery removes temp files and lists unreferenced originals. Tests stop the real binary at each capture step and check the syscall order under `strace`. [research.md](research.md) has the tests behind each decision.

## Technical Context

**Language/Version**: Go 1.27.3. TypeScript 6.0.3 and Svelte 5.57.3 for the interface.

**Primary Dependencies**:

- Runtime: Go `net/http`, `modernc.org/sqlite` v1.61.1, `golang.org/x/crypto` at `v0.58.1-0.20261009181718-2f1f834330b2` for Argon2id, and `golang.org/x/term` v0.47.0 for password prompts.
- Build only: SvelteKit 3.1.1, Vite 8.3.6, `adapter-static` 4.0.1, the fonts and icons in ADR 0004, and bun 1.4 with Node 26. [research.md](research.md#pinned-frontend-versions) lists every version.

**Storage**: SQLite in WAL mode with `synchronous=FULL` and one writer connection. Originals are files at `blobs/ab/cd/<sha256>` on one local filesystem. Failure events since the latest start stay in server memory, and every event also goes to the log.

**Testing**: `go test -race` with real SQLite and real files, a `faults` build for crashes and OS errors, `strace` for syscall order, Vitest for pure client logic, and Playwright with axe for critical journeys.

**Target Platform**: Linux on amd64, as a static binary built with `CGO_ENABLED=0`. Current browsers. Plan research tested Chromium only.

**Project Type**: Web service with an embedded static single-page interface and a small command line.

**Performance Goals**: Each list page opens in under 2 seconds with 10,000 messages (SC-007). The research test took 9.2 ms for the deepest page. First sign-in to downloaded original in under 2 minutes (SC-001).

**Constraints**: Files up to 150,000,000 bytes. The server streams file data; the browser buffers one download before saving and may retain one completed file URL for handoff. Each tab admits one active transfer and one waiter, with cancellation and visible refusal beyond that limit. No Node process at runtime and no external requests. Personal data stays out of logs and the status screen. Served at the root of a host name, behind HTTPS or on localhost.

**Scale/Scope**: Five screens, nine HTTP routes, three commands, six tables, and 10,000 messages verified in this slice. The 5 million message target isn't measured here. [Recovery's cost at that size](research.md#later-slices) stays open for the Slice 3 sizing test.

## Constitution Check

*Gate result: passes before research and again after design, with two recorded exceptions. Version 1.0.0.*

| Principle | How this plan complies |
| --------- | ---------------------- |
| I. User value, YAGNI | Scope matches the spec's. There are no generic interfaces, plugin points, or storage abstraction. Only two settings exist: `--data-dir`, which is required, and `--listen`, needed to run behind a proxy. The `x/net` and `x/text` pins wait until a slice imports them |
| II. Modular architecture | One deployable. `identity` and `archive` own their tables, and `opstatus` owns in-memory state. `web` and `cmd/mailarkiv` are adapters that call module operations, so CLI and HTTP share the password rules. A test enforces imports and table writes |
| III. Explicit contracts and failures | Outcomes, reasons, and categories are closed sets. Input is parsed once at the HTTP and CLI boundaries. Every failure reaches the client, the log, or both. Every network path has a timeout, listed in [the API contract](contracts/http-api.md#server-timeouts). A retried import is idempotent by content hash |
| IV. Security and privacy | Deny by default with a declared rule on every route. No mail content or account address in any log or status field. Argon2id, hashed session tokens, and SameSite plus `CrossOriginProtection`. Two new runtime dependencies, both from the Go team, pinned and scanned. ADR 0005 records them |
| V. Test according to risk | Failures are ranked below, each with a check at the lowest layer that catches it. Real SQLite, files, and modules. Doubles only for the disk's errors, the clock, and the proxy |
| VI. Small batches | The [delivery order](#delivery-order) starts with a walking skeleton through every layer. Each slice ends in behavior someone can verify. Critical-path failures appear on the status screen and in the log. `main` isn't a complete product until the last slice, which is a recorded exception |
| Governance | Specification defines scope and exclusions. This plan names each check and its layer, and the exceptions sit in Complexity Tracking |

## Credible failures

Ranked by harm first, then likelihood. Expected values come from the spec.

| # | Failure | Likelihood | Harm | Check | Layer |
| --- | ------- | ---------- | ---- | ----- | ----- |
| 1 | A confirmed import is lost after a crash or power loss | Medium | Critical | `TestCrashMatrix`, `TestCaptureSyscallOrder`, pragma read-back | Process |
| 2 | A failed import looks successful, or a successful one looks failed | Medium | Critical | `TestImportOutcomes`, `TestCrashMatrix`, client classifier tests | Integration, process, unit |
| 3 | A route returns mail or changes the archive without a session | Medium | Critical | `TestEveryRouteDeniesWithoutSession`, generated from the route table, so a new route is covered automatically | Integration |
| 4 | Recovery deletes a stored original | Low | Critical | `TestRecovery`, `TestCrashMatrix` count blobs before and after | Integration, process |
| 5 | Downloaded bytes differ from the input | Low | Critical | `TestImportedBytesRoundTrip`, with SC-002's samples including exactly 150,000,000 bytes | Integration |
| 6 | Mail content or an account address reaches the log or status screen | Medium | High | `TestNoMarkersInLogOrStatus` forces each category with marker fixtures and a marker account whose address is lowercase, since that's the stored form | Process |
| 7 | A hostile file name runs script, spoofs text, or reaches a path | Medium | High | Name tests in `archive`, display function tests, one rendered row, and the page policy | Integration, unit, end to end |
| 8 | A forged request imports mail, or a site frames a page | Medium | High | Middleware tests on headers and methods, plus one framing check in a browser | Integration, end to end |
| 9 | A session survives sign-out, expiry, or a password change | Low | High | `identity` tests with a fake clock, and CLI tests against a running server | Integration, process |
| 10 | A damaged original is saved as the message | Low | High | `TestDownloadVerifies` edits a real blob. `download.spec.ts` checks the browser result | Integration, end to end |
| 11 | A second server deletes the first's temp files | Low | High | `TestSecondServerRefused` | Process |
| 12 | Disk full or denied write leaves a partial file or a false result | Medium | Medium | `faults` run for each errno, asserting the result, category, and an empty `tmp/` | Process |
| 13 | The sign-in limit is bypassed, or responses reveal an account | Medium | Medium | `identity` tests with a fake clock. Status and body compared for known and unknown addresses. Review confirms the dummy hash runs | Integration |
| 14 | Simultaneous imports of the same bytes conflict | Medium | Medium | `TestConcurrentImports` under `-race` | Integration |
| 15 | A proxy's response is misread | High | Medium | Classifier table tests, stubbed responses in `import-outcomes.spec.ts`, and a manual nginx run in the quickstart | Unit, end to end, manual |
| 16 | `sudo` on an account command locks the server out of its catalog | Medium | Medium | Ownership check test | Unit |
| 17 | A screen fails keyboard, screen reader, or 360 px use | Medium | Medium | Keyboard, width, and axe checks in Playwright, plus a timed manual pass | End to end, manual |
| 18 | A dependency has a known vulnerability | Medium | Medium | `mise run audit` | Static |
| 19 | The list is too slow at 10,000 messages | Low | Low | `TestListAtTenThousand` walks every page against SC-007's budget | Integration |

Cosmetic rules in the design guide rely on review. Writing a test for each token would cost more than the bugs it could catch.

### Review fixes

- **R2, sign-in admission**: Reuse the two password slots to bound all admitted sign-in work, including catalog access. Refuse excess requests with 503 and show unavailable in the client. Saturation must not accumulate waiters, count as a wrong password, or bypass address blocking. Controlled slot occupancy and a held real SQLite connection test overload, cancellation, and resumed admission. Run `mise run lint`, `mise run typecheck`, and `mise run audit` (static), `mise run server:test` (unit, integration, process), `mise run frontend:test` (unit), and `mise run test:e2e` (end to end). No new dependency, queue, or constitutional exception is needed.
- **R3, usable credentials**: Define byte and encoding limits in identity and fit their worst-case JSON representation in the HTTP envelope. Reject unsupported creation or replacement before mutation. Test accepted ASCII, multibyte, escaped, and lowercase-expanding values over HTTP; test refused replacements retain the old password and sessions, and CLI errors explain the refusal. Run `mise run lint` and `mise run audit` (static), `mise run server:test` (unit, integration, process), and `mise run test:e2e` (end to end). The existing identity owner applies one policy to both adapters; no new dependency or constitutional exception is needed.
- **R4, buffered downloads**: Bound active transfers, waiting work, and completed object URLs to prevent repeated downloads from retaining unbounded file payloads. Test a 150 MB stream, delayed distinct requests, cancellation, URL release, and consecutive byte-identical browser saves. Keep keyboard focus through download state changes. Run `mise run lint` and `mise run typecheck` (static), `mise run frontend:test` (unit), and `mise run test:e2e` (end to end), plus a narrow-screen visual check. This follows the accepted browser buffering contract, adds no dependency, and needs no constitutional exception. Browser allocation overhead is not a measured memory ceiling.
- **R5, import completion after navigation**: Keep a started upload's result in the shared notification store. Guard page work after asynchronous boundaries with the component's lifetime. The credible failure is an exception or an unexpected focus change, refresh, or redirect on the destination page. A delayed-response browser regression accepts navigation, releases the import, and checks the result and destination. Run `mise run lint` and `mise run typecheck` (static), `mise run frontend:test` (unit), and `mise run test:e2e` (end to end). This uses the existing page and notification owners and needs no constitutional exception.
- **R9, concurrent capture schedules**: Wrap the real blob inspector in tests to hold imports before or after inspection. Exercise two absent-blob inspections, a commit before the other inspection, and two inspections of a missing or damaged stored original. Assert outcomes, retained bytes, observations, and repair events using real files and SQLite. Keep synchronization and bounded waits in the test file. Run `mise run lint` (static) and `mise run server:test` (unit, integration, process, with the race detector). The private inspection boundary adds no configuration, public interface, or constitutional exception.

### Crash matrix

`TestCrashMatrix` starts the `faults` binary with one point set to `kill`, imports a file over HTTP, restarts with the default binary, and checks the result. A client that gets no answer shows Unconfirmed. `after-commit`, `after-response`, and `recovery-after-temp-removal` only mean something as a kill, so they ignore an injected error: no error path is left at those points.

| Stop point | Client saw | After restart |
| ---------- | ---------- | ------------- |
| `before-temp-create`, `mid-temp-write`, `after-temp-fsync` | No answer | Not listed, `tmp/` empty, no blob (User Story 3 scenario 2) |
| `after-rename`, `after-dir-fsync`, `before-commit` | No answer | Not listed, blob kept and shown as unreferenced. Importing again gives Archived, one copy, and an empty unreferenced list (scenarios 3 and 4) |
| `after-commit` | No answer | Listed. Importing again gives Already archived and two history entries (scenario 5) |
| `after-response` | Archived | Listed, and the download matches byte for byte (scenario 1) |
| `recovery-after-temp-removal` | Not applicable | The next start gives the same result (FR-025) |

## Required checks

These replace the "Required checks" section of `AGENTS.md` in the same change that adds the tooling. `mise run` is the only interface, so CI, docs, and people all use these commands.

| Command | Runs | Layer |
| ------- | ---- | ----- |
| `mise run lint` | `gofmt -l`, `go vet`, `staticcheck`, `prettier --check` | Static |
| `mise run typecheck` | `svelte-kit sync`, `svelte-check --fail-on-warnings` | Static |
| `mise run audit` | `govulncheck ./...`, `bun audit --ignore=GHSA-gcqx-w48m-w2h4` | Static, needs network |
| `mise run server:test` | `go test -race ./...`, including process-level crash, syscall, and marker tests. Needs `strace` | Unit, integration, process |
| `mise run frontend:test` | `vitest run` | Unit |
| `mise run test:e2e` | `playwright test` against the built binary | End to end |
| `mise run test` | The three test tasks above | |
| `mise run check` | `lint`, `typecheck`, `test`, `audit`. CI gates on this | |

Other tasks are `setup`, `dev`, `build`, `frontend:build`, `server:build`, `format`, and `clean`. `audit` is new to the shared task convention, so the convention should gain it. `.github/workflows/check.yml` installs `strace`, caches Go builds, and runs `mise run setup` and `mise run check`.

Documentation-only changes keep the current rule: review against the constitution, check local links, and confirm scope and behavior agree across documents.

## Project Structure

### Documentation (this feature)

```text
specs/001-import-browse-retrieve/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   ├── http-api.md
│   └── cli.md
└── tasks.md             # Created by /speckit-tasks
```

### Source Code (repository root)

```text
cmd/mailarkiv/           # Command line adapter: serve, account create, account set-password
                         # Process-level tests live here: crash, syscall order, markers, lock
internal/
├── archive/             # Capture, catalog, retrieval, recovery. Owns message, source_observation, tmp/, blobs/
├── identity/            # Accounts, passwords, sessions, sign-in limit. Owns account, session, sign_in_*
├── opstatus/            # Recovery result, failure events, unreferenced list, and the failure log
├── sqlitedb/            # Open with pragmas, migrations in migrations/*.sql
├── web/                 # HTTP adapter: route table with access rules, JSON, static files
├── fault/               # At(): no-op by default, active only with -tags faults
└── archtest/            # Tests for module imports and table write ownership
frontend/
├── embed.go             # go:embed all:build
├── vite.config.ts       # Kit options live here in Kit 3
├── playwright.config.ts
├── src/
│   ├── lib/             # api.ts, import client and classifier, formatting, components
│   └── routes/
│       ├── (app)/       # Signed-in shell: +layout.ts, list and import at /, status at /status
│       └── (public)/sign-in/
└── tests/e2e/
docs/OPERATIONS.md       # Operator guide (FR-030, FR-031)
docs/adr/0005-*.md, 0006-*.md
go.mod, go.sum, mise.toml, mise.lock, .github/workflows/check.yml
```

**Structure Decision**: One Go module at the root and one frontend package under `frontend/`, where the design tokens already live. Only `cmd/mailarkiv` imports the embedded frontend, so `internal/` tests need no frontend build.

Allowed imports: `web` uses `archive`, `identity`, `opstatus`, and `fault`, because the `after-response` crash point must fire in the handler after the flush. `archive` uses `opstatus`, `sqlitedb`, and `fault`. `identity` uses `sqlitedb`. `opstatus`, `sqlitedb`, and `fault` import no other module. Only `cmd/mailarkiv` imports `frontend`. `archtest` enforces this.

The history drawer opens through `?message=<id>` on the list page. A plain URL then gives the Back button and the return-after-sign-in behavior (FR-040).

## Delivery order

Each slice ends in behavior that can be checked, and it merges to `main` with `mise run check` passing. `main` isn't a complete product until the last slice, which is the exception recorded in Complexity Tracking. The README keeps its planning status until then.

| Slice | Delivers | Verified by |
| ----- | -------- | ----------- |
| 1. Tooling | `mise.toml`, Go module, frontend scaffold with pinned packages, CI, ADR 0005 and 0006, `.gitignore`, `AGENTS.md` checks. The binary serves a static page | `mise run check` passes in CI |
| 2. Walking skeleton | The route table with deny-by-default rules and `opstatus` with its failure log, so no route exists without an access rule and capture can record failures from the start. Account create, sign-in, one import with the full durable capture order, the list, plain-link download, and the problem count in `GET /api/session` | US1's independent test, `TestImportedBytesRoundTrip`, `TestCaptureSyscallOrder` |
| 3. Access | Sign-out, session end, cross-site and framing rules, sign-in limit, set-password, return after sign-in | Scenario 2, risks 3, 8, 9, 13 |
| 4. Crash safety | Lock, startup checks, recovery, `faults` build, crash matrix, failed-import results, blob reuse and the outcome decision, and `GET /api/status`. `TestCrashMatrix` imports the same bytes again and reads the unreferenced list, so it needs the last three | Scenario 3, risks 1, 2, 4, 11, 12 |
| 5. Provenance | Repeat-import checks, history drawer, concurrent-import checks, download verification | Scenarios 4 and 5, risks 10, 14 |
| 6. Visibility | Status screen, problem count on every signed-in page, privacy test | Scenario 5, risk 6 |
| 7. Interface completion | Two-step import, progress and storing states, all six outcomes, proxy classification, leave warning, name display, paging at 10,000, keyboard, 360 px, accessibility | Scenarios 1 (untimed) and 7, risks 7, 15, 17, 19 |
| 8. Operator guide | `docs/OPERATIONS.md`, README status, sign-in link, a timed walkthrough, other browsers | Scenario 1 timed, scenarios 6 and 8 |

## Changes to other documents

| Document | Change | When |
| -------- | ------ | ---- |
| `AGENTS.md` | Replace "Required checks" with the commands above | Slice 1, with the tooling |
| `docs/adr/0005` (new) | Password hashing: Argon2id, `x/crypto` at the pinned commit, `x/term` | Slice 1 |
| `docs/adr/0006` (new) | Frontend and task tooling: pinned packages, mise, bun, Node 26, TypeScript 6 because of `svelte-check`, and the `deepmerge` exception | Slice 1 |
| `docs/ARCHITECTURE.md` | `go.mod` line: `x/net` and `x/text` arrive with the mail libraries in Slices 2 to 4 | Slice 1 |
| `frontend/DESIGN.md` | Date form becomes `2 Nov 2026, 09:14:55 +01:00`, with a wider list column. FR-043 requires the offset, and the spec wins | Slice 7 |
| `frontend/DESIGN.md` | The sign-in note also points to the operator guide, as FR-001 requires | Slice 8 |
| `README.md` | Status, setup, and a link to the operator guide | Slice 8 |
| `docs/OPERATIONS.md` (new) | FR-030 and FR-031, including nginx `client_max_body_size 150m`, `proxy_request_buffering off`, serving at the host's root, and which user runs account commands | Slice 8 |

## Remaining risks

- Neither automated check cuts power. They prove syscall order and recovery at every crash point. Actual durability also depends on the filesystem honoring flushes, and FR-031 makes that the operator's responsibility.
- Firefox and Safari behavior for plain-link downloads and the `Secure` cookie on localhost is untested. Quickstart scenario 8 covers it.
- Recovery time at 5 million messages is unmeasured. `serve` logs the duration so the Slice 3 sizing test can read it.

## Complexity Tracking

| Violation | Why Needed | Simpler Alternative Rejected Because |
| --------- | ---------- | ------------------------------------ |
| Principle IV asks for dependency versions with no known vulnerabilities. `deepmerge` 4.3.1 stays in the build, with low-severity report GHSA-gcqx-w48m-w2h4, and `mise run audit` ignores that one ID | `@sveltejs/vite-plugin-svelte` 7.3.1 requires `^4.3.1`, and the advisory lists no patched version. The call merges only our own Vite options at build time, and a search of the build output found no `deepmerge` code. Remove the ignore when a fixed version ships or the plugin drops the package | Overriding to a version below 4.2.0 breaks the plugin's declared range. Raising the audit threshold would hide future reports. Replacing SvelteKit undoes an adopted decision to remove a build-time, low-severity report |
| Principle VI says `main` MUST stay releasable. From the Slice 2 merge until Slice 8, `main` holds an incomplete import path: no crash recovery until Slice 4 and no download check until Slice 5 | Review and CI feedback on the capture path, the slice's main risk, shouldn't wait for all eight slices. Nothing in this feature publishes a release, and the README says there's no installable application until Slice 8 | One long branch delays that feedback and produces one very large merge. A flag would protect no user, because nobody can install this yet |

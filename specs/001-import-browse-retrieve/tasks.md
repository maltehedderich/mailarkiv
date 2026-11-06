---

description: "Task list for Slice 1: import, browse, and retrieve one email"
---

# Tasks: Import, Browse, and Retrieve One Email

**Input**: Design documents from `specs/001-import-browse-retrieve/`

**Prerequisites**: [plan.md](plan.md), [spec.md](spec.md), [research.md](research.md), [data-model.md](data-model.md), [contracts/http-api.md](contracts/http-api.md), [contracts/cli.md](contracts/cli.md), [quickstart.md](quickstart.md)

**Tests**: Included. The spec gives each story an independent test, SC-002 to SC-009 need automated checks, and the plan names a check for each credible failure.

**Organization**: Tasks are grouped by user story. All five stories are required before this slice is done.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependency on an unfinished task)
- **[Story]**: The user story the task serves (US1 to US5)

## Rules for every task

- Read [the constitution](../../.specify/memory/constitution.md) and [AGENTS.md](../../AGENTS.md) first. Read [frontend/DESIGN.md](../../frontend/DESIGN.md) before touching a screen.
- Work on branch `001-import-browse-retrieve`. Open a pull request at each checkpoint, and merge it when CI passes.
- Write each phase's test tasks first and watch them fail. Expected values come from the spec or a contract, never from running the code (constitution V).
- Run `mise run check` at each checkpoint. It must exit 0 before the next phase starts.
- Use real SQLite, real files, and real modules. Use doubles only for the disk's errors, the clock, and the proxy. Never skip a test or add a retry.
- Fixtures hold synthetic mail only, with addresses under `example.test`.
- Go tests send the session cookie by hand. `net/http/cookiejar` withholds `Secure` cookies over plain HTTP.
- Browser tests use `http://localhost:<port>`, the host research tested with the `Secure` cookie.
- A task that says "extend" adds to a file an earlier task created.

## How this list relates to the plan

The phases follow the plan's [delivery order](plan.md#delivery-order). Phase 8 is the plan's Slice 7. It finishes screens that need every story's server behavior, so US1 and US3 aren't done until it ends.

| Phase | Plan slice | Finishes | Shown by |
| ----- | ---------- | -------- | -------- |
| 1 Setup | 1 Tooling | | `mise run check` |
| 2 Foundational | Parts of 2, 3, and 6 | | Module, catalog, and route table tests |
| 3 US1 | 2 Walking skeleton | US1 scenarios 1 to 5, 7, 13 | US1's independent test |
| 4 US2 | 3 Access | All of US2, US1 scenario 6 | Quickstart scenario 2 |
| 5 US3 | 4 Crash safety | US3 scenarios 1 to 7 | Quickstart scenario 3 |
| 6 US4 | 5 Provenance | All of US4 | Quickstart scenarios 4 and 5 |
| 7 US5 | 6 Visibility | All of US5 | Quickstart scenario 5 |
| 8 Interface | 7 Interface completion | US1 scenarios 8 to 12, US3 scenarios 8 and 9, FR-041, FR-042 | Quickstart scenarios 1 (untimed) and 7 |
| 9 Polish | 8 Operator guide | FR-030, FR-031 | Quickstart scenarios 1 (timed), 6, 8 |

Writing these tasks exposed six places where the plan's order or design doesn't hold. T019 records them in the plan. The outcome table is already fixed in the data model.

| Change | Why |
| ------ | --- |
| The route table and deny-by-default land in Phase 2, not Slice 3 | No route should exist without an access rule, even between slices |
| `opstatus` lands in Phase 2, not Slice 6 | `GET /api/session` returns the problem count from Phase 3, and capture records failure events from Phase 3 |
| Blob reuse and the outcome decision land in Phase 5, not Slice 5 | `TestCrashMatrix` and US3 scenarios 4 and 5 import the same bytes again |
| `GET /api/status` lands in Phase 5, not Slice 6 | `TestCrashMatrix` reads the unreferenced list through it. The screen stays in Phase 7 |
| `web` may import `fault` | The `after-response` crash point must fire in the handler after the flush, and the plan's import list forbids that |
| The outcome decision reads the catalog row before it checks the blob | Two imports of new bytes can both find the blob absent. The later commit then finds the row, and the old table reported Restored with a false `stored_original_missing`, which breaks US4 scenario 4 |

Quickstart scenario 6 also moves from Slice 7 to Phase 9, because it uses the nginx block from `docs/OPERATIONS.md`.

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Plan Slice 1. Pinned tooling, CI, and a binary that serves a static page.

- [X] T001 Create `mise.toml` with `lockfile = true` under `[settings]`. Under `[tools]`, request `go = "1.27"`, `bun = "1.4"`, `node = "26"`, and the `go:` backend entries for `staticcheck` 2026.2.2 and `govulncheck` v1.9.0. Run `mise lock` and confirm `mise.lock` holds Go 1.27.3 or a newer 1.27 patch. Follow the `mise` skill. If the `go:` backend rejects `2026.2.2`, use staticcheck's matching `v0.x.y` module tag
- [X] T002 Add the tasks below to `mise.toml`, each with a `description`. `mise run` is the only interface, so CI and docs call these names
  - `setup`: `bun install --frozen-lockfile` and `bunx playwright install chromium` in `frontend/`, then `go mod download`
  - `frontend:build`: `bun run build` in `frontend/`
  - `server:build`: `CGO_ENABLED=0 go build -o bin/mailarkiv ./cmd/mailarkiv`. Set `CGO_ENABLED=0` on this task only, because `go test -race` needs cgo
  - `build`: depends on `frontend:build` and `server:build`
  - `dev`: `go run ./cmd/mailarkiv serve --data-dir .data/dev` beside `bun run dev` in `frontend/`
  - `lint`: `test -z "$(gofmt -l cmd internal frontend/embed.go)"`, `go vet ./...`, `staticcheck ./...`, and `prettier --check .` in `frontend/`. Bare `gofmt -l` always exits 0, and `.` would walk `frontend/node_modules`
  - `format`: `gofmt -w cmd internal frontend/embed.go` and `prettier --write .` in `frontend/`
  - `typecheck`: `svelte-kit sync` then `svelte-check --fail-on-warnings` in `frontend/`
  - `audit`: `govulncheck ./...` and, in `frontend/`, `bun audit --ignore=GHSA-gcqx-w48m-w2h4`
  - `server:test`: `go test -race ./...`
  - `frontend:test`: `vitest run` in `frontend/`, with `TZ=Europe/Berlin` in the task's env so time tests are deterministic
  - `test:e2e`: `playwright test` in `frontend/`, depending on `build`
  - `test`: depends on `server:test`, `frontend:test`, and `test:e2e`
  - `check`: depends on `lint`, `typecheck`, `test`, and `audit`
  - `clean`: deletes `bin/`, `frontend/build/`, and `frontend/.svelte-kit/`, and never `.data/`
  - `dev`, `lint`, `typecheck`, `audit`, `server:build`, `server:test`, and `frontend:test` depend on `frontend:build`. `//go:embed all:build` fails without that directory, and mise runs dependencies in parallel, so this also stops `svelte-kit sync` racing the build
- [X] T003 [P] Replace the single `/` line in `.gitignore` with `/bin/`, `/.data/`, `/frontend/build/`, `/frontend/.svelte-kit/`, `/frontend/node_modules/`, `/frontend/test-results/`, `/frontend/playwright-report/`, `mise.local.toml`, and `mise.*.local.toml`
- [X] T004 [P] Create `go.mod` with `module github.com/maltehedderich/mailarkiv`, `go 1.27.3`, and `ignore ./frontend/node_modules`. Later tasks add each dependency when they first import it, because `go mod tidy` removes unused ones
- [X] T005 [P] Create `frontend/package.json` with `"private": true`, `"type": "module"`, scripts `build` and `dev`, and `"imports": { "#lib": "./src/lib/index.js", "#lib/*": "./src/lib/*" }`. Run `bun install` and commit `frontend/bun.lock`. Pin these exact development dependencies:
  - `@sveltejs/kit` 3.1.1, `svelte` 5.57.3, `vite` 8.3.6, `@sveltejs/vite-plugin-svelte` 7.3.1, `@sveltejs/adapter-static` 4.0.1
  - `typescript` 6.0.3, `@types/node` 26.6.6, `svelte-check` 4.7.8, `vitest` 5.1.0, `@playwright/test` 1.65.0, `@axe-core/playwright` 4.14.0, `prettier` 3.9.10, `prettier-plugin-svelte` 4.1.1
  - `@fontsource-variable/inter` 5.3.0, `@fontsource-variable/geist-mono` 5.3.0, `unplugin-icons` 24.0.0, `@iconify-json/lucide` 1.2.151
- [X] T006 Create `frontend/vite.config.ts`. Kit 3 has no `svelte.config.js`, so options go to the `sveltekit()` plugin
  - `adapter-static` with `fallback: 'app.html'`, and `paths.relative: false`
  - `csp.mode: 'hash'` with `default-src 'self'`, `script-src 'self'`, `style-src 'self'`, `style-src-attr 'unsafe-inline'`, `object-src 'none'`, `base-uri 'none'`, `form-action 'self'`
  - The `Icons({...})` block from `frontend/DESIGN.md`, after `sveltekit()`, with `autoInstall` off
  - `test.include: ['src/**/*.test.ts']`, and a dev server proxy from `/api` to `http://127.0.0.1:8080`
- [X] T007 Create `frontend/tsconfig.json` (`"extends": "$app/tsconfig"`, `"types": ["$app/types", "node"]`, plus explicit `include` and `exclude`), `frontend/src/app.html`, `frontend/src/app.d.ts` (`import 'unplugin-icons/types/svelte'`), `frontend/.prettierrc` (with `prettier-plugin-svelte`), and `frontend/.prettierignore` (`build`, `.svelte-kit`, `node_modules`, `bun.lock`, `DESIGN.md`, `test-results`, `playwright-report`)
- [X] T008 Create `frontend/src/lib/styles/global.css` (reset, tokens on `body`, the `.icon` rules and the `:focus-visible` rule from `frontend/DESIGN.md`), `frontend/src/routes/+layout.svelte` (imports fonts and styles as `frontend/DESIGN.md` shows), `frontend/src/routes/+layout.ts` (`ssr = false`, `prerender = false`), and a placeholder `frontend/src/routes/(app)/+page.svelte` with the heading "MailArkiv"
- [X] T009 [P] Write `frontend/src/lib/format/time.test.ts`, then `frontend/src/lib/format/time.ts` (FR-043). `formatTime('2026-11-02T08:14:55.000Z')` returns `2 Nov 2026, 09:14:55 +01:00` under `TZ=Europe/Berlin`. Test `formatOffset(minutesEastOfUtc)` directly for `+01:00`, `-05:00`, and `+05:30`. This first pure function gives `vitest run` a real test
- [X] T010 [P] Create `frontend/embed.go`: `package frontend`, `//go:embed all:build`, `var Build embed.FS`. Only `cmd/mailarkiv` imports it
- [X] T011 Write `internal/web/static_test.go`, then `internal/web/static.go`: a handler over an `fs.FS` that serves known files, answers `app.html` for an unknown path, and answers 404 for a missing `/_app/` file. It handles GET and HEAD only. Tests use `fstest.MapFS`, so `internal/` tests need no frontend build
- [X] T012 Create `cmd/mailarkiv/main.go`: set umask 077 first with `syscall.Umask`, then dispatch `serve` with `--data-dir` (required) and `--listen` (default `127.0.0.1:8080`). Bad arguments print usage and exit 2. Serve `fs.Sub(frontend.Build, "build")` through the handler from T011
- [X] T013 Create `frontend/playwright.config.ts` (Chromium only, `testDir: 'tests/e2e'`, `retries: 0`), `frontend/tests/e2e/support/server.ts` (`startServer()` runs `../bin/mailarkiv serve` on a free port with a new temp data dir, and stops it after the test), and `frontend/tests/e2e/smoke.spec.ts` (the page renders and no request leaves its origin, per ADR 0004)
- [X] T014 Create `.github/workflows/check.yml`: run on pull requests and pushes to `main`, using `actions/checkout@v7` and `jdx/mise-action@v5`. Install `strace` with apt, cache `~/.cache/go-build` and `~/go/pkg/mod`, run `mise run setup`, run `bunx playwright install-deps chromium` in `frontend/`, and run `mise run check` as its own step
- [X] T015 [P] Write `docs/adr/0005-password-hashing.md` in the style of ADR 0004: Argon2id, `golang.org/x/crypto` at `v0.58.1-0.20261009181718-2f1f834330b2` and why that commit, RFC 9106's second recommended settings, and `golang.org/x/term` v0.47.0. [research.md](research.md#password-hashing) has the facts
- [X] T016 [P] Write `docs/adr/0006-frontend-and-task-tooling.md`: the pinned packages, mise, bun, Node 26, TypeScript 6 because of `svelte-check`, and the `deepmerge` exception, linking to the plan's Complexity Tracking. [research.md](research.md#tooling) has the facts
- [X] T017 [P] Update the `go.mod` sentence in `docs/ARCHITECTURE.md` under "Technology direction": `x/net` and `x/text` arrive with the mail libraries in Slices 2 to 4
- [X] T018 [P] Replace "Required checks" in `AGENTS.md` with the command table from [plan.md](plan.md#required-checks). Keep the documentation-only rule and the rule on reporting checks
- [X] T019 [P] Record the changes from "How this list relates to the plan" in `specs/001-import-browse-retrieve/plan.md`: update the Delivery order rows and the Allowed imports line, so `web` uses `archive`, `identity`, `opstatus`, and `fault`
- [X] T020 Run `mise run check` until it exits 0. Start `bin/mailarkiv serve --data-dir .data/smoke` and confirm `http://localhost:8080` shows the placeholder. The `.github/workflows/check.yml` run happens when the branch is pushed

**Checkpoint**: `mise run check` passes, and the binary serves a static page. This is Slice 1.

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: The catalog, the in-memory status, the module rules, the route table, and the server's lifecycle. Every story needs these.

**⚠️ CRITICAL**: No user story work can begin until this phase is complete.

- [X] T021 [P] Write `internal/sqlitedb/sqlitedb_test.go` against a real file. Both pools read back `journal_mode` as `wal`, `synchronous` as `2`, and `foreign_keys` as `1`. The read pool reads back `query_only` as `1` and refuses a write. The writer pool holds one connection. Migrations run once and set `user_version`. A database with a newer `user_version` refuses to open (FR-026). The `source_type` CHECK refuses any value other than `manual_import`
- [X] T022 Write `internal/sqlitedb/migrations/0001_initial.sql` with six tables. Times are integer Unix milliseconds in UTC
  - `account`: `id` INTEGER PRIMARY KEY, `email` TEXT NOT NULL UNIQUE ("`strings.ToLower` of the address given at creation. The only stored form (FR-044)"), `password_hash` TEXT NOT NULL ("Argon2id in PHC format (FR-002)"), `created_at` INTEGER NOT NULL
  - `session`: `token_hash` BLOB PRIMARY KEY ("SHA-256 of the cookie token"), `account_id` INTEGER NOT NULL REFERENCES `account`, `created_at` INTEGER NOT NULL, `expires_at` INTEGER NOT NULL ("`created_at` plus 12 hours, never extended")
  - `sign_in_failure`: `address_key` BLOB NOT NULL ("SHA-256 of the lowercased submitted address, indexed with `failed_at`"), `failed_at` INTEGER NOT NULL
  - `sign_in_block`: `address_key` BLOB PRIMARY KEY, `blocked_until` INTEGER NOT NULL
  - `message`: `id` INTEGER PRIMARY KEY AUTOINCREMENT ("Never reused"), `content_hash` TEXT NOT NULL UNIQUE ("Lowercase hex SHA-256. Identifies the message (FR-016)"), `size` INTEGER NOT NULL ("Bytes, from 1 to 150,000,000"), `latest_observation_id` INTEGER REFERENCES `source_observation` ("Set in the same transaction that creates the row, so committed rows are never NULL. Indexed descending for the list")
  - `source_observation`: `id` INTEGER PRIMARY KEY, `message_id` INTEGER NOT NULL REFERENCES `message` ("Indexed with `id`"), `source_type` TEXT NOT NULL `CHECK (source_type IN ('manual_import'))`, `observed_at` INTEGER NOT NULL, `account_email` TEXT NOT NULL ("The account's lowercase address, copied when the import is recorded"), `file_name` TEXT ("NULL means no name was supplied. Stored as received, in valid UTF-8")
- [X] T023 Implement `internal/sqlitedb/sqlitedb.go` to pass T021. `Open(path)` returns a writer `*sql.DB` with `SetMaxOpenConns(1)` and a read pool. Set `journal_mode=WAL`, `synchronous=FULL`, `foreign_keys=ON`, `busy_timeout=5000`, and `_txlock=immediate` through `_pragma=` in the DSN, plus `query_only=ON` on the read pool. Apply `//go:embed migrations/*.sql` in one transaction each, tracked with `PRAGMA user_version`. Run `go get modernc.org/sqlite@v1.61.1`
- [X] T024 [P] Write `internal/opstatus/opstatus_test.go`, then `internal/opstatus/opstatus.go`, from [the in-memory entities](data-model.md#in-memory-owner-opstatus)
  - `Start` (start time), `RecoveryResult` (finish time, temp files removed, temp files not removed, unreferenced originals found, duration), `UnreferencedOriginal` (content hash, size), `FailureEvent` (time, category, optional message ID, optional content hash, `restored` flag). No field may hold a subject, message address, body, file name, or account address (FR-029)
  - `Category` is a closed Go type with exactly `storage_full`, `storage_write_failed`, `catalog_write_failed`, `stored_original_missing`, `stored_original_damaged`, and `temp_file_not_removed`
  - Each failure and the recovery result also writes a JSON line through an injected `*slog.Logger`, with `category`, and `message_id` or `content_hash` where one applies (FR-028)
  - `ProblemCount()` is failure events plus unreferenced originals (FR-039). `RemoveUnreferenced(hash)` shrinks it. The failure list has no cap, and the type is safe for concurrent use
- [X] T025 [P] Write `internal/archtest/archtest_test.go`. Parse non-test Go files under `internal/` and `cmd/` with `go/parser`
  - Imports: `web` uses `archive`, `identity`, `opstatus`, and `fault`. `archive` uses `opstatus`, `sqlitedb`, and `fault`. `identity` uses `sqlitedb`. `opstatus`, `sqlitedb`, and `fault` use no other module. Only `cmd/mailarkiv` imports `frontend`
  - Table writes: find `INSERT INTO`, `UPDATE`, and `DELETE FROM` in string literals. `account`, `session`, `sign_in_failure`, and `sign_in_block` belong to `identity`. `message` and `source_observation` belong to `archive`
  - Run the checker on one bad in-memory source per rule, so it can't pass on an empty tree
- [X] T026 [P] Write `internal/web/web_test.go`. A zero-value route answers 403 for everyone. A `SignedIn` route with no session answers 401 with body `{"error":"no_session"}`. An unknown `/api/` path answers 401. Every response, including errors and static files, carries `MailArkiv-Response: 1`, `Content-Security-Policy: frame-ancestors 'none'; base-uri 'none'; form-action 'self'`, `X-Content-Type-Options: nosniff`, and `Referrer-Policy: same-origin`. Every `/api/` response also has `Cache-Control: no-store`. A handler's internal error answers 500 `internal_error` with all the response headers, and the log line holds the route pattern and no request values
- [X] T027 Implement `internal/web/web.go` and `internal/web/middleware.go` to pass T026
  - A closed `Access` type with `Undeclared` as the zero value, plus `Public` and `SignedIn`. `Route{Method, Path, Access, Handler}`, an exported `Routes()`, and `New(Deps)`
  - `/api/` is a catch-all, so an unknown method on a known path is treated like an unknown path. Static files take GET and HEAD only
  - `writeError` writes `{ "error": "<code>" }`. `writeInternalError` logs through `slog` and answers 500 `internal_error`. A helper writes times as RFC 3339 in UTC with milliseconds
  - The session check answers 401 `no_session` for every request until T046 connects `identity`, so it fails closed
  - Middleware order from the outside: response headers, then the cross-origin check T068 adds, then the route table. A rejected request still carries the marker header
- [X] T028 Write `cmd/mailarkiv/process_test.go` and `cmd/mailarkiv/serve_test.go`. `TestMain` builds the real binary once with `go build`. Helpers start it on a free port with a temp data dir, capture stderr, and stop it. `TestServeStartup` checks these: data dir mode `0700`, `catalog.db` mode `0600`, exit 2 without `--data-dir`, exit 1 with a log entry when the port is taken or the database is newer than the binary, exit 0 on SIGTERM, and JSON lines on stderr
- [X] T029 Implement `cmd/mailarkiv/serve.go` to pass T028, moving `serve` out of `cmd/mailarkiv/main.go`
  - Startup steps 1, 4, and 6 from [the CLI contract](contracts/cli.md#mailarkiv-serve). T086 adds the lock, the probe, and recovery
  - `http.Server` with `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `WriteTimeout` 30 s, `IdleTimeout` 120 s, and `MaxHeaderBytes` 64 KiB
  - On SIGTERM or SIGINT, `Shutdown` with a 5 second grace period, then `Close`, then exit 0
  - `log/slog` JSON on stderr, with `ErrorLog` routed through it. Log no sign-in attempts and no successful requests (FR-029)

**Checkpoint**: `mise run check` passes. `curl -i http://127.0.0.1:8080/api/messages` answers 401 `no_session` with all four headers. User story work can begin.

---

## Phase 3: User Story 1 - Import, list, and download one email (Priority: P1) 🎯 MVP

**Goal**: The walking skeleton, plan Slice 2. An administrator signs in, imports one file through the full durable capture order, sees it listed, and downloads identical bytes. This phase finishes scenarios 1 to 5, 7, and 13. Phase 4 adds sign-out (scenario 6), and Phase 8 finishes scenarios 8 to 12.

**Independent Test**: On a fresh install, create an account from the host, sign in, import a synthetic `.eml`, download it, and compare the SHA-256 of the two files.

### Tests for User Story 1

> **NOTE: Write these tests first, and ensure they fail before implementation.**

- [ ] T030 [P] [US1] Write `internal/identity/identity_test.go` with a fake clock
  - Addresses (FR-044): `Admin@example.test` is stored as `admin@example.test`. No `@`, two `@`, an empty side, or whitespace is refused. A duplicate with other capital letters is refused, and the existing account doesn't change
  - Passwords (FR-002): 11 code points refused, 12 accepted, counted as code points and not bytes. The stored hash is Argon2id in PHC format and doesn't contain the password
  - Sign-in (FR-003): different capital letters succeed (scenario 13). Wrong password, unknown address, and malformed address return one identical error (scenario 5)
  - Sessions (FR-005): the row holds the token's SHA-256 and not the token, `expires_at` is `created_at` plus 12 hours, and lookup returns the account's address
- [ ] T031 [P] [US1] Write `internal/archive/check_test.go` from [the file check](contracts/http-api.md#file-check) (FR-010, FR-011)
  - Pass: `Subject: x\r\n`, `X-Empty :\n`, `From: a@example.test\n`, `From a@example.test Mon Nov  2 09:14:55 2026\nSubject: x\n`, and a valid first line followed by binary bytes
  - `empty`: zero bytes
  - `no_mail_header`: `%PDF-1.7\n`, `PK\x03\x04`, `\r\nSubject: x`, `: value`, `Sub ject: x`, `From a@example.test\nnot a header\n`, and 5,000 bytes with no colon
- [ ] T032 [P] [US1] Write `internal/archive/capture_test.go` with real files and SQLite. An import of new bytes returns Archived, stores the exact bytes at `blobs/ab/cd/<hash>`, writes one `message` and one `source_observation`, and leaves `tmp/` empty. No committed row has a NULL `latest_observation_id`. Hostile names (`../../etc/passwd`, `<img src=x onerror=alert(1)>.eml`, a name with U+202E) are stored as text and never reach a path (FR-021). A refused file stores nothing. An over-limit body leaves `tmp/` empty. With a plain file swapped in for `tmp/`, `Import` returns `storage_write_failed`, records one failure event, logs it, and lists nothing. With the writer closed, it returns `catalog_write_failed`, keeps the blob, removes the temp file, and records one event
- [ ] T033 [P] [US1] Write `TestImportOutcomes` in `internal/web/imports_test.go` over a real listener, with real `identity`, `archive`, SQLite, and files. Rows from [the contract](contracts/http-api.md#post-apiimports): 201 `archived` with `id`, `content_hash`, `size`, `file_name`, `imported_at`, and `import_count` 1. 422 `refused` with `empty`. 422 `refused` with `no_mail_header`. 413 `refused` with `too_large`, by `Content-Length` and by a chunked body passing the limit. A body of exactly 150,000,000 bytes is accepted by `TestImportedBytesRoundTrip`. 400 `bad_file_name`. No name header gives `file_name` null. Invalid UTF-8 after decoding becomes U+FFFD. 500 `failed` with `storage_write_failed` when a plain file replaces `tmp/`
- [ ] T034 [P] [US1] Write `TestImportedBytesRoundTrip` in `internal/web/roundtrip_test.go` with SC-002's samples: valid mail, broken MIME, non-UTF-8 text, no `Message-ID`, mixed line endings, NUL bytes, no final line break, and exactly 150,000,000 bytes from a generated reader, not a buffer. The download's SHA-256 equals the input's. Headers are `Content-Type: application/octet-stream`, `Content-Disposition: attachment; filename="<content_hash>.eml"`, and `Content-Length` (FR-022). A second account lists and downloads the same message (scenario 7, FR-008)
- [ ] T035 [P] [US1] Write `internal/web/session_test.go`. `POST /api/session` answers 204 with exactly `__Host-mailarkiv_session=<token>; Path=/; Max-Age=43200; HttpOnly; Secure; SameSite=Strict`. Wrong password, unknown address, and malformed address each answer 401 `invalid_credentials`, with identical bodies. A body that isn't valid JSON or is over 16 KiB answers 401 `invalid_credentials` and counts against no address. `GET /api/session` with a session answers `{ "problem_count": 0 }`. With the catalog closed, `POST /api/session` answers 500 `internal_error`, not 401
- [ ] T036 [P] [US1] Write `TestCaptureSyscallOrder` in `cmd/mailarkiv/syscall_test.go`. Run the default binary under `strace -f -y -s 64 -e trace=fsync,fdatasync,rename,renameat,renameat2,write`, import new bytes, and assert this order, ending with the 201 written to the socket. Fail, never skip, if `strace` is missing
  - `fsync` of `tmp/up-…`, `renameat` to `blobs/ab/cd/<hash>`, `fsync` of `blobs/ab/cd`, `blobs/ab`, `blobs`, and the data dir, `fsync` of `catalog.db-wal`, then `write` of `HTTP/1.1 201` on the socket
- [ ] T037 [P] [US1] Write `cmd/mailarkiv/account_test.go`, driving `account create` over stdin. Check each exit code and exact stderr line from [the CLI contract](contracts/cli.md#shared-behavior-of-account-commands) that applies to `create`: exit 0 with one line on stdout, the invalid-address line, the already-exists line whatever the capital letters, and the 12-character line. A password given as an extra argument exits 2. A new data dir gets its catalog. Test the ownership rule as a unit on `checkOwner(dirUID, currentUID)`
- [ ] T038 [P] [US1] Write `frontend/src/lib/import/classify.test.ts` from [the classifier table](contracts/http-api.md#how-the-client-classifies-a-result), for this phase's rows: marked with a listed `outcome` gives that outcome. Marked with anything else, or a body that doesn't parse, gives Unconfirmed. Unmarked anything, including a 200 with a valid-looking body, gives Unconfirmed. Network error, abort, and timeout give Unconfirmed. T104 adds the remaining rows
- [ ] T039 [P] [US1] Write `frontend/tests/e2e/journey.spec.ts`. Create `Admin@example.test` through the CLI. Try a wrong password, then an unknown address. Each time the URL stays `/sign-in`, the line reads "That email address and password don't match.", and no session-ended message shows. Then sign in as `admin@example.test`. An empty list with "No mail yet" and a way to import opens. Import a synthetic file and see Archived, with the message first and showing file name, size, short hash, and a time ending in a UTC offset, matching `[+-]\d\d:\d\d$` (scenario 12). Download it: the page URL doesn't change, the link has no `download` attribute, and the SHA-256 matches

### Implementation for User Story 1

- [ ] T040 [P] [US1] Implement `internal/identity/password.go`: Argon2id with `t=3`, `m=64 MiB`, `p=4`, stored in PHC format, compared in constant time. At most 2 hashes run at once, and a waiter leaves the queue when its context ends. A fixed dummy hash lets unknown addresses verify at the same cost (FR-003). Run `go get golang.org/x/crypto@v0.58.1-0.20261009181718-2f1f834330b2`
- [ ] T041 [US1] Implement `internal/identity/identity.go` to pass T030, with an injected clock
  - Address rule: "exactly one `@`, text on both sides, and no whitespace". Stored form: "`strings.ToLower` of the address given at creation. The only stored form"
  - Password rule: "at least 12 characters, counted as Unicode code points"
  - `CreateAccount`, `SignIn` (steps 2 and 3 of [the sign-in steps](data-model.md#sign_in_failure-and-sign_in_block-owner-identity), with T065 adding the rest), and `LookupSession`
  - Session token: 32 random bytes, stored as its SHA-256. `expires_at`: "`created_at` plus 12 hours, never extended". Each sign-in also deletes expired rows
- [ ] T042 [US1] Implement `cmd/mailarkiv/account.go` to pass T037, dispatched from `cmd/mailarkiv/main.go`. `account create --data-dir DIR EMAIL` creates the data dir with mode 0700 if missing, then refuses to run for a user who doesn't own it. On a terminal, read the password twice with echo off through `golang.org/x/term`. Otherwise read one line from stdin. Never accept it as an argument. Run `go get golang.org/x/term@v0.47.0`
- [ ] T043 [P] [US1] Implement `internal/archive/check.go` to pass T031. Read only the first 4 KiB. A header line is "one or more bytes from 0x21 to 0x7E, excluding `:`, then optional spaces or tabs, then `:`", optionally after one line starting with `From `
- [ ] T044 [US1] Implement `internal/archive/archive.go` and `internal/archive/capture.go` to pass T032
  - `Open` creates `tmp/` and `blobs/`. `Import(ctx, body, meta)` takes the account's address and an optional file name, and returns the outcome and message
  - Refused: read the head, check it, and on a refusal drain the rest of the body and write nothing
  - Capture: create `tmp/up-*`, stream to it and to SHA-256, enforce `size` "Bytes, from 1 to 150,000,000", fsync, `MkdirAll`, rename to `blobs/ab/cd/<hash>`, and fsync `blobs/ab/cd`, `blobs/ab`, `blobs`, and the data dir. Directories fsync on this path even when they existed
  - Commit in one writer transaction: `INSERT … ON CONFLICT(content_hash) DO NOTHING`, one `source_observation` with `source_type` `manual_import`, then set `latest_observation_id`, "Set in the same transaction that creates the row, so committed rows are never NULL". A row that already existed gives Already archived. T083 replaces the always-rename step
  - Keep a body read error apart from a temp write error. A read error removes the temp file and returns a dropped result with no failure event. Remove the temp file on every failure path. Map failures as [the capture states](data-model.md#capture-states) say: `ENOSPC` from create, write, or fsync gives `storage_full`, and other errors there give `storage_write_failed`. A rename or directory fsync failure gives `storage_write_failed`. A commit failure gives `catalog_write_failed`, keeps the blob, and removes the temp file. Record each through `opstatus`, which also logs it
- [ ] T045 [US1] Implement `internal/archive/catalog.go`: `List(ctx, page)` runs [the list query](data-model.md#list-query) through the read pool, with `SELECT COUNT(*) FROM message` for the total. `LimitedPath(id)` returns the blob's path from `content_hash` only, never from a supplied name
- [ ] T046 [US1] Implement `internal/web/session.go` to pass T035: `POST /api/session` (`Public`) and `GET /api/session` (`SignedIn`), with the count from `opstatus.ProblemCount()`. In `internal/web/web.go`, connect the session check to `identity.LookupSession`, and put the account's address in the request context. A session lookup error goes to `writeInternalError` and never answers 401
- [ ] T047 [US1] Implement `internal/web/imports.go` to pass T033, with `POST /api/imports` (`SignedIn`). The session check runs before any body read
  - Decode `MailArkiv-File-Name`: bad percent-encoding is 400 `bad_file_name`, invalid UTF-8 becomes U+FFFD, and an absent header means no name
  - A `Content-Length` over the limit gets 413 before any read. Read the body through a reader that resets a 60 second read deadline on each read through `http.ResponseController`
  - Reset the write deadline just before responding. The server's 30 second `WriteTimeout` started when the headers were read, so a long upload would otherwise lose its answer
  - A dropped result writes nothing. Map each result to [the status table](contracts/http-api.md#post-apiimports): 201 `archived`, 200 `already_archived`, 422 and 413 `refused`, 507 `storage_full`, and 500 for `storage_write_failed` and `catalog_write_failed`. Any other error goes to `writeInternalError`
- [ ] T048 [US1] Implement `internal/web/messages.go` and `internal/web/download.go`, both `SignedIn`. `GET /api/messages?page=N`: `page` starts at 1 and defaults to 1, anything else is 400 `bad_page`, and a page past the end returns no items with the real total (FR-019). `GET /api/messages/{id}/original` streams the file with the success headers from T034 and a rolling 60 second write deadline. T094 adds the hash check before headers
- [ ] T049 [US1] Connect `identity` and `archive` to `web.New` in `cmd/mailarkiv/serve.go`, and run `mise run server:test`
- [ ] T050 [P] [US1] Write `frontend/src/lib/format/size.test.ts`, then `frontend/src/lib/format/size.ts`. `formatSize` uses decimal units: `31 B`, `48 KB`, `1.2 MB`, `150 MB`. `formatBytes` gives exact bytes: `150,000,000 bytes`
- [ ] T051 [US1] Write `frontend/src/lib/api.test.ts`, then implement `frontend/src/lib/api.ts`: typed `Message`, `MessagePage`, and `SessionInfo`, parsed once at this boundary. Every call has a 30 second timeout through `AbortSignal.timeout` and returns a closed union: success, `session_ended` for a marked 401, or `unavailable` for anything else, including a 500, an unmarked response, a network error, a timeout, and a body that doesn't parse (FR-045). `signIn` has its own union: `ok`, `invalid`, `blocked`, or `unavailable`
- [ ] T052 [P] [US1] Implement `frontend/src/lib/import/classify.ts` to pass T038. `ImportResult` is a closed union of the six outcomes, and the default for anything unrecognized is Unconfirmed
- [ ] T053 [US1] Implement `frontend/src/lib/import/upload.ts` with `XMLHttpRequest`: the raw file as the body, the name in `MailArkiv-File-Name` through `encodeURIComponent`, and `onProgress` and `onUploaded` callbacks. Abort and report Unconfirmed after 120 seconds with no upload progress, or 120 seconds after the upload finishes with no answer. Read `MailArkiv-Response` and pass the response to `classify`
- [ ] T054 [US1] Build `frontend/src/routes/(public)/sign-in/+page.svelte` as `frontend/DESIGN.md` describes: email address, password, a full-width primary Sign in, and the standing note "Accounts and passwords are managed on the server." on every visit. `invalid` shows FR-003's line, "That email address and password don't match." `unavailable` shows FR-045's sign-in line. If `window.isSecureContext` is false, say that HTTPS is required, because the browser would drop the cookie silently
- [ ] T055 [US1] Build `frontend/src/routes/(app)/+layout.ts` and `frontend/src/routes/(app)/+layout.svelte`. The load reads `GET /api/session`, and a 401 opens `/sign-in`. The shell is a sidebar on `--canvas` with the mark, the name, and a Messages item, beside one `--panel`. Build `frontend/src/lib/components/FailedLoad.svelte` with FR-045's words and `frontend/DESIGN.md`'s Failed load rule, with Try again calling `refreshAll()`. A session read that fails with anything but a marked 401 shows it in the panel, with no count pill
- [ ] T056 [US1] Build `frontend/src/lib/components/MessageRow.svelte` from `frontend/DESIGN.md`'s import list row: file name in `<bdi>`, or `(no file name)` when missing, then time, size, short hash, the count as `3×` only above 1, and a plain `<a href="/api/messages/{id}/original">` icon link with an `aria-label` and no `download` attribute
- [ ] T057 [US1] Build `frontend/src/routes/(app)/+page.ts` and `frontend/src/routes/(app)/+page.svelte`: the list for `?page=N`, "No mail yet" on an empty archive, `FailedLoad` on `unavailable` and never "No mail yet", and a thin import form. Label its controls "Choose a file" and "Import", so `journey.spec.ts` keeps passing after T115. The form shows the classified result as plain text until the next import starts, then refreshes page 1

**Checkpoint**: US1's independent test passes by hand: create an account, sign in, import a synthetic `.eml`, download it, and compare the SHA-256 of the two files. Quickstart scenario 1 also expects the history (Phase 6) and the two-step import (Phase 8), so it passes in full at Phase 8's checkpoint. Don't use this path with real mail yet: sign-out, crash recovery, and repeat-import checks are still missing.

---

## Phase 4: User Story 2 - Keep mail closed to anyone not signed in (Priority: P1)

**Goal**: Plan Slice 3. Direct requests get nothing without a valid session, forged requests and frames are refused, sessions end when they should, and the sign-in limit holds.

**Independent Test**: Send direct requests to every operation with no session, an expired session, a signed-out session, and a forged cross-site request. Check that each is denied and that the archive has not changed.

### Tests for User Story 2

- [ ] T058 [P] [US2] Write `TestEveryRouteDeniesWithoutSession` in `internal/web/access_test.go`, generated from `web.Routes()` so a new route is covered automatically (FR-006, SC-004)
  - For every `SignedIn` route, with no cookie, a garbage token, an expired session (fake clock), a signed-out session, and a session ended by a password change: 401 with body exactly `{"error":"no_session"}`, and no change to the message count or the blob count
  - A fixed allowlist from FR-006 holds `POST /api/session` and the static files as the only `Public` routes, so adding another fails the test
  - A route with no declared rule answers 403, even with a valid session. An unknown `/api/` path answers 401 without a session and 404 with one
  - The download route with `Sec-Fetch-Mode: navigate` answers 303 to `/sign-in?ended=1&next=<path>` from a same-origin `Referer`, and to `next=/` from another origin or none
  - With a session cookie and the catalog closed, every `SignedIn` route answers 500 `internal_error` and its handler doesn't run
- [ ] T059 [P] [US2] Extend `internal/web/web_test.go` (FR-007). `Sec-Fetch-Site: cross-site` on every non-GET route answers 403 with `{"error":"cross_origin"}` and the marker header, and stores nothing. `same-origin` and a request with no such header reach the handler. `POST` to a static path isn't served
- [ ] T060 [P] [US2] Write `internal/identity/limit_test.go` with a fake clock (FR-004). The tenth failure in 15 minutes blocks the next attempt, even with the correct password, the same for known and unknown addresses. The block lasts 15 minutes, and blocked attempts neither count nor extend it. Failures older than 15 minutes don't count. `Admin@example.test` and `admin@example.test` share one count. Success clears the address's failures. `sign_in_failure` stores the SHA-256 and never the address
- [ ] T061 [P] [US2] Write `internal/identity/session_test.go` with a fake clock (FR-005). Sign-out deletes the row. A session is refused at 12 hours and isn't extended by use. Setting a password deletes that account's sessions, leaves other accounts' sessions, and lifts the address's block and failures in one transaction
- [ ] T062 [P] [US2] Extend `internal/web/session_test.go`. Compare status and body byte for byte for a known and an unknown address, at a first failure and when blocked (429 `sign_in_blocked`). `DELETE /api/session` answers 204, deletes the row, and clears the cookie
- [ ] T063 [P] [US2] Extend `cmd/mailarkiv/account_test.go` against a running server. `account set-password` ends the account's session at once (risk 9), and a blocked address can sign in at once (scenario 7). Check the no-account line and the 12-character line from the CLI contract
- [ ] T064 [P] [US2] Write `frontend/tests/e2e/access.spec.ts`
  - A page on another origin can't show MailArkiv in a frame (scenario 8)
  - A fresh install shows only sign-in with its note, and has no way to create an account (scenario 6)
  - After `account set-password` ends a session, the next action opens sign-in with the session-ended message, and signing in returns to the same URL (scenario 9, FR-040). A first visit with no session shows no such message
  - A download link with an ended session lands on sign-in. Sign-out opens sign-in, and Back shows no list (US1 scenario 6)

### Implementation for User Story 2

- [ ] T065 [US2] Implement `internal/identity/limit.go` and call it from `SignIn`. Lowercase the address and hash it with SHA-256 as `address_key`. Follow [the sign-in steps](data-model.md#sign_in_failure-and-sign_in_block-owner-identity): step 1, "If an unexpired block exists, refuse without checking the password. The attempt doesn't count and doesn't extend the block". Step 4, "delete failures older than 15 minutes, add one, and count. At 10, write a block for 15 minutes from now and delete the address's failures". Delete old failures for all addresses, and expired blocks with them, so an anonymous caller can't grow these tables without limit
- [ ] T066 [US2] Add `SignOut` in `internal/identity/session.go` and `SetPassword` in `internal/identity/identity.go`. `SetPassword` applies the password rule, then in one transaction replaces the hash, deletes the account's sessions, and deletes the address's failures and block
- [ ] T067 [US2] Add `DELETE /api/session` (`SignedIn`) and the 429 `sign_in_blocked` answer in `internal/web/session.go`
- [ ] T068 [US2] Wrap the route table in `http.NewCrossOriginProtection` in `internal/web/middleware.go`, with a deny handler answering 403 `cross_origin` as JSON. In `internal/web/download.go`, add the 303 rule: with no session and `Sec-Fetch-Mode: navigate`, redirect to `/sign-in?ended=1&next=<path from a same-origin Referer, or />`, and answer 401 otherwise
- [ ] T069 [US2] Add `account set-password --data-dir DIR EMAIL` to `cmd/mailarkiv/account.go`, sharing the lowercase rule, the password reading, and the ownership check with `create`. It takes no lock, so it's safe while the server runs
- [ ] T070 [US2] Implement `frontend/src/lib/session.ts` and call it from `frontend/src/lib/api.ts` on `session_ended`. `signIn` has no such result, because its 401 means wrong credentials. Open `/sign-in?ended=1&next=<current URL>`. The signed-in layout sets a `sessionStorage` flag in `try`/`catch`, and sign-out clears it. Accept `next` only when it resolves to this origin
- [ ] T071 [US2] Update `frontend/src/routes/(public)/sign-in/+page.svelte`. Show the session-ended message only when `ended=1` is present and this tab's flag is set. After sign-in, go to a validated `next`, or `/`. On a 429, show "Too many failed sign-ins for this address. Try again in 15 minutes, or ask the operator to set a new password."
- [ ] T072 [US2] Add a Sign out item with the `log-out` icon in `frontend/src/routes/(app)/+layout.svelte`. In `frontend/src/routes/(app)/+page.svelte`, call `GET /api/session` just before an upload. Only a marked 401 changes anything: it opens sign-in. After any other result, the upload goes ahead and its own answer decides the outcome. If sign-out returns `unavailable`, stay on the page and show FR-045's sign-out line

**Checkpoint**: Quickstart scenario 2 passes, and T058 to T064 cover risks 3, 8, 9, and 13.

---

## Phase 5: User Story 3 - Never lose a confirmed import or confirm a failed one (Priority: P1)

**Goal**: Plan Slice 4. A confirmation survives a crash, a failure never looks like success, and an unknown result says so. This phase finishes scenarios 1 to 7. Phase 8 finishes the proxy scenarios 8 and 9.

**Independent Test**: An automated check stops the server at each capture step, restarts it, and compares what the archive holds with what the client was told.

### Tests for User Story 3

- [ ] T073 [US3] Create `internal/fault/fault.go` (`//go:build !faults`, `At(point string) error` returns nil) and `internal/fault/fault_faults.go` (`//go:build faults`). The second reads `MAILARKIV_FAULTS` as comma-separated `point=action`, where `kill` sends the process SIGKILL and `ENOSPC`, `EACCES`, or `EIO` returns that `syscall.Errno`
- [ ] T074 [US3] Extend `TestMain` in `cmd/mailarkiv/process_test.go` to also build with `-tags faults`, and add helpers that start that binary with `MAILARKIV_FAULTS` and wait for it to die
- [ ] T075 [P] [US3] Write `TestCrashMatrix` in `cmd/mailarkiv/crash_test.go` from [the crash matrix](plan.md#crash-matrix) (FR-012, FR-013, FR-018, FR-024, FR-025, SC-003). For each row, import a 1 MiB file over HTTP with the point set to `kill`, restart with the default binary, and check the result. Count blobs before and after every restart. Read the unreferenced list from `GET /api/status`, and read history size from `import_count`, because `GET /api/messages/{id}` arrives in Phase 6
  - `before-temp-create`, `mid-temp-write`, `after-temp-fsync`: no answer. Not listed, `tmp/` empty, no blob
  - `after-rename`, `after-dir-fsync`, `before-commit`: no answer. Not listed, blob kept and unreferenced. Importing again gives Archived, one copy, and an empty unreferenced list
  - `after-commit`: no answer. Listed. Importing again gives Already archived and `import_count` 2
  - `after-response`: Archived. Listed, and the download matches byte for byte
  - `recovery-after-temp-removal`: seed a leftover temp file and an unreferenced blob. The next start gives the same result
- [ ] T076 [P] [US3] Extend `TestCaptureSyscallOrder` in `cmd/mailarkiv/syscall_test.go` with the reuse path. Write a matching blob into the data dir before start, then import those bytes. Expect `fsync` of the blob, then `blobs/ab/cd`, `blobs/ab`, `blobs`, and the data dir, all before `fsync` of `catalog.db-wal`, then the 201
- [ ] T077 [P] [US3] Write `cmd/mailarkiv/faults_test.go` (scenario 6, FR-013, risk 12). Each case asserts the status, the reason, nothing new listed, `tmp/` empty, and a status and log event with the category
  - `TestStorageFailures`: `temp-create`, `temp-write`, or `temp-fsync` with `ENOSPC` gives 507 `storage_full`. With `EACCES` it gives 500 `storage_write_failed`. `rename=EACCES` and `dir-fsync=EIO` give 500 `storage_write_failed`. `commit=EIO` gives 500 `catalog_write_failed` with the blob kept, and it's listed as unreferenced after a restart
  - `TestRowReadFailure`: `row-read=EIO` gives 500 `internal_error`, with nothing new listed, `tmp/` empty, a log line, and no failure event
  - `TestDefaultBuildIgnoresFaults`: the default binary with `MAILARKIV_FAULTS=after-rename=kill` imports normally
- [ ] T078 [P] [US3] Write `cmd/mailarkiv/lock_test.go`
  - `TestSecondServerRefused`: a second `serve` on one data dir exits 1 with a log entry about the lock, and leaves the first server's in-progress temp file alone (risk 11)
  - `TestUnusableStartup`: a plain file where `tmp` belongs exits 1 and logs storage, and a garbage `catalog.db` exits 1 and logs the catalog (US5 scenario 6, FR-026). Plain files keep this true when the test runs as root
  - `TestTempFileNotRemoved`: `temp-remove=EACCES` with a seeded temp file leaves the server running, with `temp_file_not_removed` in status and the log (US5 scenario 5)
- [ ] T079 [P] [US3] Write `TestRecovery` in `internal/archive/recovery_test.go` (FR-024, FR-025). A leftover temp file is removed. An unreferenced blob is listed with hash and size and not deleted. A referenced blob isn't listed. A name in `blobs/` that isn't a valid hash at its matching path is logged, not listed, and not deleted. A damaged referenced blob isn't flagged, because recovery doesn't hash. A second run gives the same result, and the blob count never falls
- [ ] T080 [P] [US3] Extend `internal/web/imports_test.go`
  - Add `TestImportOutcomes` rows for every line of [the outcome table](data-model.md#import-outcome), seeding state by editing real files: Archived with an unreferenced blob, Archived replacing a damaged blob (`stored_original_damaged`, restored), Already archived, Restored from a missing blob (`stored_original_missing`, restored), and Restored from a damaged or unreadable one (FR-015, FR-017, FR-018). Each row adds exactly one observation
  - Write `TestDroppedUpload`: close a chunked upload mid-body over a real listener. Then `tmp/` is empty, nothing is listed, and no failure event exists (scenario 7)
  - In `internal/archive/capture_test.go`, table-test the decision function for the concurrent row (no row before the check, row at commit, blob absent), which `TestConcurrentImports` hits only by timing
- [ ] T081 [P] [US3] Write `internal/web/status_test.go` from [the status contract](contracts/http-api.md#get-apistatus): `started_at`, `recovery`, `unreferenced`, and `failures`, with `message_id` and `content_hash` absent where they don't apply. `problem_count` in `GET /api/session` equals failures plus unreferenced originals (FR-039)
- [ ] T082 [P] [US3] Write `TestShutdownDuringImport` in `cmd/mailarkiv/shutdown_test.go`. Send SIGTERM during a stalled chunked upload. The process exits 0 within about 6 seconds, and the client gets no success. After a restart, nothing is listed and `tmp/` is empty

### Implementation for User Story 3

- [ ] T083 [US3] Replace the always-rename step in `internal/archive/capture.go` with `ensureBlob` and a pure `decideOutcome`, following [the Import outcome table](data-model.md#import-outcome). A failed row read removes the temp file and answers 500 `internal_error` with no failure event, because FR-038's Catalog write failed says the original was stored. The client shows Unconfirmed
  - Read whether the row exists, then inspect the blob by hashing the whole file: absent, matching, or not matching. Unreadable counts as not matching
  - Absent or not matching: rename the temp file over the path and fsync `blobs/ab/cd`, `blobs/ab`, `blobs`, and the data dir
  - Matching: remove the temp file, then fsync the blob and those directories before the commit, so a visible blob from an earlier crash becomes durable
  - After the commit, record `stored_original_missing` or `stored_original_damaged` with `restored` true where the table says, and call `opstatus.RemoveUnreferenced(hash)`
- [ ] T084 [US3] Add `fault.At` calls in `internal/archive/capture.go` at `before-temp-create`, `temp-create`, `mid-temp-write`, `temp-write`, `temp-fsync`, `after-temp-fsync`, `row-read`, `rename`, `after-rename`, `dir-fsync`, `after-dir-fsync`, `before-commit`, `commit`, and `after-commit`. Return an injected error on the same path as a real one, so T044's mapping and recording handle it
- [ ] T085 [US3] Implement `internal/archive/recovery.go` to pass T079, following [the recovery steps](data-model.md#recovery)
  - Remove each entry in `tmp/`, with `fault.At("temp-remove")` before each removal. A failure adds a `temp_file_not_removed` event, and startup continues. Then call `fault.At("recovery-after-temp-removal")`
  - Walk `blobs/`. For each valid blob, look up the hash in `message`. A miss adds an `UnreferencedOriginal`. "Blobs are never hashed here, and none is ever deleted". Log the count of invalid names, and never the names
  - Record and log the result with its duration, so the Slice 3 sizing test can read it
- [ ] T086 [US3] Complete the startup order in `cmd/mailarkiv/serve.go` to pass T078: take an exclusive `flock` on `<data-dir>/lock` with `LOCK_NB`, write and remove a probe file in `tmp/` and `blobs/`, open the catalog, run recovery, then listen. Each failure exits 1, with a log entry naming the lock, storage, the catalog, or the port
- [ ] T087 [US3] Add 200 `restored` to the mapping in `internal/web/imports.go`. Flush the response through `http.ResponseController`, then call `fault.At("after-response")`
- [ ] T088 [US3] Implement `GET /api/status` (`SignedIn`) in `internal/web/status.go` to pass T081. It only reads `opstatus`, and no route here changes the archive (FR-027)

**Checkpoint**: Quickstart scenario 3 passes, opening `/api/status` in the signed-in tab until the screen arrives in Phase 7. Risks 1, 2, 4, 11, and 12 have their checks.

---

## Phase 6: User Story 4 - Keep every import on record without another copy (Priority: P2)

**Goal**: Plan Slice 5. Repeat imports add history and no copies, the history can be read, and a download checks the stored file first. T083 already holds the outcome logic, and T080 covers scenario 5.

**Independent Test**: Import one file three times under two names. Check for one list entry, three history entries, and one stored copy.

### Tests for User Story 4

- [ ] T089 [P] [US4] Write `TestRepeatImports` in `internal/web/repeat_test.go` (FR-014, FR-015, FR-016, FR-020, SC-005). Importing one file 10 times under two names gives one list entry with `import_count` 10, showing the latest name and time, plus 10 observations newest first with `source_type`, `observed_at`, `account_email`, and `file_name`, and exactly one file under `blobs/`. Two files that differ by one byte and share every header, including `Message-ID`, give two entries. An unknown ID answers 404 `not_found`
- [ ] T090 [P] [US4] Write `TestConcurrentImports` in `internal/archive/concurrent_test.go`, run under `-race`. For 50 rounds with new bytes each round, release two imports together. Both succeed, one is Archived and one Already archived, with one blob, two observations, no failure events, and an empty `tmp/` (scenario 4, risk 14)
- [ ] T091 [P] [US4] Write `TestDownloadVerifies` in `internal/web/download_test.go`, editing a real blob, with `fstest.MapFS` holding `download-failed/missing.html` and `download-failed/damaged.html` (FR-023, risk 10). From [the download table](contracts/http-api.md#get-apimessagesidoriginal): a matching file gives 200 with exact bytes. An unknown ID gives 404 with the missing page and no event. A missing file gives 404 with the missing page and a `stored_original_missing` event. A damaged or unreadable file gives 500 with the damaged page and a `stored_original_damaged` event. A failure response has no `Content-Disposition`, and its events carry the message ID
- [ ] T092 [P] [US4] Write `frontend/tests/e2e/download.spec.ts`. Damage a blob on disk and click the download link. The browser shows "This message wasn't downloaded" with status 500, loaded CSS, no scripts, no download event, and a link back to the list. Repeat with the blob deleted for status 404. `journey.spec.ts` already covers the successful path

### Implementation for User Story 4

- [ ] T093 [US4] Implement `internal/archive/retrieve.go`, replacing `LimitedPath` from T045. `OpenVerified(ctx, id)` opens the file, hashes all of it, seeks to 0, and returns the open file and size, so the bytes sent are the bytes checked. It returns distinct errors for unknown, missing, and damaged, and records the last two as failure events with message ID and content hash. `History(ctx, id)` returns the full content hash, the size, and every observation, newest first with no cap
- [ ] T094 [US4] Add the check to `internal/web/download.go` to pass T091. Call `OpenVerified` before any header. Write `download-failed/missing.html` or `download-failed/damaged.html` from the `fs.FS` with the error status. If a read fails after the headers, record `stored_original_damaged` and abort the connection with `panic(http.ErrAbortHandler)`
- [ ] T095 [US4] Add `GET /api/messages/{id}` (`SignedIn`) to `internal/web/messages.go`, with the response from [the contract](contracts/http-api.md#get-apimessagesid), and add `getMessage` to `frontend/src/lib/api.ts`
- [ ] T096 [P] [US4] Build `frontend/src/routes/download-failed/[reason]/+page.ts` and `frontend/src/routes/download-failed/[reason]/+page.svelte`: `prerender = true`, `ssr = true`, `csr = false`, and `entries` for `missing` and `damaged`. Show the heading "This message wasn't downloaded", the reason and next step from FR-038's two stored-original rows, and a link back to `/` styled as a default button. Confirm the build emits both files with no `<script>` and an absolute stylesheet link
- [ ] T097 [US4] Build `frontend/src/lib/components/FullHash.svelte` (16 groups of 4 in 4 columns, with a copy button that copies 64 characters without spaces) and `frontend/src/lib/components/HistoryDrawer.svelte` (a native `<dialog>` opened with `showModal()`, as `frontend/DESIGN.md` describes: title and Close, full hash, exact size, every observation with source type, time, account, and file name, and Download pinned in a footer). `unavailable` shows `FailedLoad` in the drawer body, and Close still works
- [ ] T098 [US4] Connect the drawer in `frontend/src/routes/(app)/+page.svelte` and `frontend/src/lib/components/MessageRow.svelte`. `?message=<id>` opens it, so Back and return-after-sign-in work. A row opens it on click or Enter, with `preventDefault()` on that key press. Its accessible name includes the file name. The download link doesn't open it. Escape closes it, and focus returns to the row

**Checkpoint**: Quickstart scenario 4 and the first half of scenario 5 pass. Risks 10 and 14 have their checks.

---

## Phase 7: User Story 5 - See capture and recovery problems without seeing mail (Priority: P2)

**Goal**: Plan Slice 6. The status screen, the problem count on every signed-in page, and proof that neither they nor the log carry mail content.

**Independent Test**: Sign in to an account whose email address is a unique marker string, and import fixtures with unique marker strings in the subject, addresses, body, attachment name, and file name. Force each failure category, then check that each failure appears on the status screen and in the log and that no marker does.

### Tests for User Story 5

- [ ] T099 [P] [US5] Write `TestNoMarkersInLogOrStatus` in `cmd/mailarkiv/markers_test.go` (FR-028, FR-029, SC-006, risk 6). Use a lowercase marker account, and fixtures with distinct markers in subject, addresses, body, attachment name, and supplied file name
  - Force all six categories: `temp-write=ENOSPC`, `temp-write=EACCES`, `commit=EIO`, a deleted blob then a download, an edited blob then a download, and `temp-remove=EACCES` with a seeded temp file
  - Each category appears in `GET /api/status` and on stderr with its time, and `problem_count` includes it
  - Also try a failed sign-in with its own marker address. No marker, password, or session token appears in stderr or the status response
- [ ] T100 [P] [US5] Write `frontend/tests/e2e/status.spec.ts`. On a clean start, Status shows the start time and that recovery found nothing, and the page doesn't contain "healthy". Force `stored_original_damaged` through a download. The count on the Status item reads 1 on every signed-in page, and Status shows the category, meaning, next step, time, and message ID. After a restart, the failure is gone and the note says the screen covers only the time since the latest start and that older failures are in the server log (scenarios 1, 3, 4, 7, 8). Seed a leftover temp file and an unreferenced blob in the data dir before a start. Status then shows the temp files removed and the blob's hash and size (scenario 2)

### Implementation for User Story 5

- [ ] T101 [P] [US5] Create `frontend/src/lib/status/categories.ts`: a `Record` over the closed category union holding FR-038's six names, meanings, and next steps, word for word, plus the next step for unreferenced originals, so a new category fails `svelte-check`
- [ ] T102 [US5] Build `frontend/src/routes/(app)/status/+page.ts` and `frontend/src/routes/(app)/status/+page.svelte` from `frontend/DESIGN.md`'s Status section (FR-027). The first line says what the screen covers. Sections hold the start time, the recovery result, temp files removed and not removed, unreferenced originals by hash and size with their next step, and failures. A `restored` failure says the copy was restored, in place of the next step. Offer no action, and never say "healthy". `unavailable` shows `FailedLoad`
- [ ] T103 [US5] Add a Status item with the `activity` icon in `frontend/src/routes/(app)/+layout.svelte`, with the count pill from `frontend/DESIGN.md` when the count is above 0. Re-read `GET /api/session` in `frontend/src/routes/(app)/+layout.ts` at each page load and in-app navigation (FR-039)

**Checkpoint**: Quickstart scenario 5 passes in full, including the status screen. Quickstart scenario 3's browser half passes too. Risk 6 has its check.

---

## Phase 8: Complete the Interface

**Goal**: Plan Slice 7. Two-step import, progress and storing states, all six outcomes on screen, proxy classification, the leave warning, safe name display, paging, keyboard use, 360 px, and accessibility. This finishes US1 scenarios 8 to 12 and US3 scenarios 8 and 9.

**Independent Test**: Each FR-032 outcome, forced, appears exactly as specified (SC-009), and the whole flow from sign-in to download works by keyboard and at 360 px (SC-008).

### Tests

- [ ] T104 [P] [US3] Extend `frontend/src/lib/import/classify.test.ts` with the remaining rows (FR-033): marked 401 opens sign-in, marked 403 gives Failed, and unmarked 413 gives Failed naming the proxy's limit. Unmarked 200, 502, and 504 give Unconfirmed
- [ ] T105 [P] [US1] Write `frontend/src/lib/import/choose.test.ts` (FR-009, FR-036): two or more files give "several", a 0-byte file gives "empty", 150,000,001 bytes gives "too large", and exactly 150,000,000 bytes is accepted
- [ ] T106 [P] [US1] Write `frontend/src/lib/format/file-name.test.ts` (FR-021). U+202E and control characters become a code part such as `U+202E`. HTML stays literal text. `../` stays literal text. A long name splits into a start and an ending that keeps the extension. A missing name gives `(no file name)`
- [ ] T107 [P] [US1] Write `TestListAtTenThousand` in `internal/web/list_test.go` (FR-019, SC-007, risk 19). Seed 10,000 messages with direct SQL in one transaction, since real imports would take minutes and `archtest` ignores test files. Walk every page over HTTP. Each answers in under 2 seconds, every message appears, and the order is most recent first
- [ ] T108 [P] [US3] Write `frontend/tests/e2e/import-outcomes.spec.ts` with responses stubbed through `page.route` (FR-032, FR-033, SC-009). Check the exact words for Archived, Already archived, Restored, Refused, Failed, Unconfirmed, an unmarked 413, unmarked 502 and 504, and an aborted request. Then, against the real server, confirm an Archived message is listed and a Failed one isn't
- [ ] T109 [P] [US1] Write `frontend/tests/e2e/import-flow.spec.ts` (FR-034, FR-035, FR-036, FR-037). Choosing a file shows its name, exact bytes, and the removal warning, and nothing is stored until Import is pressed (scenario 8). With a delayed stub, progress moves to a separate storing state, and a second submission isn't possible (scenario 9). The result stays until the next import starts (scenario 10). Leaving mid-import raises a warning (scenario 11). Several files and an empty file are refused with a message and no request
- [ ] T110 [P] [US1] Write `frontend/tests/e2e/file-name.spec.ts` (risk 7). Import through a direct request with `MailArkiv-File-Name` holding HTML and U+202E, then open the list. One row shows the name as text with the code visible, no script runs, and no CSP violation is reported
- [ ] T111 [P] Write `frontend/tests/e2e/screens.spec.ts` (FR-041, FR-042, SC-008, risk 17)
  - Keyboard only: sign-in through import, open history, close it with focus back on the row, and download
  - At 360 px: `scrollWidth` doesn't exceed `innerWidth` on sign-in, the list, import, history, status, and the failed-download page
  - axe with WCAG 2.2 AA tags on each screen, in both themes through `page.emulateMedia({ colorScheme })`, with no violations
  - With `/api/messages` stubbed to 500, then aborted, the list shows the failed-load state and no "No mail yet", and Try again loads the list once the stub is gone. Repeat for status and history
  - With `POST /api/session` and `DELETE /api/session` stubbed to 500, sign-in and sign-out show FR-045's lines, with no wrong-password line (FR-045)

### Implementation

- [ ] T112 [US3] Add the rows from T104 to `frontend/src/lib/import/classify.ts`, and open sign-in with the session-ended message on a marked 401
- [ ] T113 [P] [US1] Implement `frontend/src/lib/import/choose.ts` to pass T105
- [ ] T114 [P] [US1] Implement `frontend/src/lib/format/file-name.ts` to pass T106 and `frontend/src/lib/components/FileName.svelte`, with `<bdi>`, codes in a small `--radius-small` mark, and the middle cut so the ending stays visible. Use it in `frontend/src/lib/components/MessageRow.svelte` and `frontend/src/lib/components/HistoryDrawer.svelte`, where the drawer shows the name in full. Never use `{@html}`
- [ ] T115 [US1] Build `frontend/src/lib/components/ImportBox.svelte` from `frontend/DESIGN.md`'s step table: Ready, Chosen, Uploading with a percentage and bar, Storing with no percentage, and Refused in the browser. Keep a "Choose a file" button. Dropping adds to it and never replaces it. Set `multiple` on the input, so several files reach the T105 refusal and the interface never picks one silently. Import is off while busy. Model the steps as a closed union
- [ ] T116 [US1] Build `frontend/src/lib/components/ImportOutcome.svelte` in a `role="status"` element, with the dot from `frontend/DESIGN.md`'s outcome table beside the word
  - Archived: the file name, the size, and a link to `?message=<id>`
  - Already archived: "This import was added to the history, and no second copy was stored."
  - Restored: "The stored copy was restored from this file."
  - Refused: the reason (`This file is empty.`, `This file is over the 150 MB limit.`, or `This file doesn't start with a mail header line.`), then "Nothing was stored."
  - Failed: "The file is not archived." For an unmarked 413, add that the reverse proxy's upload limit refused it
  - Unconfirmed: "The file may be archived, and importing it again is safe and stores no second copy."
- [ ] T117 [US1] Replace the thin form in `frontend/src/routes/(app)/+page.svelte` with `ImportBox` and `ImportOutcome`. Refresh to page 1 without dropping focus after an import. While one runs, warn on leaving through `beforeunload` for the tab and `beforeNavigate` for in-app links. The result stays until the next import starts
- [ ] T118 [US1] Build `frontend/src/lib/components/Pager.svelte` ("1–50 of 7,000", Previous, Next, driven by `?page=N`) and non-animated skeleton rows with the real columns. Use both in `frontend/src/routes/(app)/+page.svelte`. A page past the end moves to the last page. Skeleton rows end in the list or the failed-load state, at the latest when T051's 30 second timeout fires
- [ ] T119 [US1] Update the list's time column in `frontend/src/lib/components/MessageRow.svelte`. Measure `22 Nov 2026, 09:14:55 +05:30` in Inter at 12 px with tabular numerals, and set the width from that. In `frontend/DESIGN.md`, change the date form to `2 Nov 2026, 09:14:55 +01:00` and the 112 px column, because FR-043 requires the offset and the spec wins
- [ ] T120 Apply `frontend/DESIGN.md`'s "At 720 px and below" table in `frontend/src/routes/(app)/+layout.svelte`, `frontend/src/lib/components/MessageRow.svelte`, `frontend/src/lib/components/ImportBox.svelte`, `frontend/src/lib/components/HistoryDrawer.svelte`, and `frontend/src/routes/(app)/status/+page.svelte`. Add the drawer's `--motion-slide` entrance, and remove animations under `prefers-reduced-motion` in `frontend/src/lib/styles/global.css`. Fix whatever T111 reports
- [ ] T121 Manual, needs a person. Run quickstart scenario 7 from `specs/001-import-browse-retrieve/quickstart.md`: 15 minutes with a screen reader on sign-in, import, and history. Confirm each import result is announced and that focus returns to the row when the drawer closes. Automate any repeatable finding

**Checkpoint**: `mise run check` passes. Risks 7, 15, 17, and 19 have their checks, and all five stories' automated checks pass. Quickstart scenario 1 passes in full, untimed.

---

## Phase 9: Polish & Cross-Cutting Concerns

**Purpose**: Plan Slice 8. The operator guide, the README, the manual scenarios, and the last review.

- [ ] T122 [P] Write `docs/OPERATIONS.md` for an operator who has never seen the project (FR-030, FR-031)
  - How to run the server, including `--data-dir`, `--listen`, and its default, where data lives, how to create an account and set a password, which user runs account commands, the 150 MB limit, and what a confirmation guarantees
  - What this slice doesn't promise: no tamper-proof storage, no encryption by the application, no backup until Slice 7, no deletion until Slice 6, no splitting of mbox files, and no email from the application, so no password reset by email
  - The operator provides HTTPS and a local filesystem that honors flush requests
  - An nginx block with `client_max_body_size 150m` and `proxy_request_buffering off`, served at the host's root
  - Safari over plain HTTP on localhost may refuse the session cookie. T127 confirms the result
- [ ] T123 [P] Update the sign-in note in `frontend/src/routes/(public)/sign-in/+page.svelte` to point to the operator guide (FR-001), with a link to `docs/OPERATIONS.md` on GitHub and the path as visible text, so it still helps on a closed network. Update the Sign-in section of `frontend/DESIGN.md` to match
- [ ] T124 [P] Update `README.md`: replace the planning status and "Start here" text with the current status and setup, and link to `docs/OPERATIONS.md`
- [ ] T125 Run quickstart scenario 6 from `specs/001-import-browse-retrieve/quickstart.md` through nginx in Docker with the block from `docs/OPERATIONS.md`: a file over 1 MiB imports and moves from progress to storing, the default limit gives Failed naming the proxy, and a stopped server gives Unconfirmed (US3 scenarios 8 and 9). Correct the guide if the block doesn't work
- [ ] T126 Manual, needs a person. Follow quickstart scenario 1 from `specs/001-import-browse-retrieve/quickstart.md`, using only `docs/OPERATIONS.md`. Time it from the sign-in page opening to the saved file. It must take under 2 minutes (SC-001). Fix whatever caused the delay if it doesn't, and record the time in the pull request
- [ ] T127 Manual, needs a person. Run quickstart scenario 8 from `specs/001-import-browse-retrieve/quickstart.md` in Firefox and Safari: scenario 1 and the failed download. Record any difference, and the Safari cookie result, in `docs/OPERATIONS.md`
- [ ] T128 Run quickstart scenarios 2 to 5 as written in `specs/001-import-browse-retrieve/quickstart.md`, then walk its Issue #1 acceptance table and confirm that each criterion's named test exists and passes
- [ ] T129 Run `mise run check`, then review against the constitution and `specs/001-import-browse-retrieve/plan.md`. Confirm by reading the code that the dummy hash runs for unknown addresses (risk 13). Search `frontend/src` for `{@html}` and "healthy". Check `frontend/DESIGN.md`'s cosmetic rules on each screen in both themes. See whether `deepmerge` has a fixed version or the plugin has dropped it, which would end the exception. Report every check run and any that couldn't run, as `AGENTS.md` requires

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies.
- **Foundational (Phase 2)**: Depends on Setup. Blocks all user stories.
- **US1 (Phase 3)**: Depends on Foundational.
- **US2 (Phase 4) and US3 (Phase 5)**: Each depends on US1, which holds the routes, sign-in, and capture path they harden.
- **US4 (Phase 6)**: Depends on US3, for `ensureBlob`, `decideOutcome`, and the rows in T080.
- **US5 (Phase 7)**: Depends on US3 for `faults` and `GET /api/status`, and on US4 for the download failures it forces.
- **Interface (Phase 8)**: Depends on US1 to US5, since it shows every outcome and tests every screen.
- **Polish (Phase 9)**: Depends on Phase 8.

### User Story Dependencies

```text
Setup → Foundational → US1 ─┬─ US2 ───────────────────┬─ Interface → Polish
                            └─ US3 → US4 → US5 ───────┘
```

US2 and US3 can run side by side. They touch different files, apart from new entries in the route table in `internal/web/web.go`. These stories aren't independent of US1, because the plan builds them on one walking skeleton.

### Within Each Phase

- Tests come first and fail until the phase's code is done.
- Go modules before their HTTP handlers, handlers before screens.
- T073 and T074 come before the other Phase 5 tests, which need the `faults` binary.

### Parallel Opportunities

- Setup: T003, T004, T005, then T009, T010, and T015 to T019.
- Foundational: T021, T024, T025, T026. T022 and T023 run in sequence.
- Each story's test tasks are all `[P]`, except T073 and T074.
- US1 implementation: T040, T043, T050, and T052 touch separate files.
- Phase 8: T104 to T111 are all `[P]`, as are T113 and T114.
- Phase 9: T122, T123, T124.

---

## Parallel Examples

```bash
# User Story 1 tests:
Task: "Write internal/identity/identity_test.go"
Task: "Write internal/archive/check_test.go"
Task: "Write TestImportedBytesRoundTrip in internal/web/roundtrip_test.go"
Task: "Write frontend/src/lib/import/classify.test.ts"

# User Story 2 tests:
Task: "Write TestEveryRouteDeniesWithoutSession in internal/web/access_test.go"
Task: "Write internal/identity/limit_test.go"
Task: "Write frontend/tests/e2e/access.spec.ts"

# User Story 3 tests, after T073 and T074:
Task: "Write TestCrashMatrix in cmd/mailarkiv/crash_test.go"
Task: "Write cmd/mailarkiv/faults_test.go"
Task: "Write TestRecovery in internal/archive/recovery_test.go"

# User Story 4 tests:
Task: "Write TestRepeatImports in internal/web/repeat_test.go"
Task: "Write TestConcurrentImports in internal/archive/concurrent_test.go"
Task: "Write TestDownloadVerifies in internal/web/download_test.go"

# User Story 5:
Task: "Write TestNoMarkersInLogOrStatus in cmd/mailarkiv/markers_test.go"
Task: "Write frontend/tests/e2e/status.spec.ts"
Task: "Create frontend/src/lib/status/categories.ts"
```

---

## Implementation Strategy

### MVP First

Phases 1 to 3 give the walking skeleton: sign in, import, list, and download, with the durable capture order proven by `TestCaptureSyscallOrder`. Stop there and run the independent test. It proves the path works. Don't use it with real mail yet: the spec requires all five stories, and crash recovery arrives in Phase 5.

### Incremental Delivery

Merge to `main` at each checkpoint, with `mise run check` passing. `main` isn't a complete product until Phase 9, which is the exception in the plan's Complexity Tracking. Follow the phase table above. The README keeps its planning status until T124.

### Parallel Team Strategy

1. Finish Setup, Foundational, and US1 together.
2. Developer A takes US2. Developer B takes US3, then US4.
3. Whoever finishes first takes US5, then Phase 8's server and classifier tasks, while the other takes its screens.

---

## Notes

- `mise run audit` needs network access. `mise run server:test` needs `strace`.
- `audit` isn't in the shared mise task vocabulary yet. The plan says the convention should gain it, and that convention lives outside this repository.
- T121, T126, and T127 need a person, with a screen reader, a stopwatch, and Safari. Report them as not run until someone does them.

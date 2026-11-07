# Research: Import, Browse, and Retrieve One Email

Each section records one decision for Slice 1, why we made it, and what we rejected. "Tested" means we ran it on 2026-11-06 in a throwaway module on this host (4 CPUs, Linux 6.8, Go 1.27.3, Chromium through `playwright-cli`, nginx 1.30.6 in Docker). No test was run in Firefox or Safari.

The technical context in [plan.md](plan.md) has no open clarifications left.

## Capture and durability

### Capture order and fsync

**Decision:** Follow rule 3 in [ARCHITECTURE.md](../../docs/ARCHITECTURE.md), with two additions the rule leaves implicit.

1. fsync every directory on the path from `blobs/ab/cd` up to `blobs`, because `MkdirAll` may have just created `ab` and `cd`, and their entries need to be durable too.
2. On the path that reuses an existing blob, fsync the blob and those directories before the SQLite commit. Without this, a process crash after a rename but before the directory fsync leaves a visible blob whose directory entry isn't durable. A later import would reuse it, commit, and confirm, and a power loss could then lose a confirmed message.

**Rationale:** Tested under `strace -f -y`. A sample capture produced this order with `modernc.org/sqlite` v1.61.1:

```text
fsync(data/tmp/up-…)
renameat(data/tmp/up-…, data/blobs/ea/94/ea94…)
fsync(data/blobs/ea/94)
fsync(data/blobs/ea)
fsync(data/blobs)
fsync(data)
fsync(data/catalog.db-wal)     <- the SQLite commit
```

`PRAGMA journal_mode`, `synchronous`, and `foreign_keys` read back `wal`, `2`, and `1` when set through `_pragma=` in the DSN. Twenty separate commits took 29.5 ms in total on this host's disk.

**Alternatives rejected:** A single transaction across file and catalog isn't possible. Syncing only the leaf directory leaves the hole described in item 1.

### Concurrent imports of the same bytes

**Decision:** No capture lock. Each import writes its own temp file, renames it into place if needed, and lets the catalog transaction decide the outcome with `INSERT … ON CONFLICT(content_hash) DO NOTHING`.

**Rationale:** `rename` is atomic and both temp files hold the same fsynced bytes, so a double rename leaves one correct copy. The transaction picks one Archived and one Already archived, which satisfies User Story 4 scenario 4. Fewer locks means fewer ways to stall capture.

**Alternatives rejected:** A per-hash mutex adds a keyed lock for a race that is already harmless. A global mutex would block every import while one verifies a 150 MB blob.

### Outcome decision

**Decision:** The outcome comes from three facts: whether the catalog row existed before the blob check, whether it exists at commit, and what `ensureBlob` found. The table is in [data-model.md](data-model.md#import-outcome).

### Early rejection during upload

**Decision:** Read and discard the rest of the body before answering Refused. Answer over-limit requests with 413 as soon as the limit is passed, since the browser never sends those. Run a session check through `GET /api/session` just before an import starts.

**Rationale:** Tested: a Go handler that replied 422 after 4 KiB of a 60 MB XHR upload did reach Chromium on localhost. That result depends on timing, because Go closes the connection after a bounded read and a reset can arrive before the browser reads the response. A reset would show as Unconfirmed where the spec requires Refused. Discarding is one line, and only a signed-in administrator can reach it. The session check covers the same problem for a 401, where we must not read an unauthenticated body.

**Alternatives rejected:** Repeating the header check in the browser would keep the rule in two languages. `Expect: 100-continue` isn't available to browsers.

### Graceful shutdown

**Decision:** On SIGTERM, call `http.Server.Shutdown` with a 5 second grace period, then `Close`. Imports that finish in time are confirmed. Others see a dropped connection, which the browser shows as Unconfirmed. The next start removes their temp files.

### One server per data directory

**Decision:** `serve` holds an exclusive `flock` on `<data-dir>/lock` and refuses to start if another process has it (FR-026).

**Rationale:** Startup recovery deletes temp files. A second server started by accident would delete the temp files of imports running in the first. The kernel releases `flock` when a process dies, so a crash leaves no stale lock.

### Forcing crashes and OS failures in tests

**Decision:** A package `internal/fault` exposes `At(point string) error`. The default build compiles it to a function that returns nil. A build with `-tags faults` reads `MAILARKIV_FAULTS`, such as `after-rename=kill` or `temp-write=ENOSPC`, and either sends itself SIGKILL or returns that errno. Tests build the real binary with the tag, drive it over real HTTP, and restart it without the variable.

A second check runs the default-build binary under `strace -f -y` and asserts the syscall order, ending with the response write on the socket. It catches a reordered fsync or a wrong pragma, which a kill test cannot see, because SIGKILL keeps the page cache.

**Rationale:** Constitution principle V allows doubles for systems we don't control and asks for real owned collaborators. The disk is the OS's. Everything else stays real. Production builds contain no fault code, so the variable has no effect there.

**Remaining risk:** Neither check cuts power. Together they show that the right syscalls run in the right order and that recovery handles each crash point. A filesystem that ignores flushes can still lose data, and FR-031 tells the operator so.

**Alternatives rejected:** `dm-log-writes` or a VM power-cut needs root and is too slow for the default suite. An injected filesystem interface would replace the syscalls we most want to exercise.

## Accounts and sessions

### Password hashing

**Decision:** Argon2id from `golang.org/x/crypto/argon2`, with RFC 9106's second recommended settings: `t=3`, `m=64 MiB`, `p=4`, stored in PHC format so the settings can change later. At most 2 hashes run at once, which caps memory at 128 MiB. Unknown addresses verify against a dummy hash, so timing doesn't reveal which accounts exist (FR-003).

**Rationale:** Tested: 146 ms per hash on this host. The standard library's only password-suitable function is `crypto/pbkdf2`, which isn't memory-hard. Constitution principle IV names cryptography as a reason to add a dependency.

**Version:** Pin `golang.org/x/crypto` at `v0.58.1-0.20261009181718-2f1f834330b2`. Tagged v0.58.0, the newest tag, makes `govulncheck` report GO-2026-5932 at module level for `x/crypto/openpgp`, which we don't import. Tested: the pinned commit builds, and `govulncheck` v1.9.0 reports no vulnerabilities. Move to the next tag when one exists.

**Alternatives rejected:** PBKDF2 for the reason above. bcrypt can't take a password over 72 bytes, a limit a long passphrase can hit.

### Reading a password on the host

**Decision:** Add `golang.org/x/term` v0.47.0. On a terminal, read the password twice with echo off. Otherwise, read one line from stdin. Never take it from an argument, which would land in shell history and `/proc`.

**Rationale:** Turning off echo needs `termios` calls that the standard library doesn't wrap. `x/term` comes from the Go team, and `govulncheck` reports nothing for it (tested).

### Session cookie

**Decision:** A random 32-byte token in a cookie named `__Host-mailarkiv_session`, with `Secure`, `HttpOnly`, `SameSite=Strict`, and `Path=/`. The catalog stores only the token's SHA-256.

**Rationale:** Tested in Chromium: `http://localhost` accepts and returns this cookie, so the spec's "local machine only" setup works with no setting to disable `Secure`. A plain HTTP address other than localhost makes the browser drop the cookie, so the sign-in page checks `window.isSecureContext` and says that HTTPS is required instead of failing silently.

**Remaining risk:** Safari has historically refused `Secure` cookies on localhost. Operators using Safari need HTTPS. We have not tested this, so the operator guide states it as a limit to check.

### Cross-site requests and framing

**Decision:** Wrap the whole server in `http.CrossOriginProtection`, which has been in the standard library since Go 1.25. Set `frame-ancestors 'none'` in a response header (FR-007).

**Rationale:** Tested from a second origin in Chromium: a `no-cors` credentialed POST and a form POST both failed to reach the handler (0 of 2), and the iframe was blocked by `frame-ancestors`. With curl, `Sec-Fetch-Site: cross-site` got 403 and `same-origin` got 200. No token handling is needed, and no dependency.

### Sign-in limit storage

**Decision:** Keep the limit in SQLite.

**Rationale:** Setting a new password must lift a block (FR-004), and the operator does that from a separate CLI process, which can't reach server memory. Keys are the SHA-256 of the lowercased submitted address, so a password typed into the email field isn't stored in clear text.

## Web interface

### Download through fetch

**Decision:** The interface fetches `/api/messages/{id}/original`, reads the whole body, and saves it through an object URL with `download="<content_hash>.eml"`. A failure answers JSON with status 404 or 500, and the page shows it as a notification.

**Rationale:** This replaced the first build's plain link. That link worked in Chromium: a success downloaded with the page left in place, and a failure showed a prerendered "This message wasn't downloaded" page at the download address. But the failure page took the administrator out of the app and needed its own way back. Fetching keeps the list in place and lets one notification report both imports and downloads. The client saves only after every byte has arrived and the count matches `Content-Length`, so a broken transfer saves nothing. The server still hashes the file before it sends any header.

**Cost:** The tab holds the whole file, up to 150 MB, until it's saved. The browser's own download list shows the file only at the end, so the interface shows progress for any download that takes longer than 400 ms.

**Remaining risk:** Firefox and Safari were not tested. A manual check belongs in the final walkthrough.

**Alternatives rejected:** The plain link with prerendered failure pages, described above. `showSaveFilePicker` streams to disk with no memory cost, but only Chromium has it.

### Session ended on a download

**Decision:** With no valid session, the download route answers 401 to the interface's fetch, and the interface opens sign-in with the session-ended message (FR-040). A navigation, such as the address opened in a tab, still gets 303 to `/sign-in?ended=1&next=<referring page>`.

**Rationale:** The 303 was tested in Chromium with a plain link: it sends `Sec-Fetch-Mode: navigate` and the full same-origin `Referer`, and the redirect landed on `/sign-in?ended=1&next=%2F%3Fpage%3D3`. The server sets `Referrer-Policy: same-origin` so the header doesn't leave the site. The client validates `next` against its own origin before using it.

### Serving at the root

**Decision:** Build with `paths.relative: false`, so MailArkiv must be served at the root of its host name. The operator guide says so.

**Rationale:** `app.html` is the fallback for every unknown path, and its asset links must work at any depth. Absolute links do that. Relative ones would break below the first level.

### Recognizing a proxy's response

**Decision:** Every response MailArkiv generates carries `MailArkiv-Response: 1`. A response without it came from something else. Unmarked 413 is Failed and names the proxy's limit. Every other unmarked response, and any network error, is Unconfirmed (FR-033).

**Rationale:** Tested through real nginx 1.30.6 with default settings. Files of 2 MB and 60 MB each got a readable 413 with no marker, 3 out of 3 times, with upload progress reaching 100% first. With `client_max_body_size 150m` the marker passed through, and a server abort became an unmarked 502. A stripped header fails safely, since every result then shows as Unconfirmed.

### Upload request

**Decision:** `POST /api/imports` with the file as the raw request body. The name travels percent-encoded in a `MailArkiv-File-Name` header. The browser sends it with `XMLHttpRequest`.

**Rationale:** A raw body means no framing sits between the request and the stored bytes, and the limit applies to exactly the file's size. A header stays out of proxy access logs, where a query string with a file name would end up. `XMLHttpRequest` has upload progress and a separate upload-finished event. Tested through nginx with `proxy_request_buffering off` and a server that took 800 ms to answer: the upload finished at 302 ms and the answer arrived at 1,020 ms, which gives the two states FR-035 requires. The operator guide recommends that setting so nginx doesn't spool a second copy of each message to its own temp directory. We didn't test nginx with buffering on.

**Alternatives rejected:** `multipart/form-data` adds framing, and Go's `Part.FileName` strips path parts, which would hide a hostile name the spec wants shown neutralized.

### Static output and content security policy

**Decision:** `adapter-static` with `fallback: 'app.html'` and `csp.mode: 'hash'`. Directives: `default-src 'self'`, `script-src 'self'`, `style-src 'self'`, `style-src-attr 'unsafe-inline'`, `object-src 'none'`, `base-uri 'none'`, `form-action 'self'`.

**Rationale:** Tested in Chromium. With `style-src 'self'` alone, one inline-style error appeared on load. Allowing `style-src-attr 'unsafe-inline'` cleared it, and `style:width` still worked. `style-src-attr` permits `style=""` attributes, but no `<style>` elements or remote sheets, so remote loads stay blocked as the architecture requires. Kit writes the policy to a `<meta>` tag, which can't carry `frame-ancestors`, so that one stays in the response header.

### Time format

**Decision:** `2 Nov 2026, 09:14:55 +01:00` everywhere, including lists.

**Rationale:** [DESIGN.md](../../frontend/DESIGN.md) gives `2 Nov, 09:14:55` for lists, which lacks the UTC offset FR-043 requires. The spec wins, as the design guide itself says. It also lacks a year, so it isn't absolute in an archive that spans years. Slice 1 updates the design guide and widens the 112 px column. The task measures the string in Inter at 12 px to set the width.

### List paging

**Decision:** Offset paging, 50 rows per page, driven by `?page=N`.

**Rationale:** Tested with 10,001 messages: the deepest page plus the total count took 9.2 ms, against the 2 second budget in SC-007. New imports go to the top, so paging forward can show a row twice and can't skip one, which keeps every message reachable (FR-019). A URL with a page number also gives "return to the same page" for free (FR-040).

**Alternatives rejected:** Keyset paging can't produce "1–50 of 7,000" without extra work and has no measured need at this size. Revisit it when the Slice 3 sizing test runs.

## Tooling

### mise and bun

**Decision:** Use the team's mise stack: Go, bun, and Node in `mise.toml`, exact versions in `mise.lock`, and canonical task names. bun installs packages. Node 26 runs Vite and Playwright, which use a Node shebang.

**Rationale:** Tested: with Node 26.13.0 and bun 1.4.4, `bun install`, `vite build`, `svelte-check`, `vitest run`, and `playwright --version` all succeed. ADR 0004's test used npm, but it didn't choose a package manager.

### Pinned frontend versions

| Package | Version | Note |
| ------- | ------- | ---- |
| `@sveltejs/kit` | 3.1.1 | From ADR 0004 |
| `svelte` | 5.57.3 | From ADR 0004 |
| `vite` | 8.3.6 | From ADR 0004 |
| `@sveltejs/vite-plugin-svelte` | 7.3.1 | |
| `@sveltejs/adapter-static` | 4.0.1 | |
| `typescript` | 6.0.3 | 7.0.2 is newer, but `svelte-check` 4.7.8 only accepts `^5 \|\| ^6` |
| `@types/node` | 26.6.6 | Added during T020: `svelte-check` needs it for the browser-test helpers, and `tsconfig.json` names it because TypeScript 6 no longer includes ambient types on its own |
| `svelte-check` | 4.7.8 | |
| `vitest` | 5.1.0 | |
| `@playwright/test` | 1.65.0 | |
| `@axe-core/playwright` | 4.14.0 | |
| `prettier` | 3.9.10 | |
| `prettier-plugin-svelte` | 4.1.1 | |

Kit 3 needs `"extends": "$app/tsconfig"` plus explicit `include` and `exclude` in `tsconfig.json`. Tested: the older `./.svelte-kit/tsconfig.json` form fails the build.

### The `deepmerge` report

**Decision:** Record an exception, as ADR 0004 requires. The details are in the plan's Complexity Tracking section. `mise run audit` passes `--ignore=GHSA-gcqx-w48m-w2h4`, so a new report of any severity still fails the check.

**Rationale:** GHSA-gcqx-w48m-w2h4 covers `deepmerge` `>=4.2.0 <=4.3.1` and lists no patched version, and 4.3.1 is the newest release. `@sveltejs/vite-plugin-svelte` requires `^4.3.1` and calls it only to merge our own Vite plugin options at build time. A search of the build output found no `deepmerge` code.

**Alternatives rejected:** Raising the threshold with `--audit-level=moderate` would hide every future low report. Overriding to a version below 4.2.0 breaks the plugin's declared range. Dropping SvelteKit is a much larger change, made to remove a build-time, low-severity report.

### Go checks

**Decision:** `gofmt -l`, `go vet`, `staticcheck` 2026.2.2, `govulncheck` v1.9.0, and `go test -race`, with the two scanners installed through mise's `go:` backend.

**Rationale:** Tested: `staticcheck` and `govulncheck` work on Go 1.27.3. `go test -race` passes with concurrent writes through `modernc.org/sqlite`, with no pointer-check failures. That small test took 1.4 s under race and 0.4 s without, and its first race build took 82 s of mostly compile time, so CI needs a Go build cache.

### Go module contents

**Decision:** Slice 1's `go.mod` holds Go 1.27.3, `modernc.org/sqlite` v1.61.1, `x/crypto`, and `x/term`, plus `ignore ./frontend/node_modules`. It doesn't list `golang.org/x/net` v0.62.0 or `golang.org/x/text` v0.43.0.

**Rationale:** Tested: `go mod tidy` removes both, because nothing in this slice imports them. Issue #1 and ARCHITECTURE.md name them as the baseline, but those pins exist for the mail libraries. Slices 2 to 4 add them with those libraries, and `govulncheck` guards the versions then. The `ignore` directive stops `./...` from walking into npm packages that happen to contain `.go` files.

### Embedding the frontend

**Decision:** `frontend/embed.go` holds `//go:embed all:build`, and only `cmd/mailarkiv` imports it. `internal/web` takes an `fs.FS`, so its tests use a small in-memory tree and need no frontend build.

**Rationale:** `go:embed` can't reach outside its package directory and fails the build on a missing directory. That failure is loud and correct for a binary without its interface.

## Later slices

Recovery checks each blob with one indexed catalog lookup. Seeding and listing are measured above, but recovery at 5 million messages isn't. `serve` logs recovery's duration and counts, so the Slice 3 sizing test can read them. The spec already notes that this test doesn't list recovery, and it should.

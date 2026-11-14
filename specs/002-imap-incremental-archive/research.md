# Research: Archive an IMAP Mailbox Incrementally

Each section records one decision for Slice 2, why we made it, and what we rejected. "Tested" means we ran it on 2026-11-08 in a throwaway module on this host (Linux 6.8, Go 1.27.3, `go-imap/v2` v2.0.0-beta.8 with its in-memory server `imapmemserver`, `modernc.org/sqlite` v1.61.1). "From the source" means we read the library code at that version and didn't run it. No test ran against a real mail server. [Quickstart](quickstart.md) scenario 1 covers one.

The technical context in [plan.md](plan.md) has no open clarifications left.

## IMAP client

### Library and dependencies

**Decision:** Use `github.com/emersion/go-imap/v2` v2.0.0-beta.8, as [ADR 0003](../../docs/adr/0003-mail-libraries.md) adopted. It brings `github.com/emersion/go-message` v0.18.2 and `github.com/emersion/go-sasl` v0.0.0-20241020182733-b788ff22d5a6. The test double, `imapserver/imapmemserver`, ships in the same module.

**Rationale:** Tested: `go mod tidy` in a module importing `imapclient` and `imapmemserver` added only those two indirect modules. It needed neither `golang.org/x/net` nor `golang.org/x/text`, and `govulncheck` found no vulnerabilities. The `go.mod` line in [ARCHITECTURE.md](../../docs/ARCHITECTURE.md) expects both to arrive with this slice, so that line changes.

**Alternatives rejected:** v1 of the library last changed in 2023. Writing an IMAP parser ourselves puts untrusted input through code nobody else uses.

### Timeouts

**Decision:** Wrap the TCP connection, below TLS, in our own type with two phases.

1. **Connect:** one absolute deadline 30 seconds after the dial starts. It covers DNS, the TCP connect, the TLS handshake, and the greeting. For STARTTLS it also covers the plain greeting, the STARTTLS command, and the handshake after it.
2. **After connect:** while a command is outstanding, each `Read` sets a fresh 60 second deadline, and each `Write` does the same. With no command outstanding there is no read deadline, because the client's reader goroutine waits for server data at all times.

The wrapper ignores the deadlines the library sets. The first read error is kept and returned for every later read. The sync's context closes the connection when it ends, which is how pause, removal, and shutdown stop a sync. Timeouts are classified from the wrapper's own record of the error.

**Rationale:**

- From the source: the client sets its own deadlines, 30 seconds per response and 5 minutes per literal. Tested: against a server that sent a greeting and then nothing, `NOOP` was still waiting after 95 seconds. Those deadlines don't cover this case, so we need our own.
- The 5 minute literal deadline would also cut off a slow transfer that keeps sending data, which the spec says must finish. A 150 MB message at 200 KB/s takes 12.5 minutes.
- Tested: with the wrapper set to 2 seconds, `NOOP` ended after 2.0 seconds. Without the sticky error it took 8 seconds, because the decoder retries a read after `bufio` clears the error.
- Tested: the library wraps errors with `%v`, so `errors.As` can't find the `net.Error` timeout. The wrapper's record can.

**Alternatives rejected:** A context deadline per command can't tell a slow transfer from a stall. Relying on TCP keepalive takes minutes and doesn't detect a peer that is alive but silent.

### Bounding what the server sends

**Decision:** The wrapper counts the bytes read since the current command started. A body fetch for one message may read 150,000,000 bytes plus 1 MiB. Any other command may read 16 MiB. Past the bound the wrapper fails the connection, and the sync fails as Server error. The counter resets when a command starts, so bytes the server sends between commands count toward the next one.

**Rationale:** From the source: the client's decoder has no size limit, and it reads a whole text reply or folder list into memory. 16 MiB holds a `UID SEARCH` reply for about 2 million new messages at 8 bytes each, which covers the spec's 100,000 message first sync with room to spare.

**Alternatives rejected:** A limit on the count of folders or UIDs needs the library to expose a parse hook, which it doesn't.

### Secure connections

**Decision:** TLS 1.2 or later, with the system's trusted certificates, and `ServerName` set to the source's host. For implicit TLS, hand the TLS connection to `imapclient.New` and wait for the greeting. For STARTTLS, use `imapclient.NewStartTLS`. Any failure of the STARTTLS step other than a timeout is Secure connection failed. Nothing is sent after a failed handshake.

**Rationale:**

- Tested: with only the system's roots, a test certificate failed with `x509: certificate signed by unknown authority`, and no `LOGIN` was sent.
- Tested: against a server that doesn't offer STARTTLS, the library sends `STARTTLS` anyway, the server answers `NO`, and `NewStartTLS` returns an error before any `LOGIN`. That meets FR-019 without a capability check of our own.
- From the source: `NewStartTLS` refuses a `PREAUTH` greeting, which would otherwise skip the upgrade.
- Go reads `SSL_CERT_FILE` and `SSL_CERT_DIR` on Linux. An operator with a private issuer adds it to the host's store, and process tests point `SSL_CERT_FILE` at a test CA.

**Alternatives rejected:** Accepting a certificate on first use or pinning is out of scope (spec, Excluded).

### Sign-in results

**Decision:** Sign in with `LOGIN`. Classify a refusal by its response code:

| Server answer to `LOGIN` | Category | Retried |
| ------------------------ | -------- | ------- |
| `NO [UNAVAILABLE]` | Server error | Yes, with growing waits |
| Any other `NO`, with or without a code, including `AUTHENTICATIONFAILED`, `AUTHORIZATIONFAILED`, `EXPIRED`, and `CONTACTADMIN` | Sign-in rejected | No, until an administrator acts |
| `BAD`, or the connection closes | Server error | Yes |

**Rationale:** Tested: a wrong password got `NO` with code `AUTHENTICATIONFAILED`. A non-ASCII password was sent as a literal and accepted. RFC 5530 marks only `UNAVAILABLE` as temporary. Treating an unknown `NO` as a rejection protects the mailbox from a provider's lockout, and the spec accepts that an outage reported as a wrong password needs Sync now.

**Alternatives rejected:** `AUTHENTICATE PLAIN` does the same job, and every server we target accepts `LOGIN` after TLS. Retrying an unknown `NO` risks the lockout the spec guards against.

### Reading without changing the mailbox

**Decision:** Open folders with `EXAMINE` (`Select` with `ReadOnly: true`), fetch bodies with `BODY.PEEK[]`, and send no other command that can change a mailbox. A test wraps the double's session and fails on `SELECT`, `STORE`, `COPY`, `MOVE`, `EXPUNGE`, `APPEND`, `CREATE`, `DELETE`, `RENAME`, `SUBSCRIBE`, and `UNSUBSCRIBE`. It also compares every message's flags before and after a sync.

**Rationale:** Tested: after `EXAMINE` and `BODY.PEEK[]`, the message's flags stayed empty, so `\Seen` wasn't set.

**Alternatives rejected:** `CONDSTORE` and `QRESYNC` track flag and deletion changes. We record neither, so they add nothing.

### Finding and fetching new messages

**Decision:** For each chosen folder, in the order the folders were added:

1. `EXAMINE`. Compare `UIDVALIDITY` with the stored value. If `UIDNEXT` is at most the checkpoint plus one, skip to step 5.
2. `UID SEARCH UID <checkpoint+1>:*`, then drop UIDs at or below the checkpoint.
3. `UID FETCH` `UID RFC822.SIZE INTERNALDATE` in batches of 500 UIDs.
4. For each UID in rising order, `UID FETCH <uid> BODY.PEEK[]`, store it, and move the checkpoint.
5. Try that folder's messages waiting for retry.

[data-model.md](data-model.md#sync) has the full rules.

**Rationale:**

- Tested: `UID SEARCH UID 50:*` on a folder whose highest UID was 2 returned `[2]`. RFC 9051 says `n:*` always includes the highest UID, so the client must filter.
- Tested: the fetched literal streams, and its size is known before the first read. `RFC822.SIZE` equalled the literal's size on the double.
- Fetching metadata first lets a message over the limit be skipped without downloading it. One body per command keeps one literal in flight, so a timeout or a stop affects one message and memory stays flat.

**Alternatives rejected:** Fetching the body with the metadata sends the literal before we can refuse it. Pipelining several bodies would speed up small messages, but a stop would leave several partial transfers. A first sync of 100,000 messages over a link with 20 ms round trips spends about 35 minutes in round trips. That's an estimate, not a measurement, and the checkpoint lets it run across restarts.

### Messages the server doesn't return

**Decision:** When a `UID FETCH` returns no data, or a NIL body, for a UID that search found, send `UID SEARCH UID <uid>`.

- Still there: record it as waiting for retry, move the checkpoint, and continue. Message unavailable.
- Gone, and it wasn't waiting for retry: move the checkpoint with no record. It was deleted before a sync could read it.
- Gone, and it was waiting for retry: record it as not archived because it was removed. Removed before it was archived.

**Rationale:** Tested: a NIL body arrives as a nil literal. The second search costs one round trip, only on this rare path, and separates a deletion from a server failure, which the spec treats differently.

### Message size

**Decision:**

- `RFC822.SIZE` over 150,000,000: record as not archived because too large, move the checkpoint, and don't download.
- A literal announced over the limit: close the connection without reading it, record the message as waiting for retry, and fail the sync as Server error.
- A literal that ends early: capture removes its temp file and the sync fails. A timeout is Timed out, and anything else is Server error. The checkpoint stays before the message.
- A literal whose length differs from `RFC822.SIZE`: store it. The literal's length is what the server announced for this transfer.

**Rationale:** The spec's "length differs from what it announced" is the literal: IMAP's literal syntax announces the byte count. Refusing a mismatch with `RFC822.SIZE` would turn a server's sizing quirk into a message that never archives. Archive enforces the limit as well, as it does for imports.

### Folder names

**Decision:** Use the names the library returns, already decoded from modified UTF-7, and send them back through the library, which encodes them. Store the decoded name. A folder's identity is its source and that name. List folders with `LIST "" "*"`, skipping `\Noselect` and `\NonExistent`. Read counts with `LIST-STATUS` when the server offers it, and otherwise with `STATUS (MESSAGES)` for each folder.

**Rationale:** Tested: a non-ASCII folder name created on the double came back decoded in `LIST` and `STATUS`. `INBOX` is normalized to upper case. The double doesn't advertise `LIST-STATUS`, and the `STATUS` fallback worked. From the source: the decoder also accepts raw UTF-8 names, and rejects a name that isn't valid UTF-8, which fails the whole `LIST`.

**Remaining risk:** A server that sends folder names in a legacy encoding fails Connect as Server error, and the source can't be added. No server we know of does this, and none was tested.

## Capture and checkpoints

### Sharing the Slice 1 capture

**Decision:** Archive gains `StoreCollected`, which runs the same capture as `Import` with three differences. It records a collection entry instead of an import entry. It skips the check that the bytes look like an email, because collection keeps whatever the server returns. It records no failure event on the status screen for storage or catalog failures, because the source shows them. Restore events are still recorded, and an empty body is refused.

**Rationale:** FR-010 requires the Slice 1 rules, and one capture path keeps one set of crash tests. A failure counted on both the source and the status screen's failure list would count twice in the problem count.

**Alternatives rejected:** A second capture path in `collect` would need its own fsync order and crash matrix.

### Two commits per message

**Decision:** Archive commits the message and its entry. Then `collect` commits the checkpoint, plus any retry or not-archived record, in its own transaction. A crash between them collects the message again at the next sync. The capture then finds the bytes stored and the entry present, and adds nothing.

**Rationale:** Each module writes only its own tables (constitution II). The repeat is harmless because the entry is unique per folder and message, and archive reuses the stored blob. That is the idempotence ARCHITECTURE rule 2 asks for.

**Alternatives rejected:** One transaction across both modules would mean archive writes `collect`'s checkpoint, or `collect` writes `archive`'s tables.

### One history entry per folder

**Decision:** A partial unique index on `source_observation (imap_folder_id, message_id) WHERE imap_folder_id IS NOT NULL`, and an insert with `ON CONFLICT … DO NOTHING`.

**Rationale:** Tested: a second insert for the same folder and message added 0 rows, and repeated import entries for the same message were still accepted. A folder belongs to one source, so this gives at most one entry per source, folder, and message (FR-014). Two copies of the same bytes in one folder, a renumbered folder, and a folder removed and added again all reuse the folder's row and hit the same index.

### Counting collected messages

**Decision:** Archive keeps a counter per source in its own table. The capture transaction adds one when it records the first entry for that source and message. `source_observation` gains `imap_source_id` with an index, so that check stays inside archive's tables.

**Rationale:** Tested: `COUNT(DISTINCT message_id)` per source over 1,180,000 entries for 50 sources took 1.53 seconds. The sources screen refreshes while a sync runs, so that query would run every few seconds.

**Alternatives rejected:** Counting entries instead of messages counts the same message once per folder. A counter in `collect` would need archive to report whether the entry was the source's first, and a crash between the two commits would lose the increment.

### Widening `source_observation`

**Decision:** Migration 0002 rebuilds the table with SQLite's 12-step procedure. The migration runner sets `PRAGMA foreign_keys = OFF` before each migration's transaction, runs `PRAGMA foreign_key_check` inside it before the commit, rolls back if it returns a row, and sets `foreign_keys = ON` after.

**Rationale:**

- SQLite can't change a `CHECK` constraint or a column's `NOT NULL` in place.
- Tested with `PRAGMA defer_foreign_keys = ON` instead: the commit failed with `FOREIGN KEY constraint failed` (787). The `DROP TABLE` counts deferred violations that the rename never clears.
- Tested with the runner change: `foreign_key_check` returned 0 rows, the commit succeeded, and `foreign_keys` read back 1. `message.latest_observation_id` still pointed at the right rows, and a dangling reference was still refused afterward.

**Alternatives rejected:** Adding new columns without the rebuild leaves `source_type` limited to `manual_import` and `account_email` required. A second observation table for collections splits the history and the list query.

## Passwords and the key

### Encryption

**Decision:** AES-256-GCM from the standard library, through `cipher.NewGCMWithRandomNonce`. The additional data is `mailarkiv-imap-password-v1`, a zero byte, the lowercase host, a zero byte, and the lowercase user name. The stored value is the nonce, the ciphertext, and the tag.

**Rationale:**

- Tested: sealing a 6-byte password produced 34 bytes, 28 bytes of overhead. Opening failed with another key, and with another host in the additional data.
- Binding to host and user name means a ciphertext copied to another source's row can't be opened, so a password can only reach its own server and user (FR-028). Both are fixed after saving (FR-005), so the binding never has to change.
- A random 96-bit nonce is safe for far more than the few thousand encryptions an installation makes.
- The standard library adds no dependency.

**Alternatives rejected:** `x/crypto/nacl/secretbox` is in the `x/crypto` module we already pin, but GCM with a random nonce needs no package outside the standard library. A key derived from an administrator's password would stop collection after every restart until someone signs in.

### Key file

**Decision:** One line: `mailarkiv-key-v1:` followed by the 32 key bytes in standard padded base64, then a newline. `mailarkiv key create` writes it with `O_CREATE|O_EXCL` and mode 0600, fsyncs the file and its directory, and prints the path. `serve --key-file` refuses to start with cause `key` if the file is missing, unreadable, larger than 1 KiB, or not exactly that format.

**Rationale:** A text line can be pasted into a secret store, and the prefix makes a wrong file obvious. `O_EXCL` refuses to overwrite a key, which would make every stored password unreadable (FR-025). The server doesn't check the file's mode, because container secrets are often mounted readable by everyone in the container.

**Alternatives rejected:** A key in an environment variable leaks into process listings and crash reports. A raw 32-byte file can't be told apart from other binary files.

### Where the key may live

**Decision:** Both `key create` and `serve` make the data directory and the key path absolute and resolve symbolic links, then refuse a key path equal to or under the data directory. For a path that doesn't exist yet, they resolve its longest existing parent.

**Rationale:** A plain prefix check misses `./data/../data/key` and a symbolic link into the data directory. A hard link can't be detected without walking the directory, so the documentation tells operators to keep the key elsewhere.

### What the data directory still holds

**Decision:** Replacing or removing a password overwrites or clears the column. We don't turn on `PRAGMA secure_delete`.

**Rationale:** Old ciphertext can stay in free pages or WAL frames until SQLite reuses them. Reading it needs the key, which is the case the key already protects. `secure_delete` would add writes to every delete in the catalog for no gain.

## Scheduling

### One loop per source

**Decision:** Each active source runs one goroutine. It waits for its timer or a wake-up from Sync now, resume, a saved password, or an added folder, then runs one sync. The next sync is due 5 minutes after a success ends. After the nth failure in a row it's due after min(2^(n−1), 60) minutes. At start every active source syncs at once, after recovery. The due time lives in memory. The failure count, the start of the failures, and a rejected sign-in are stored.

**Rationale:** 50 goroutines cost nothing, and a stalled source can't hold up another (FR-023). Storing the failure count keeps the backoff growing across restarts. Storing the rejected sign-in keeps the source stopped after a restart (FR-022).

**Alternatives rejected:** A worker pool needs a size limit, and a full pool would let a stalled source delay others.

### Shutdown, pause, and removal

**Decision:** Each sync runs under its own context. Pause, removal, and shutdown cancel it, which closes the connection. A capture reading the literal then fails, and archive removes its temp file. A capture already committing finishes its commit, then the sync stops before the checkpoint commit. Shutdown cancels every sync while `http.Server.Shutdown` runs, inside the same 5 second grace. Removal waits for the sync to stop, then clears the password in one transaction.

**Rationale:** Closing the connection unblocks a read in progress at once. Leaving the checkpoint behind is safe, because the next sync collects that message again without a copy or a second entry.

## Interface and API

### Long requests for Connect

**Decision:** The two check routes, adding a source, and an edit that changes the port, security, or password run a sign-in. Each has a 120 second overall limit from the request's start, after which it ends as Timed out. The handler raises its write deadline to 130 seconds through `http.ResponseController`, and the client waits 150 seconds.

**Rationale:** A slow server can use 30 seconds to connect and 60 seconds for one silent step. The server's 30 second `WriteTimeout` would cut the answer off before the check could report why it failed.

### Untrusted text

**Decision:** Folder names, server replies, and source names go through the display rules Slice 1 uses for file names in `frontend/src/lib/format/file-name.ts`: control and bidirectional characters neutralized, and long text shortened in the middle. The rules move to a function for any text, and file names call it. Server replies are stored at most 1,000 characters long, keeping the start and the end.

**Rationale:** Three callers now share the rule (constitution I). Storing a bounded reply keeps a hostile server from filling the catalog.

### Field renames in the message API

**Decision:** `imported_at` becomes `observed_at` and `import_count` becomes `observation_count` in `GET /api/messages`. List items carry their latest entry in the same shape as a history entry. The frontend changes in the same commit.

**Rationale:** The old names would be wrong for collected messages. The API isn't published, so nobody else depends on the old names.

### Collection during development

**Decision:** Collection runs on normal server startup. The source routes are always registered, `key create` and `--key-file` appear in usage text, and Sources is part of the main navigation.

**Rationale:** MailArkiv is a greenfield application with no users yet. The owner chose direct access during development on 2026-11-13. The [plan's Complexity Tracking](plan.md#complexity-tracking) records the exception to constitution VI. Migration 0002 still has a test covering a Slice 1 catalog.

## Testing

### IMAP test double

**Decision:** A package `internal/imaptest` runs `imapmemserver` with a test CA made in memory at each run. It listens with implicit TLS or STARTTLS, holds synthetic messages, and wraps each session so a test can make any command stall, close the connection, return `NO` or `BAD` with given text, return a NIL body, change `UIDVALIDITY`, or send a reply over a size. `internal/imaptest/cmd/imaptestserver` runs it as a process for the browser tests.

**Rationale:** `imapserver.Options.NewSession` returns the `imapserver.Session` interface, so a wrapper sees every command before the in-memory server does. Plan research didn't build that wrapper: its stall tests used a raw TCP server that sends a greeting and then nothing. The double is part of the library we already depend on, so it adds no module. Constitution V asks for doubles for systems we don't control, and a mail server is one.

### Timeouts in tests

**Decision:** Sync tests set the connect and idle limits to a few hundred milliseconds through an unexported option. A unit test asserts the production values of 30 and 60 seconds.

**Rationale:** SC-005 checks behavior at each stall point. Ten stall points at the real 60 seconds would add ten minutes to every run.

### Scheduler tests

**Decision:** Test the per-source loop with `testing/synctest`, which runs the test's goroutines on a fake clock, and a fake sync function. Backoff, the 5 minute schedule, Sync now, pause, and a stalled source beside a healthy one run in microseconds of real time.

**Rationale:** The loop's logic is all timing. A synctest bubble can't contain real network I/O, so sync behavior stays in the double-backed tests.

### Process tests and certificates

**Decision:** Process tests start `imaptest` in the test process, write its CA to a file, and start the binary with `SSL_CERT_FILE` set to that file. The crash matrix uses the `faults` build as in Slice 1, adding points in `collect`.

**Rationale:** The binary trusts the test server the way an operator's host trusts a private issuer, so the tests exercise the documented path with no test-only code in the binary.

### Interoperability

**Decision:** Quickstart scenario 1 runs against a real Dovecot server. The operator guide lists only servers we actually ran.

**Rationale:** The double shares the library's own parser, so it can't catch a disagreement between the library and a real server.

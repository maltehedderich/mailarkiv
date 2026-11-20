# Research: Find Messages Quickly and Safely

Each section records one Slice 3 decision, the reason for it, and the alternatives we rejected. "Tested" means we ran it on 2026-11-15 on this host: Linux 7.0, 4 cores, 7 GiB of memory, Go 1.27.4. Browser tests used Playwright's Chromium 158.0.8127.18, and Rust builds ran in the `rust:alpine` image with cargo 1.100.0. "From the source" means we read the code at the stated version but didn't run it. The probes ran in throwaway directories outside the repository, on synthetic mail only.

The technical context in [plan.md](plan.md) has no open clarifications left. Four questions came up that the spec doesn't settle. Each is decided below and marked **Spec gap**, so `/speckit-analyze` can check them: a worker that crashes, a search of punctuation only, deep result pages, and an index that can't be opened.

## Processing

### Parser and its dependencies

**Decision:** Use `github.com/jhillyerd/enmime/v2` v2.5.0, as [ADR 0003](../../docs/adr/0003-mail-libraries.md) adopted. Pin `golang.org/x/net` v0.62.0, `golang.org/x/text` v0.43.0, and `golang.org/x/sys` v0.49.0 in `go.mod`. Use `x/net/html` directly for the preview rewrite and for reading the text out of HTML.

**Rationale:**

- Tested: a module importing enmime gained 20 modules. Among them are `inbucket/html2text`, `olekukonko/tablewriter` and its relatives, `fatih/color`, `goccy/go-json`, `cespare/xxhash`, `gogs/chardet`, `ssor/bom`, `cention-sany/utf7`, and `pkg/errors`. enmime imports `html2text` unconditionally, so all of them link into the binary even with the HTML-to-text option off.
- Tested: at enmime's own `x/net` v0.55.0, `govulncheck` reports GO-2026-6860. With the three pins above, it reports nothing.
- enmime collects errors per part in `Part.Errors` and keeps parsing, which the spec's "broken part doesn't stop the rest" needs (FR-004).

**Alternatives rejected:** `go-message` alone, already in `go.mod` through `go-imap`, has no charset fallback or per-part error list, so we would write the error handling enmime already has. A hand-written MIME parser runs untrusted input through code nobody else uses.

### Options

**Decision:** Parse with `SkipMalformedParts(true)`, `DisableTextConversion(true)`, `DisableCharacterDetection(true)`, and `MaxMIMEParts(1000)`.

**Rationale:** From the source: `DisableTextConversion` stops enmime from turning HTML into plain text when a message has no text part. We read text out of HTML ourselves, with the same tokenizer the preview uses, so the index and the preview agree. `DisableCharacterDetection` makes enmime trust a declared character set instead of guessing over it. An undeclared one is still guessed. `MaxMIMEParts` is a backstop only; the next section explains why.

### Part and nesting bounds

**Decision:** Before enmime runs, a boundary scanner of our own walks the multipart structure line by line. It counts parts in document order and tracks nesting depth. At the first part that would be the 1,001st, or that sits 21 levels deep, it cuts the input there and appends closing delimiters for every boundary still open. enmime parses the shortened copy. The stored original never changes. The message gets the problem "too many parts" or "nested too deeply" at the position of the first part left out. If the scanner reaches the end with a boundary still open, the message gets "message cut short".

The scanner reads only header blocks and delimiter lines. It doesn't descend into `message/rfc822` parts, because enmime treats an attached message as one part.

**Rationale:**

- Tested: `MaxMIMEParts` fails the whole parse with `TooManyPartsError` and returns no parts at all. The spec wants what was read before the bound to show and be searchable.
- Tested: enmime has no depth limit, and its time grows with the square of the depth: 63 ms at 200 levels, 183 ms at 400, 658 ms at 800. A run at 5,000 levels didn't finish within 300 s. Cutting at 20 levels removes that cost.
- Tested: a flat message of 1,200 parts parsed in 82 ms and allocated 46 MB. One of 12,000 parts took 540 ms and 230 MB before failing on the limit.
- Tested: enmime parsed a message that ends mid-part and reported 2 errors. Closing the open boundaries ourselves keeps a cut from adding structure errors that the original doesn't have.

**Alternatives rejected:**

- Parsing with the default limit of 10,000 parts and dropping parts after the 1,000th still pays the full parse, and nesting still costs quadratic time below that limit.
- Only the header fields for any message over the bound: simpler, but the spec's edge case asks for the parts before the bound.

### Time limit and isolation

**Decision:** Parse in worker processes. A worker is the server binary started with the hidden subcommand `parse-worker`. It reads requests on standard input and writes results on standard output, one message at a time. It opens no file and no network connection. The server keeps two pools, started with the server and restarted when a worker exits:

- **Background**, 2 workers: processing and the text the index needs.
- **Interactive**, 2 workers: the message view, the preview, and attachment downloads. A request waits at most 30 s for a free worker, then answers 503 `busy`.

The server kills a worker with `SIGKILL` when one request passes 60 s, and starts a new one. Each worker gets `Pdeathsig: SIGKILL`, so it dies with the server.

**Rationale:**

- Go can't stop a goroutine from outside. A parse that runs too long in the server would keep its CPU and memory after we gave up on it.
- A stack overflow, an out-of-memory error, or a concurrent map write ends a Go process, and `recover` can't catch them. In the server, any of these would stop capture. Rule 4 in [ARCHITECTURE.md](../../docs/ARCHITECTURE.md) says a broken part may fail processing but never a capture.
- Originals are up to 150,000,000 bytes (`archive.MaxSize`). A parse that allocates several times that should hit the kernel's limit in a worker, not in the server.
- Long-lived workers avoid a process start per message. Two passes over 5,000,000 messages (processing, then a rebuild) would start 10 million processes.
- The bounds above keep real mail well under 60 s. The limit is for whatever we haven't foreseen, and it must be testable. The pool takes the limit as a parameter, so tests run with 2 s, and a unit test pins 60 s.

**Spec gap: a worker that crashes.** FR-003 says Couldn't be read means "the header couldn't be read, or processing passed its time limit". A worker that dies on a message fits neither. We retry the message once in a fresh worker, so one bad moment doesn't mark it for good. If the second worker dies too, the message is Couldn't be read with the kind "malformed structure", and the log gets `processing_worker_stopped` with the message ID and exit status. It is the message's structure that broke the parser, and the closed list of kinds has no better fit.

**Alternatives rejected:**

- Parsing in the server with `recover`: it misses the fatal errors above, and it can't stop a parse at 60 s.
- A process per message: the start cost above.
- A Rust parser inside the search process: a second parser in a second language, and a parser crash would stop search too.

### Problem kinds

**Decision:** Map enmime's error names to the spec's kinds:

| enmime error name | Kind |
| ----------------- | ---- |
| Character Set Conversion, Character Set Declaration Mismatch | Unknown character set |
| Malformed Base64, Content Encoding | Invalid encoding |
| Malformed Header, Missing Content-Type | Malformed header |
| Missing Boundary, Malformed child part, Data contains boundary marker | Malformed structure |
| Any other name | Malformed structure |

Our own code adds message cut short, too many parts, nested too deeply, body cut at the size limit, and processing took too long. A header that enmime can't read at all makes the message Couldn't be read with "malformed header".

**Rationale:** Tested: an unknown character set gave one Character Set Conversion error and the text with replacement characters. A garbage header made `ReadParts` fail. From the source: each error's `Detail` can quote message bytes, so only `Name` is used. A unit test lists every name enmime v2.5.0 defines, so an upgrade that adds one fails the test instead of falling through unseen.

**Alternatives rejected:** Storing enmime's error text would put message content in the catalog's problem list, which FR-004 forbids.

### What the catalog keeps

**Decision:** The catalog keeps only what list rows, the date order, and the processing state need: the first sender, the count of further senders, the subject, the Date header's text, the list date and why it was chosen, whether the message has attachments, and the problems. The message view, the preview, attachment downloads, and indexing parse the original again in a worker. Bodies, recipients, and attachment lists aren't stored.

**Rationale:**

- Tested: a typical 43 KB message parses in 3.2 ms and allocates 0.3 MB, so parsing again when a message opens costs little.
- Bodies in SQLite would add up to 4 MB per message to the catalog and to every `VACUUM INTO` backup. The index already holds a copy of the text.
- Parsing is deterministic for a given original and enmime version, so a view shows what processing saw. Processing doesn't run again after an upgrade (spec, Excluded), so stored problems keep the version that found them.

**Alternatives rejected:** Storing every parsed field and the attachment list adds two tables and duplicates the original, for no visible behavior in this slice.

### List date and order

**Decision:** Add `first_archived_at`, `list_date`, `processing_state`, and `processed_seq` to `message`, owned by `archive`. Capture sets `first_archived_at` and `list_date` to the first entry's time. Processing replaces `list_date` with the Date header's time, unless the rule in FR-012 says otherwise. One index on `(list_date DESC, first_archived_at DESC, id DESC)` serves the list, and search sorts by the same three values. The Date header is read with `net/mail.ParseDate`.

**Rationale:** The order has to come from a table the list's owner owns, and an index can't sort by a value that lives in another table or needs `COALESCE`. Messages that aren't processed use their first archive time, so the column is never empty.

**Alternatives rejected:** A separate processing table with the date would need a cross-module join for every list page and a sort over 5,000,000 rows.

### Bodies and their text

**Decision:** The displayed bodies are the first `text/plain` and the first `text/html` part that aren't attachments. Each is decoded to UTF-8 and cut at 2,000,000 bytes, on a character boundary. The index gets the words of both. Text in HTML is read with the `x/net/html` tokenizer, skipping `script`, `style`, and `head`, with a space at each block element so words don't run together.

**Rationale:** A message with both bodies can carry words in only one of them. The index records only which documents contain a word, not positions or counts, so a word in both bodies costs nothing extra.

## Search process

### Tantivy version

**Decision:** Use Tantivy 0.27.0, not the 0.26.1 that ADR 0007 names. Build with `default-features = false` and only `mmap` and `lz4-compression`. Pin it with `=0.27.0` and commit `Cargo.lock`. ADR 0007 is amended in step 1.

**Rationale:**

- Checked on crates.io: 0.26.0 was released 2026-03-31, 0.26.1 on 2026-04-21, 0.26.2 on 2026-09-08, and 0.27.0 on 2026-10-17. 0.26.1 and 0.26.2 require `lru` 0.16, which has [RUSTSEC-2026-0253](https://osv.dev/vulnerability/RUSTSEC-2026-0253). 0.27.0 requires `lru` 0.18.2, which fixes it. ADR 0007 says 0.26.1 came out 2026-05-10 and that no release has the fix. Both statements were wrong when it was written.
- Tested: with only our two features and mimalloc 0.1.52, cargo resolves 113 crates, with `lru` 0.18.5. An OSV batch query of all 113 found no advisories. The same query for `lru` 0.16.4 returns RUSTSEC-2026-0253, so the query works.
- Tested: a probe that creates an index, adds a document, and commits built in 6 min 0 s on 3 cores with LTO, as a 3.8 MB binary, and ran.
- From the source: 0.27.0 keeps everything this plan uses: `IndexWriter::merge`, `garbage_collect_files`, `set_merge_policy`, `NoMergePolicy`, `LogMergePolicy::compute_merge_candidates`, `delete_term`, `RangeQuery::new`, `TopDocs::order_by` with a tuple of sort keys, and the `Collector::collect_segment` hook. Its breaking changes (`set_fast` takes a tokenizer name, segment plugins, `FieldType::TieBreaker`) touch only how the schema is written.
- No principle IV exception is needed, which removes the one ADR 0007 expected.

**Alternatives rejected:** 0.26.1 with the exception: it ships a known vulnerable crate when a release without it exists. The ADR's benchmark ran 0.26.1, so its numbers may not carry over, but the sizing test measures 0.27.0 with our own code either way.

### Why a separate process

**Decision:** Keep the search process that ADR 0007 decided. Constitution principle II asks the plan to justify it against its operational cost.

**Rationale:** Tantivy is Rust. In the server through cgo, it would end the static `CGO_ENABLED=0` build that ADR 0002 depends on, and a crash in Rust or C would stop capture, which User Story 3 forbids. A separate process limits a crash to search, and the server restarts it. The cost:

| Cost | How the plan bounds it |
| ---- | ---------------------- |
| A second toolchain: Rust, the musl target, a C compiler for mimalloc, `cargo-audit` | mise installs Rust and `cargo-audit` at pinned versions. CI installs `musl-tools`. `AGENTS.md` lists the commands |
| A second file to ship and start | It sits next to the server binary, and the server starts it. Operators install nothing else |
| A protocol between two languages | One contract, [search-process.md](contracts/search-process.md), tested from both sides |
| Supervision: restarts, backoff, crash counting | One Go type in `search`, tested by killing the real process |
| 113 crates | The `cargo audit` check |

**Alternatives rejected:** See ADR 0007: Bleve, SQLite FTS5, Tantivy through cgo, and a search server.

### Connection

**Decision:** The server starts the search process with pipes on standard input and output, and talks over them. There is no socket file and no port.

**Rationale:** FR-043 says only the server's host account may connect. A pipe has no address, so no other process can connect to it. Another process could only reach it through `/proc/<pid>/fd`, which needs the same account or `ptrace` rights, and those can already read the index files. A Unix socket would need a path, its permissions, and cleanup after a crash, all for one client. ARCHITECTURE.md's "listens only on a Unix socket" changes in step 1.

**Alternatives rejected:** A Unix socket in the data directory: more to get wrong, no gain. A socket pair passed as an extra file descriptor: the same as pipes with more setup.

### Frames

**Decision:** Each frame is a 4-byte big-endian length followed by that many bytes of JSON, at most 64 MiB. Requests carry an ID, so searches and index batches can be in flight together. [The contract](contracts/search-process.md) has the details.

**Rationale:** `serde` and `serde_json` are already in the 113 crates, because Tantivy writes `meta.json` with them. Go has `encoding/json`. A batch of 500 messages with two 2,000,000-byte bodies each would pass 64 MiB, so batches are bounded by bytes as well as count.

**Alternatives rejected:** Protocol Buffers or gRPC add code generation and crates on both sides for a contract with seven operations.

### Where the program lives

**Decision:** Build it as `bin/mailarkiv-search`, a static musl binary, and ship it next to `bin/mailarkiv`. The server looks for it in the directory of its own executable. `mise run dev:server` changes from `go run` to building `bin/mailarkiv` first, so the program is beside it. Go tests find it at `bin/mailarkiv-search` in the repository and fail with "run mise run search:build" when it is missing.

**Rationale:** Inside the server binary, it would have to be written to disk before it could run, which needs a writable executable location and cleanup. A second file is how the release ships it anyway in Slice 6. Finding it next to the server needs no new option.

**Alternatives rejected:** A `--search-program` option serves only development. Embedding and extracting the binary at start: see above.

### Index maintenance

**Decision:** Set `NoMergePolicy` on the writer. The server asks for maintenance after 50 commits or 10 minutes, whichever comes first. The search process computes candidates with `LogMergePolicy::compute_merge_candidates`, runs `IndexWriter::merge` for each, waits for each result, runs `garbage_collect_files`, and returns what failed. Three failed runs in a row mark the index Needs rebuild (FR-040).

**Rationale:**

- From the source, 0.26.1 and 0.27.0 alike: automatic merges start in `consider_merge_options` after each commit and after each finished merge. A failed merge only logs `warn!("Merge of {:?} was cancelled: {:?}")` and drops the operation. The segments stay, so the next commit chooses them again. Automatic merges therefore retry after a failure, which answers the question ADR 0007 left open. But their failures reach us only as log text, which can't be counted reliably and would have to be parsed.
- With merges started by request, every failure comes back as a result, so "3 in a row" is a count the server keeps. The Rust test `maintenance_reports_failed_merge_and_retries` damages a segment, checks that maintenance reports the failure, and checks that the next run tries the same segments again.
- `garbage_collect_files` after each run removes the files a failed merge leaves behind, which ADR 0007 measured at 375 to 436 MiB against 244 MiB live.

**Alternatives rejected:** Keeping automatic merges and catching their warnings in the log facade: the warning's text is the only signal, and its format can change in any release.

### Exactly one entry per message

**Decision:** Processing gives each message a `processed_seq` in the same transaction that records its result. The numbers have no gaps, because the catalog has one writer. Each index generation has a cursor, the last `processed_seq` it saved. The indexer takes the next messages after the cursor, sends them in batches, and asks for a commit. The search process deletes each message's ID before adding its document. Once the commit returns, the server records the new cursor.

**Rationale:**

- A crash after the commit and before the cursor is recorded sends the same batch again. The delete first means it replaces the entries instead of adding a second one (FR-034).
- From the source: Tantivy applies a delete to documents added before it, in opstamp order, so a delete and an add of the same ID in one commit leave exactly the new document.
- Messages waiting to be indexed are the highest `processed_seq` minus the cursor. No count query is needed.
- A rebuild is the same loop on a new generation whose cursor starts at 0. When it reaches the highest `processed_seq`, it has caught up, and the server swaps generations in one SQLite transaction.

**Alternatives rejected:** An "indexed" flag on each message can't say which generation holds it, so a rebuild would have to clear 5,000,000 flags first.

### Matching

**Decision:**

| Field in the index | Content | Analysis |
| ------------------ | ------- | -------- |
| `all` | Subject, sender and recipient names and addresses, both bodies, attachment names | Words |
| `subject`, `from`, `to`, `attachment` | One each. `to` holds To, Cc, and Bcc | Words |
| `from_address`, `to_address` | Each complete address | Lowercased, whole |
| `from_domain`, `to_domain` | The part of each address after the last `@` | Lowercased, whole |
| `id` | Message ID | Number, for deletes and the last sort key |
| `list_date`, `first_archived_at` | Unix milliseconds | Numbers, for the date range and sorting |
| `has_attachments` | 0 or 1 | Number |

"Words" means Tantivy's `SimpleTokenizer`, then a filter that drops words over 100 bytes, then `LowerCaser`. Words fields record only which documents contain a word. The search process applies the same analysis to what the administrator typed. A word of two or more characters matches every indexed word that starts with it, through a `RangeQuery` from the word up to the first term after all words with that prefix. A one-character word matches only itself. Every word and filter must match.

A From or To value with an `@` and text on both sides is a complete address. A value without spaces that contains a dot, or starts with `@`, is a domain. Anything else is words in that field. Results sort by `list_date`, `first_archived_at`, and `id`, all newest first, with `TopDocs::order_by` and a tuple of three sort keys.

**Rationale:**

- From the source: `SimpleTokenizer` splits at every character that isn't alphanumeric, and `LowerCaser` lowercases Unicode. So "MÜLLER" becomes "müller", and "muller" stays a different word (FR-015).
- From the source: `PhrasePrefixQuery` stops at `DEFAULT_MAX_EXPANSIONS = 50` and silently drops further matches. A `RangeQuery` on a text field without fast values uses `InvertedIndexRangeWeight`, which has no such limit.
- Recording only documents keeps the index smaller. Phrases aren't searchable (spec, Excluded), so positions have no use.
- Words over 100 bytes in mail are mostly encoded data. From the source: Tantivy drops a word over 65,530 bytes and logs a warning, so some limit applies anyway, and ours keeps those warnings from happening. German compounds stay well under 100 bytes. The operator guide states the limit.

**Spec gap: punctuation only.** A search whose words field has no letters or digits and no filter counts as an empty search and shows the full list (FR-018). The search process reports that the words gave no terms, and the server answers from the catalog.

**Alternatives rejected:** Matching a domain as a suffix, so `example.com` also finds `mail.example.com`: FR-016 says "addresses at that domain", and a suffix match would let `attacker-example.com` lookalikes hide in results for `example.com`. It can be added later without a rebuild.

### Search time limit

**Decision:** The search process stops a search after 10 s. A collector of our own overrides `collect_segment`: it checks the deadline before each segment, drives the segment's scorer itself, and checks again every 4,096 documents. It passes documents to the `TopDocs` and `Count` collectors underneath. The server's limit for the call is 12 s, so the search process normally answers `timed_out` first.

**Rationale:** From the source: Tantivy has no query timeout, and its default `collect_segment` runs `Weight::for_each`, whose callback can't stop the loop. `Weight::scorer` returns a `DocSet` we can advance ourselves. Building one segment's document set for a short prefix still runs to the end once started, so a search can overrun 10 s by one segment's setup. The server's 12 s limit covers that.

**Alternatives rejected:** Running the search on a thread and abandoning it after 10 s: Rust can't stop a thread either, so the work would go on.

### Deep result pages

**Decision:** Search answers pages 1 to 2,000, the first 100,000 results. A later page answers 400 `page_too_deep`, and the total still counts every match.

**Spec gap:** FR-018 sets no limit. From the source: `TopDocs` with an offset keeps offset plus 50 entries in a heap, so page 100,000 of a 5,000,000 match search would hold 5,000,000 entries of 32 bytes, at least 160 MB, against a 1 GiB target. The pager offers only Previous and Next, so reaching page 2,001 takes 2,000 clicks. The plain list, which SQLite serves, keeps no limit.

**Alternatives rejected:** Paging by the last result's sort key avoids the heap, but it can't jump to a page number, and the Back behavior in User Story 1 restores a page number.

### Keeping mail out of what the search process reports

**Decision:**

- Errors cross the protocol as closed codes, never as Tantivy's error text, which can contain terms and paths.
- A panic hook prints one line, `mailarkiv-search panic at <file>:<line>`, and the process aborts. The panic message isn't printed.
- The `log` facade passes on only warnings and errors, as `{"kind":"log","level":"warn","target":"<module path>"}`, without the message text.
- The server reads the program's standard error and logs only lines that match the panic line's exact pattern. It counts other lines and logs the count.

**Rationale:** FR-048 forbids mail content in the log and on the status screen, "including in anything the search process reports". A Tantivy error or a panic message can quote a term from an indexed document. Allowing only fixed shapes makes the guarantee checkable: the Go marker test and the Rust test `reports_no_text` send marker strings through every operation and every forced failure, then search everything the program wrote.

**Alternatives rejected:** Scrubbing known values from free text: the program doesn't know which values are mail.

### Supervision

**Decision:** The server starts the search process at startup, after recovery. If it exits, the server starts it again after 1 s, doubling the wait each time up to 60 s, and back to 1 s once it has run for 10 minutes (FR-039). An exit, an abort, or a kill counts as a crash. A failed start, such as a missing program or a wrong protocol version in its first frame, counts as Search process didn't start and not as a crash. Five crashes within 30 minutes mark the index Needs rebuild. Consecutive failures of one category, with no success between them, become one failure event with a count and the last time.

**Rationale:** `Pdeathsig: SIGKILL` and an exit when standard input closes keep an orphaned search process from holding the index after a server crash. Merging repeats keeps the event list bounded when the program fails every minute for days, while the problem count still adds every failure (FR-047). The event list in `opstatus` is otherwise unbounded, and its comment says events come only from signed-in actions and startup, which stops being true in this slice. The comment changes with it.

**Alternatives rejected:** A fixed cap on stored events drops the oldest failures, which may be the cause.

**Spec gap: an index that can't be opened.** FR-040 names crashes and maintenance failures as the causes of Needs rebuild. When the search process reports that the active index's files exist but can't be opened, we mark Needs rebuild too, with the cause `index_damaged`, and record Index needs rebuild. Building a new index automatically would treat a damaged index like a missing one, but the spec's assumption "Automatic build, manual rebuild" leaves that choice to an administrator. If the damage crashes the search process at every start, the rebuild can't run either. The operator guide then says to stop the server and delete the index, which leads to an automatic build. The plan lists this under remaining risks.

### Free disk space

**Decision:** The search module reads free space with `statfs` on `<data-dir>/index` before each batch and every 30 s. Below 2 GiB, indexing and rebuilding pause. They resume at 3 GiB. A rebuild starts only when free space exceeds 2 GiB plus the size of the active generation's directory.

**Rationale:** The index lives inside the data directory, so the space it uses is the space capture needs. `statfs` costs microseconds. ADR 0007 measured 5.6 GiB at 5,000,000 messages, so a rebuild needs about 8 GiB free at that size.

### Toolchain

**Decision:** Add to `mise.toml`: `rust = { version = "1.100.0", targets = "x86_64-unknown-linux-musl", profile = "minimal", components = "rustfmt,clippy" }` and `"cargo:cargo-audit" = "0.23.0"`. mimalloc's C code builds with `musl-gcc`, from the `musl-tools` package, which CI installs and `mise run setup` checks for.

**Rationale:** Looked up in the mise documentation through Context7: the Rust tool takes `targets`, `profile`, and `components`, and runs rustup with its own `RUSTUP_HOME` and `CARGO_HOME`. The `cargo:` backend installs pinned Rust tools. Tested: cargo 1.100.0 is the current stable in `rust:alpine`, and Tantivy 0.27.0 needs 1.88 or later. This host has `gcc` but not `musl-gcc`, so a developer installs `musl-tools` once with `sudo`. The quickstart says so.

**Alternatives rejected:** Building in a container: CI and developers would need Docker for every check. Using `cross`: another tool to pin for one target.

## Preview

### Isolation

**Decision:** Serve the preview from `GET /api/messages/{id}/html` with this policy and nothing else from the global middleware's policy:

```text
Content-Security-Policy: sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'
Referrer-Policy: no-referrer
X-DNS-Prefetch-Control: off
```

The page shows it in `<iframe sandbox referrerpolicy="no-referrer">`, with no `allow-` tokens.

**Rationale:** Tested in Chromium with a hostile message served this way:

| Attempt | Result |
| ------- | ------ |
| Remote style sheets, `@import`, fonts, images, CSS backgrounds, SVG `<image>`, video posters, prefetch, preload, iframes, objects, embeds, pings | Blocked by the policy. No request left the browser |
| Scripts and event handlers | Didn't run |
| Meta refresh, form submit, `target=_top`, `target=_blank` | Nothing sent |
| Preconnect, `dns-prefetch`, link DNS prefetch, meta refresh, checked in Chromium's network log | No DNS lookup |
| Clicking `<a href>` with the default target or `_self`, and an SVG `<a href>` | **The frame navigated, and a DNS lookup happened** |
| `<noscript>` content | Parsed as markup, because the sandbox disables scripting |
| The frame's request | Carried the `__Host-` session cookie (`SameSite=Strict`, `Sec-Fetch-Site: same-origin`) |
| The preview's address opened in a tab | Same isolation. Its script didn't run |

The policy blocks everything except link navigation, so the server must also rewrite links (next section). Playwright's `request` event fires even for requests the policy blocks. The probe used `requestfailed` and its `errorText`, where `csp` means blocked and `net::ERR_NAME_NOT_RESOLVED` means a request went out. The end-to-end tests do the same.

**Alternatives rejected:** An HTML sanitizer library: ARCHITECTURE.md decides against one, and the probe shows the policy carries the isolation.

### HTML rewrite

**Decision:** The worker parses the HTML body with `x/net/html` and `ParseOptionEnableScripting(false)`, changes the tree, and renders it again:

- Replaces each `cid:` reference in an `img` `src` with a `data:` URL of the embedded image, when the next section allows it, and removes the `src` otherwise.
- Removes `href` from `a` and `area`, and `href` and `xlink:href` from SVG `a`.
- Removes every `link`, `meta`, and `base` element.
- Removes every `ping` and `referrerpolicy` attribute.

**Rationale:** The link rewrite closes the one leak the probe found. Parsing with scripting off matches how the sandboxed frame parses `<noscript>`, so links hidden there are rewritten too. `link` and `meta` removal and the attribute removals matter after images load, as the next sections show. The policy stays the main barrier: a rewrite bug can leave a link that navigates the frame, which the end-to-end tests check for, but it can't make the policy allow a request.

### Embedded images

**Decision:** An embedded image is an `image/*` part whose `Content-ID` the HTML body references through `cid:`. The worker decodes it and checks its first bytes. Only PNG, JPEG, GIF, and WebP signatures become `data:` URLs, typed by the signature, not the declared type. Images go in document order, and an image that would bring the decoded bytes shown past 10,000,000 is left out. An image shown twice counts once. A part with an encoding error isn't shown. Every embedded image stays in the attachment list, and none sets the attachment mark.

**Rationale:** The declared type is the sender's claim. A part declared `image/png` that holds SVG would otherwise become a `data:image/png` URL holding markup. The browser wouldn't run it as an image, but checking the signature makes the format rule in FR-027 hold by construction.

### Images from the internet

**Decision:** With `?images=1`, the policy's `img-src` becomes `data: https:`, and `upgrade-insecure-requests` is added. Nothing else changes. The view reports whether the HTML references remote images, so the page can show the notice before the frame loads.

**Rationale:** Tested in Chromium with a local image host:

- An `http:` image was requested over `https:`.
- The image host received no `Referer` and no cookie, from the frame and from the preview opened in a tab, even with `<meta name="referrer" content="unsafe-url">` and `referrerpolicy="unsafe-url"` in the message.
- **`<link rel=prefetch>` still went out** after images were allowed, as a request with `Sec-Fetch-Dest: empty`. Removing `link` elements in the rewrite closes this.

**Alternatives rejected:** Fetching images through the server: the spec excludes it, because the server would fetch any address found in untrusted mail.

### Attachment downloads

**Decision:** Send decoded bytes with `Content-Type: application/octet-stream`, `Content-Disposition: attachment` with the cleaned name through `mime.FormatMediaType`, so non-ASCII names get `filename*`, and `Content-Security-Policy: sandbox; default-src 'none'; frame-ancestors 'none'`.

**Rationale:** `nosniff` from the middleware, the generic type, and `attachment` together make every browser save the file. The sandbox policy covers a browser that ignores them, and `frame-ancestors 'none'` stops other sites from framing it (FR-042).

## Search requests

**Decision:** The interface sends a search as `POST /api/search` with a JSON body. It keeps the search, the page, and the last results in a SvelteKit snapshot, which the browser restores on Back. Dates go to the server as instants that the browser computed from midnight in its own time zone.

**Rationale:** FR-022 keeps search words out of page and request addresses. A `GET` would put them in the query string, where reverse proxies and browser history record them. The snapshot restores the same results without asking again (User Story 1 scenario 11). The browser knows the administrator's time zone, so the server needs no time zone database.

**Alternatives rejected:** Words in the address fragment stay out of request logs, but they reach browser history.

## Sizing test

**Decision:** `mise run sizing` runs `go run ./internal/sizing/cmd/sizing` against a fresh data directory, outside `mise run check`. It:

1. Generates 5,000,000 synthetic messages from a fixed seed, with German and English words, about 8 KB each on average, and a small attachment on one in ten. It imports them through `archive.Import`.
2. Runs the real processing and search modules and the shipped search program until the index is up to date.
3. Runs a fixed set of newest-first searches, with and without prefix words, and records the median and 95th percentile.
4. Starts a rebuild and times it.
5. Samples `RssAnon` of the search process from `/proc` every 100 ms, which counts allocated memory and leaves out mapped index files.
6. Records the index directory's size, and writes the results to `docs/research/search-sizing/README.md`.

**Rationale:** FR-049 asks for our own code and the shipped program. This host has 114 GB free, and an 8 KB average keeps the originals near 40 GB. Attachment contents aren't indexed, so their size doesn't matter. ADR 0007 reports allocated memory apart from mapped index files, and `RssAnon` draws the same line.

**Alternatives rejected:** Writing catalog rows and blobs directly would be faster to set up, but it would skip the code under test.

## Delivery without a flag

**Decision:** Processing and search run on normal startup while the slice is built. This is the same greenfield exception to principle VI as Slice 2, recorded in the plan's Complexity Tracking.

**Rationale:** The owner confirmed on 2026-11-13 that MailArkiv has no users yet. A flag would protect nobody and add a code path to test.

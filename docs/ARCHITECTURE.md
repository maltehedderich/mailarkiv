# Architecture principles

The [engineering constitution](../.specify/memory/constitution.md) governs these choices. This document applies it to email capture, storage, and recovery.

## Shape of the system

Use a **Go modular monolith** that runs an HTTP API, ingestion connectors, and background jobs. Compile the SvelteKit frontend to static assets and serve it from the same Go application. Start with **SQLite for metadata and job state**, a Tantivy search index in a helper process that the server starts, and raw message files on local persistent storage.

No required Node.js process at runtime, no required Redis/message broker, and no required external search service. Operators install no search service, because the release includes the helper. Single-node operation is a deliberate v1 constraint.

## Conceptual components

| Responsibility      | What it owns                                                                    |
| ------------------- | ------------------------------------------------------------------------------- |
| Source adapters     | SMTP journal intake, IMAP synchronization, manual imports                       |
| Durable capture     | Safely persisting the original message and recording the ingestion attempt      |
| Catalog             | Message metadata, source observations, and processing state                     |
| Processing          | MIME parsing, attachment extraction, derived metadata, bounded background work  |
| Search              | Indexing and queries; an entirely rebuildable projection of catalog and content |
| Retrieval, deletion | Viewing, original-byte export, integrity verification, permanent deletion       |
| Policy and identity | Authentication, authorization, audit trail                                      |

Introduce these boundaries as the slices need them; the first slice may use a simpler structure. Keep presentation, business operations, and data access distinct within each module.

Each module exposes a declared interface and keeps its implementation private. Access another module's behavior and data through that interface. Every table has exactly one owning module, and only that module writes to it; record ownership when the table is introduced. HTTP, ingestion, CLI, and job adapters reuse the owning module's business operations and authorization rules.

## Non-negotiable data model concepts

- **Raw message:** The exact RFC 5322/MIME bytes as received, with a content hash. Do not rewrite the source file to normalize headers or encodings.
- **Source observation:** Where and when that message was seen (journal envelope/recipients, mailbox and source ID, or import). One raw blob may have multiple observations.
- **Derived content:** Parsed fields, extracted text, previews, and search documents. Safe to regenerate.
- **Processing status:** Explicit, typed capture and processing states with defined transitions. Record retry attempts separately so retry history does not obscure the current state.

`Message-ID` is useful for display and correlation, **not** a reliable unique identifier. Deduplicate identical raw bytes without discarding distinct source observations or their source context. Any future cross-organization deduplication needs explicit isolation analysis.

## Reliability rules

1. **SMTP:** Acknowledge with `250` only after the raw message is durably persisted. Temporary storage failure returns a transient SMTP error so the upstream can retry.
2. **IMAP:** Persist the message before advancing the durable checkpoint. Reconnects and retries must be idempotent.
3. **Crash consistency:** File storage and SQLite are not one atomic transaction, so capture always follows this order: write a temp file on the same filesystem, fsync it, rename it to its content-addressed path, fsync the directory, commit to SQLite, then acknowledge. A crash leaves at most an unreferenced blob, and a retry of the same bytes reuses it. Startup recovery removes temp files and never deletes a blob; the integrity report lists unreferenced ones. SQLite runs in WAL mode with `synchronous=FULL` and one writer connection, because with `NORMAL` a commit can roll back after power loss and break rule 1.
4. **Parsing failures:** A broken MIME part or attachment can fail processing but cannot remove an already captured raw message.
5. **Index failures:** Search is never the archive's source of truth. Index data is versioned and rebuildable; the UI makes lag/failure visible.
6. **Verification:** Compare stored content hashes with the catalog and expose any missing/corrupt message, unreferenced blob, or inconsistent index state.
7. **No overclaiming:** Hashes alone do not provide immutable/WORM compliance, and mailbox polling cannot guarantee capture of messages deleted before synchronization.
8. **Network and jobs:** Every network call has a timeout. Define failure, retry, and recovery behavior at network and asynchronous boundaries. Retried operations must be idempotent wherever duplicates would be harmful.
9. **Deletion:** An administrator can permanently delete a message. One SQLite transaction removes all its source observations, records the pending removal, and writes an audit entry holding no message content. The index document and blob go next, and an interrupted delete resumes at startup. Backups keep their copy until rotation expires it, and a source that delivers the same bytes again archives them again.
10. **Backup:** Snapshot SQLite with `VACUUM INTO`, then copy the blobs. Skip the index and rebuild it after restore. The snapshot can't reference a blob added later, and verification after restore catches a delete that ran during the backup.
11. **Export:** Export a ZIP of byte-exact `.eml` files with a manifest of SHA-256 hashes and source metadata. mbox escapes `From ` lines, so it can't keep the bytes exact.

## Search index

[ADR 0007](adr/0007-tantivy-search-process.md) records this decision and [its measurements](research/search-benchmark/README.md). The batch, rebuild, and disk rules come from the production failures in [ADR 0001](adr/0001-embedded-bleve-for-search.md).

- Run Tantivy in a Rust helper process that the Go server starts and supervises. The helper listens only on a Unix socket that only the server's user can open. The server enforces authorization before every call and gives each call a timeout.
- A helper crash stops search and indexing, not capture. The server restarts the helper with a backoff and shows search as unavailable. After repeated crashes, mark the index failed and rebuild it. Releases still ship a systemd unit and a container restart policy for the server itself.
- Background merges report failure only through a `log` warning. The helper captures its log output and panics and sends them to operator status. After repeated errors, mark the index failed and rebuild it.
- Index in bounded batches. Mark a message indexed, for that index version, only after the helper confirms `commit` returned.
- Tantivy has no primary key, so `add_document` alone creates a duplicate. In each batch, delete a document's ID before adding it, so re-indexing is harmless and a rebuild can resume from the marks.
- Keep one writer open for the helper's lifetime, and run `garbage_collect_files` after a failed merge, which can leave unreferenced files behind.
- Rebuild in a new directory and swap when done, so the old index keeps serving.
- Stop indexing when free disk space falls below a set threshold, so the index can't use up the space capture needs.
- For rule 6, verification runs Tantivy's `validate_checksum`, which took 17 s at 5 million messages.
- Deleting a document hides it from search after the next commit, but its text stays in segment files until a merge rewrites them. The slice that adds deletion decides when that merge runs. A full merge at 5 million messages took 8 minutes and wrote 5.4 GiB.
- Index message text, sender, recipients, date, subject, and attachment filenames. Do not extract attachment contents.
- Analyze text with lowercasing only, without stemming or stop words, and query with prefix matching. Mail mixes languages, and a wrong choice costs one rebuild.
- Run a sizing test on synthetic mail at the target in the [product scope](PRODUCT.md), with our own code and the helper's socket. Record query time, rebuild time, peak memory, and index size, and publish the supported size from the result. Proposed targets for a host with 4 cores and 8 GiB of memory are p95 under 1 second for newest-first searches, prefix terms included, a full rebuild within one night, and under 1 GiB allocated by search.

## Technology direction

| Area            | Starting choice                                                                                            | Revisit when                                                |
| --------------- | ---------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------- |
| HTTP/API        | Go `net/http`                                                                                              | Routing or client-contract needs justify additional tooling |
| UI              | SvelteKit + TypeScript, static output                                                                      | Dedicated SSR service only with a proven need               |
| Metadata/jobs   | SQLite via `modernc.org/sqlite`, with migrations and durable job claims                                    | PostgreSQL if true multi-node operation is needed           |
| Search          | Tantivy 0.26.1 in a supervised Rust helper process                                                         | Tantivy misses measured search, memory, or rebuild targets  |
| Raw storage     | Local filesystem, SHA-256 content-addressed blobs at `blobs/ab/cd/<hash>`, plain files without compression | Users require object storage or retention guarantees        |
| Email protocols | `go-smtp`, `go-imap/v2`, `enmime/v2`                                                                       | A current source requires another protocol or provider      |

ADRs record each adopted dependency and its version: [Tantivy](adr/0007-tantivy-search-process.md), [SQLite driver](adr/0002-modernc-sqlite-driver.md), [mail libraries](adr/0003-mail-libraries.md), [interface fonts and icons](adr/0004-interface-fonts-and-icons.md), [password hashing](adr/0005-password-hashing.md), [frontend and task tooling](adr/0006-frontend-and-task-tooling.md). `go.mod` starts on Go 1.27.3. `go-imap` v2.0.0-beta.8 brings `go-message` v0.18.2 and `go-sasl` and needs neither `golang.org/x/net` nor `golang.org/x/text`, so `x/net` v0.62.0 and `x/text` v0.43.0 arrive with Slices 3 and 7, if at all. On 2026-11-06, `govulncheck` found no vulnerabilities in a test module importing these libraries at these versions. It reported known vulnerabilities on Go 1.27.0 and the older `x/net` and `x/text` versions the libraries pull in by default.

Keep module contracts clear. Generalized interfaces, options, and extension points must serve at least two current callers or variants. A possible future storage backend or provider is not a reason to add one now.

## Security and privacy from the first slice

- Require authentication for archived content. Enforce authorization server-side on every operation and deny by default, including direct API requests. Limit administration and source ingestion separately, and give credentials only the permissions they need.
- The first release has local administrator accounts only, and each one sees the whole archive. Source observations already record the source, so per-source access can come later with an index rebuild.
- Parse untrusted API and protocol input into precise types at the boundary. Treat email headers, HTML, attachments, and filenames as untrusted content. Preserve raw bytes even when content parsing fails.
- Serve HTML previews from their own endpoint with CSP `sandbox` and `default-src 'none'`, and show them in a sandboxed iframe. The browser then blocks scripts and remote content, so the first release needs no HTML sanitizer library. Any CSP exception, such as for inline styles, must still block remote loads.
- Limit SMTP acceptance to configured upstreams/authentication, and cap message size/concurrency.
- Keep secrets and personal data out of the repository, logs, error reports, and analytics. This includes message bodies, subjects, addresses, and attachment filenames containing personal data. Use synthetic mail fixtures and diagnostics that explain failures without copying private content.
- Disk encryption is the operator's job, and the documentation says so. The app encrypts only IMAP passwords, with a key kept outside the data directory, so a stolen backup alone doesn't expose them. Operators back up that key separately.

## Decision rules

- Prefer the standard library, platform, and existing dependencies. Add a dependency only when it replaces substantial or hard-to-get-right code. Verify the intended package and maintenance status, choose a version with no known vulnerabilities, and pin it.
- Prefer observable correctness over ingestion throughput claims.
- Optimize for a supported recovery path and reproducible tests, not just a successful demo.
- Record lasting choices, such as an adopted dependency, service, or pattern, in a short repository ADR when the decision is made. Proposed choices stay provisional until adopted.

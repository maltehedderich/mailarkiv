# ADR 0007: Run Tantivy in a separate search process

**Status:** Adopted, 2026-11-06. Amended 2026-11-20 for Slice 3: Tantivy 0.27.0, pipes instead of a socket, a second file beside the server, and merges by request. Supersedes [ADR 0001](0001-embedded-bleve-for-search.md).

## Context

ADR 0001 chose embedded Bleve without measuring it. On 2026-11-06 we compared Bleve v2.6.1, SQLite FTS5 through `modernc.org/sqlite` v1.61.1, and Tantivy 0.26.1 on synthetic mail. The host had 4 cores and 7.8 GiB RAM, and each run was capped at 3 GiB and 3 cores. All engines returned the same hit counts. [The benchmark](https://github.com/maltehedderich/mailarkiv/blob/f61d6a1/docs/research/search-benchmark/README.md) has the method, full results, and limits. Commit `696b3a0` removed it from the repository, so the link points to the commit that added it.

At 1 million messages, warm cache, median / p95 in ms, newest first unless noted:

| | Bleve | FTS5, no prefix index | Tantivy |
| --- | --- | --- | --- |
| Build | 26.5 min | 33 min | 4.8 min |
| Index size | 5.4 GiB | 3.3 GiB | 1.2 GiB |
| Peak build memory | 3.0 GiB | 0.2 GiB | 0.67 GiB, 0.23 GiB allocated |
| Common word, in about 40% of mail | 554 / 721 | 6.6 / 17 | 8.1 / 18 |
| 4-character prefix | 281 / 796 | 57 / 393 | 5.4 / 25 |
| Two words and a 3-character prefix | 339 / 1321 | 1549 / 4907 | 22 / 54 |
| 2-character prefix | 1854 / 3052 | 2946 / 9208 | 57 / 97 |
| Common word, by relevance | 95 / 147 | 2625 / 3527 | 5.7 / 15 |

Tantivy at 5 million messages built in 30 min to 5.6 GiB. Its slowest warm query, a 2-character prefix, took 212 / 395 ms. Searching allocated 11 MiB plus 1.07 GiB of mapped index files, and a checksum check of the whole index took 17 s. We did not run Bleve or FTS5 at 5 million. Bleve's query times grew about fivefold from 200,000 to 1 million messages.

All three kept every acknowledged document after `kill -9` and after a full disk, and resumed. With one damaged 4 KiB block:

- **Bleve:** force merge rewrote over 4 GiB in 180 s at 2 of 3 locations without stopping, as in [bleve#2402](https://github.com/blevesearch/bleve/issues/2402), and missed the damage at the third. Bleve has no checksum check.
- **Tantivy:** its checksum check found 4 of 4 damaged files. A damaged term file crashed the search. Forced merges through damaged postings, term, or position files failed with an error, and one through a damaged fast field file succeeded without one.
- **FTS5:** `quick_check` found the damage, and `optimize` stopped with an error.

## Decision

Use `tantivy` 0.27.0 in a small Rust program, `mailarkiv-search`, that the Go server starts, supervises, and calls over pipes on the program's standard input and output. Build it as a static musl binary with mimalloc and only Tantivy's `mmap` and `lz4-compression` features. The musl default allocator built the index 2.4 times slower. The Go server stays a static `CGO_ENABLED=0` binary, as [ADR 0002](0002-modernc-sqlite-driver.md) requires. The program ships as a second file beside the server binary, and the server looks for it in its own executable's directory.

We first chose 0.26.1 and a Unix socket. The [amendment](#amendment-2026-11-20) explains both changes.

We rejected:

- **Bleve:** slowest on most newest-first searches at 1 million messages, near the memory cap, and it loops on corrupt segments with no checksum check.
- **SQLite FTS5:** it adds no dependency and commits with its "indexed" marks in one transaction, and it was the fastest on most word searches sorted newest first. But we query with prefix matching, and without prefix indexes a search with a short prefix took 1.5 to 3 s at 1 million messages. At 200,000 messages with them, it built at 203 docs/s and the index was 2.4 times the size of the text. Without them, at 1 million, it built at 503 docs/s.
- **Tantivy through cgo**, including `anyproto/tantivy-go`: it reverses ADR 0002's reason for the pure Go driver, and a crash in Rust or C code would stop SMTP intake. `tantivy-go` has no range queries or sorting and pins Tantivy 0.23.
- **A search server** such as Meilisearch or Manticore: the architecture requires no external search service.

## Checks on 2026-11-06

- The repository's latest commit was from 2026-11-05. The license is MIT. We recorded 0.26.1's release date as 2026-05-10, which was wrong; the amendment has the correct dates.
- With `rust:alpine` and rustc 1.99.1, the benchmark program builds as a 6.6 MB static binary in 4 min 25 s on 3 cores.
- It pulls in 107 crates, all under permissive licenses.
- An OSV query of those crates found one advisory, in `lru` 0.16.4. The amendment covers it.

## Amendment, 2026-11-20

Slice 3's research corrected two facts and settled what this ADR left open.

**Version.** On crates.io, 0.26.0 was released on 2026-03-31, 0.26.1 on 2026-04-21, 0.26.2 on 2026-09-08, and 0.27.0 on 2026-10-17. 0.26.1 and 0.26.2 require `lru` 0.16, which has [RUSTSEC-2026-0253](https://osv.dev/vulnerability/RUSTSEC-2026-0253), a use-after-free when a key's `Drop` panics. 0.27.0 requires `lru` 0.18.2, which fixes it. This ADR first said no release had the fix, which was already wrong. We use 0.27.0, pinned with `=0.27.0` and a committed `Cargo.lock`. With our two features and mimalloc 0.1.52, cargo resolves 113 crates, and an OSV query of all of them found no advisory on 2026-11-15. No principle IV exception is needed. 0.27.0 keeps every API Slice 3 uses; its breaking changes touch only how the schema is written. The benchmark ran 0.26.1, so its numbers may not carry over. Slice 3's sizing test measures 0.27.0 with our own code.

**Connection.** The server talks to the search process over pipes, not a Unix socket. A pipe has no address, so no other process can connect to it. Another process could reach it only through `/proc/<pid>/fd`, which needs the same account or `ptrace` rights, and those can already read the index files. A socket would need a path, its permissions, and cleanup after a crash, all for one client.

**A second file.** Embedded in the server binary, the program would have to be written to disk before it could run, which needs a writable location and cleanup. Finding it beside the server needs no new option.

**Merges.** From the source of 0.26.1 and 0.27.0: automatic merges start after each commit and after each finished merge. A failed merge logs `Merge of {:?} was cancelled: {:?}` as a warning and drops the operation. Its segments stay, so the next commit chooses them again. Automatic merges therefore retry, but their failures reach us only as log text, which can't be counted reliably. So the writer uses `NoMergePolicy`, and the server asks for maintenance after 50 commits or 10 minutes. The search process computes candidates with `LogMergePolicy::compute_merge_candidates`, runs and waits for each merge, runs `garbage_collect_files`, and returns what failed. Three failed runs in a row mark the index as needing a rebuild.

## Consequences

**A second process.** Constitution principle II asks the plan to justify this boundary against its operational cost. The Slice 3 plan does that. The reason is fault isolation: a search crash stops search, not capture, which removes the risk ADR 0001 accepted. Operators still install no search service, because the release includes the program beside the server and the server starts it.

**A second toolchain.** Slice 3 adds Rust and a C compiler for mimalloc to the build, and adds the program's tests and `cargo audit` to `mise run check` and `AGENTS.md`. Its 113 crates raise the count that principle IV asks us to keep low.

**Rules Tantivy needs that Bleve didn't.** The "Search index" section of `docs/ARCHITECTURE.md` turns these into rules:

- Tantivy has no primary key, so `add_document` alone creates a duplicate. Each batch must delete a document's ID before adding it. The benchmark's build times don't include this delete, while Bleve's upsert is part of its times.
- Merges run only when the server asks, so every failure comes back as a result the server counts.
- Failed merges left unreferenced files: 375 to 436 MiB against 244 MiB live. [Paperless-ngx](https://github.com/paperless-ngx/paperless-ngx/issues/14591) filled a 251 GB volume with such files by opening a writer per commit across processes.
- Deleted text stays in segment files until a merge rewrites them. A full merge at 5 million messages took 8 min and wrote 5.4 GiB.

**What stays.** The index is still a second store that can't commit with SQLite, so a message is marked indexed only after `commit` returns.

**Maintenance risk.** Tantivy's main authors joined Datadog with Quickwit and [say](https://www.paradedb.com/blog/tantivy-interview) they can't spend much time on work outside their product's agenda. [tantivy#2465](https://github.com/quickwit-oss/tantivy/issues/2465) reports dictionary errors above 20 million terms. It has no reproduction, its error text isn't in the 0.26.1 sources, and our 5 million index had about 15 million terms, so we did not test that size.

Revisit this decision if Slice 3's sizing test on our own code misses the targets in `docs/ARCHITECTURE.md`.

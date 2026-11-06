# ADR 0007: Run Tantivy in a separate search process

**Status:** Adopted, 2026-11-06. Supersedes [ADR 0001](0001-embedded-bleve-for-search.md).

## Context

ADR 0001 chose embedded Bleve without measuring it. On 2026-11-06 we compared Bleve v2.6.1, SQLite FTS5 through `modernc.org/sqlite` v1.61.1, and Tantivy 0.26.1 on synthetic mail. The host had 4 cores and 7.8 GiB RAM, and each run was capped at 3 GiB and 3 cores. All engines returned the same hit counts. [The benchmark](../research/search-benchmark/README.md) has the method, full results, and limits.

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

Use `tantivy` 0.26.1 in a small Rust program that the Go server starts, supervises, and calls over a Unix socket. Build it as a static musl binary with mimalloc and only Tantivy's `mmap` and `lz4-compression` features. The musl default allocator built the index 2.4 times slower. The Go server stays a static `CGO_ENABLED=0` binary, as [ADR 0002](0002-modernc-sqlite-driver.md) requires.

We rejected:

- **Bleve:** slowest on most newest-first searches at 1 million messages, near the memory cap, and it loops on corrupt segments with no checksum check.
- **SQLite FTS5:** it adds no dependency and commits with its "indexed" marks in one transaction, and it was the fastest on most word searches sorted newest first. But we query with prefix matching, and without prefix indexes a search with a short prefix took 1.5 to 3 s at 1 million messages. At 200,000 messages with them, it built at 203 docs/s and the index was 2.4 times the size of the text. Without them, at 1 million, it built at 503 docs/s.
- **Tantivy through cgo**, including `anyproto/tantivy-go`: it reverses ADR 0002's reason for the pure Go driver, and a crash in Rust or C code would stop SMTP intake. `tantivy-go` has no range queries or sorting and pins Tantivy 0.23.
- **A search server** such as Meilisearch or Manticore: the architecture requires no external search service.

## Checks on 2026-11-06

- 0.26.1 was released on 2026-05-10, and the repository's latest commit is from 2026-11-05. The license is MIT.
- With `rust:alpine` and rustc 1.99.1, the benchmark program builds as a 6.6 MB static binary in 4 min 25 s on 3 cores.
- It pulls in 107 crates, all under permissive licenses.
- An OSV query of those crates found one advisory, covered below.

## Consequences

**A second process.** Constitution principle II asks the plan to justify this boundary against its operational cost. The Slice 3 plan does that. The reason is fault isolation: a search crash stops search, not capture, which removes the risk ADR 0001 accepted. Operators still install no search service, because the release includes the helper and the server starts it. Slice 3 decides whether it ships as a second file or inside the server binary, and how the socket contract looks. We did not measure the socket's overhead.

**A second toolchain.** Slice 3 adds Rust and a C compiler for mimalloc to the build, and adds the helper's tests and `cargo audit` to `mise run check` and `AGENTS.md`. The helper's dependencies raise the count that principle IV asks us to keep low.

**One advisory.** `lru` 0.16.4 has [RUSTSEC-2026-0253](https://osv.dev/vulnerability/RUSTSEC-2026-0253), a use-after-free when a key's `Drop` panics. Tantivy's cache keys are `usize`, whose `Drop` can't panic. Tantivy's main branch requires the fixed 0.18.2, but no release has it yet. Until one does, Slice 3 records this as a principle IV exception, like `deepmerge` in [ADR 0006](0006-frontend-and-task-tooling.md).

**Rules Tantivy needs that Bleve didn't.** The "Search index" section of `docs/ARCHITECTURE.md` turns these into rules:

- Tantivy has no primary key, so `add_document` alone creates a duplicate. Each batch must delete a document's ID before adding it. The benchmark's build times don't include this delete, while Bleve's upsert is part of its times.
- Background merges drop their result and report failure only through a `log` warning. We tested damaged segments with forced merges only, so whether an automatic merge retries after later commits is unmeasured. Slice 3 must test this.
- Failed merges left unreferenced files: 375 to 436 MiB against 244 MiB live. [Paperless-ngx](https://github.com/paperless-ngx/paperless-ngx/issues/14591) filled a 251 GB volume with such files by opening a writer per commit across processes.
- Deleted text stays in segment files until a merge rewrites them. A full merge at 5 million messages took 8 min and wrote 5.4 GiB.

**What stays.** The index is still a second store that can't commit with SQLite, so a message is marked indexed only after `commit` returns.

**Maintenance risk.** Tantivy's main authors joined Datadog with Quickwit and [say](https://www.paradedb.com/blog/tantivy-interview) they can't spend much time on work outside their product's agenda. [tantivy#2465](https://github.com/quickwit-oss/tantivy/issues/2465) reports dictionary errors above 20 million terms. It has no reproduction, its error text isn't in the 0.26.1 sources, and our 5 million index had about 15 million terms, so we did not test that size.

Revisit this decision if Slice 3's sizing test on our own code misses the targets in `docs/ARCHITECTURE.md`.

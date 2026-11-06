# Search engine benchmark, November 2026

We compared Bleve, SQLite FTS5, and Tantivy on synthetic mail to choose MailArkiv's search index, which led to [ADR 0007](../../adr/0007-tantivy-search-process.md). At 1 million messages, Tantivy built 5.5 times faster than Bleve, its index was 4.5 times smaller, and most of its newest-first searches were 20 to 130 times faster with a warm cache. It was also the only engine we ran at the 5 million message target.

This tested each engine through a small harness, not MailArkiv. The Slice 3 sizing test still has to measure our own code, including the helper's socket.

## Setup

**Host:** a shared VPS with 4 cores and 7.8 GiB of memory, running Linux 6.8, with uncached reads of about 1.2 GB/s. Each run was capped with `MemoryMax=3G`, `MemorySwapMax=0`, and `CPUQuota=300%`, and ran at low priority with `nice -n 15` and `ionice -c3`. At 1 million messages, Bleve runs and the FTS5 build also set `GOMEMLIMIT=2400MiB`. The 200,000 message builds of Bleve, FTS5, and Tantivy with glibc ran at low priority without the caps.

**Mail:** one fixed generator, [`gen.go`](harness/go/gen.go), and no real mail.

- A vocabulary of 400,000 invented words, used with a Zipf distribution.
- Body lengths follow a log-normal curve with a median of about 245 words, capped at 30,000.
- Each message has 3 unique tokens, standing in for ticket and tracking IDs.
- 20 planted phrases each appear in 0.5% of messages.
- 25% of messages have attachment filenames.
- Dates are spread evenly over 4 years, in random order.
- The mean message is 4185 bytes, or 19.5 GiB of text at 5 million.

**Engines:**

| Engine | Version | Setup |
| --- | --- | --- |
| Bleve | v2.6.1 | Unicode tokenizer and lowercasing. Text isn't stored, and term vectors are on for phrase search. `date` has doc values. Date sort uses `-date` with `Score="none"` |
| FTS5 | `modernc.org/sqlite` v1.61.1 | Contentless table with `contentless_delete=1`, `detail=full`, `secure-delete`, and `synchronous=NORMAL`. `rowid` holds the date in its high bits, so newest first reads `rowid` backwards with `LIMIT 50` |
| Tantivy | 0.26.1 | `TEXT` fields, with `id` and `date` as fast fields, and a 256 MB writer budget. 200,000 and 1 million use a glibc build unless noted. 5 million uses the static musl and mimalloc build |

Builds used batches of 5000, each ending in a durable commit. A client sent queries one at a time and asked for the top 50.

## Build

| Messages | Engine | Build | Docs/s | Index | Peak memory |
| --- | --- | --- | --- | --- | --- |
| 200,000 | Bleve | 5.3 min | 635 | 1183 MiB | 1977 MiB |
| 200,000 | FTS5 with prefix indexes 2, 3, 4 | 16.5 min | 203 | 1879 MiB | 200 MiB |
| 200,000 | Tantivy, glibc | 1.0 min | 3417 | 261 MiB | 571 MiB |
| 200,000 | Tantivy, static with musl's allocator | 2.4 min | 1406 | 300 MiB | 194 MiB |
| 200,000 | Tantivy, static with mimalloc | 1.0 min | 3373 | 300 MiB | 309 MiB |
| 1 million | Bleve | 26.5 min | 629 | 5448 MiB | 3048 MiB |
| 1 million | FTS5 without prefix indexes | 33.1 min | 503 | 3375 MiB | 201 MiB |
| 1 million | Tantivy, glibc | 4.8 min | 3488 | 1222 MiB | 785 MiB |
| 1 million | Tantivy, static with mimalloc | 5.9 min | 2844 | 1235 MiB | 668 MiB |
| 5 million | Tantivy, static with mimalloc | 30.4 min | 2742 | 5737 MiB | 2667 MiB |

Peak memory is `VmHWM`, which includes mapped index files. [Memory](#memory) separates the two.

## Query times

Times are median / p95 in ms, newest first unless noted. `*` means the class hit its time budget before every query ran. Hits come from Tantivy. The other engines matched it on every class in the validation pass, except that FTS5's `LIMIT` caps its reported hits at 50.

- **Warm** repeats each query five times in one process.
- **Fresh process** runs each query once in a new process, with the OS cache warm. It's the closest to someone searching for something new on a running server.
- **Cold** runs each query once after evicting the index from the OS cache.

### 1 million messages, warm

| Query | Hits | Bleve | FTS5 | Tantivy |
| --- | --- | --- | --- | --- |
| Unique token | 1 | 0.25 / 0.70 | 0.37 / 4.6 | 0.02 / 0.16 |
| Rare word, in 0.01% of mail | 100 | 15 / 20 | 0.36 / 5.2 | 0.70 / 0.88 |
| Less common word, in 1% | 9,909 | 163 / 203 | 0.52 / 1.2 | 1.2 / 1.7 |
| Common word, in about 40% | 407,225 | 554 / 721 | 6.6 / 17 | 8.1 / 18 |
| Common and less common word | 6,479 | 162 / 189 | 7.3 / 13 | 3.6 / 4.7 |
| 4-character prefix | 105,510 | 281 / 796 | 57 / 393 | 5.4 / 25 |
| 2-character prefix | 777,581 | 1854 / 3052\* | 2946 / 9208\* | 57 / 97 |
| Two words and a 3-character prefix | 5,160 | 339 / 1321 | 1549 / 4907\* | 22 / 54 |
| Phrase | 5,011 | 176 / 247 | 1.0 / 35 | 2.0 / 2.4 |
| Sender within 90 days | 583 | 78 / 143 | 1.1 / 46 | 2.5 / 3.8 |
| Less common word within one year | 2,472 | 165 / 191 | 0.81 / 0.98 | 3.6 / 4.2 |
| Attachment filename | 209 | 30 / 41 | 4.8 / 70 | 0.65 / 0.96 |
| Less common word, by relevance | 9,909 | 2.0 / 24 | 62 / 89 | 1.2 / 1.4 |
| Common word, by relevance | 407,225 | 95 / 147 | 2625 / 3527\* | 5.7 / 15 |
| Both words, by relevance | 6,479 | 21 / 36 | 178 / 255 | 5.5 / 14 |
| Count of a common word | 407,225 | 51 / 72 | 122 / 179 | 2.1 / 3.4 |

### 1 million messages, fresh process

| Query | Hits | Bleve | FTS5 | Tantivy |
| --- | --- | --- | --- | --- |
| Unique token | 1 | 8.9 / 26 | 0.47 / 0.75 | 6.7 / 11 |
| Rare word, in 0.01% of mail | 100 | 52 / 164 | 0.69 / 0.93 | 12 / 28 |
| Less common word, in 1% | 9,909 | 208 / 228 | 1.3 / 1.5 | 11 / 19 |
| Common word, in about 40% | 407,225 | 668 / 820 | 7.1 / 29 | 16 / 22 |
| Common and less common word | 6,479 | 197 / 227 | 8.0 / 12 | 3.5 / 5.9 |
| 4-character prefix | 105,510 | 350 / 863 | 59 / 374 | 12 / 24 |
| 2-character prefix | 777,581 | 1901 / 3207 | 3955 / 8724\* | 70 / 132 |
| Two words and a 3-character prefix | 5,160 | 249 / 1346 | 176 / 2768 | 12 / 62 |
| Phrase | 5,011 | 232 / 270 | 1.4 / 1.8 | 12 / 17 |
| Sender within 90 days | 583 | 111 / 213 | 1.3 / 2.1 | 4.3 / 9.3 |
| Less common word within one year | 2,472 | 191 / 207 | 0.74 / 0.84 | 3.5 / 3.7 |
| Attachment filename | 209 | 35 / 58 | 5.6 / 7.0 | 1.0 / 9.1 |
| Less common word, by relevance | 9,909 | 2.5 / 3.8 | 62 / 88 | 1.4 / 1.8 |
| Common word, by relevance | 407,225 | 115 / 142 | 2205 / 3764 | 5.8 / 8.2 |
| Both words, by relevance | 6,479 | 24 / 40 | 185 / 258 | 4.5 / 7.3 |
| Count of a common word | 407,225 | 52 / 81 | 113 / 181 | 1.9 / 2.9 |

### 1 million messages, cold

| Query | Hits | Bleve | FTS5 | Tantivy |
| --- | --- | --- | --- | --- |
| Unique token | 1 | 14 / 142 | 4.2 / 6.1 | 5.7 / 15 |
| Rare word, in 0.01% of mail | 100 | 71 / 210 | 7.3 / 11 | 9.3 / 20 |
| Less common word, in 1% | 9,909 | 211 / 257 | 14 / 18 | 8.8 / 17 |
| Common word, in about 40% | 407,225 | 592 / 759 | 331 / 631 | 13 / 17 |
| Common and less common word | 6,479 | 153 / 181 | 9.1 / 12 | 3.1 / 4.2 |
| 4-character prefix | 105,510 | 301 / 722 | 151 / 476 | 11 / 22 |
| 2-character prefix | 777,581 | 1894 / 3141 | 5710 / 11351\* | 66 / 109 |
| Two words and a 3-character prefix | 5,160 | 422 / 1655 | 393 / 6170 | 13 / 70 |
| Phrase | 5,011 | 198 / 252 | 31 / 46 | 13 / 22 |
| Sender within 90 days | 583 | 92 / 181 | 21 / 64 | 4.5 / 9.1 |
| Less common word within one year | 2,472 | 158 / 174 | 0.74 / 0.88 | 2.9 / 3.1 |
| Attachment filename | 209 | 32 / 62 | 62 / 88 | 0.70 / 15 |
| Less common word, by relevance | 9,909 | 2.2 / 3.1 | 67 / 85 | 1.1 / 1.8 |
| Common word, by relevance | 407,225 | 101 / 184 | 2328 / 3401 | 5.6 / 8.1 |
| Both words, by relevance | 6,479 | 21 / 33 | 185 / 266 | 4.1 / 6.3 |
| Count of a common word | 407,225 | 52 / 73 | 123 / 183 | 2.0 / 3.0 |

### 5 million messages, Tantivy only

| Query | Hits | Cold | Fresh process | Warm |
| --- | --- | --- | --- | --- |
| Unique token | 1 | 9.7 / 22 | 0.28 / 0.49 | 0.13 / 0.42 |
| Rare word, in 0.01% of mail | 491 | 37 / 79 | 4.5 / 6.2 | 4.3 / 5.6 |
| Less common word, in 1% | 49,616 | 29 / 50 | 8.9 / 18 | 8.5 / 19 |
| Common word, in about 40% | 2,035,653 | 62 / 87 | 39 / 52 | 41 / 62 |
| Common and less common word | 32,605 | 20 / 29 | 20 / 30 | 19 / 31 |
| 4-character prefix | 527,713 | 39 / 90 | 27 / 86 | 22 / 80 |
| 2-character prefix | 3,889,243 | 265 / 497 | 187 / 338 | 212 / 395 |
| Two words and a 3-character prefix | 25,622 | 48 / 240 | 46 / 174 | 60 / 234 |
| Phrase | 24,974 | 35 / 57 | 11 / 22 | 11 / 22 |
| Sender within 90 days | 2,979 | 17 / 23 | 13 / 23 | 14 / 25 |
| Less common word within one year | 12,331 | 15 / 24 | 15 / 24 | 16 / 26 |
| Attachment filename | 1,085 | 6.4 / 17 | 3.8 / 4.3 | 4.2 / 5.3 |
| Less common word, by relevance | 49,616 | 6.5 / 15 | 5.7 / 8.0 | 6.4 / 17 |
| Common word, by relevance | 2,035,653 | 30 / 42 | 22 / 38 | 29 / 45 |
| Both words, by relevance | 32,605 | 22 / 37 | 20 / 34 | 22 / 33 |
| Count of a common word | 2,035,653 | 9.6 / 17 | 8.3 / 19 | 9.5 / 19 |

Bleve and FTS5 weren't run at this size. After a full merge to one segment, Tantivy's first pass was no faster than its 13-segment index: `5m-tantivy-fm.jsonl` has the numbers.

### 200,000 messages, warm

This is the only size where FTS5 ran with prefix indexes. Its medians here come mostly from repeated queries: a common word took 0.76 ms warm, against 66 ms in a first pass in [`val-go.txt`](results/val-go.txt). Compare the p95s instead.

| Query | Hits | Bleve | FTS5 with prefix indexes | Tantivy, glibc | Tantivy, static |
| --- | --- | --- | --- | --- | --- |
| Unique token | 1 | 0.30 / 4.1 | 0.26 / 6.3 | 0.01 / 2.8 | 0.02 / 0.30 |
| Rare word, in 0.01% of mail | 20 | 3.6 / 8.1 | 0.26 / 9.3 | 0.14 / 3.0 | 0.13 / 0.47 |
| Less common word, in 1% | 1,982 | 31 / 43 | 0.32 / 8.6 | 0.46 / 1.9 | 0.43 / 0.69 |
| Common word, in about 40% | 81,704 | 109 / 147 | 0.76 / 78 | 1.7 / 3.1 | 2.0 / 2.7 |
| Common and less common word | 1,295 | 32 / 40 | 1.3 / 1.8 | 0.83 / 1.2 | 0.85 / 1.1 |
| 4-character prefix | 21,184 | 65 / 179 | 0.46 / 33 | 2.3 / 12 | 2.8 / 15 |
| 2-character prefix | 155,510 | 519 / 982\* | 1.8 / 166 | 20 / 37 | 46 / 81 |
| Two words and a 3-character prefix | 1,028 | 62 / 401 | 3.8 / 159 | 3.9 / 23 | 7.1 / 50 |
| Phrase | 1,000 | 35 / 46 | 1.0 / 17 | 0.59 / 1.9 | 0.77 / 1.1 |
| Sender within 90 days | 124 | 16 / 26 | 0.90 / 12 | 0.75 / 1.5 | 0.60 / 0.97 |
| Less common word within one year | 503 | 32 / 40 | 0.80 / 0.98 | 0.89 / 1.2 | 0.75 / 1.1 |
| Attachment filename | 44 | 6.7 / 10 | 3.3 / 21 | 0.32 / 0.48 | 0.13 / 0.20 |
| Less common word, by relevance | 1,982 | 0.56 / 0.77 | 10 / 21 | 0.52 / 0.66 | 0.39 / 0.50 |
| Common word, by relevance | 81,704 | 18 / 29 | 407 / 639 | 1.5 / 2.2 | 1.5 / 2.0 |
| Both words, by relevance | 1,295 | 4.7 / 7.5 | 37 / 55 | 1.2 / 1.8 | 1.4 / 2.0 |
| Count of a common word | 81,704 | 9.3 / 19 | 22 / 37 | 0.43 / 0.68 | 0.56 / 0.86 |

### What stands out

- Bleve's date sort is the expensive part. A common word took 554 ms newest first and 95 ms by relevance.
- Bleve scaled about linearly. From 200,000 to 1 million messages, a common word went from 108 to 554 ms and a less common word from 31 to 163 ms. At that rate a common word would take about 2.8 s at 5 million, which is a projection.
- FTS5 is the reverse of Bleve. Its `rowid` trick makes newest-first word searches fast, while relevance ranking on common words and short prefixes without prefix indexes take seconds.
- FTS5's prefix indexes are expensive. Scaled linearly from 200,000 messages, they'd mean about 46 GiB and about 7 hours at 5 million, which is also a projection.
- Tantivy took about five times as long at 5 million as at 1 million on a common word, 41 ms against 8.1 ms.

## Updates and merges

| Engine | Batch of 100, median / p95 | Single delete | Full merge |
| --- | --- | --- | --- |
| Bleve, 1 million | 224 / 289 ms | 3.3 ms | 102 s, wrote 2621 MiB, directory at 8069 MiB right after |
| FTS5, 1 million | 302 / 796 ms | 6.0 ms | Not run at this size. At 200,000 with prefix indexes: 413 s, wrote 5429 MiB |
| Tantivy, 1 million | 72 / 90 ms | 11.4 ms | 79 s, wrote 1142 MiB |
| Tantivy, 5 million | 105 / 141 ms | 22.7 ms | 502 s, wrote 5502 MiB |

Deleted text stays in Bleve and Tantivy segment files until a merge rewrites them. FTS5's `secure-delete` removes it at delete time, and also needs `PRAGMA secure_delete=1` for freed pages, which the harness didn't set.

For 20 batches of 1000, `strace` counted 246 fsyncs for Bleve, 65 for FTS5, and 1165 for Tantivy.

## Memory

We sampled `/proc` every second to separate memory the engine allocated from index files mapped into its address space. The OS can drop mapped pages under pressure.

| Run | Allocated, peak | Mapped files, peak |
| --- | --- | --- |
| Tantivy, searches at 5 million | 11 MiB | 1071 MiB |
| Bleve, searches at 1 million | 245 MiB | 1472 MiB |
| FTS5, searches at 1 million | 280 MiB | 14 MiB |
| Tantivy, build at 1 million | 227 MiB | 434 MiB |
| Bleve, build at 200,000 | 1820 MiB | 375 MiB |
| Tantivy, full merge at 5 million | 282 MiB | 3399 MiB |
| Bleve, full merge at 1 million | 473 MiB | 2839 MiB |

We didn't split Tantivy's 2667 MiB peak in the 5 million build or Bleve's 3048 MiB peak in the 1 million build.

## Failure tests

All of these ran at 200,000 messages. Raw output is in [`failures2.txt`](results/failures2.txt).

**`kill -9` during indexing, 3 rounds each:** all three kept exactly the acknowledged documents and resumed. This doesn't test power loss.

**Full disk, with searches running, on a 128 MiB tmpfs:** all three returned an error without panicking, kept the acknowledged documents (Bleve 9000, FTS5 5000, Tantivy 31000), and resumed after we freed space. This is one run, so a race like the one in [opencloud#3790](https://github.com/opencloud-eu/opencloud/issues/3790) could still exist.

**One random 4 KiB overwrite, then a forced merge:**

| Engine | Detection | Merge |
| --- | --- | --- |
| Bleve | No checksum check exists. Queries returned no errors | At 2 of 3 locations it never finished: over 4 GiB written by 180 s, repeating `error loading roaring bitmap`, killed at 200 s. This reproduces [bleve#2402](https://github.com/blevesearch/bleve/issues/2402) on v2.6.1, and the async error callback did fire. At the third, it finished with no error |
| FTS5 | `quick_check` named the damaged page | `database disk image is malformed` after 103 s, with no loop. Queries that missed the page kept working |
| Tantivy | `validate_checksum` named the file, 4 of 4. Damage to a `.term` file crashed the query thread, and 5 of 16 classes finished | Postings, term, and position damage: the merge thread's panic came back as an error, with no retry, leaving 375 to 436 MiB against 244 MiB live. `.fast` damage: merged with no error |

## Static build

A static musl binary with musl's own allocator built the index 2.4 times slower. mimalloc restored the speed, at the price of a C compiler at build time. Its 2-character prefix query took 46 ms against 20 ms for glibc at 200,000 messages. Those indexes had 10 and 3 segments, and musl's allocator showed the same slowdown (42 ms) at 10 segments, so segment count may be the cause. We didn't test that.

## Dependencies

| | Size | Notes |
| --- | --- | --- |
| Tantivy with `mmap`, `lz4-compression`, and mimalloc | 107 crates | All permissive licenses. `lru` 0.16.4 has RUSTSEC-2026-0253. See [ADR 0007](../../adr/0007-tantivy-search-process.md) |
| Bleve | 26 Go modules, including 7 major versions of `zapx` | |
| `modernc.org/sqlite` | 8 Go modules | Already in `go.mod` |

## Limits

- The mail is synthetic, so the gaps between engines mean more than the absolute times.
- The host was shared, and other services ran during the tests.
- Bleve and FTS5 stopped at 1 million messages, so their 5 million figures are projections.
- We tested one setup per engine and tuned none of them further. FTS5's merge settings and Bleve without term vectors are untried.
- Tantivy's build times don't include the delete-before-add that [ARCHITECTURE](../../ARCHITECTURE.md) requires. Bleve's times include its upsert.
- We tested damaged Tantivy segments with forced merges only. Background merges report failure only as a `log` warning, and whether they retry after later commits is unmeasured.
- One client searched at a time, so concurrent users are unmeasured.
- Nothing here tests power loss, or damage beyond a few 4 KiB samples.

## Rerun

The harness is a separate Go module and Rust crate. `go ./...` at the repository root skips it, and `mise run check` doesn't run it. The scripts expect it in `$SCRATCH/spike`, and paths in the results show that placeholder.

```sh
SCRATCH=/some/large/disk            # About 40 GiB free
mkdir -p "$SCRATCH/results" "$SCRATCH/data/s" "$SCRATCH/data/m"
cp -r harness "$SCRATCH/spike"
cd "$SCRATCH/spike/go" && CGO_ENABLED=0 go build -o ../spike-go-f .
cd "$SCRATCH/spike/rs" && cargo build --release
cargo build --release --target x86_64-unknown-linux-musl   # Needs that rustup target
docker run --rm -v "$PWD:/src" -w /src -e CARGO_TARGET_DIR=/src/target-mi rust:alpine \
  cargo build --release --features mi
cd "$SCRATCH"
G=spike/spike-go-f; T=spike/rs/target/release/tspike
$G gen data/s 200000 200000
$G index bleve data/s/bleve data/s/corpus.tsv 0 200000 5000 1
$G index fts data/s/fts data/s/corpus.tsv 0 200000 5000 1
$T index data/s/tantivy data/s/corpus.tsv 0 200000 5000 1
bash spike/bench1m.sh "$SCRATCH" > results/bench1m.log 2>&1   # Generates the 5M corpus
bash spike/bench2.sh "$SCRATCH" > results/bench2.log 2>&1
bash spike/failures2.sh "$SCRATCH" > results/failures2.txt 2>&1
uv run --no-project spike/report.py results                   # The query tables
```

The original `bench1m.sh` run was interrupted during Bleve's update step, and `bench2.sh` finished it, so running both repeats the FTS5 and Bleve update steps. `200k-tantivy-mi.jsonl` came from running the `index` and `query` commands by hand with the static build. The memory table came from `memsplit.sh`.

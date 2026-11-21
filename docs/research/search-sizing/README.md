# Search sizing

This page records how large an archive MailArkiv's search supports. The targets come from the Slice 3 spec: at 5,000,000 messages on a host with 4 cores and 8 GiB of memory, the 95th percentile search time stays under 1 s, a full rebuild finishes within 8 hours, and the search process allocates under 1 GiB. The supported size is the largest size tested that met all three.

Unlike the [search engine benchmark](https://github.com/maltehedderich/mailarkiv/blob/f61d6a1/docs/research/search-benchmark/README.md) behind [ADR 0007](../../adr/0007-tantivy-search-process.md), this test runs MailArkiv's own code: the archive's import, processing in parse workers, the search module, and the release build of `mailarkiv-search`.

## Method

`mise run sizing` builds `bin/mailarkiv-search` and `bin/sizing`, then runs two steps on a fresh data directory, `.data/sizing`.

**`bin/sizing seed`** imports 5,000,000 messages through `archive.Import`, eight at a time, as an upload would. The archive's own file check refuses `unreadable-header.eml` from the fixed set, so 15 of its 16 files are imported and 4,999,985 messages are generated. Generated messages come from a fixed seed, so a second run produces the same bytes:

- Half are German and half English. Each draws words from that language's 157 or 169 common words and then from 300,000 invented words, with a Zipf distribution: the most common word is in nearly every message of its language, and an invented word near the end of the list is in about 1 in 10,000.
- Body length follows a log-normal curve with a median of 640 words, capped at 20,000. 3 in 10 messages add an HTML version of the body. The average message is about 7.9 KB.
- One in ten has an attachment of 2 to 10 KiB of random bytes, named like `Rechnung_2025-0412.pdf`.
- 20,000 senders on 2,000 domains under `example.test`. The most frequent sender writes about 2% of all mail.
- Dates spread evenly over the four years before 2026-11-01.
- Each message holds a token such as `ref0001234` that no other message has.

**`bin/sizing measure`** runs processing and the search module in one process, with two background parse workers, as `mailarkiv serve` does. It waits until every message is processed and the index is up to date, then:

1. Runs 13 classes of searches, 10 searches each, 5 times over, newest first. The classes and their words follow the benchmark's: a unique token, common, less common, and rare words, two words together, a sender's address and domain, an attachment name, a word within one year, a word with attachments, and prefixes of 4, 3, and 2 characters. Each time runs from the call to the search module until it has the page's 50 rows from the catalog.
2. Starts a full rebuild as an administrator would, and times it until the new index replaces the old one.
3. Reads the search process's `RssAnon` from `/proc` every 100 ms throughout. `RssAnon` counts allocated memory and leaves out the mapped index files, which the kernel can drop under pressure.
4. Sums the sizes of the files in `index/` after the rebuild.

A failure event, such as a search process crash or low disk space, stops the run, because its numbers wouldn't describe normal operation.

`measure` appends a dated section below with the host, the versions, and the results.

## Runs

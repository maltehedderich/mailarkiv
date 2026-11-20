# Quickstart: Validate Slice 3

Use this guide to validate Slice 3. Automated checks cover most of it. The manual scenarios cover what they can't: an upgrade from a Slice 2 archive, a look at the preview in a real browser, kills and disk space on a running server, and the sizing test. All mail here is synthetic.

[The API](contracts/http-api.md), [the search process protocol](contracts/search-process.md), [the command line](contracts/cli.md), and [the data model](data-model.md) hold the details. Test names match the [plan](plan.md#credible-failures).

## Prerequisites

- Linux on amd64, with [mise](https://mise.jdx.dev) and `strace` installed.
- `musl-gcc`, for the search process's allocator. On Debian or Ubuntu, run `sudo apt-get install musl-tools` once in your own terminal.
- `curl` and `jq` for the manual scenarios. Scenario 7 also needs `sudo` to mount a small file system.

```sh
mise trust
mise install
mise run setup
```

`mise install` adds Rust 1.100.0 with the musl target and `cargo-audit` 0.23.0. `mise run setup` stops with a message if `musl-gcc` is missing.

## Automated checks

```sh
mise run check
```

Expected: exit 0. That covers lint and type checks for Go, Rust, and the interface, all test layers, and both dependency audits.

To run one layer:

| Command | Covers |
| ------- | ------ |
| `mise run search:test` | The search process alone: matching, sorting, the deadline, delete before add, maintenance, and that it reports no text |
| `mise run server:test` | Processing bounds and workers, migration 0003, the index loop and its crash matrix, supervision, routes, and markers |
| `mise run frontend:test` | Row and view parsing, date range instants, state and category words |
| `mise run test:e2e` | Search, the preview's isolation, embedded and remote images, attachments, Back to results, keyboard, 360 px, axe |

## Setup for the manual scenarios

Use two terminals in the repository root. Stop any development server on port 8080 first. Everything lives in `.data/`, which Git ignores.

```sh
mise run build
mkdir -p .data
chmod 700 .data
go run ./internal/sizing/cmd/sizing seed --data-dir .data/demo --messages 2000
bin/mailarkiv account create --data-dir .data/demo admin@example.test
```

`seed` imports 2,000 synthetic messages through the archive's own import, including the fixed set the automated tests search. Then, in the second terminal:

```sh
bin/mailarkiv serve --data-dir .data/demo 2> .data/demo.log
```

Sign in at <http://localhost:8080>.

## 1. Upgrade from a Slice 2 archive

This scenario uses its own data directory, `.data/upgrade`, and doesn't need the setup above.

1. Copy `internal/mailparse/testdata/plain/` to `.data/upgrade-mail/`. Check out commit `392102f`, the last one before this slice, and run `mise run build`. Create an account on `.data/upgrade`, start the server on it, import the files in `.data/upgrade-mail/`, and stop the server.
2. Check out this slice, run `mise run build`, and start `bin/mailarkiv serve --data-dir .data/upgrade 2> .data/upgrade.log`.

Expected:

- The server starts, and `.data/upgrade.log` shows migration 0003 and `search state` with `building`.
- The status screen shows Not processed yet counting down to 0, then Building, then Up to date.
- Rows change from capture data to sender, subject, and date as messages are processed. The list's order changes to list date.
- History and downloads show exactly what they showed before. `sha256sum` of every file under `blobs/` matches its name.

## 2. Search

Search for each of these and compare with the expected results:

| Search | Expected |
| ------ | -------- |
| Words `quartals` | The message whose body says "Quartalsbericht" |
| From `anna@example.com` | Only Anna's messages from `example.com`, none from `example.org` |
| From `example.com` | Every message from an address at `example.com` |
| Attachment name `vertrag` | The message with `Vertrag_2026.pdf` |
| Words `müller` | Messages with "Müller" and "MÜLLER", none with only "Muller" |
| Words `a` | Messages with the word "a", not every word starting with "a" |
| Words `?!` with no filter | The full list |
| Date range covering one day | Only messages whose list date falls on that day, in this browser's time zone |

For each, the address bar never shows a search word, and `.data/demo.log` doesn't contain one:

```sh
grep -c -i -e quartals -e vertrag -e müller .data/demo.log
```

Expected: `0`.

Open a result, go Back, and check that the same search, page, and results show.

## 3. Preview

Import the files in `frontend/tests/e2e/fixtures/hostile/` and `frontend/tests/e2e/fixtures/images/` through Import. Open the browser's developer tools on the Network tab, and open each message.

Expected:

- No request goes to any host but `localhost:8080`. Clicking every link changes nothing.
- The HTML shows its embedded images. The message with images past 10,000,000 bytes says some are only in the attachment list.
- A message with remote images says they aren't loaded and offers to load them. After loading, the new requests are images only, and none has a `Referer` header.
- Open the preview's address, `/api/messages/<id>/html`, in a new tab. Its script doesn't run, and nothing loads from other hosts.
- Save each attachment from the hostile set. Each saves with a cleaned name and never opens in the browser.

## 4. A crashing search process

With the server running and imports going on:

```sh
pkill -KILL -x mailarkiv-search
```

Expected:

- The status screen lists Search process stopped with the time, and the problem count rises by 1. Search shows Unavailable with the next start time.
- An import during the outage succeeds.
- Within a few seconds, search shows Available, and the new message is found once.

Kill it four more times within 30 minutes, waiting for each restart. Expected: search shows Needs rebuild with a Rebuild button, and the state is still there after the server restarts.

## 5. A deleted index

Note the results of three searches. Stop the server, then:

```sh
rm -rf .data/demo/index
bin/mailarkiv serve --data-dir .data/demo 2> .data/demo.log
```

Expected: the status screen shows Building with progress. Searches during the build say how many messages they can't include yet. Once Up to date, the three searches return the same messages as before.

## 6. Rebuild

Press Rebuild on the status screen and confirm. While it runs:

- Search, and check that results match what they were before the rebuild.
- Press Rebuild again. The screen says one is running.
- Stop and start the server. Progress continues from where it was.
- Import a message.

Expected when it finishes: search shows Available, the imported message is found, and `.data/demo/index/` holds one directory.

## 7. Low disk space

This needs `sudo`. Mount a 4 GiB file system, and use it as a new data directory:

```sh
mkdir -p .data/small
sudo mount -t tmpfs -o size=4g,uid=$(id -u),gid=$(id -g),mode=700 tmpfs .data/small
go run ./internal/sizing/cmd/sizing seed --data-dir .data/small/demo --messages 2000
bin/mailarkiv account create --data-dir .data/small/demo admin@example.test
bin/mailarkiv serve --data-dir .data/small/demo 2> .data/small.log
```

In the first terminal, fill the file system to below 2 GiB free:

```sh
fallocate -l 2200m .data/small/filler
```

Expected: by the next refresh, the status screen shows indexing Paused with the free space and the 2 GiB threshold. Imports still succeed. Search still answers. Remove the filler, and indexing resumes once 3 GiB is free. Afterwards:

```sh
sudo umount .data/small
```

## 8. Sizing test

This takes most of a day and about 60 GB of disk. Run it on a host with 4 cores and 8 GiB of memory:

```sh
mise run sizing
```

Expected: `docs/research/search-sizing/README.md` gains the run's date, versions, and results: median and 95th percentile search times with and without prefix words, full rebuild time, the search process's peak allocated memory, and index size. Compare them with the targets in FR-049: 95th percentile under 1 s, rebuild within 8 hours, under 1 GiB allocated. `docs/OPERATIONS.md` states the supported size that follows (User Story 7).

## Clean up

```sh
rm -rf .data/demo .data/demo.log .data/upgrade .data/upgrade.log .data/upgrade-mail .data/small .data/small.log
```

# Quickstart: Validate Slice 1

Use this guide to show that Slice 1 works once it is built. Automated checks cover most of it, and the manual scenarios cover what a script can't judge. All mail here is synthetic.

Commands and test names below describe the planned implementation. They don't exist yet, so don't report any of them as passing until the tasks add them. [The API](contracts/http-api.md), [the command line](contracts/cli.md), and [the data model](data-model.md) hold the details.

## Prerequisites

- Linux on amd64, with [mise](https://mise.jdx.dev) and `strace` installed.
- Docker, only for the proxy scenario.

```sh
mise trust
mise install
mise run setup
```

## Automated checks

```sh
mise run check
```

Expected: exit 0. That run covers lint, type checks, all three test layers, and the dependency audit. [The plan](plan.md#required-checks) lists each check and what it catches.

To run the riskiest checks alone:

```sh
go test ./cmd/mailarkiv -run 'TestCrashMatrix|TestCaptureSyscallOrder|TestNoMarkersInLogOrStatus' -v
go test ./internal/web -run TestEveryRouteDeniesWithoutSession -v
```

## Scenario 1: first import and download

Covers User Story 1 and SC-001. Follow only `docs/OPERATIONS.md`. Start timing when the sign-in page opens and stop when the file is saved. It should take under 2 minutes. The build and account steps come first and aren't timed.

```sh
mise run build
mkdir -p .data
printf 'From: a@example.test\r\nSubject: Synthetic\r\n\r\nHello\r\n' > .data/sample.eml
bin/mailarkiv account create --data-dir .data/demo Admin@example.test
bin/mailarkiv serve --data-dir .data/demo
```

Open `http://localhost:8080` and sign in as `admin@example.test`, in lowercase. Import `.data/sample.eml`, then download it from the list.

Expected:

- An empty list with a way to import appears after sign-in.
- Choosing the file shows its name, its size, and the removal warning, and nothing is stored until you press Import.
- The result reads Archived and the message is first in the list, with an absolute time and UTC offset.
- The page stays in place during the download.
- The message's history shows the account as `admin@example.test`, although it was created with a capital letter.
- These two hashes match:

```sh
sha256sum .data/sample.eml ~/Downloads/<content-hash>.eml
```

## Scenario 2: direct requests without a session

Covers User Story 2 and SC-004. With the server from scenario 1 running:

```sh
for r in 'POST /api/imports' 'GET /api/messages' 'GET /api/messages/1' \
         'GET /api/messages/1/original' 'GET /api/status' 'GET /api/session' 'DELETE /api/session'; do
  set -- $r
  curl -s -o /dev/null -w "$r %{http_code}\n" -X "$1" "http://127.0.0.1:8080$2"
done
curl -s -o /dev/null -w 'forged %{http_code}\n' -X POST -H 'Sec-Fetch-Site: cross-site' \
  --data-binary @.data/sample.eml http://127.0.0.1:8080/api/imports
curl -sI http://127.0.0.1:8080/ | grep -i frame-ancestors
```

Expected: 401 for all seven, 403 for the forged request, and a `frame-ancestors 'none'` header. The list in the browser hasn't changed.

## Scenario 3: a crash during capture

Covers User Story 3 and SC-003. `TestCrashMatrix` runs the full table in [the plan](plan.md#crash-matrix). This run shows the same thing by hand:

```sh
go build -tags faults -o bin/mailarkiv-faults ./cmd/mailarkiv
bin/mailarkiv account create --data-dir .data/crash admin@example.test
MAILARKIV_FAULTS=after-rename=kill bin/mailarkiv-faults serve --data-dir .data/crash
```

Sign in and import a file. The server stops itself partway through the capture.

Expected:

- The browser shows Unconfirmed.
- After starting `bin/mailarkiv serve --data-dir .data/crash`, the list is empty and Status shows one unreferenced original with its hash, size, and next step. No stored file was deleted.
- Importing the same file again shows Archived. Status no longer lists the original, and `find .data/crash/blobs -type f | wc -l` prints 1.

## Scenario 4: repeat imports

Covers User Story 4 and SC-005. Import the same file three times, once under a different name.

Expected: one list entry showing `3×`, three history entries with file name, time, and account address, and exactly one file under `blobs/`.

## Scenario 5: damaged and missing originals

Covers User Story 4 scenario 5, User Story 5, and SC-006. Take the full hash from the message's history.

```sh
hash=<full content hash>
printf 'x' >> ".data/demo/blobs/${hash:0:2}/${hash:2:2}/$hash"
```

Download the message. Expected: the "This message wasn't downloaded" page with a way back to the list, and nothing saved. Status and the server log both show Stored original damaged with the message's ID, and the problem count on every signed-in page has risen by 1.

Import the original file again. Expected: the result reads Restored, and the download matches byte for byte again.

Restart the server. Expected: the failure is gone from Status, and the screen says it covers only the time since the latest start.

## Scenario 6: behind a reverse proxy

Covers User Story 3 scenarios 8 and 9, and FR-031. Use the nginx block from `docs/OPERATIONS.md`, then repeat with `client_max_body_size` removed, which restores the 1 MiB default.

Expected:

- With the documented setting, a file over 1 MiB imports normally, and the screen moves from upload progress to storing.
- With the default limit, the same file ends in Failed, naming the proxy's upload limit, and nothing is listed.
- With the MailArkiv server stopped, an import ends in Unconfirmed.

## Scenario 7: keyboard and small screens

Covers FR-041, FR-042, and SC-008. `mise run test:e2e` automates the keyboard path, the width check at 360 px, and axe on each screen in both themes. Automated rules can't judge everything in WCAG 2.2 AA, so also take 15 minutes with a screen reader on sign-in, import, and history. Check that each import result is announced and that focus returns to the row after the history drawer closes.

## Scenario 8: other browsers

Repeat scenario 1 and the failed download in scenario 5 in Firefox and Safari. Plan research tested them only in Chromium. Record any difference in `docs/OPERATIONS.md`. Over plain HTTP on localhost, Safari may refuse the session cookie, and the guide should state the result.

## Issue #1 acceptance

| Issue criterion | Shown by |
| ---------------- | -------- |
| Downloaded bytes match the input | `TestImportedBytesRoundTrip`, scenario 1 |
| Import survives restart | `TestCrashMatrix`, scenario 3 |
| A failed write never appears as successful | `TestImportOutcomes`, `TestCrashMatrix`, `import-outcomes.spec.ts` |
| Repeat imports keep provenance without extra copies | `TestRepeatImports`, scenario 4 |
| Automated interruption between file and catalog writes | `TestCrashMatrix`, `TestCaptureSyscallOrder` |
| Direct requests without authorization are denied | `TestEveryRouteDeniesWithoutSession`, scenario 2 |
| Failures visible without exposing message content | `TestNoMarkersInLogOrStatus`, scenario 5 |
| Recovery removes temp files, keeps originals, lists unreferenced ones | `TestCrashMatrix`, `TestRecovery`, scenario 3 |

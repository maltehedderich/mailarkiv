# Quickstart: Validate Slice 2

Use this guide to show that Slice 2 works once it is built. Automated checks cover most of it. The manual scenarios run against a real mail server, which no automated check does. All mail here is synthetic.

[The API](contracts/http-api.md), [the command line](contracts/cli.md), and [the data model](data-model.md) hold the details. Test names match the [plan](plan.md#credible-failures).

## Prerequisites

- Linux on amd64, with [mise](https://mise.jdx.dev) and `strace` installed.
- Docker and `openssl`, only for the manual scenarios.

```sh
mise trust
mise install
mise run setup
```

## Automated checks

```sh
mise run check
```

Expected: exit 0. That covers lint, type checks, all three test layers, and the dependency audit.

To run the riskiest checks alone:

```sh
go test ./cmd/mailarkiv -run 'TestCollectCrashMatrix|TestNoPasswordInDataDir|TestNoMarkersInLogOrStatus|TestShutdownDuringSync' -v
go test ./internal/collect -run 'TestStallAtEachStep|TestCollectionChangesNothing|TestEntryAndCopyCounts|TestSecureConnection' -v
go test ./internal/archive -run TestMigrateSlice1Catalog -v
go test ./internal/web -run 'TestEveryRouteDeniesWithoutSession|TestSourceResponsesHaveNoPassword' -v
```

## Setup for the manual scenarios

1. Make a throwaway CA with `openssl` and a certificate for `localhost` signed by it. Keep the CA as `.data/ca.pem`.
2. Run Dovecot in Docker with that certificate, IMAP with TLS on port 993 and STARTTLS on port 143, and one user. Record the image tag: the operator guide lists it as a tested server.
3. Fill the user's `INBOX` with 50 synthetic messages saved with CRLF line endings, and a second folder with a non-ASCII name, such as `Entwürfe`, with 10.
4. Build MailArkiv, create an account and a key, and start it trusting the CA:

```sh
mise run build
bin/mailarkiv account create --data-dir .data/demo admin@example.test
bin/mailarkiv key create --data-dir .data/demo .data/mailarkiv.key
SSL_CERT_FILE=.data/ca.pem bin/mailarkiv serve --data-dir .data/demo --key-file .data/mailarkiv.key
```

Until delivery step 8, add `--preview-collection` and open `http://localhost:8080/sources` directly, because the navigation item doesn't exist yet.

## Scenario 1: connect a mailbox

Covers User Story 1 and SC-001. Follow only `docs/OPERATIONS.md`, starting from an empty data directory. Start timing at `key create` and stop when the first collected message is listed. It should take under 10 minutes. Docker setup isn't timed.

Add a source for the Dovecot user with TLS on port 993, press Connect, choose both folders, and save.

Expected:

- Connect lists every folder with its message count, `INBOX` is preselected, and `Entwürfe` appears as written.
- The removal warning is visible before Save.
- The source shows archived out of found while syncing, and the archived count only rises.
- All 60 messages are listed, each with the source name and folder.
- A message's history shows the source name, `localhost`, the user name, the folder, the server's received time, and the collection time.
- A downloaded message has the same SHA-256 as the CRLF file you saved into Dovecot.
- Connect with a wrong password names Sign-in rejected and shows the server's reply. Nothing is saved.

Repeat Connect with STARTTLS on port 143.

## Scenario 2: new mail within 6 minutes

Covers User Story 1 scenario 4 and SC-009. After the first sync, save a new message into `INBOX` and note the time. Expected: it's listed within 6 minutes. Do 5 trials. Each must pass.

## Scenario 3: the mailbox doesn't change

Covers FR-011 and SC-008. Before adding the source, record every message's flags and the folder list with Dovecot's `doveadm fetch` and `doveadm mailbox list`. Record them again after the first sync and one scheduled sync.

Expected: both records are identical. No message gained `\Seen`.

## Scenario 4: an interrupted sync

Covers User Story 2 and SC-002. `TestCollectCrashMatrix` and `TestShutdownDuringSync` automate this. By hand, put 2,000 messages in a folder and add it to the source.

1. Stop the server with `kill -9` while the source shows Syncing, then start it again.
2. Stop it with `kill -TERM` during the next first-sync run.

Expected:

- After step 1, the sync restarts at once, and every message ends up listed once, with one history entry for the folder.
- After step 2, the server exits within about 5 seconds, and the next start resumes without action.
- The source's collected total equals the number of distinct messages on the server.

## Scenario 5: a stalled or failing server

Covers User Story 3. `TestStallAtEachStep` and `TestSchedule` automate this with short limits.

| Do this | Expected on the source |
| ------- | ---------------------- |
| `docker pause` the container during a sync | Timed out about 60 seconds after the last data, then Retrying with the next attempt 1 minute later, then 2, then 4 |
| `docker unpause` | The next attempt succeeds, and the source returns to Waiting with a new last successful sync |
| `docker stop` the container | Server not reachable |
| Change the user's password in Dovecot | Sign-in rejected and Needs attention after one attempt. Dovecot's log shows no further attempt over 15 minutes, and none after a MailArkiv restart |
| Save the new password in MailArkiv | Sign-in check passes, and a sync starts at once |
| Serve a certificate for another name | Secure connection failed, and Dovecot's log shows no sign-in attempt |

Each failure also appears on the status screen and in the problem count, and the log has `source_id` and `category` only.

## Scenario 6: passwords and the key

Covers User Story 4 and SC-006. `TestNoPasswordInDataDir` automates the search.

1. Add a second source whose password is `MARKER-PW-4f1c`. While the server runs, copy the data directory with `cp -a`, so the WAL is included.
2. Run `grep -rac MARKER-PW-4f1c` and the same for the password's base64 form on the copy and on the server log. Expected: 0 everywhere.
3. Create a second key and start with it. Expected: every source shows Password can't be read and Needs attention. Saving the password again resumes collection.
4. Start without `--key-file`. Expected: the log says `collection unavailable`, the sources screen says collection needs the key and links to the guide, Add is unavailable, every source shows Needs attention, and imports and downloads still work.
5. Run `key create` with a path inside `.data/demo`, through a symbolic link into it, and onto an existing file. Expected: each exits 1 and names the reason. `stat -c %a` on a new key prints `600`.
6. Start `serve` with a key path inside the data directory. Expected: exit 1 with cause `key`.

## Scenario 7: direct requests without a session

Covers FR-031 and SC-007. `TestEveryRouteDeniesWithoutSession` automates this. By hand, send each route in [the access table](contracts/http-api.md#access) with `curl` and no cookie, then with a valid cookie and `Origin: https://evil.example`.

Expected: 401 `no_session` without a cookie, and 403 `cross_origin` for the forged request. Nothing changes, and no answer holds source details.

## Scenario 8: manage sources

Covers User Story 6. On a source with 2,000 messages left to sync:

- Pause during a sync. It stops at once and stays paused across a restart. Resume starts a sync.
- Press Sync now twice. The second press says a sync is running.
- Add a folder. It syncs at once and collects every message in it. Remove it. Its messages stay listed.
- Save a wrong password. It's refused, and the old one keeps working.
- Edit the source. The password field is empty, and saving it empty keeps the password.
- Add a second source for the same server and user name in other capital letters. It's refused, with a link to the existing one.
- Remove the source and confirm. It disappears from Sources, and its messages' history still names it.

## Scenario 9: keyboard and small screens

Covers FR-042 and SC-011. `mise run test:e2e` automates the keyboard path through adding a source, the width check at 360 px, and axe on each sources screen in both themes. Automated rules can't judge all of WCAG 2.2 AA, so also take 15 minutes with a screen reader on adding a source and following its first sync. Check that state changes are announced without stealing focus.

## Issue #4 acceptance

| Issue criterion | Shown by |
| --------------- | -------- |
| Interrupting sync and restarting does not skip safely available messages | `TestCollectCrashMatrix`, `TestShutdownDuringSync`, scenario 4 |
| Re-sync avoids incorrect duplicates | `TestEntryAndCopyCounts`, `TestCollectCrashMatrix`, scenario 4 |
| Progress reflects stored messages rather than merely fetched headers | `TestProgressCountsDurable`, scenario 1 |
| Stalled network calls time out, expose the failure, and leave the checkpoint safe | `TestStallAtEachStep`, `TestNoMarkersInLogOrStatus`, scenario 5 |
| A copy of the data directory without the key does not reveal IMAP passwords | `TestNoPasswordInDataDir`, scenario 6 |
| A user can complete the workflow through the UI | `sources.spec.ts`, scenario 1 timed |

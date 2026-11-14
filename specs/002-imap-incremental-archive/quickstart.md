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

1. From the repository root, create a throwaway CA at `.data/ca.pem` and a certificate for `localhost` signed by it. These certificates expire after 30 days. The private keys are unencrypted and for local testing only; `.data/` is ignored by Git.

   ```sh
   (
     set -eu
     umask 077
     mkdir -p .data

     openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days 30 \
       -keyout .data/ca.key -out .data/ca.pem \
       -subj '/CN=MailArkiv test CA' \
       -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
       -addext 'keyUsage=critical,keyCertSign,cRLSign'

     openssl req -new -newkey rsa:2048 -nodes \
       -keyout .data/localhost.key -out .data/localhost.csr \
       -subj '/CN=localhost'

     cat > .data/localhost.ext <<'EOF'
   basicConstraints=critical,CA:FALSE
   keyUsage=critical,digitalSignature,keyEncipherment
   extendedKeyUsage=serverAuth
   subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1
   EOF

     openssl x509 -req -in .data/localhost.csr \
       -CA .data/ca.pem -CAkey .data/ca.key -CAcreateserial \
       -out .data/localhost.pem -days 30 -sha256 \
       -extfile .data/localhost.ext

     openssl verify -CAfile .data/ca.pem -purpose sslserver \
       -verify_hostname localhost .data/localhost.pem
   )
   ```

   Expected: `.data/localhost.pem: OK`.

2. From the repository root, start [Dovecot 2.4.2](https://github.com/dovecot/documentation/blob/2.4.2/docs/installation/docker.md) with one test user, `test@example.test`. Keep ports 143 and 993 free. The container publishes them only on loopback; its internal ports are 31143 and 31993.

   ```sh
   (
     set -eu
     umask 077
     mkdir -p .data/dovecot
     openssl rand -hex 16 > .data/dovecot-password
     printf 'test@example.test:{PLAIN}%s:1000:1000::/srv/vmail/test@example.test::\n' \
       "$(cat .data/dovecot-password)" > .data/dovecot/users

     cat > .data/dovecot/local.conf <<'EOF'
   protocols = imap
   ssl = required
   ssl_server {
     cert_file = /etc/dovecot/ssl/tls.crt
     key_file = /etc/dovecot/ssl/tls.key
   }
   auth_mechanisms = plain login
   passdb passwd-file {
     passwd_file_path = /etc/dovecot/conf.d/users
   }
   userdb passwd-file {
     passwd_file_path = /etc/dovecot/conf.d/users
   }
   namespace inbox {
     inbox = yes
     separator = /
   }
   EOF

     # Dovecot's worker processes run as UID 1000 inside the container.
     chmod 755 .data/dovecot
     chmod 644 .data/dovecot/local.conf .data/dovecot/users

     docker pull dovecot/dovecot:2.4.2
     docker run -d --name mailarkiv-dovecot --user 0:0 \
       -p 127.0.0.1:143:31143 -p 127.0.0.1:993:31993 \
       --mount "type=bind,src=$PWD/.data/dovecot,dst=/etc/dovecot/conf.d,readonly" \
       --mount "type=bind,src=$PWD/.data/localhost.pem,dst=/etc/dovecot/ssl/tls.crt,readonly" \
       --mount "type=bind,src=$PWD/.data/localhost.key,dst=/etc/dovecot/ssl/tls.key,readonly" \
       --mount type=volume,src=mailarkiv-dovecot-mail,dst=/srv/vmail \
       dovecot/dovecot:2.4.2

     docker image inspect dovecot/dovecot:2.4.2 \
       --format '{{index .RepoDigests 0}}' > .data/dovecot-image.txt
   )
   ```

   The master process runs as root inside the container so it can read the private key with its existing permissions. Mail processes use UID 1000. The mounted configuration replaces the image's default authentication, which accepts arbitrary user names. The generated password stays under the Git-ignored `.data/` directory; use `cat .data/dovecot-password` when entering it in MailArkiv.

   Check startup and both TLS modes before seeding mail:

   ```sh
   docker logs mailarkiv-dovecot
   openssl s_client -connect 127.0.0.1:993 -servername localhost \
     -CAfile .data/ca.pem -verify_hostname localhost -verify_return_error </dev/null
   openssl s_client -starttls imap -connect 127.0.0.1:143 -servername localhost \
     -CAfile .data/ca.pem -verify_hostname localhost -verify_return_error </dev/null
   ```

   Expected: Dovecot starts without a fatal error, and both TLS checks report `Verify return code: 0 (ok)`. If a connection is refused while the container starts, check its logs and repeat the check. In MailArkiv, use host `localhost`, user `test@example.test`, and either TLS on port 993 or STARTTLS on port 143. Record the image tag and the digest in `.data/dovecot-image.txt` with the scenario results; list it as tested in the operator guide only after running the scenarios.

3. Create `INBOX` and `Entwürfe`, then save 50 and 10 synthetic messages respectively. Run this once against an empty test mailbox. If it stops partway through, use the reset commands below before retrying. Each message has a unique Message-ID, and the original CRLF files stay in `.data/fixtures/` for the download comparison in scenario 1.

   ```sh
   (
     set -eu
     umask 077
     mkdir -p .data/fixtures
     docker exec mailarkiv-dovecot doveadm mailbox create \
       -u test@example.test -s INBOX 'Entwürfe'

     for folder in INBOX 'Entwürfe'; do
       case "$folder" in
         INBOX) count=50; prefix=inbox ;;
         *) count=10; prefix=drafts ;;
       esac
       i=1
       while [ "$i" -le "$count" ]; do
         file=$(printf '.data/fixtures/%s-%02d.eml' "$prefix" "$i")
         printf '%s\r\n' \
           'From: Sender <sender@example.test>' \
           'To: Test User <test@example.test>' \
           'Date: Fri, 13 Nov 2026 12:00:00 +0000' \
           "Message-ID: <$prefix-$i@mailarkiv.example.test>" \
           "Subject: Synthetic $prefix message $i" \
           'MIME-Version: 1.0' \
           'Content-Type: text/plain; charset=us-ascii' \
           '' \
           "Synthetic message $i for $prefix." > "$file"
         docker exec -i mailarkiv-dovecot doveadm save \
           -u test@example.test -m "$folder" < "$file"
         i=$((i + 1))
       done
     done

     docker exec mailarkiv-dovecot doveadm mailbox status \
       -u test@example.test messages INBOX 'Entwürfe'
   )
   ```

   Expected: `INBOX messages=50` and `Entwürfe messages=10`. Use `shasum -a 256 .data/fixtures/inbox-01.eml` and compare it with the downloaded copy of “Synthetic inbox message 1” in scenario 1.

   The named volume keeps mail across container restarts. Use `docker stop mailarkiv-dovecot` and `docker start mailarkiv-dovecot` to stop and resume it. To start over, remove only this throwaway server and its mail with `docker rm -f mailarkiv-dovecot` followed by `docker volume rm mailarkiv-dovecot-mail`, then repeat steps 2 and 3.

4. Build MailArkiv, create an account and a key, and start it trusting the CA:

```sh
mise run build
bin/mailarkiv account create --data-dir .data/demo admin@example.test
bin/mailarkiv key create --data-dir .data/demo .data/mailarkiv.key
SSL_CERT_FILE=.data/ca.pem bin/mailarkiv serve --data-dir .data/demo --key-file .data/mailarkiv.key
```

Open `http://localhost:8080`, sign in, and choose **Sources → Add a source**.

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

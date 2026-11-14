# Quickstart: Validate Slice 2

Use this guide to validate Slice 2. Automated checks cover most of it. The manual scenarios run against a real mail server, which no automated check does. All mail here is synthetic.

[The API](contracts/http-api.md), [the command line](contracts/cli.md), and [the data model](data-model.md) hold the details. Test names match the [plan](plan.md#credible-failures).

## Prerequisites

- Linux on amd64, with [mise](https://mise.jdx.dev) and `strace` installed.
- Docker, OpenSSL 3 or later, `curl`, and `jq`, only for the manual scenarios.

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

To run the server checks alone, including the crash, timeout, mailbox integrity, credential, migration, and route-access tests:

```sh
mise run server:test
```

## Setup for the manual scenarios

You need two terminals in the repository root: one for commands, one that runs MailArkiv. Every command works in Bash and zsh. Stop any development server on port 8080 first.

Everything the guide creates lives in `.data/`, which Git ignores:

| Path                                         | Holds                                                |
| -------------------------------------------- | ---------------------------------------------------- |
| `.data/ca.pem`, `.data/ca.key`               | Throwaway CA                                         |
| `.data/localhost.pem`, `.data/localhost.key` | Dovecot's certificate and key                        |
| `.data/dovecot/`, `.data/dovecot-password`   | Dovecot's configuration and the mailbox password     |
| `.data/fixtures/`                            | The last message of each seeded batch                |
| `.data/demo/`, `.data/demo.log`              | MailArkiv's archive and log                          |
| `.data/mailarkiv.key`                        | MailArkiv's password key, shared with `mise run dev` |

### 1. Certificates

Create a CA and a certificate for `localhost` signed by it. Both expire after 30 days. The private keys are unencrypted and for local testing only.

```sh
mkdir -p .data/dovecot .data/fixtures
chmod 700 .data

openssl req -x509 -newkey rsa:2048 -nodes -days 30 \
  -keyout .data/ca.key -out .data/ca.pem \
  -subj '/CN=MailArkiv test CA' \
  -addext 'basicConstraints=critical,CA:TRUE,pathlen:0' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign'

openssl req -x509 -newkey rsa:2048 -nodes -days 30 \
  -CA .data/ca.pem -CAkey .data/ca.key \
  -keyout .data/localhost.key -out .data/localhost.pem \
  -subj '/CN=localhost' \
  -addext 'basicConstraints=critical,CA:FALSE' \
  -addext 'extendedKeyUsage=serverAuth' \
  -addext 'subjectAltName=DNS:localhost,IP:127.0.0.1,IP:::1'

openssl verify -CAfile .data/ca.pem -verify_hostname localhost .data/localhost.pem
```

Expected: `.data/localhost.pem: OK`.

### 2. Dovecot

Start [Dovecot 2.4.2](https://github.com/dovecot/documentation/blob/2.4.2/docs/installation/docker.md) with one user, `test@example.test`, and a generated password. Keep ports 143 and 993 free; the container publishes them on loopback only.

```sh
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
auth_verbose = yes
auth_verbose_passwords = no
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

chmod 755 .data/dovecot
chmod 644 .data/dovecot/local.conf .data/dovecot/users

docker run -d --name mailarkiv-dovecot --user 0:0 \
  -p 127.0.0.1:143:31143 -p 127.0.0.1:993:31993 \
  --mount "type=bind,src=$PWD/.data/dovecot,dst=/etc/dovecot/conf.d,readonly" \
  --mount "type=bind,src=$PWD/.data/localhost.pem,dst=/etc/dovecot/ssl/tls.crt,readonly" \
  --mount "type=bind,src=$PWD/.data/localhost.key,dst=/etc/dovecot/ssl/tls.key,readonly" \
  --mount type=volume,src=mailarkiv-dovecot-mail,dst=/srv/vmail \
  dovecot/dovecot:2.4.2
```

The master process runs as root inside the container so it can read the private key. Mail processes run as UID 1000, which is why the configuration is world-readable. The mounted configuration replaces the image's default authentication, which accepts any user name.

Check that it started and that both TLS modes verify:

```sh
docker logs mailarkiv-dovecot
openssl s_client -connect 127.0.0.1:993 -servername localhost \
  -CAfile .data/ca.pem -verify_hostname localhost -verify_return_error </dev/null
openssl s_client -starttls imap -connect 127.0.0.1:143 -servername localhost \
  -CAfile .data/ca.pem -verify_hostname localhost -verify_return_error </dev/null
```

Expected: no fatal error in the log, and `Verify return code: 0 (ok)` twice. If the connection is refused, the container is still starting; try again.

Record the image digest with your scenario results. List the image as tested in the operator guide only after running the scenarios.

```sh
docker image inspect dovecot/dovecot:2.4.2 --format '{{index .RepoDigests 0}}'
```

The named volume keeps mail across `docker stop` and `docker start`. To start over, run `docker rm -f mailarkiv-dovecot` and `docker volume rm mailarkiv-dovecot-mail`, then repeat this step and the next.

### 3. Seed mail

Create `INBOX` and `Entwürfe`:

```sh
docker exec mailarkiv-dovecot doveadm mailbox create \
  -u test@example.test -s INBOX 'Entwürfe'
```

This is the **seeding loop**. It saves `count` messages into `folder`. Later scenarios reuse it with a different first line.

```sh
folder=INBOX prefix=inbox count=50
for i in $(seq 1 "$count"); do
  printf '%s\r\n' \
    'From: Sender <sender@example.test>' \
    'To: Test User <test@example.test>' \
    'Date: Fri, 13 Nov 2026 12:00:00 +0000' \
    "Message-ID: <$prefix-$i@mailarkiv.example.test>" \
    "Subject: Synthetic $prefix message $i" \
    'MIME-Version: 1.0' \
    'Content-Type: text/plain; charset=us-ascii' \
    '' \
    "Synthetic message $i for $prefix." \
    | tee ".data/fixtures/$prefix.eml" \
    | docker exec -i mailarkiv-dovecot doveadm save -u test@example.test -m "$folder"
done
```

Run it a second time with this first line:

```sh
folder='Entwürfe' prefix=drafts count=10
```

Use each `prefix` once. MailArkiv stores identical messages only once, so a reused prefix would add nothing to the archive. The loop keeps the last message of each batch as `.data/fixtures/<prefix>.eml`.

Check the counts:

```sh
docker exec mailarkiv-dovecot doveadm mailbox status \
  -u test@example.test messages INBOX 'Entwürfe'
```

Expected: `INBOX messages=50` and `Entwürfe messages=10`.

Record the mailbox's folders and every message's folder, UID, and flags. Scenario 3 compares against this file. Until then, don't add mail or open the mailbox in a mail client.

```sh
{
  docker exec mailarkiv-dovecot doveadm mailbox list -u test@example.test
  docker exec mailarkiv-dovecot doveadm -f tab fetch -u test@example.test \
    'mailbox uid flags' all
} | sort > .data/mailbox-before.txt
```

### 4. Start MailArkiv

Scenario 1 times the setup from `key create` to the first listed message. Follow [the operator guide](../../docs/OPERATIONS.md) for these steps, start a stopwatch before `key create`, and note any step where you needed this guide instead. The build is outside the timed interval.

```sh
mise run build
bin/mailarkiv account create --data-dir .data/demo admin@example.test
bin/mailarkiv key create --data-dir .data/demo .data/mailarkiv.key
```

If `.data/mailarkiv.key` already exists from `mise run dev`, reuse it, skip `key create`, and record that key creation wasn't timed.

In the second terminal, run the **start command**:

```sh
SSL_CERT_FILE=.data/ca.pem bin/mailarkiv serve \
  --data-dir .data/demo --key-file .data/mailarkiv.key >> .data/demo.log 2>&1
```

`SSL_CERT_FILE` makes MailArkiv trust the test CA. The server stays in the foreground and writes its log to `.data/demo.log`. Stop it with Ctrl-C and start it again with the same command. Never recreate the account or the key.

Open `http://localhost:8080` and sign in as `admin@example.test` with the password you chose in `account create`.

The mailbox details, used throughout:

| Field               | Value                                  |
| ------------------- | -------------------------------------- |
| Server              | `localhost`                            |
| Connection security | TLS                                    |
| Port                | `993`                                  |
| User name           | `test@example.test`                    |
| Password            | Output of `cat .data/dovecot-password` |

The mailbox password is separate from the administrator's MailArkiv password.

`mise run dev` can use the same Dovecot server; see [local development](../../README.md#local-development).

## Order

Run scenario 1, then 3, then 2, then 4 to 9. Scenario 3 needs the mailbox exactly as setup left it. Finish each scenario's recovery step before the next one.

## Scenario 1: connect a mailbox

Covers User Story 1 and SC-001.

1. Choose **Sources → Add a source**. Enter the mailbox details, but with `deliberately-wrong` as the password. Press **Connect**. Expect **Sign-in rejected**, the server's reply, and no saved source.
2. Enter the real password and press **Connect**. Check that `INBOX` is preselected, both folder counts are shown, and `Entwürfe` appears as written. Select both folders, name the source `Local Dovecot`, read the removal warning, and press **Save**.
3. Watch the source's archived/found progress. The archived count must only rise. Open the message list. Stop the stopwatch when the first message is listed: it should show under 10 minutes.
4. After the source returns to **Waiting**, check that 60 messages are listed across two pages. Open a row's history and check the source name, `localhost`, `test@example.test`, the folder, the received time, and the collection time.
5. Check that a download is byte-identical. `.data/fixtures/inbox.eml` is message 50 of `INBOX`. Print its hash, find the row with that hash in the message list, and download it.

   ```sh
   sha256sum .data/fixtures/inbox.eml
   cmp .data/fixtures/inbox.eml ~/Downloads/<downloaded file>
   ```

   Expected: no output from `cmp`.

6. Open the source's **Edit** form, choose **STARTTLS** and port **143**, leave the password empty, and save. If **Add folders** is shown, press it. Check that both folders are still selected, then press **Sync now** and wait for **Waiting**. Change back to **TLS** and port **993**, and save.

Continue with scenario 3.

## Scenario 2: new mail within 6 minutes

Covers User Story 1 scenario 4 and SC-009. The source must be **Waiting**, and its last sync must have taken under 30 seconds. Don't press **Sync now**.

Run the seeding loop with this first line and note the time:

```sh
folder=INBOX prefix=arrival-1 count=1
```

Refresh the message list until the total rises by one. Check the new row's hash against `sha256sum .data/fixtures/arrival-1.eml`.

Do this five times, with `arrival-1` to `arrival-5`, waiting for each message before sending the next.

Expected: each message is listed within 6 minutes of being saved. `INBOX` then holds 55 messages.

## Scenario 3: the mailbox doesn't change

Covers FR-011 and SC-008. Run this right after scenario 1, before adding any mail.

After the first sync has finished, record the mailbox again and compare:

```sh
{
  docker exec mailarkiv-dovecot doveadm mailbox list -u test@example.test
  docker exec mailarkiv-dovecot doveadm -f tab fetch -u test@example.test \
    'mailbox uid flags' all
} | sort > .data/mailbox-after.txt
diff .data/mailbox-before.txt .data/mailbox-after.txt
```

Note the source's **Last successful sync**. Wait for the next scheduled sync, five minutes later, without pressing **Sync now**. When the time has changed and the source is **Waiting** again, run the same commands once more.

Expected: no output from `diff` both times. No message gained `\Seen`, disappeared, or moved, and no folder was added.

Continue with scenario 2.

## Scenario 4: an interrupted sync

Covers User Story 2 and SC-002. `TestCollectCrashMatrix` and `TestShutdownDuringSync` cover the full crash matrix. This check interrupts two first syncs by hand.

1. Create a folder with 2,000 messages. Run the first command, then the seeding loop with the first line below it. Seeding takes a few minutes.

   ```sh
   docker exec mailarkiv-dovecot doveadm mailbox create -u test@example.test -s Crash
   ```

   ```sh
   folder=Crash prefix=crash count=2000
   ```

2. In the source's **Edit** form, select `Crash` as well and save. While the source shows **Syncing**, kill the server from the first terminal:

   ```sh
   pkill -KILL -f 'mailarkiv serve'
   ```

   Run the start command again. Collection must resume without pressing anything. If the sync finished before the kill, repeat with a new folder and prefix, and note that the first attempt tested nothing.

3. Wait for **Waiting**, then compare the mailbox total with the source's collected total and the message list's total:

   ```sh
   docker exec mailarkiv-dovecot doveadm mailbox status \
     -u test@example.test -t messages '*'
   ```

   Expected: all three are equal, 2,065 after five scenario 2 trials. Open a few messages from `Crash`: each history has exactly one entry.

4. Repeat steps 1 to 3 with a folder `Term` and prefix `term`, but stop the server with SIGTERM:

   ```sh
   pkill -TERM -f 'mailarkiv serve'
   ```

   Expected: the server exits within about five seconds, the restart resumes collection, and the totals reach 4,065.

5. Press **Sync now** and wait for **Waiting**. The totals and history counts must not grow.

## Scenario 5: a stalled or failing server

Covers User Story 3. `TestStallAtEachStep` and `TestSchedule` automate the timeout and retry limits. Keep **Sources** and **Status** open. Each failure below must also appear on **Status** and in the problem count.

### A stalled server

Pause Dovecot, then press **Sync now**. The port still accepts connections, but nothing answers.

```sh
docker pause mailarkiv-dovecot
```

Expect **Timed out** about 60 seconds after the last data, then **Retrying**. Leave it paused through three failed attempts. The waits between attempts are 1, 2, then 4 minutes. Check the times in the log:

```sh
jq -c 'select(.msg == "sync failed") | {time, category, failed_attempts, next_attempt_at}' \
  .data/demo.log
```

Run `docker unpause mailarkiv-dovecot` and let the next retry run. Expect **Waiting** and a newer last successful sync.

### An unreachable server

Run `docker stop mailarkiv-dovecot` and press **Sync now**. Expect **Server not reachable**.

Run `docker start mailarkiv-dovecot`, press **Sync now**, and wait for **Waiting**.

### A rejected password

Change the mailbox password and restart Dovecot:

```sh
openssl rand -hex 16 > .data/dovecot-password
printf 'test@example.test:{PLAIN}%s:1000:1000::/srv/vmail/test@example.test::\n' \
  "$(cat .data/dovecot-password)" > .data/dovecot/users
docker restart mailarkiv-dovecot
```

Press **Sync now** once. Expect **Sign-in rejected** and **Needs attention**. Don't press it again and don't open **Edit**; both sign in. Find the failed sign-in in Dovecot's log and note its time:

```sh
docker logs --since 30m --timestamps mailarkiv-dovecot
```

Wait 15 minutes, restart MailArkiv, wait one more minute, and read the log again. Expected: no sign-in attempt after the one you noted.

Enter the new password from `cat .data/dovecot-password` in **Edit → Password** and save. Expect a successful sign-in and a sync at once.

### A certificate for another name

Create a certificate for `wrong.example.test` from the same key and CA, and put it in place of the real one. Dovecot mounts the file itself, so overwrite it rather than replacing it.

```sh
openssl req -x509 -key .data/localhost.key -days 30 \
  -CA .data/ca.pem -CAkey .data/ca.key \
  -out .data/wrong-name.pem -subj '/CN=wrong.example.test' \
  -addext 'basicConstraints=critical,CA:FALSE' \
  -addext 'extendedKeyUsage=serverAuth' \
  -addext 'subjectAltName=DNS:wrong.example.test'

docker stop mailarkiv-dovecot
cp .data/localhost.pem .data/localhost.pem.orig
cat .data/wrong-name.pem > .data/localhost.pem
docker start mailarkiv-dovecot
```

Press **Sync now**. Expect **Secure connection failed**. Dovecot's log must show a TLS disconnect and no sign-in attempt:

```sh
docker logs --since 5m --timestamps mailarkiv-dovecot
```

Restore the certificate, then press **Sync now** and wait for **Waiting**:

```sh
docker stop mailarkiv-dovecot
cat .data/localhost.pem.orig > .data/localhost.pem
rm .data/localhost.pem.orig .data/wrong-name.pem
docker start mailarkiv-dovecot
```

### The log

Read the `sync failed` lines in `.data/demo.log`. They may hold IDs, categories, retry counts, and times. They must hold no mailbox user name, source or folder name, password, message content, or server reply.

## Scenario 6: passwords and the key

Covers User Story 4 and SC-006. `TestNoPasswordInDataDir` automates the search.

1. Add a second Dovecot user whose password is a marker to search for:

   ```sh
   printf 'marker@example.test:{PLAIN}MARKER-PW-4f1c:1000:1000::/srv/vmail/marker@example.test::\n' \
     >> .data/dovecot/users
   docker restart mailarkiv-dovecot
   docker exec mailarkiv-dovecot doveadm mailbox create -u marker@example.test -s INBOX
   ```

   Add a source with `localhost`, TLS, port 993, user `marker@example.test`, password `MARKER-PW-4f1c`, and name `Marker`. Select `INBOX`, save, and wait for **Waiting**.

2. Copy the data directory while MailArkiv runs, so the copy includes the database's write-ahead log. Search the copy and the server log for the password and its base64 form:

   ```sh
   cp -a .data/demo .data/demo-copy
   grep -r -a -l -F -e 'MARKER-PW-4f1c' -e 'TUFSS0VSLVBXLTRmMWM=' .data/demo-copy .data/demo.log
   rm -rf .data/demo-copy
   ```

   Expected: `grep` prints no file name.

3. Stop MailArkiv. Create a second key and start with it:

   ```sh
   bin/mailarkiv key create --data-dir .data/demo .data/second.key
   SSL_CERT_FILE=.data/ca.pem bin/mailarkiv serve \
     --data-dir .data/demo --key-file .data/second.key >> .data/demo.log 2>&1
   ```

   Expect **Password can't be read** and **Needs attention** on both sources. Edit `Local Dovecot`, enter the current mailbox password, and save. Expect collection to resume. Do the same for `Marker`.

4. Stop MailArkiv and start it without a key:

   ```sh
   SSL_CERT_FILE=.data/ca.pem bin/mailarkiv serve --data-dir .data/demo >> .data/demo.log 2>&1
   ```

   Expect `collection unavailable` in `.data/demo.log`, a key explanation with a guide link on **Sources**, **Add** unavailable, and both sources in **Needs attention**. Imports and downloads must still work: import `.data/fixtures/inbox.eml` from the message list, expect **Already archived**, and download it again.

5. Stop MailArkiv and try the key paths that must be refused. Run the commands one at a time.

   ```sh
   bin/mailarkiv key create --data-dir .data/demo .data/demo/forbidden.key
   ln -s "$PWD/.data/demo" .data/demo-link
   bin/mailarkiv key create --data-dir .data/demo .data/demo-link/forbidden.key
   bin/mailarkiv key create --data-dir .data/demo .data/second.key
   bin/mailarkiv serve --data-dir .data/demo --key-file .data/demo/forbidden.key
   stat -c %a .data/second.key
   ```

   Expected: each `mailarkiv` command exits 1. The first two say the key must be outside the data directory, the third refuses to replace a file, and `serve` names the cause `key`. `stat` prints `600`.

6. Remove the leftovers and run the original start command:

   ```sh
   rm .data/demo-link .data/second.key
   ```

   Save each source's password once more, so it is encrypted with the original key. Wait for **Waiting** on both.

## Scenario 7: direct requests without a session

Covers FR-031 and SC-007. `TestEveryRouteDeniesWithoutSession` automates this for every route.

Set `id` to the number in the `Local Dovecot` source's address, `/sources/<id>`. Then send every route from [the access table](contracts/http-api.md#access) without a session:

```sh
id=1
while read -r method route; do
  curl -s -o /dev/null -w "$method $route: %{http_code}\n" -X "$method" \
    -H 'Content-Type: application/json' -d '{}' "http://localhost:8080$route"
done <<EOF
POST /api/sources/check
GET /api/sources
POST /api/sources
GET /api/sources/$id
PATCH /api/sources/$id
POST /api/sources/$id/check
POST /api/sources/$id/pause
POST /api/sources/$id/resume
POST /api/sources/$id/sync
DELETE /api/sources/$id
EOF
```

Expected: `401` on every line.

Sign in from the terminal, replacing the password. The cookie goes to `.data/demo.cookies`.

```sh
curl -s -c .data/demo.cookies -H 'Content-Type: application/json' \
  -d '{"email":"admin@example.test","password":"<administrator password>"}' \
  http://localhost:8080/api/session
```

Run the loop again with a valid session and a foreign origin. Add this line after the `curl` line:

```sh
    -b .data/demo.cookies -H 'Origin: https://evil.example' \
```

Expected: `403` for every POST, PATCH, and DELETE, and `200` for the GET requests, because the origin guard lets read-only methods through. Both sources are unchanged on **Sources**.

Check one denied body of each kind:

```sh
curl -s -X POST "http://localhost:8080/api/sources/$id/sync"
curl -s -X POST -b .data/demo.cookies -H 'Origin: https://evil.example' \
  "http://localhost:8080/api/sources/$id/sync"
rm .data/demo.cookies
```

Expected: `{"error":"no_session"}`, then `{"error":"cross_origin"}`, and nothing else.

## Scenario 8: manage sources

Covers User Story 6. Use `Local Dovecot` and leave `Marker` alone. Create a folder `Manage` with 2,000 messages, as in scenario 4:

```sh
docker exec mailarkiv-dovecot doveadm mailbox create -u test@example.test -s Manage
```

```sh
folder=Manage prefix=manage count=2000
```

1. In **Edit**, select `Manage` and save. While **Syncing**, press **Pause**. Restart MailArkiv. Expect **Paused** after the restart. Press **Resume** and expect a sync at once. If the sync finished before you paused, repeat with a new folder and prefix.
2. After the sync, run `docker pause mailarkiv-dovecot` so the next sync stays in flight. Press **Sync now** twice. The second press must say that a sync is running. Run `docker unpause mailarkiv-dovecot` and wait for **Waiting**.
3. Note the message list's total. In **Edit**, uncheck `Manage` and save. The total must stay the same, and messages from `Manage` must still open.
4. In **Edit**, enter `deliberately-wrong` as the password and save. Expect a rejection. Cancel, press **Sync now**, and check that it succeeds with the old password. Reopen **Edit**: the password field is empty. Change the name to `Local Dovecot renamed` and save with the password empty. **Sync now** must succeed again.
5. Choose **Add a source**. Enter host `LOCALHOST`, user `TEST@EXAMPLE.TEST`, TLS, port 993, and the mailbox password. Press **Connect**. Expect a duplicate-source error with a link to the existing source. Follow the link.
6. Open a message from `Manage` and note its history. Remove `Local Dovecot renamed`; the dialog must name it. It disappears from **Sources**, and its messages stay listed. The noted message's history still names the removed source.

## Scenario 9: keyboard and small screens

Covers FR-042 and SC-011. Run the browser checks:

```sh
mise run test:e2e
```

They cover adding a source by keyboard, the 360 px layout, and axe checks on the source screens in both themes. They use their own test server and data.

Automated rules can't establish full WCAG 2.2 AA conformance, so also spend 15 minutes with a screen reader, such as Orca, on the running demo. Scenario 8 removed the original source, so its server and user name are free again.

1. Set the browser's viewport to 360 CSS pixels in developer tools. From **Sources**, use only Tab, Shift+Tab, Enter, and Space to add a source with the mailbox details, select both first folders, and save it as `Keyboard Dovecot`.
2. Follow the first sync with the screen reader. Progress and state changes must be announced without moving focus. Open the source, open **Edit** and cancel, then open removal and cancel. Focus must return to the control that opened each dialog, and Escape must close them.
3. Check that controls, folder names, errors, and the removal warning are readable at 360 px without horizontal scrolling. Repeat in the other theme by switching the system appearance, and again at desktop width.
4. Record the browser, screen reader, viewport, themes, and any unannounced state or unreachable control.

## Clean up

Stop MailArkiv with Ctrl-C. Then remove the Dovecot server, its mail, and everything this guide created:

```sh
docker rm -f mailarkiv-dovecot
docker volume rm mailarkiv-dovecot-mail
rm -rf .data/demo .data/demo.log .data/dovecot .data/dovecot-password .data/fixtures \
  .data/mailbox-before.txt .data/mailbox-after.txt \
  .data/ca.pem .data/ca.key .data/localhost.pem .data/localhost.key
```

This keeps `.data/dev` and `.data/mailarkiv.key`, which `mise run dev` uses.

## Issue #4 acceptance

| Issue criterion                                                                   | Shown by                                                        |
| --------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| Interrupting sync and restarting does not skip safely available messages          | `TestCollectCrashMatrix`, `TestShutdownDuringSync`, scenario 4  |
| Re-sync avoids incorrect duplicates                                               | `TestEntryAndCopyCounts`, `TestCollectCrashMatrix`, scenario 4  |
| Progress reflects stored messages rather than merely fetched headers              | `TestProgressCountsDurable`, scenario 1                         |
| Stalled network calls time out, expose the failure, and leave the checkpoint safe | `TestStallAtEachStep`, `TestNoMarkersInLogOrStatus`, scenario 5 |
| A copy of the data directory without the key does not reveal IMAP passwords       | `TestNoPasswordInDataDir`, scenario 6                           |
| A user can complete the workflow through the UI                                   | `sources.spec.ts`, scenario 1 timed                             |

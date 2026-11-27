# Operator guide

This guide is for the person who runs MailArkiv on a server. It covers building it, running it, managing accounts, putting it behind HTTPS, and what this version does and doesn't promise.

MailArkiv is built in seven slices toward its first release. Slices 1 to 3 are done. MailArkiv imports `.eml` files, collects mail from IMAP mailboxes, lists stored messages with their sender, subject, and date, searches them, shows each one in a preview that can't run code or reach the internet, saves attachments, and downloads the exact original bytes. IMAP collection has been tested against Dovecot 2.4.2, not against hosted providers. Deletion and backup come in later slices. [What this version doesn't promise](#what-this-version-doesnt-promise) lists the limits.

## What you need

- Linux on amd64.
- `git` and [mise](https://mise.jdx.dev). mise installs Go, Rust, bun, and Node for the build.
- `musl-gcc`, which builds the search program. On Debian or Ubuntu, run `sudo apt-get install musl-tools`.
- A local filesystem for the archive that honors flush requests. See [Storage](#storage).
- HTTPS through a reverse proxy, unless people only use MailArkiv on the server itself. See [HTTPS and the reverse proxy](#https-and-the-reverse-proxy).
- For up to 5,000,000 messages, 4 cores and 8 GiB of memory. See [Supported size](#supported-size).

## Build

There are no release packages yet. Build from source:

```sh
git clone https://github.com/maltehedderich/mailarkiv.git
cd mailarkiv
mise trust
mise install
mise run setup
mise run build
```

`mise run setup` stops with an install hint when `musl-gcc` is missing, and also downloads Chromium for the browser tests. The build writes two static binaries:

- `bin/mailarkiv`, the server, with the web interface inside it.
- `bin/mailarkiv-search`, the search program, which the server starts and supervises.

Copy both to the same directory on the server:

```sh
sudo install -m 0755 bin/mailarkiv bin/mailarkiv-search /usr/local/bin/
```

The server looks for `mailarkiv-search` only beside its own executable. Without it, the server still runs: capture, the list, previews, and downloads work, and search shows Unavailable with Search process didn't start.

## Set up the server

Run MailArkiv as its own user, which owns the data directory:

```sh
sudo useradd --system --home-dir /var/lib/mailarkiv --shell /usr/sbin/nologin mailarkiv
sudo install -d -o mailarkiv -g mailarkiv -m 0700 /var/lib/mailarkiv
```

Create the first account as that user. The command asks for the password twice:

```sh
sudo -u mailarkiv mailarkiv account create --data-dir /var/lib/mailarkiv admin@example.test
```

Then start the server:

```sh
sudo -u mailarkiv mailarkiv serve --data-dir /var/lib/mailarkiv
```

It listens on `127.0.0.1:8080`, so nothing outside the server can reach it. Open `http://localhost:8080` on the server, or put a reverse proxy in front. To listen elsewhere, add `--listen ADDR`, such as `--listen 127.0.0.1:9000`.

`--data-dir` is required on every command. There's no default, so a wrong working directory can't start a second, empty archive.

### Run it as a service

A systemd unit at `/etc/systemd/system/mailarkiv.service`:

```ini
[Unit]
Description=MailArkiv
After=network.target

[Service]
User=mailarkiv
ExecStart=/usr/local/bin/mailarkiv serve --data-dir /var/lib/mailarkiv
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now mailarkiv
journalctl -u mailarkiv
```

On SIGTERM or SIGINT, the server gives requests in progress 5 seconds, then exits with code 0. Within the same 5 seconds it stops processing, indexing, the search process, and the parse workers. A batch that the index hadn't saved is sent again after the next start.

### Startup

At each start, `serve`:

1. Creates the data directory if it's missing, readable only by its user.
2. Takes the lock on `<data-dir>/lock`, so a second server on the same directory refuses to start.
3. Checks that it can write to `tmp/` and `blobs/`.
4. Opens the catalog and updates its tables.
5. Runs recovery. See [Recovery](#recovery).
6. Creates `index/` if it's missing, and removes any index directory the catalog doesn't name.
7. Starts the parse workers. See [Processing](#processing).
8. Starts listening.
9. Starts processing and the search process in the background, so a slow or failing search process doesn't delay serving.

If a step fails, the server exits with code 1 and logs `startup failed` with a `cause`:

| `cause`   | What failed                                                                |
| --------- | -------------------------------------------------------------------------- |
| `storage` | The data directory, `tmp/`, or `blobs/` can't be created, written, or read |
| `catalog` | The catalog can't be opened, or a newer version of MailArkiv wrote it      |
| `lock`    | Another server holds the lock on this data directory                       |
| `port`    | The listen address is taken or not valid                                   |

Code 2 means the arguments were wrong, and the usage text follows.

Steps 6 and 9 never stop startup. If `index/` can't be created, the server runs without search, which shows Unavailable with Index write failed until a restart after the cause is fixed.

## Accounts and passwords

Accounts are managed only on the server. The web interface can't create an account or reset a password, and MailArkiv sends no email.

```sh
sudo -u mailarkiv mailarkiv account create --data-dir /var/lib/mailarkiv EMAIL
sudo -u mailarkiv mailarkiv account set-password --data-dir /var/lib/mailarkiv EMAIL
```

- **Which user:** run both as the user that owns the data directory, the same one that runs `serve`. Run as anyone else, including root, they refuse with "Run this as the user that owns the data directory." A root-run command could leave catalog files that the server's user can't open.
- **Passwords:** at least 12 characters, never given as an argument. On a terminal, the command asks twice with echo off. Without a terminal, it reads one line from stdin, so a script can pipe the password in.
- **Addresses:** stored in lowercase. `Admin@example.test` creates and signs in as `admin@example.test`. An address can't be changed later.
- **While the server runs:** both commands work without stopping it. `set-password` ends all of that account's sessions and lifts any sign-in block on the address.
- **Sign-in limit:** 10 failed sign-ins for one address within 15 minutes block that address for 15 minutes, whether or not the account exists. Setting a new password lifts the block.
- **Sessions:** a session ends at sign-out, 12 hours after sign-in, or when the account's password is set.
- **Removing someone:** accounts can't be removed. To shut someone out, set a password they don't know. Their address stays in the import history of the mail they imported.
- **A mistyped address:** create a new account with the right address, then set an unknown password on the wrong one.

Every account sees the whole archive. There are no roles.

## The key

IMAP collection needs a key file outside the data directory. Create it as the user that runs MailArkiv, in a directory that user owns:

```sh
sudo install -d -o mailarkiv -g mailarkiv -m 0700 /etc/mailarkiv
sudo -u mailarkiv mailarkiv key create --data-dir /var/lib/mailarkiv /etc/mailarkiv/mailarkiv.key
```

Then start the server with the key:

```sh
sudo -u mailarkiv mailarkiv serve --data-dir /var/lib/mailarkiv --key-file /etc/mailarkiv/mailarkiv.key
```

In the [systemd unit](#run-it-as-a-service), the line becomes:

```ini
ExecStart=/usr/local/bin/mailarkiv serve --data-dir /var/lib/mailarkiv --key-file /etc/mailarkiv/mailarkiv.key
```

The key command refuses to overwrite an existing file and creates new files with mode 0600. Keep the key readable only by the service user, and back it up separately from the archive. Anyone who can read both the key and the archive can read the saved IMAP passwords. Paths inside the data directory, including symbolic links into it, are refused; hard links into it are not detected.

Open **Sources → Add a source**, enter the mailbox details, press **Connect**, select folders, and save. The account created with `account create` signs you into MailArkiv; the source holds your IMAP credentials.

Without `--key-file`, Sources explains that collection needs the key. No mail is collected and no source can be added, but imports and downloads still work. Startup logs `collection unavailable` with reason `no key file given`.

If the key is lost, every source's IMAP password must be saved again. To replace it, stop the server, create a key at a new path outside the data directory, and start with that path. Each source shows **Needs attention** until you save its password again. Archived messages remain available. There is no command to re-encrypt saved passwords under a new key.

For development, `mise run dev` uses `.data/dev` for the archive and `.data/mailarkiv.key` for its key. It creates the key if missing and reuses it on later starts.

## Imports and what a result means

Administrators import one file at a time, up to 150 MB, which here means 150,000,000 bytes. MailArkiv refuses a file that is empty, over the limit, or doesn't start with a mail header line. It doesn't reject or change a file for any other defect. An mbox file passes the check and is stored as one message.

Every import ends in one of six results:

| Result           | What it means                                                                                                                                                                            |
| ---------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Archived         | The message is stored. The original bytes and the catalog entry were flushed to disk before the server answered, so a crash or power loss after this result loses neither                |
| Already archived | The same bytes were archived before. The import was added to the message's history, and no second copy was stored                                                                        |
| Restored         | The same bytes arrived while the stored original was missing or damaged. The stored copy was replaced from this file and flushed to disk like an Archived one                            |
| Refused          | The file is empty, over the limit, or has no mail header line. Nothing was stored                                                                                                        |
| Failed           | The import didn't finish, and the file is not archived. The status screen and the log say why                                                                                            |
| Unconfirmed      | The browser got no answer from MailArkiv, for example after a dropped connection or a proxy timeout. The file may be archived. Importing it again is safe and never stores a second copy |

The guarantee behind Archived and Restored holds only on a filesystem that honors flush requests.

Before a download starts, the server checks the stored original against its content hash. If the file is missing or damaged, the page shows "This message wasn't downloaded" in a notification instead of saving anything, and the failure appears on the status screen and in the log. Importing the same file again restores the original.

## Processing

After each capture, MailArkiv reads the message in the background: sender, recipients, subject, date, bodies, and attachments. After an upgrade, messages archived by an earlier version are read the same way. Until a message is read, its row shows the capture data and Not processed yet. At 5,000,000 messages, reading everything and building the first index took 6 hours on the [sizing host](#supported-size).

Each message is processed once and ends in one of three states:

| State                   | What it means                                                                                                                               |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| Processed               | Every part was read                                                                                                                         |
| Processed with problems | Some parts couldn't be read, or a bound below was reached. The rest shows and is searchable                                                 |
| Couldn't be read        | The header couldn't be read, or reading passed its time limit. The row keeps the capture data, and no search with words or filters finds it |

A message whose stored original is missing or damaged stays Not processed yet. It's tried again at the next start, and after an import restores the original.

The status screen counts the messages in each state and links to **Processing problems**, the list of messages Processed with problems or Couldn't be read. They don't add to the problem count: malformed mail, mostly spam, is common and permanent, and counting it would hide real failures.

Processing never changes, moves, or deletes an original or a history entry, and it never delays or refuses a capture.

### Parse workers

Mail is read in worker processes, so a hostile message can't hang or crash the server. A worker is the server's own binary, started as `mailarkiv parse-worker`. It reads only what the server sends it and opens no file or connection. Don't run it yourself.

| Bound                                 | Value                                                                                      |
| ------------------------------------- | ------------------------------------------------------------------------------------------ |
| Time per message                      | 60 seconds. The worker is killed, and the message becomes Couldn't be read                 |
| Parts per message                     | 1,000. Later parts are left out                                                            |
| Nesting depth                         | 20 levels. Deeper parts are left out                                                       |
| Text and HTML bodies                  | The first 2,000,000 bytes of each are searched and shown. The rest is only in the original |
| Workers                               | 2 for background processing and 2 for opening messages and attachments                     |
| Wait for a worker when opening a view | 30 seconds. Then the view says both workers are busy                                       |

A worker that dies on a message is replaced, and the message is tried once more in a new worker. If that one dies too, the message becomes Couldn't be read. Each worker that dies or passes 60 seconds logs `processing_worker_stopped` with `message_id` and `exit`.

Workers have no memory limit. A 150 MB original can make a worker allocate several times that, so leave memory free for it. The kernel's out-of-memory killer normally picks the largest process, which is then the worker, but that isn't guaranteed.

## Search

Search is on the messages page. **Words** matches the subject, sender, recipients, body text, and attachment names. **Filters** opens the rest: From, To, Subject, Attachment name, First day and Last day, and Has attachments. To covers To, Cc, and Bcc. A message must match every word and every filter.

How words match:

- Capital letters are ignored: `quartals` finds "Quartalsbericht" and "QUARTALS".
- Accents are not: `müller` finds "Müller" and "MÜLLER", but not "Muller".
- A word of two or more characters also matches longer words that start with it. A word of one character matches only itself, so `a` doesn't find "apfel".
- Spaces and punctuation separate words. There are no phrases, operators, or wildcards: `OR` is an ordinary word, and quotes and `-` are punctuation. A search with no letters or digits and no filter shows the full list.
- Words over 100 bytes aren't indexed. In mail, they are almost always encoded data.

In From and To:

- A complete address, such as `anna@example.com`, finds exactly that address.
- A domain, such as `example.com` or `@example.com`, finds every address at that domain. It doesn't find `mail.example.com` or `attacker-example.com`.
- Anything else matches as words in that field.

Each message has a list date. It's the date in the Date header, unless that header is missing, can't be read, or is more than 24 hours after MailArkiv first archived the message. Then it's the first archive time, and the row marks it as the archive time. The list and the results are in list date order, newest first. The date range includes its first and last day, in the browser's time zone. Has attachments ignores embedded images such as signature logos.

What search covers:

- **Covered:** subject, sender, recipients, the first 2,000,000 bytes of each text and HTML body, and attachment names.
- **Not covered:** attachment contents and the contents of attached messages. Messages that Couldn't be read match nothing; **Processing problems** lists them.

Limits:

- Results come 50 to a page, up to page 2,000, which is the first 100,000 results. The total still counts every match. Add words or filters to reach later ones.
- A search stops after 10 seconds, and the page says "This search took too long. Add words or filters."
- Results say how many messages they can't include yet: those not processed yet, waiting to be indexed, or not yet reached by a build.
- When nothing else waits, a new message is searchable within 60 seconds.

Search words and filters travel in request bodies, never in a page or request address, so reverse proxy logs and browser history don't record them. The log and the status screen don't contain them either. The browser keeps the last search and its results in the tab's session storage until the tab closes, so Back from a message shows them again.

## Messages, the preview, and attachments

Opening a message shows its sender, recipients, date, state and problems, attachments, and body, with links to download the original and open its history. The HTML body shows in a preview. When the message also has a text body, you can switch to it. Plain text shows as text, and links in it can't be clicked.

The preview guarantees, in Chromium, the only browser it has been tested in:

- No script or plugin runs, no form submits, and no window or dialog opens. The preview can't navigate MailArkiv, change the page around it, or read the session or any other MailArkiv data.
- Nothing loads from the internet: no images, style sheets, fonts, frames, or prefetch hints. Opening the preview's own address, `/api/messages/<id>/html`, in a tab gives the same isolation.
- Links don't open. MailArkiv removes their targets. To follow a link, copy it from the plain text, which shows addresses as text.
- Embedded images in PNG, JPEG, GIF, and WebP show in place, up to 10,000,000 decoded bytes per message. The preview says when some were left out for the limit. Other formats, such as SVG and TIFF, and damaged images don't show. Every embedded image is listed as an attachment either way.
- Styles written inside the message apply.

When the HTML refers to images on the internet, the view says they aren't loaded and offers **Load images**. The choice covers only that view of that message and isn't remembered. After loading:

- Only images load, always over HTTPS, and without the preview's address in a `Referer` header.
- Loading can tell the sender, or a tracking service, when and from which network address the message was opened. For phishing and spam, it confirms that someone reads the mailbox. Open other people's mail with that in mind.
- The images weren't archived. They show as they are now, not as they arrived, and may have changed or be gone.

The offer comes from scanning `img`, `srcset`, `background`, and CSS `url(` references. A reference the scan misses still can't load, but the message may then show no offer.

Each attachment shows its name, type, size, and whether its encoding is damaged. **Save** always downloads the decoded bytes. The browser never displays an attachment, even when someone opens its address directly. The saved name drops control characters, bidirectional characters, `/`, and `\`. It's `attachment-<position>` when the part has no name, and an attached message gets `.eml`. A damaged attachment saves the bytes that could be decoded. MailArkiv doesn't read attachment contents.

## The search index

The index lives in `<data-dir>/index/`, one directory per index. It holds the words of every message's subject, addresses, bodies, and attachment names, so it needs the same protection as the rest of the data directory. The server creates it readable only by its user.

The archive stays the source of truth. Backups can skip `index/`, because the server builds a missing index at start.

### The automatic build

When the server starts without an index, it builds one without anyone acting. The status screen shows Building with messages done out of messages to do. Search answers from what's built so far, and results say how many messages they can't include yet.

### Rebuild

On the status screen, under **Search**, press **Rebuild index** and confirm. Search answers from the current index until the new one is complete. Then the new one replaces it in one step, and the old one's files are removed. Messages captured during the rebuild are searchable once it finishes. A restart continues the rebuild from its last saved batch, and only one runs at a time. At 5,000,000 messages, a rebuild took 2 hours 44 minutes on the [sizing host](#supported-size).

Both indexes exist until the switch, so a rebuild starts only when free space where the index lives is above 2 GiB plus the current index's size. A refused rebuild says how much space it needs and how much is free.

### Deleting the index

Stop the server first. Deleting the index while the server runs isn't supported. Then remove `index/` and start again, which starts the automatic build:

```sh
sudo systemctl stop mailarkiv
sudo -u mailarkiv rm -rf /var/lib/mailarkiv/index
sudo systemctl start mailarkiv
```

This is also the recovery when a damaged index crashes the search process at every start, which keeps a rebuild from running.

### Low disk space

The index shares the disk with the archive. When free space where the index lives falls below 2 GiB, indexing and rebuilding pause, and the status screen shows Paused with the free space. Capture continues, and search keeps answering. Indexing and rebuilding resume once 3 GiB is free. MailArkiv reads free space before each batch and every 30 seconds.

### The search process

`mailarkiv-search` holds the index. The server starts it, without arguments, with the data directory as its working directory. They talk only over pipes. The search process has no network address and no socket, so no other account on the host can connect to it. It ends when the server does.

If it stops, the server starts it again after 1 second, doubling the wait after each further failure up to 60 seconds. The wait drops back to 1 second once it has run for 10 minutes. While it's down, search shows Unavailable with the next start time, and capture, the list, previews, and downloads keep working. After a restart, indexing continues where it stopped, with no message lost or indexed twice.

MailArkiv has the search process merge the index's files after every 50 commits, or after 10 minutes, whichever comes first.

### Needs rebuild

MailArkiv marks the index Needs rebuild and stops indexing after:

- 5 search process crashes within 30 minutes,
- 3 failed merges of index files in a row, or
- an index whose files exist but can't be opened.

Search keeps answering when it can, with a notice that results may be incomplete. The mark survives a restart. A finished rebuild clears it, and so does the build that follows [deleting the index](#deleting-the-index). Until that build finishes, the status screen still shows Needs rebuild with indexing Stopped.

## Supported size

Search supports archives of up to 5,000,000 messages, on a host with 4 cores and 8 GiB of memory. The sizing test on 2026-11-26 ran MailArkiv's own import, processing, indexing, and search on 5,000,000 synthetic messages averaging 7.9 KB, on such a host, with 7.25 GiB of its memory visible to Linux:

| Measure                                      | Result             | Target         |
| -------------------------------------------- | ------------------ | -------------- |
| 95th percentile search, without prefix words | 211 ms             | Under 1 second |
| 95th percentile search, with prefix words    | 461 ms             | Under 1 second |
| Full rebuild                                 | 2 hours 44 minutes | Within 8 hours |
| Peak memory allocated by the search process  | 774 MiB            | Under 1 GiB    |
| Index size                                   | 2.43 GiB           |                |
| Processing and first index build             | 5 hours 59 minutes |                |

Every target held, so 5,000,000 is the supported size. Larger archives haven't been tested. The slowest single search was a 2-character prefix, which matches nearly every message: its 95th percentile was 2.1 seconds. [The sizing results](research/search-sizing/README.md) have the method and every number.

## Storage

The archive lives in the data directory. Keep the password key outside it:

| Path                                             | Holds                                                                                                   |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------- |
| `catalog.db`, `catalog.db-wal`, `catalog.db-shm` | The catalog, in SQLite: accounts, sessions, messages, and each message's import history                 |
| `blobs/ab/cd/<hash>`                             | Each original's exact bytes, named by the SHA-256 of those bytes. `sha256sum` on a file prints its name |
| `tmp/`                                           | Imports in progress. Recovery removes anything left here                                                |
| `index/`                                         | The search index. Delete it only with the server stopped. See [The search index](#the-search-index)     |
| `lock`                                           | Held by the running server                                                                              |

Only the owning user can read the directory and its files.

Keep the whole data directory on one local filesystem that honors flush requests. A network filesystem, a disk cache that drops flushes, or a mount option that skips them can lose a confirmed import in a power loss. Don't mount `tmp/` or `blobs/` separately: a finished import moves its file from `tmp/` into `blobs/`, and that move only works within one filesystem.

MailArkiv has no backup until Slice 6. If you copy the data directory yourself, stop the server first and copy the whole directory, so the catalog and the originals match. You can leave out `index/`: the server builds a new one at start.

## Recovery

At each start, before it accepts imports, the server:

- Removes leftover files in `tmp/` from imports that never finished.
- Finds unreferenced originals: stored files with no catalog entry, usually left by a crash between storing the file and writing its catalog entry.

Recovery never deletes a stored original. The status screen lists each unreferenced original by hash and size. Importing the same file again puts it back in the list and uses the stored copy.

## Status screen and log

The status screen shows the latest start, what recovery found, unreferenced originals, and every capture, recovery, download, search, and index failure since that start. Each signed-in page shows how many problems the status screen lists. A restart clears the failures from the screen, and the log keeps them.

Its **Search** section shows:

- The search state: Available, Unavailable with the next start time, or Needs rebuild with its cause and since when.
- The indexing state: Up to date, Catching up, Building or Rebuilding with progress, Paused with the free space, or Stopped while search is Unavailable or Needs rebuild.
- How many messages wait to be indexed, and how many times the search process restarted since the server started.
- **Rebuild index**, unless search is Unavailable, a rebuild is running, or the first index is still being built.

Its **Processing** section counts messages Not processed yet, Processed with problems, and Couldn't be read.

The problem count adds each failure, unreferenced original, and source with a problem, plus 1 while search is Unavailable or Needs rebuild and 1 while indexing is Paused. When a search or index failure repeats with no success in between, the screen shows one entry with a count and the last time, and the count adds every repeat. A search process that fails for days can therefore show a large count.

The log is JSON lines on stderr, one object per event, with `time`, `level`, and `msg`. Failures have `"msg":"failure"` and a `category`:

| `category`                | Next step                                                                                         |
| ------------------------- | ------------------------------------------------------------------------------------------------- |
| `storage_full`            | Free disk space, then import the file again                                                       |
| `storage_write_failed`    | Fix the storage location, such as its permissions, then import the file again                     |
| `catalog_write_failed`    | Import the same file again. If this repeats, check the rest of the log                            |
| `stored_original_missing` | Import the same file again, if you still have it, to restore the original                         |
| `stored_original_damaged` | Import the same file again, if you still have it, to restore the original                         |
| `temp_file_not_removed`   | Check the permissions on `tmp/`. The file is harmless, and recovery tries again at the next start |

Where they apply, entries carry `message_id` and `content_hash`. Recovery logs `recovery finished` with its counts and `duration_ms`, and one `unreferenced original` line for each.

Search and index failures have `"msg":"search failure"`, a `category`, and the internal IDs and closed causes in the third column. Each repeat logs its own line.

| `category`                      | What happened                                                                                                                                                                        | Next step                                                                                   |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------- |
| `search_process_stopped`        | The search process exited or crashed. `exit_status` or `signal`                                                                                                                      | None if it passes. If it repeats, check the rest of the log                                 |
| `search_process_did_not_start`  | It couldn't be started. `cause` is `missing`, `not_executable`, or `bad_hello`, the last when it gave no valid first answer within 10 seconds, as a program from another build would | Install `mailarkiv-search` from the same build beside `mailarkiv`                           |
| `search_process_did_not_answer` | A call passed its time limit. `operation` names it. Indexing sends the batch again                                                                                                   | If it repeats, check the rest of the log and the host's free memory                         |
| `index_write_failed`            | A batch couldn't be saved. `generation_id`, `first_message_id`, `last_message_id`, and `cause`: `no_space`, `permission_denied`, or `other`                                          | Fix the index location, such as its permissions. The batch is sent again with growing waits |
| `index_maintenance_failed`      | Merging index files failed. `generation_id`                                                                                                                                          | If search shows Needs rebuild, start a rebuild                                              |
| `index_needs_rebuild`           | The index was marked Needs rebuild. `cause`: `search_process_stopped`, `index_maintenance_failed`, or `index_damaged`                                                                | Start a rebuild on the status screen                                                        |
| `low_disk_space`                | Free space where the index lives fell below 2 GiB. `free_bytes`                                                                                                                      | Free disk space                                                                             |
| `rebuild_failed`                | A rebuild stopped with an error other than low disk space or a restart. `rebuild_id`, `generation_id`. The old index keeps answering                                                 | Start the rebuild again. If it repeats, check the rest of the log                           |

Other lines about search and processing:

| `level` | `msg`                           | Fields                                    | When                                                                    |
| ------- | ------------------------------- | ----------------------------------------- | ----------------------------------------------------------------------- |
| INFO    | `search process started`        | `restarts`                                | Each start of the search process                                        |
| INFO    | `search state`                  | `state`, `indexing`                       | Each change of the search or indexing state                             |
| INFO    | `rebuild started`               | `rebuild_id`, `generation_id`             | An administrator started a rebuild                                      |
| INFO    | `rebuild finished`              | `rebuild_id`, `generation_id`, `messages` | The new index replaced the old one                                      |
| WARN    | `search process log`            | `log_level`, `target`                     | The search process reported a warning or error, without its text        |
| ERROR   | `search process panic`          | `location`                                | The search process panicked, at that source file and line               |
| WARN    | `search process output ignored` | `lines`                                   | It wrote other lines to its standard error, which aren't logged         |
| ERROR   | `processing_worker_stopped`     | `message_id`, `exit`                      | A parse worker died or passed 60 seconds                                |
| WARN    | `processing skipped`            | `message_id`, `category`                  | A stored original was missing or damaged. Its failure is logged as well |

Neither the log nor the status screen contains subjects, mail addresses, names, message text, attachment or file names, search words or filters, passwords, session tokens, or account addresses. That includes anything the search process reports: it sends only closed codes and source locations. Processing results and problems aren't logged. Sign-in attempts and successful requests aren't logged.

## HTTPS and the reverse proxy

The session cookie is marked `Secure`, so browsers keep it only over HTTPS or on `localhost`. Over plain HTTP at any other address, the sign-in page says HTTPS is required.

MailArkiv doesn't serve HTTPS itself. Choose one:

- **Only on the server:** open `http://localhost:8080` there. From another machine, forward the port over SSH with `ssh -L 8080:127.0.0.1:8080 you@server`, then open `http://localhost:8080` locally.
- **Through a reverse proxy:** the proxy serves HTTPS and passes requests to MailArkiv. Serve MailArkiv at the root of its own host name, such as `https://archive.example.test/`, not under a path like `/mailarkiv/`.

### nginx

```nginx
server {
    listen 443 ssl;
    server_name archive.example.test;

    ssl_certificate     /etc/ssl/archive.example.test/fullchain.pem;
    ssl_certificate_key /etc/ssl/archive.example.test/privkey.pem;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Host $host;

        client_max_body_size 150m;
        proxy_request_buffering off;
    }
}
```

- `client_max_body_size 150m` lets uploads up to the 150 MB limit through. nginx's default of 1 MiB refuses most messages with attachments. MailArkiv shows such an import as Failed and names the proxy's upload limit as the cause.
- `proxy_request_buffering off` streams each upload to MailArkiv as it arrives, instead of first writing a second copy to nginx's temp directory. The import screen then shows upload progress, then a separate storing state.
- `proxy_http_version 1.1` keeps streaming on for uploads sent in chunks. It's the default from nginx 1.29.7.
- `proxy_set_header Host $host` passes the browser's host name, which the server compares with the `Origin` and `Referer` headers.

Another proxy needs the same things: an upload limit of at least 150,000,000 bytes, uploads streamed rather than buffered, and the original host name passed through.

A proxy that answers with a timeout or a bad-gateway error, for example while MailArkiv is stopped, makes an import end in Unconfirmed.

If the proxy runs on another machine, set `--listen` to an address that only the proxy can reach. Traffic between the proxy and MailArkiv is plain HTTP and carries passwords and mail.

## Browsers

MailArkiv has been tested in Chromium. Firefox and Safari haven't been checked yet. That includes the preview's isolation: each browser implements the sandbox and the content policy on its own.

Safari may refuse the session cookie over plain HTTP on `localhost`. If signing in there brings you back to the sign-in page, use HTTPS or another browser.

## What this version doesn't promise

- **No tamper-proof storage.** Anyone who can write to the data directory can change or delete originals and the catalog. A changed original is caught when someone downloads it, not prevented.
- **No encryption of archived mail by MailArkiv.** Originals are plain files and the catalog is a plain SQLite database. Only saved IMAP passwords are encrypted with the separate key. Use disk encryption if you need it.
- **No backup** until Slice 6.
- **No deletion** until Slice 5. An imported message can't be removed through MailArkiv, and the import screen says so before each import.
- **No splitting of mbox files.** An mbox file is stored as one message.
- **No email from MailArkiv,** so no password reset by email. The operator sets new passwords on the server.
- **No search of attachment contents** or of attached messages' contents.
- **No second reading of a message.** Each message is processed once. A later version that reads mail better won't reread messages already processed.
- **No check of who sent a message.** Rows show the From address, and it can be forged as easily as the name. MailArkiv doesn't read SPF, DKIM, or DMARC results.

You provide HTTPS and a local filesystem that honors flush requests. The durability promise depends on both.

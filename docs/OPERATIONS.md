# Operator guide

This guide is for the person who runs MailArkiv on a server. It covers building it, running it, managing accounts, putting it behind HTTPS, and what this version does and doesn't promise.

MailArkiv is built in seven slices toward its first release, and this is Slice 1. It imports one `.eml` file at a time through the web interface, lists what it stores, and gives back the exact original bytes on download. Search, mail collection, deletion, and backup come in later slices. [What this version doesn't promise](#what-this-version-doesnt-promise) lists the limits.

## What you need

- Linux on amd64.
- `git` and [mise](https://mise.jdx.dev). mise installs Go, bun, and Node for the build.
- A local filesystem for the archive that honors flush requests. See [Storage](#storage).
- HTTPS through a reverse proxy, unless people only use MailArkiv on the server itself. See [HTTPS and the reverse proxy](#https-and-the-reverse-proxy).

## Build

There are no release packages yet. Build the binary from source:

```sh
git clone https://github.com/maltehedderich/mailarkiv.git
cd mailarkiv
mise trust
mise install
mise run setup
mise run build
```

`mise run setup` also downloads Chromium for the browser tests. The build writes `bin/mailarkiv`, a static binary with the web interface inside it. Copy it to the server:

```sh
sudo install -m 0755 bin/mailarkiv /usr/local/bin/mailarkiv
```

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
Description=MailArkiv email archive
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

On SIGTERM or SIGINT, the server gives requests in progress 5 seconds, then exits with code 0.

### Startup

At each start, `serve`:

1. Creates the data directory if it's missing, readable only by its user.
2. Takes the lock on `<data-dir>/lock`, so a second server on the same directory refuses to start.
3. Checks that it can write to `tmp/` and `blobs/`.
4. Opens the catalog and updates its tables.
5. Runs recovery. See [Recovery](#recovery).
6. Starts listening.

If a step fails, the server exits with code 1 and logs `startup failed` with a `cause`:

| `cause` | What failed |
| ------- | ----------- |
| `storage` | The data directory, `tmp/`, or `blobs/` can't be created, written, or read |
| `catalog` | The catalog can't be opened, or a newer version of MailArkiv wrote it |
| `lock` | Another server holds the lock on this data directory |
| `port` | The listen address is taken or not valid |

Code 2 means the arguments were wrong, and the usage text follows.

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

## Imports and what a result means

Administrators import one file at a time, up to 150 MB, which here means 150,000,000 bytes. MailArkiv refuses a file that is empty, over the limit, or doesn't start with a mail header line. It doesn't reject or change a file for any other defect. An mbox file passes the check and is stored as one message.

Every import ends in one of six results:

| Result | What it means |
| ------ | ------------- |
| Archived | The message is stored. The original bytes and the catalog entry were flushed to disk before the server answered, so a crash or power loss after this result loses neither |
| Already archived | The same bytes were archived before. The import was added to the message's history, and no second copy was stored |
| Restored | The same bytes arrived while the stored original was missing or damaged. The stored copy was replaced from this file and flushed to disk like an Archived one |
| Refused | The file is empty, over the limit, or has no mail header line. Nothing was stored |
| Failed | The import didn't finish, and the file is not archived. The status screen and the log say why |
| Unconfirmed | The browser got no answer from MailArkiv, for example after a dropped connection or a proxy timeout. The file may be archived. Importing it again is safe and never stores a second copy |

The guarantee behind Archived and Restored holds only on a filesystem that honors flush requests.

Before a download starts, the server checks the stored original against its content hash. If the file is missing or damaged, the page shows "This message wasn't downloaded" in a notification instead of saving anything, and the failure appears on the status screen and in the log. Importing the same file again restores the original.

## Storage

Everything lives in the data directory:

| Path | Holds |
| ---- | ----- |
| `catalog.db`, `catalog.db-wal`, `catalog.db-shm` | The catalog, in SQLite: accounts, sessions, messages, and each message's import history |
| `blobs/ab/cd/<hash>` | Each original's exact bytes, named by the SHA-256 of those bytes. `sha256sum` on a file prints its name |
| `tmp/` | Imports in progress. Recovery removes anything left here |
| `lock` | Held by the running server |

Only the owning user can read the directory and its files.

Keep the whole data directory on one local filesystem that honors flush requests. A network filesystem, a disk cache that drops flushes, or a mount option that skips them can lose a confirmed import in a power loss. Don't mount `tmp/` or `blobs/` separately: a finished import moves its file from `tmp/` into `blobs/`, and that move only works within one filesystem.

MailArkiv has no backup until Slice 7. If you copy the data directory yourself, stop the server first and copy the whole directory, so the catalog and the originals match.

## Recovery

At each start, before it accepts imports, the server:

- Removes leftover files in `tmp/` from imports that never finished.
- Finds unreferenced originals: stored files with no catalog entry, usually left by a crash between storing the file and writing its catalog entry.

Recovery never deletes a stored original. The status screen lists each unreferenced original by hash and size. Importing the same file again puts it back in the list and uses the stored copy.

## Status screen and log

The status screen shows the latest start, what recovery found, unreferenced originals, and every capture, recovery, and download failure since that start. Each signed-in page shows how many problems the status screen lists. A restart clears the failures from the screen, and the log keeps them.

The log is JSON lines on stderr, one object per event, with `time`, `level`, and `msg`. Failures have `"msg":"failure"` and a `category`:

| `category` | Next step |
| ---------- | --------- |
| `storage_full` | Free disk space, then import the file again |
| `storage_write_failed` | Fix the storage location, such as its permissions, then import the file again |
| `catalog_write_failed` | Import the same file again. If this repeats, check the rest of the log |
| `stored_original_missing` | Import the same file again, if you still have it, to restore the original |
| `stored_original_damaged` | Import the same file again, if you still have it, to restore the original |
| `temp_file_not_removed` | Check the permissions on `tmp/`. The file is harmless, and recovery tries again at the next start |

Where they apply, entries carry `message_id` and `content_hash`. Recovery logs `recovery finished` with its counts and `duration_ms`, and one `unreferenced original` line for each.

Neither the log nor the status screen contains subjects, mail addresses, message text, attachment or file names, passwords, session tokens, or account addresses. Sign-in attempts and successful requests aren't logged.

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

MailArkiv has been tested in Chromium. Firefox and Safari haven't been checked yet.

Safari may refuse the session cookie over plain HTTP on `localhost`. If signing in there brings you back to the sign-in page, use HTTPS or another browser.

## What this version doesn't promise

- **No tamper-proof storage.** Anyone who can write to the data directory can change or delete originals and the catalog. A changed original is caught when someone downloads it, not prevented.
- **No encryption by MailArkiv.** Originals are plain files and the catalog is a plain SQLite database. Use disk encryption if you need it.
- **No backup** until Slice 7.
- **No deletion** until Slice 6. An imported message can't be removed through MailArkiv, and the import screen says so before each import.
- **No splitting of mbox files.** An mbox file is stored as one message.
- **No email from MailArkiv,** so no password reset by email. The operator sets new passwords on the server.

You provide HTTPS and a local filesystem that honors flush requests. The durability promise depends on both.

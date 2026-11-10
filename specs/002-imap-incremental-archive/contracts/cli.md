# Command line changes

This adds to [the Slice 1 command line](../../001-import-browse-retrieve/contracts/cli.md). `--data-dir` stays required on every command, and the exit codes keep their meaning.

Until the last delivery step, `key create`, `--key-file`, and `--preview-collection` work but don't appear in the usage text.

## `mailarkiv key create`

```text
mailarkiv key create --data-dir DIR PATH
```

Creates the key that encrypts stored IMAP passwords (FR-025). It doesn't open the catalog, and `DIR` doesn't have to exist yet.

1. Make `DIR` and `PATH` absolute and resolve symbolic links, through the longest existing parent of each.
2. Refuse a `PATH` equal to or under `DIR`.
3. Create `PATH` with `O_CREATE|O_EXCL` and mode 0600, write [the key line](../data-model.md#key-file), fsync the file, and fsync its directory.

It doesn't create a missing parent directory of `PATH`, so a typing mistake can't scatter keys. The ownership check of the `account` commands doesn't apply, because no file in the data directory is written. The key belongs to whoever ran the command, so the operator runs it as the service user or changes the owner after (FR-043).

| Exit | Output | When |
| ---- | ------ | ---- |
| 0 | `Created the key at /etc/mailarkiv/key. Back it up apart from the data directory. Without it, every IMAP password must be saved again.` on stdout | Done |
| 1 | `The key must be outside the data directory.` | Step 2 (User Story 4 scenario 4) |
| 1 | `A file already exists at /etc/mailarkiv/key. MailArkiv never replaces a key.` | `O_EXCL` failed |
| 1 | `The key couldn't be written: <cause>` | Any other error. A partly written file is removed |
| 2 | Usage text | Bad arguments |

## `mailarkiv serve`

```text
mailarkiv serve --data-dir DIR [--listen ADDR] [--key-file PATH]
```

`--key-file` names the key. Without it, collection is unavailable and everything else works (FR-026).

Startup order, with the new steps in bold:

1. Create the data directory if it's missing, with mode 0700.
2. **If `--key-file` is given, load the key: refuse a path equal to or under the data directory, then read and parse the file.**
3. Take the lock.
4. Check storage.
5. Open the catalog and apply migrations. The migration runner [turns foreign keys off around each migration](../data-model.md#migration-0002).
6. Run recovery.
7. **Load sources and try to open each stored password. With no key, every source's password is unreadable.**
8. Bind the port.
9. **Start one sync for every source that isn't paused or in Needs attention.**
10. Serve.

Loading the key before the lock means a bad key fails fast, and touches nothing in the data directory except creating it.

On SIGTERM or SIGINT, the server stops every sync while `http.Server.Shutdown` runs, inside the same 5 second grace. A message partway through transfer isn't stored (FR-024).

### Exit 1 causes

Slice 1's `storage`, `catalog`, `lock`, and `port` stay. `key` is new:

```json
{"time":"…","level":"ERROR","msg":"startup failed","cause":"key","reason":"inside_data_dir"}
```

| `reason` | When |
| -------- | ---- |
| `inside_data_dir` | The path is the data directory or under it |
| `missing` | No file at the path |
| `unreadable` | The file can't be opened or read, such as when another user owns it |
| `invalid` | Larger than 1 KiB, or not exactly the key line |

The log never contains the key's content.

### Log entries

All are JSON lines from `log/slog` on stderr, as in Slice 1. None contains a password, a user name, a source name, a folder name, a server reply, or message content (FR-036). `source_id` and `folder_id` are internal IDs.

| `level` | `msg` | Other fields | When |
| ------- | ----- | ------------ | ---- |
| INFO | `collection unavailable` | `reason`: `no key file given` | At start without `--key-file` (User Story 4 scenario 10) |
| WARN | `password unreadable` | `source_id`, `category`: `password_unreadable` | A stored password didn't open at start or at a sign-in |
| WARN | `sync failed` | `source_id`, `category`, `failed_attempts`, `next_attempt_at` when one is scheduled | Every failed sync |
| WARN | `folder unavailable` | `source_id`, `folder_id`, `category`: `folder_unavailable` | Every sync that can't open a chosen folder |
| WARN | `message unavailable` | `source_id`, `folder_id`, `category`: `message_unavailable` | The server didn't return a message, at each attempt |
| WARN | `message not archived` | `source_id`, `folder_id`, `category`: `message_too_large` or `message_removed` | Once per message |

A storage or catalog failure during collection also logs archive's own line, with the cause, as imports do. Successful syncs aren't logged.

## Hidden flag

`--preview-collection` registers the [source routes](http-api.md#access) and starts collection at step 9. Without it, steps 7 and 9 are skipped and no `collection unavailable` line is written. The flag is removed in the last delivery step, and collection then always runs.

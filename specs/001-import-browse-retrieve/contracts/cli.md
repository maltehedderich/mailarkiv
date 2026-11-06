# Command line

One binary, `mailarkiv`, with three commands. Operators run them on the server host. Access to the host and data directory is the credential, so the web interface never offers account creation (FR-001).

`--data-dir` is required on every command. A default would let a wrong working directory create a second, empty archive without warning.

## `mailarkiv serve`

```text
mailarkiv serve --data-dir DIR [--listen ADDR]
```

`--listen` defaults to `127.0.0.1:8080`, so a new install isn't reachable from the network until the operator chooses that.

Startup order:

1. Create the data directory if it's missing, with mode 0700.
2. Take the lock on `<data-dir>/lock`.
3. Check `tmp/` and `blobs/` by writing a probe file.
4. Open the catalog and apply migrations.
5. Run recovery.
6. Start listening.

| Exit | When |
| ---- | ---- |
| 0 | Clean shutdown after SIGTERM or SIGINT |
| 1 | Startup failed: storage unusable, catalog unusable, a database newer than the binary, lock held, or the port is taken. The log says which |
| 2 | Bad arguments |

Logs are JSON lines from `log/slog` on stderr, for the journal or a container runtime to collect. Fields: `time`, `level`, `msg`, `category` where one applies, `message_id` or `content_hash` where one applies. A refused start logs `cause` as `storage`, `catalog`, `lock`, or `port`. Sign-in attempts and successful requests aren't logged, so no submitted address can reach the log (FR-029).

## `mailarkiv account create`

```text
mailarkiv account create --data-dir DIR EMAIL
```

Creates an administrator account. Creates the catalog first if this is the first command run on a new data directory.

## `mailarkiv account set-password`

```text
mailarkiv account set-password --data-dir DIR EMAIL
```

Replaces the password. In one transaction, it ends all of that account's sessions and lifts any sign-in block on the address (FR-004, FR-005). It's safe to run while the server is up.

## Shared behavior of `account` commands

Both commands lowercase `EMAIL` before using it, so `Admin@example.test` creates or finds `admin@example.test`.

The password is never an argument. On a terminal, the command asks twice with echo off. Otherwise it reads one line from stdin, so scripts and tests can drive it.

Both commands refuse to run when the current user doesn't own the data directory. Without that check, `sudo mailarkiv account create` could leave root-owned `catalog.db-wal` and `catalog.db-shm` files behind, and the service user could then no longer open its catalog.

| Exit | Message on stderr | When |
| ---- | ----------------- | ---- |
| 0 | None. Prints one confirmation line on stdout, such as `Created the account admin@example.test.` | Done |
| 1 | That email address isn't valid. | FR-044 |
| 1 | An account with that email address already exists. | `create`, whatever the capital letters. The existing account doesn't change |
| 1 | No account has that email address. | `set-password` |
| 1 | Passwords must have at least 12 characters. | FR-002 |
| 1 | The passwords don't match. | Terminal only |
| 1 | Run this as the user that owns the data directory. | Ownership check |
| 2 | Usage text | Bad arguments |

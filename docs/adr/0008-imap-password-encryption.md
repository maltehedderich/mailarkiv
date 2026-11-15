# ADR 0008: Encrypt IMAP passwords with AES-256-GCM and a key file

**Status:** Adopted, 2026-11-10

## Context

IMAP collection signs in to each mailbox at every sync, so MailArkiv must store the password itself. A hash won't work. A copy of the data directory, including the catalog, its WAL, and temporary files, must not reveal any password, so the key that protects them has to live elsewhere.

Constitution principle IV prefers the standard library. Go's `crypto/cipher` offers authenticated encryption with random nonces, so this needs no new dependency.

## Decision

| Part | Choice |
| ---- | ------ |
| Cipher | AES-256-GCM through `cipher.NewGCMWithRandomNonce` |
| Additional data | `mailarkiv-imap-password-v1`, a zero byte, the lowercase host, a zero byte, and the lowercase user name |
| Stored value | Nonce, ciphertext, and tag together in `imap_source.sealed_password` |
| Key file | One line: `mailarkiv-key-v1:`, the 32 key bytes in standard padded base64, and a newline. 62 bytes in all |

**Binding to the source:** The additional data ties each ciphertext to its host and user name. A ciphertext copied into another source's row fails to open, so a password reaches only its own server as its own user. Host and user name can't change after a source is saved, so the binding never has to change either.

**Nonces:** NIST SP 800-38D allows 2^32 encryptions under one key with random 96-bit nonces. An installation seals a few thousand passwords at most.

**Creating the key:** `mailarkiv key create --data-dir DIR PATH` writes the file with `O_CREATE|O_EXCL` and mode 0600, fsyncs the file and its directory, and prints the path. It never replaces a file, because a replaced key makes every stored password unreadable. It removes a partly written file and doesn't create a missing parent directory.

**Loading the key:** `serve --key-file PATH` refuses to start, with cause `key`, if the file is missing, unreadable, larger than 1 KiB, or not exactly the key line. It doesn't check the file's mode, because container secrets are often mounted readable by everyone in the container. No error message includes the file's content.

**Where the key may live:** Both commands make the data directory and the key path absolute, resolve symbolic links, and refuse a key path equal to or under the data directory. A path that doesn't exist yet resolves through its longest existing parent. A plain prefix check would miss `data/../data/key` and a symbolic link into the data directory.

**No key rotation in this slice:** To replace the key, the operator creates a new one, starts the server with it, and saves every IMAP password again. Re-encrypting stored passwords under a new key has no issue yet.

We rejected:

- **`x/crypto/nacl/secretbox`:** it's in the `x/crypto` module we already pin, but GCM with a random nonce needs nothing outside the standard library.
- **A key in an environment variable:** it leaks into process listings and crash reports.
- **A raw 32-byte file:** it can't be told apart from other binary files or pasted into a secret store as text. The prefix makes a wrong file obvious.
- **A key derived from an administrator's password:** collection would stop after every restart until someone signs in.

## Checks on 2026-11-10

We ran these in a throwaway module with Go 1.27.3. They repeat the planning checks from 2026-11-08.

- Sealing a 6-byte password produced 34 bytes: a 12-byte nonce and a 16-byte tag around the ciphertext.
- Two seals of the same password differed.
- Opening failed with another key, another host, and another user name.

## Consequences

Replacing or removing a password overwrites or clears its column, but the old ciphertext can stay in SQLite's free pages or WAL frames until SQLite reuses them. Reading it needs the key, which is the case the key already protects. We don't turn on `PRAGMA secure_delete`, which would add writes to every delete in the catalog.

The key protects copies of the data directory, not a host where someone can read both the key and the data directory. It needs its own backup, and a lost key means saving every IMAP password again.

A hard link to the key inside the data directory isn't detected, because that would need a walk of the directory. The operator guide tells operators where to keep the key.

Plaintext passwords pass through memory during sign-in and request handling. Go can't reliably wipe them.

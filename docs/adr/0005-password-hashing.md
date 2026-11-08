# ADR 0005: Hash passwords with Argon2id and prompt with x/term

**Status:** Adopted, 2026-11-06

## Context

Administrators sign in with a password that the operator sets from the server host (FR-001, FR-002). The standard library's only password-suitable function is `crypto/pbkdf2`, which isn't memory-hard. Constitution principle IV names cryptography as a reason to add a dependency.

The account commands must read that password without echoing it, and never from an argument, which would land in shell history and `/proc`. Turning off echo needs `termios` calls that the standard library doesn't wrap.

## Decision

| Need | Package | Version | Why |
| ---- | ------- | ------- | --- |
| Password hash | `golang.org/x/crypto/argon2` | `v0.58.1-0.20261009181718-2f1f834330b2` | Argon2id from the Go team. The next section explains the commit |
| Password prompt | `golang.org/x/term` | v0.47.0 | Reads a password with echo off. From the Go team |

**Settings:** RFC 9106's second recommended settings: `t=3`, `m=64 MiB`, `p=4`. Hashes are stored in PHC format, so the settings can change later. At most 2 hashes run at once, which caps memory at 128 MiB. Unknown addresses verify against a dummy hash, so timing doesn't reveal which accounts exist (FR-003).

**Why a commit and not a tag:** Tagged v0.58.0, the newest tag, makes `govulncheck` report GO-2026-5932 at module level for `x/crypto/openpgp`, which we don't import. The pinned commit builds, and `govulncheck` v1.9.0 reports no vulnerabilities on it. Move to the next tag when one exists.

**Password input:** On a terminal, the command reads the password twice with echo off. Otherwise it reads one line from stdin.

We rejected:

- **PBKDF2:** it isn't memory-hard.
- **bcrypt:** it can't take a password over 72 bytes, a limit a long passphrase can hit.

## Checks on 2026-11-06

We ran these in a throwaway module on a host with 4 CPUs, Linux 6.8, and Go 1.27.3.

- One hash took 146 ms with these settings.
- `govulncheck` v1.9.0 reported no vulnerabilities for the pinned `x/crypto` commit or for `x/term` v0.47.0.

## Consequences

`go.mod` holds a pseudo-version of `x/crypto` until a newer tag exists.

Both packages arrive in `go.mod` when the first code imports them, because `go mod tidy` removes unused ones.

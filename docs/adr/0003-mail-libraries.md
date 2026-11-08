# ADR 0003: Choose SMTP, IMAP, and MIME libraries

**Status:** Adopted, 2026-11-06

## Context

SMTP, IMAP, and MIME parsing are hard to get right, and all three handle untrusted input, so they justify dependencies. Each library must be maintained and pinned.

## Decision

| Need            | Library                          | Version       | Why                                                                                                                                          |
| --------------- | -------------------------------- | ------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| SMTP intake     | `github.com/emersion/go-smtp`    | v0.25.1       | Released on 2026-11-05                                                                                                                       |
| IMAP collection | `github.com/emersion/go-imap/v2` | v2.0.0-beta.8 | Development happens on v2. The v1 branch last changed in 2023                                                                                |
| MIME parsing    | `github.com/jhillyerd/enmime/v2` | v2.5.0        | Released on 2026-09-05. It collects part errors in `Envelope.Errors`, which fits our rule that a broken part can't remove a captured message |

We rejected `github.com/emersion/go-message` as our MIME parser because it has had no commit since February 2025. It stays in the build as an indirect dependency of `go-imap`.

## Consequences

`go-imap/v2` is still a beta, and its API can change. We pin the exact version and accept the risk, since the alternative is a branch with no recent changes.

IMAP login is password-only in the first release. Microsoft 365 needs OAuth 2.0 for IMAP and sends journal mail as reports with the original attached, so it stays out of the first release ([#9](https://github.com/maltehedderich/mailarkiv/issues/9)).

Revisit `go-imap` when v2 reaches a stable release, and `enmime` if it stops being maintained or fails on real malformed mail.

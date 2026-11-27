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

### How we run enmime

`internal/mailparse` builds its parser with four options:

- `SkipMalformedParts(true)`, so a broken part becomes a problem on the message instead of ending the parse.
- `DisableTextConversion(true)`. Without it, enmime turns HTML into plain text when a message has no text part. We read text out of HTML ourselves, with the tokenizer the preview uses, so the index and the preview agree.
- `DisableCharacterDetection(true)`, so a declared character set is trusted instead of guessed over. An undeclared one is still guessed.
- `MaxMIMEParts(1000)`, as a backstop only.

`MaxMIMEParts` can't be the bound, because at the limit enmime fails the whole parse with `TooManyPartsError` and returns no parts. We want the parts before the bound to show and be searchable. enmime also has no depth limit, and its time grows with the square of the depth: 63 ms at 200 levels, 658 ms at 800, and no result within 300 s at 5,000.

So a boundary scanner of our own (`internal/mailparse/scanner.go`) runs first. It reads only header blocks and delimiter lines, counts parts in document order, and tracks depth. At the 1,001st part, or a part 21 levels deep, it cuts the input and closes every boundary still open. enmime parses that shortened copy; the stored original never changes. The message gets "too many parts" or "nested too deeply" at the first part left out, and "message cut short" when the input ends with a boundary open. The scanner doesn't descend into `message/rfc822` parts, because enmime treats an attached message as one part. `server:test` fuzzes it for 10 seconds on each run.

### What enmime brings

Importing enmime added 20 modules to `go.mod`, among them `inbucket/html2text`, `olekukonko/tablewriter` and its relatives, `fatih/color`, `goccy/go-json`, `cespare/xxhash`, `gogs/chardet`, `ssor/bom`, `cention-sany/utf7`, and `pkg/errors`. enmime imports `html2text` unconditionally, so all of them link into the binary even with text conversion off.

At the `golang.org/x/net` version enmime asks for, v0.55.0, `govulncheck` reports GO-2026-6860. `go.mod` pins `x/net` v0.62.0, `x/text` v0.43.0, and `x/sys` v0.49.0, and with those `govulncheck ./...` reports nothing. `mise run audit` keeps checking.

## Consequences

`go-imap/v2` is still a beta, and its API can change. We pin the exact version and accept the risk, since the alternative is a branch with no recent changes.

IMAP login is password-only in the first release. Microsoft 365 needs OAuth 2.0 for IMAP and sends journal mail as reports with the original attached, so it stays out of the first release ([#9](https://github.com/maltehedderich/mailarkiv/issues/9)).

Raise the `x/net` pin when enmime's own version catches up, and drop it once enmime requires a fixed version.

Revisit `go-imap` when v2 reaches a stable release, and `enmime` if it stops being maintained or fails on real malformed mail.

# MailArkiv

A self-hosted email archive for Linux, designed to help you keep, find, and export email on your own server.

MailArkiv is for individuals, small businesses, and IT teams who want a searchable archive with little infrastructure to maintain.

> **Status:** Slice 1 of 7 works: sign in, import one `.eml` file at a time, list what's stored, and download the exact original. There are no release packages yet, so you build from source. The features below describe the intended first release.

IMAP collection is available during Slice 2 development. Start the server with its [password key](docs/OPERATIONS.md#the-key), then choose **Sources → Add a source**. Real-server validation of Slice 2 is still pending.

## Start here

- To run MailArkiv, follow the [operator guide](docs/OPERATIONS.md). It covers the build, accounts, HTTPS, and what this version doesn't promise.
- To work on it, install [mise](https://mise.jdx.dev), then run `mise trust`, `mise install`, `mise run setup`, and `mise run check`. [AGENTS.md](AGENTS.md) lists every check.
- Read the [product scope](docs/PRODUCT.md) for user journeys, first-release requirements, and candidates for later releases.
- Read the [architecture principles](docs/ARCHITECTURE.md) for the design, storage model, and reliability rules.
- Read the [architecture decision records](docs/adr/) for adopted dependencies and their versions.

## Planned features

- Import `.eml` files, collect email from IMAP mailboxes that allow password login, or receive archive copies from a mail server through SMTP journaling.
- Search by message text, sender, recipient, date, subject, and attachment filename in a web interface.
- Preserve original messages byte for byte, download individual emails, and export selected messages as a ZIP of `.eml` files.
- Preview messages with remote content blocked by default, and limit access to local administrator accounts.
- See collection failures and indexing delays, check stored messages for corruption, and back up and restore the archive.
- Permanently delete a message when someone asks you to erase their data.

## Planned deployment

The planned distribution is a Linux binary or a single application container with a built-in web interface and local storage. It should require no separate database or search server to operate.

## Scope

MailArkiv stores archive copies alongside your existing mail service. Sending email, spam filtering, and everyday mailbox management remain with your mail server and email client.

The first release does not support Microsoft 365, restoring messages into a mailbox, or access for anyone other than administrators. These may follow if users need them. Legal holds, guaranteed immutable retention, and regulatory compliance certification are also outside the first release.

## Contributing

Before proposing or implementing changes, read the [engineering constitution](.specify/memory/constitution.md) and [repository guidelines](AGENTS.md). The constitution is the project's authoritative engineering policy.

Keep changes focused on the [product scope](docs/PRODUCT.md). For documentation changes, check local links and confirm that requirements and expected behavior agree across the affected documents. Use synthetic email examples; keep credentials and personal data out of the repository.

## License

MailArkiv is licensed under the [Elastic License 2.0](LICENSE).

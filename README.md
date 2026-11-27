# MailArkiv

A self-hosted email archive for Linux, designed to help you keep, find, and export email on your own server.

MailArkiv is for individuals, small businesses, and IT teams who want a searchable archive with little infrastructure to maintain.

> **Status:** Slices 1 to 3 of 7 work: sign in, import one `.eml` file at a time, collect mail from IMAP mailboxes, list messages by sender, subject, and date, search them by words, sender, recipient, subject, attachment name, and date, read each one in a preview that runs no script and loads nothing from the internet unless you ask for its images, save attachments, and download the exact original. Search has been measured at 5,000,000 messages. IMAP collection has been tested against Dovecot 2.4.2, not against hosted providers. There are no release packages yet, so you build from source. The features below describe the intended first release.

To collect from IMAP, start the server with its [password key](docs/OPERATIONS.md#the-key), then choose **Sources → Add a source**.

## Start here

- To run MailArkiv, follow the [operator guide](docs/OPERATIONS.md). It covers the build, accounts, HTTPS, and what this version doesn't promise.
- To work on it, install [mise](https://mise.jdx.dev) and `musl-tools`, then run `mise trust`, `mise install`, `mise run setup`, and `mise run check`. [AGENTS.md](AGENTS.md) lists every check.
- Read the [product scope](docs/PRODUCT.md) for user journeys, first-release requirements, and candidates for later releases.
- Read the [architecture principles](docs/ARCHITECTURE.md) for the design, storage model, and reliability rules.
- Read the [architecture decision records](docs/adr/) for adopted dependencies and their versions.

## Local development

Run `mise run dev:account admin@example.test` once to create a local account, then
`mise run dev` and open `http://localhost:5173`.

For a local IMAP source, follow the [Dovecot setup](specs/002-imap-incremental-archive/quickstart.md).
When `.data/ca.pem` exists, `mise run dev` uses it to verify the mailbox server's
certificate. An existing `SSL_CERT_FILE` setting takes precedence. Restart
`mise run dev` after creating or replacing the CA file.

With that Dovecot setup, add a source using server `localhost`, TLS on port `993`
(or STARTTLS on port `143`), user `test@example.test`, and the generated password
in `.data/dovecot-password`. The mail container must be running separately.

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

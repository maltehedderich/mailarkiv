# MailArkiv

A self-hosted email archive for Linux, designed to help you keep, find, and export email on your own server.

MailArkiv is for individuals, small businesses, and IT teams who want a searchable archive with little infrastructure to maintain.

> **Status:** MailArkiv is in the planning stage. There is no installable application yet. The features below describe the intended first release.

## Start here

This repository contains product and architecture plans, engineering policy, and development workflow tooling. Application code and build, lint, type-check, and test commands are not yet available.

- Read the [product scope](docs/PRODUCT.md) for user journeys, first-release requirements, and candidates for later releases.
- Read the [architecture principles](docs/ARCHITECTURE.md) for the proposed design, storage model, and reliability rules.

## Planned features

- Import `.eml` files, collect email from IMAP mailboxes, or receive archive copies from a mail server through SMTP journaling.
- Search by message text, sender, recipient, date, subject, and attachment filename in a web interface.
- Preserve original messages byte for byte, download individual emails, and export selected messages in standard formats.
- Preview messages with remote content blocked by default, and control who can access the archive.
- See collection failures and indexing delays, check stored messages for corruption, and back up and restore the archive.

## Planned deployment

The planned distribution is a Linux binary or a single application container with a built-in web interface and local storage. It should require no separate database or search server to operate.

## Scope

MailArkiv stores archive copies alongside your existing mail service. Sending email, spam filtering, and everyday mailbox management remain with your mail server and email client.

Restoring messages directly into a mailbox is still under consideration. Legal holds, guaranteed immutable retention, and regulatory compliance certification are outside the initial release.

## Contributing

Before proposing or implementing changes, read the [engineering constitution](.specify/memory/constitution.md) and [repository guidelines](AGENTS.md). The constitution is the project's authoritative engineering policy.

Keep changes focused on the [product scope](docs/PRODUCT.md). For documentation changes, check local links and confirm that requirements and expected behavior agree across the affected documents. Use synthetic email examples; keep credentials and personal data out of the repository.

## License

MailArkiv is licensed under the [Elastic License 2.0](LICENSE).

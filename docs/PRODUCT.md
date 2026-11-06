# Product scope

## What we are building

A self-hosted, searchable **archive of email**, not another mail server or a replacement email client. It should be straightforward for a small IT team to install, operate, search, and recover from on Linux.

**Primary audience:** individuals, small businesses, and IT administrators who want a dependable alternative to Windows-only archival products.

**Positioning:** lightweight first, enterprise-capable later. A small installation should not pay the operational cost of features it does not use.

## Core user journeys

1. **Capture:** Configure SMTP journaling or an IMAP source; observe the first successfully archived messages.
2. **Find:** Search by text, sender, recipient, date, subject, and attachment filename; safely preview results.
3. **Recover:** Download an original EML or export selected messages. Add mailbox restore if initial users need it.
4. **Operate:** See the last successful sync, pending indexing, retries, failures, and storage integrity status.
5. **Control access:** Sign in and access only the archives permitted by the installation's roles and policies.

## MVP capabilities

- Raw `.eml` import, **SMTP journal capture**, and scheduled **IMAP collection**.
- Preservation of the exact original message bytes, source context, and capture history.
- Browse and search archived messages; preview text/HTML safely and inspect attachments.
- Local administrator login, baseline authorization, and restricted SMTP ingress.
- EML download and selected-message export.
- Visible job status, retry paths, index rebuilding, integrity verification, and documented backup/restore.
- Simple Linux/Docker distribution without mandatory external services.

Mailbox restore remains a candidate beyond export and has a separate conditional slice. Backup and restore of the archive itself remain required for the MVP.

## Not in the initial release

- Hosting or sending end-user email, spam filtering, or mailbox replacement.
- Multi-node clustering, horizontal scaling, or shared multi-tenant infrastructure.
- Enterprise eDiscovery, legal holds, WORM retention guarantees, or regulatory compliance certification.
- Every importer and provider integration at once (e.g., PST and Microsoft Graph can follow).
- AI-powered search or analytics before dependable capture and ordinary search work well.

## Candidates for later releases

Consider these capabilities when user demand or measured workloads justify them:

- Provider adapters: Microsoft 365 Graph, Google Workspace, PST/MBOX import.
- Raw-message storage: S3-compatible or object-lock storage.
- Policy and governance: retention, legal holds, audit export, stronger tamper evidence.
- Identity and access: OIDC/SSO, finer-grained permissions, organization isolation.
- Scaling: alternative metadata store, separate workers, high availability if workloads warrant it.

Preserve original evidence and source provenance now. These candidates do not justify configuration, plugin frameworks, or interchangeable backends in the current scope. Generalize when at least two current callers or variants need it.

# ADR 0001: Use embedded Bleve for search

**Status:** Adopted, 2026-11-06

## Context

MailArkiv needs full-text and field search without a separate search server. Search is a projection of the catalog and raw messages, so it can always be rebuilt.

## Decision

Use `github.com/blevesearch/bleve/v2` v2.6.1 inside the Go process, built without the `vectors` tag. The project owner chose Bleve over SQLite FTS5 and dropped the planned comparison benchmark.

Checks on 2026-11-06:

- v2.6.1 was released on 2026-08-24, and the repository's latest commit is from 2026-11-04.
- The license is Apache-2.0.
- A test module builds as a static binary with `CGO_ENABLED=0`.
- `govulncheck` reports no vulnerabilities with the baseline in `docs/ARCHITECTURE.md`.

## Consequences

The index is a second store that can't commit atomically with SQLite, so a message is marked indexed only after `Batch` returns.

OpenCloud embeds Bleve in a single binary, as we will, and has reported two production failures:

- A corrupt segment made the merger rewrite data at about 50 MB/s for two weeks. Nothing was logged because no async error callback was registered ([bleve#2402](https://github.com/blevesearch/bleve/issues/2402), open; [opencloud#3594](https://github.com/opencloud-eu/opencloud/issues/3594)).
- A full disk panicked the whole process. The reindex that followed had no checkpoint and reached about 21 GiB of memory ([opencloud#3790](https://github.com/opencloud-eu/opencloud/issues/3790)).

The rules in the "Search index" section of `docs/ARCHITECTURE.md` respond to these reports.

Bleve's behavior at our 5 million message target is unmeasured. Revisit this decision if the sizing test misses search, memory, or rebuild targets.

# ADR 0002: Use the pure Go SQLite driver

**Status:** Adopted, 2026-11-06

## Context

MailArkiv ships as a Linux binary or a single container, and SQLite holds its metadata and job state. A driver that needs CGO adds a C toolchain to every build and complicates cross-compiling.

## Decision

Use `modernc.org/sqlite` v1.61.1, released on 2026-10-31. Run it in WAL mode with `synchronous=FULL` and one writer connection.

`mattn/go-sqlite3` wraps the C library and is the main alternative. We rejected it because it requires CGO, and nothing else in the build does.

## Consequences

A test module with this driver and the other adopted libraries builds as a static binary with `CGO_ENABLED=0` on linux/amd64.

`synchronous=FULL` costs write throughput. We accept that because rule 1 in `docs/ARCHITECTURE.md` promises `250` only after durable capture, and with `NORMAL` a commit can roll back after power loss.

We have not measured this driver against the C one. Revisit this decision if the sizing test shows the driver is the bottleneck.

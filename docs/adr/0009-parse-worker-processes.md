# ADR 0009: Parse mail in worker processes of the server binary

**Status:** Adopted, 2026-11-20

## Context

Slice 3 parses every archived message to fill list rows, the index, the message view, the preview, and attachment downloads. The mail is untrusted, and the spec requires processing to stop a message at 60 s. Rule 4 in [ARCHITECTURE.md](../ARCHITECTURE.md) says a broken part may fail processing but never a capture. Parsing in the server can't keep either promise:

- Go can't stop a goroutine from outside. A parse that runs too long keeps its CPU and memory after the server gives up on it.
- A stack overflow, an out-of-memory error, or a concurrent map write ends a Go process, and `recover` can't catch them. In the server, any of these would stop capture.
- Originals reach 150,000,000 bytes (`archive.MaxSize`). A parse that allocates several times that should hit the kernel's limit in a worker, not in the server.

## Decision

Parse in long-lived worker processes. A worker is the server binary started with the hidden subcommand `parse-worker`.

| Rule | Value |
| ---- | ----- |
| Background pool | 2 workers, for processing and the text the index needs |
| Interactive pool | 2 workers, for the message view, the preview, and attachment downloads. A request waits at most 30 s for a free worker, then answers 503 `busy` |
| Time limit | The server kills a worker with `SIGKILL` when one request passes 60 s, and starts a new one. The message is Couldn't be read with "took too long" |
| Server exit | Each worker gets `Pdeathsig: SIGKILL`, so it dies with the server |
| Worker death | The message is tried once more in a fresh worker. If that one dies too, the message is Couldn't be read with "malformed structure", and the log gets `processing_worker_stopped` with the message ID and exit status |
| What a worker may do | Read one request at a time on standard input and write the answer on standard output. It opens no file and no network connection, and gets an empty environment except the fault settings tests use |

Requests and answers use the search process's length-prefixed JSON frames, with their own operations: `read`, `html`, `text`, and `attachment`. A frame holds at most 64 MiB, so the original follows its request frame as raw bytes, and a decoded attachment follows its answer frame the same way. The server and the worker are the same binary, so the format has no version. Started from a terminal, `parse-worker` exits with status 2, and the usage text doesn't list it.

The pool takes the time limit as a parameter, so tests run real workers with a 2 s limit, and a unit test pins 60 s.

We rejected:

- **Parsing in the server with `recover`:** it misses the fatal errors above, and it can't stop a parse at 60 s.
- **A process per message:** processing and then a rebuild of 5,000,000 messages would start 10 million processes.
- **A Rust parser in the search process:** a second parser in a second language, and a parser crash would stop search too.

## Checks on 2026-11-15

We ran these with enmime v2.5.0 on a host with 4 cores, Linux 7.0, and Go 1.27.4.

- A typical 43 KB message parsed in 3.2 ms and allocated 0.3 MB, so two background workers parse about 600 messages a second, and parsing again when a message opens costs little.
- enmime's time grows with the square of the nesting depth: 63 ms at 200 levels, 183 ms at 400, and 658 ms at 800. A message 5,000 levels deep didn't finish within 300 s. The bound of 20 levels removes that cost, and the 60 s limit catches what the bounds miss.
- A flat message of 12,000 parts took 540 ms and allocated 230 MB.

## Consequences

Nothing new ships, because workers run the server binary. Processing adds four processes beside the server and the search process.

`Pdeathsig` fires when the thread that started the child exits, not the process. The Go runtime ends a locked thread when its goroutine exits, so workers must be started from goroutines that don't lock their OS thread, or a healthy worker could be killed.

Workers have no memory cap. A 150,000,000-byte original can make one allocate several times that. The kernel's out-of-memory killer normally picks the largest process, the worker, but that isn't guaranteed.

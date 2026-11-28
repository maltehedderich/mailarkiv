# ADR 0010: Run on Linux amd64 and arm64, develop on macOS

**Status:** Adopted, 2026-11-27

## Context

MailArkiv is deployed on Linux servers, on amd64 and on arm64. It is developed on macOS, on a Mac with Apple silicon. Until now the build and the checks assumed Linux on amd64:

- `search:build` built the search program only for `x86_64-unknown-linux-musl`.
- Code that uses a Linux-only feature, such as `Pdeathsig` for worker processes, didn't compile on macOS.
- Tests that trace syscalls with `strace` or read `/proc` failed on macOS. So did one that expected an error type macOS's certificate check doesn't return.

## Decision

| Platform | Role | Checked by |
| -------- | ---- | ---------- |
| Linux on amd64 | Supported | Every CI part |
| Linux on arm64 | Supported | The CI parts `unit` and `server`, and `mise run linux:check` on a Mac with Apple silicon |
| macOS | Development only | The tasks that build and test without Linux |

- **Linux builds stay static.** The server is built with `CGO_ENABLED=0`. The search program is built for `x86_64-unknown-linux-musl` or `aarch64-unknown-linux-musl`, whichever matches the machine. Each architecture builds on its own machine, so nothing cross-compiles.
- **On macOS, the search program builds for the Mac.** Go code that needs a Linux feature puts it in a `_linux.go` file, beside a `//go:build !linux` file that does without it. The only such feature so far is `Pdeathsig`. Without it, a worker stuck on a message can outlive the server.
- **Tests of Linux guarantees run only on Linux.** They skip on other systems. On Linux they still fail when a tool they need, such as `strace`, is missing.
- **Full checks from a Mac run in a container.** `mise run linux:check` runs `mise run check` in Ubuntu 24.04 through Docker Desktop, one task at a time. `mise run linux:shell` opens a shell in the same container. The checkout is mounted, and builds, dependencies, and tools live in Docker volumes, so no Linux file reaches the Mac's checkout. CI's runners use the same Ubuntu version.
- **Measurements name their architecture.** ADR 0007 doesn't say which one its figures come from. The Slice 3 sizing run and every limit in `docs/OPERATIONS.md` name the architecture they were measured on. A limit holds for arm64 only once it has been measured there.

We rejected:

- **Running MailArkiv on macOS:** nobody deploys it there. The durability and isolation guarantees are checked with Linux tools and would need macOS versions.
- **Cross-compiling on the Mac**, for example with `cargo-zigbuild`: the binaries still need Linux to run their tests, and it is another tool to pin.
- **Only the container on a Mac, with no macOS build:** every test run and `mise run dev` would go through Docker, and the editor would mark Linux-only code as errors.
- **Every CI part on arm64:** `lint`, `typecheck`, and `audit` don't depend on the CPU. `server:test` already starts the server and the search program on arm64.

## Consequences

- Slice 3's research rejected building in a container because every check would need Docker. CI and developers on Linux still don't need it. Only a Mac needs Docker, and only for `linux:check` and `linux:shell`.
- The first `linux:check` installs every tool into a volume, so it takes several minutes longer than later runs.
- Two checkouts on one Mac share the container's volumes, so their `linux:check` runs must not overlap.

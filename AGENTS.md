# Repository Guidelines

## Engineering Policy

Read [the engineering constitution](.specify/memory/constitution.md) before planning or implementing changes. MailArkiv adopted **version 1.0.0, approved and ratified on 2026-11-06**, as its authoritative engineering policy. It overrides conflicting repository guidance. Check changes against its principles and record exceptions in the plan's Complexity Tracking section.

## Frontend design

Read [the design guide](frontend/DESIGN.md) before you build or change a screen. Exact values live in [the tokens](frontend/src/lib/styles/tokens.css).

## Required checks

`mise run` is the only interface, so CI, docs, and people all use these commands. Run `mise run setup` first on a new clone.

| Command | Runs | Layer |
| ------- | ---- | ----- |
| `mise run lint` | `gofmt -l`, `go vet`, `staticcheck`, `prettier --check` | Static |
| `mise run typecheck` | `svelte-kit sync`, `svelte-check --fail-on-warnings` | Static |
| `mise run audit` | `govulncheck ./...`, `bun audit --ignore=GHSA-gcqx-w48m-w2h4` | Static, needs network |
| `mise run server:test` | `go test -race ./...`, including process-level crash, syscall, and marker tests. Needs `strace` | Unit, integration, process |
| `mise run frontend:test` | `vitest run` | Unit |
| `mise run test:e2e` | `playwright test` against the built binary | End to end |
| `mise run test` | The three test tasks above | |
| `mise run check` | `lint`, `typecheck`, `test`, `audit`. CI gates on this | |

Other tasks are `setup`, `dev`, `dev:account`, `build`, `frontend:build`, `server:build`, `format`, and `clean`. `.github/workflows/check.yml` installs `strace`, caches Go builds, and runs `mise run setup` and `mise run check`.

- For documentation-only changes, review affected text against the constitution, check local links, and verify that scope and acceptance behavior agree across documents. These checks are sufficient while runtime behavior is unchanged.
- When adding tooling, document its exact required check commands here in the same change. Feature plans must identify the checks that apply and the layer each runs at.
- Report checks run and any that could not run, with the reason and remaining risk. Do not report an unconfigured check as passed.

## Code comments

Use clear names and simple code. Comment only on non-obvious decisions, constraints, invariants, safety rules, or public contracts. Keep comments short and self-contained; put longer rationale in maintained documentation. Update or delete stale comments when changing the code.

Do not reference feature specs, plans, task lists, or their numbered requirements and scenarios in code comments (including tests). Those documents may be deleted, and identifiers such as `FR-003` are reused across features. State the relevant behavior or reason directly. Links to maintained documentation or external standards may supplement an explanation, but must not replace it.

## Writing

These rules apply to everything you write: docs, UI copy, comments, commit messages, and replies.

- Write for a specific reader and put the main point first, so it lands on the first read.
- Use ordinary words, short sentences, and concrete nouns. Use a technical term only when it adds needed precision.
- Back claims with names, numbers, examples, comparisons, or observed behavior from the source material. Never invent specifics or hide missing substance behind vague language.
- Cut throat-clearing, stacked hedges, empty adjectives, repetition, and clever phrasing. Stop when the work is done, without a summary, moral, or generic closing.
- Keep a human voice: no corporate tone, platform formulas, motivational filler, or repetitive AI-style sentence patterns.
- Tighten every non-trivial draft before you return it. Cut words that don't change the meaning, and keep the author's intent and useful nuance.

Commit messages follow Conventional Commits with a scope, such as `fix(frontend): …`.

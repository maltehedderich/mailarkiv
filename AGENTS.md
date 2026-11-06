# Repository Guidelines

## Engineering Policy

Read [the engineering constitution](.specify/memory/constitution.md) before planning or implementing changes. MailArkiv adopted **version 1.0.0, approved and ratified on 2026-11-06**, as its authoritative engineering policy. It overrides conflicting repository guidance. Check changes against its principles and record exceptions in the plan's Complexity Tracking section.

## Frontend design

Read [the design guide](frontend/DESIGN.md) before you build or change a screen. Exact values live in [the tokens](frontend/src/lib/styles/tokens.css).

## Required checks

The repository currently contains planning documents and workflow tooling; application build, lint, type-check, and test commands are not configured yet.

- For documentation-only changes, review affected text against the constitution, check local links, and verify that scope and acceptance behavior agree across documents. These checks are sufficient while runtime behavior is unchanged.
- When adding application tooling, document its exact required check commands here in the same change. Feature plans must identify the checks that apply and the layer each runs at.
- Report checks run and any that could not run, with the reason and remaining risk. Do not report an unconfigured check as passed.

## Writing

These rules apply to everything you write: docs, UI copy, comments, commit messages, and replies.

- Write for a specific reader and put the main point first, so it lands on the first read.
- Use ordinary words, short sentences, and concrete nouns. Use a technical term only when it adds needed precision.
- Back claims with names, numbers, examples, comparisons, or observed behavior from the source material. Never invent specifics or hide missing substance behind vague language.
- Cut throat-clearing, stacked hedges, empty adjectives, repetition, and clever phrasing. Stop when the work is done, without a summary, moral, or generic closing.
- Keep a human voice: no corporate tone, platform formulas, motivational filler, or repetitive AI-style sentence patterns.
- Tighten every non-trivial draft before you return it. Cut words that don't change the meaning, and keep the author's intent and useful nuance.

Commit messages follow Conventional Commits with a scope, such as `fix(frontend): …`.

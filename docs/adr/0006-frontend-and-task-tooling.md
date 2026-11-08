# ADR 0006: Pin the frontend toolchain and run tasks through mise

**Status:** Adopted, 2026-11-06

## Context

Slice 1 needs a frontend build, Go checks, and one way to run them that works the same on a laptop and in CI. [ADR 0004](0004-interface-fonts-and-icons.md) adopted the fonts and icons and tested them with Kit 3.1.1, Svelte 5.57.3, and Vite 8.3.6. It left the package manager and the rest of the toolchain open, and it requires us to resolve or record the `deepmerge` report when we pin these packages.

All of this runs at build or check time. The binary holds only the built output, so there's no Node process at runtime.

## Decision

**Tasks and tool versions:** mise is the only interface, so CI, docs, and people all call `mise run <task>`. `mise.toml` requests Go 1.27, bun 1.4, and Node 26, plus `staticcheck` 2026.2.2 and `govulncheck` v1.9.0 through mise's `go:` backend. `mise.lock` records the exact versions.

**Packages:** bun installs them. Node 26 runs Vite and Playwright, which use a Node shebang. ADR 0004's test used npm, but it didn't choose a package manager.

**Frontend versions,** pinned exactly in `frontend/package.json`:

| Package | Version | Note |
| ------- | ------- | ---- |
| `@sveltejs/kit` | 3.1.1 | From ADR 0004 |
| `svelte` | 5.57.3 | From ADR 0004 |
| `vite` | 8.3.6 | From ADR 0004 |
| `@sveltejs/vite-plugin-svelte` | 7.3.1 | |
| `@sveltejs/adapter-static` | 4.0.1 | |
| `typescript` | 6.0.3 | 7.0.2 is newer, but `svelte-check` 4.7.8 only accepts `^5 \|\| ^6` |
| `@types/node` | 26.6.6 | Types for the browser-test helpers, which use Node modules. `tsconfig.json` names it, because TypeScript 6 no longer includes ambient types on its own. It holds no running code |
| `svelte-check` | 4.7.8 | |
| `vitest` | 5.1.0 | |
| `@playwright/test` | 1.65.0 | |
| `@axe-core/playwright` | 4.14.0 | |
| `prettier` | 3.9.10 | |
| `prettier-plugin-svelte` | 4.1.1 | |

ADR 0004 lists the font and icon packages.

**The `deepmerge` exception:** `deepmerge` 4.3.1 stays in the build, with low-severity report GHSA-gcqx-w48m-w2h4, and `mise run audit` ignores that one ID. The report covers `>=4.2.0 <=4.3.1` and lists no patched version, and 4.3.1 is the newest release. `@sveltejs/vite-plugin-svelte` requires `^4.3.1` and calls it only to merge our own Vite plugin options at build time. A search of the build output found no `deepmerge` code. The plan's [Complexity Tracking](../../specs/001-import-browse-retrieve/plan.md#complexity-tracking) records this as an exception to constitution principle IV.

We rejected:

- **An audit threshold such as `--audit-level=moderate`:** it would hide every future low report. With one ignored ID, a new report of any severity still fails the check.
- **Overriding `deepmerge` to a version below 4.2.0:** it breaks the plugin's declared range.
- **Replacing SvelteKit:** a much larger change, made to remove a build-time, low-severity report.

## Checks on 2026-11-06

- With Node 26.13.0 and bun 1.4.4, `bun install`, `vite build`, `svelte-check`, `vitest run`, and `playwright --version` all succeed.
- `staticcheck` and `govulncheck` work on Go 1.27.3.
- `go test -race` passes with concurrent writes through `modernc.org/sqlite`. Its first race build took 82 s of mostly compile time, so CI caches Go builds.
- Kit 3 needs `"extends": "$app/tsconfig"` plus explicit `include` and `exclude` in `tsconfig.json`. The older `./.svelte-kit/tsconfig.json` form fails the build.

## Consequences

`audit` isn't in the shared mise task vocabulary yet, so that convention should gain it.

TypeScript stays on 6 until `svelte-check` accepts 7.

Remove the `deepmerge` ignore when a fixed version ships or the plugin drops the package. The final review of Slice 1 checks for both.

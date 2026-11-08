# ADR 0004: Bundle Inter, Geist Mono, and Lucide icons

**Status:** Adopted, 2026-11-06

## Context

The interface needs a typeface, a mono face for hashes, and icons. MailArkiv is self-hosted and may run on a closed network, and each visitor's address is private, so nothing can load from a third party at runtime. The packages must be pinned and maintained, and they must add little to one binary. The project owner chose Inter and asked for icons through `unplugin-icons`, with a set such as Lucide.

## Decision

| Need | Package | Version | Why |
| ---- | ------- | ------- | --- |
| Interface text | `@fontsource-variable/inter` | 5.3.0 | Inter "Version 4.001", OFL-1.1. Covers Latin, Greek, and Cyrillic, which file names in mail use. Published 2026-07-19 |
| Hashes | `@fontsource-variable/geist-mono` | 5.3.0 | Geist Mono "Version 1.701", OFL-1.1. Hashes need no Greek. Published 2026-07-19 |
| Icon build step | `unplugin-icons` | 24.0.0 | MIT. Inlines each imported icon at build time. Published 2026-09-11 |
| Icon set | `@iconify-json/lucide` | 1.2.151 | ISC. One consistent line style, used at build time only. Published 2026-11-03 |

All four are development dependencies at build time, and their output is embedded in the binary. Pin exact versions when the frontend is scaffolded. Font imports live in [the root layout](../../frontend/src/routes/+layout.svelte), icon setup in [the Vite config](../../frontend/vite.config.ts), and shared styling in [global.css](../../frontend/src/lib/styles/global.css).

We rejected:

- **Google Fonts at runtime:** it sends visitors' addresses to a third party and fails on a closed network.
- **Hand-copied SVG icons:** the owner chose `unplugin-icons`, which also fails the build on a mistyped name.
- **Geist as the interface font:** the owner chose Inter, and Geist has no Greek.

## Checks on 2026-11-06

We built a throwaway SvelteKit app with Kit 3.1.1, Svelte 5.57.3, and Vite 8.3.6, then loaded it in Chromium.

- It made no external request at runtime.
- The fonts total 13 woff2 files and 288,960 bytes: Inter 7 subsets and 218,512 bytes, Geist Mono 6 subsets and 70,448 bytes. A page with Latin, Cyrillic, Greek, and a hash fetched 4 files.
- Only imported icons reach the output. Each SVG rendered at 16 by 16 px with `aria-hidden="true"`. The Lucide package is 684 KB on disk and isn't shipped whole.
- Tabular numerals work in Inter: `1111` and `0000` both measure 33.72 px, against 21.16 px for proportional `1111`.
- `npm audit` found 4 low-severity reports, all one chain: `deepmerge` (GHSA-gcqx-w48m-w2h4, prototype poisoning) through `@sveltejs/vite-plugin-svelte`, `@sveltejs/kit`, and `@sveltejs/adapter-static`. None is in the four packages above.

## Consequences

The bundled Inter has only the `wght` axis and no `cv01` or `ss03` alternates. Linear's look depends on those, so MailArkiv uses Inter's default drawing. Getting the alternates would need the upstream rsms build, which we don't plan to use.

Geist Mono has no Greek, so a Greek character in a mono context falls back to the system mono. Only hashes use it in Slice 1.

The SvelteKit toolchain has the `deepmerge` report above. It runs at build time, but constitution principle IV asks for a version with no known vulnerabilities, so the Slice 1 plan must resolve it or record an exception when it pins those packages.

SvelteKit 3 has no `svelte.config.js`, so its options go to the `sveltekit()` Vite plugin. It also replaces `$lib` with `#lib`, which needs `"imports": { "#lib": "./src/lib/index.js", "#lib/*": "./src/lib/*" }` in `package.json`. Icon types need `import 'unplugin-icons/types/svelte'` in `src/app.d.ts`.

Revisit a font if it stops covering scripts seen in real mail, and the icon set if Lucide stops being maintained.

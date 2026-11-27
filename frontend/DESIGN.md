# MailArkiv interface design

This guide sets the MailArkiv visual system: how the interface should feel, which token plays which role, and the patterns every screen shares. Read it before you build or change a screen.

It holds no values and no screen layouts. Each fact lives in one place:

| What                                                            | Where                                                                                                |
| --------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| Token values: colour, type, space, size, radius, motion, shadow | [`src/lib/styles/tokens.css`](src/lib/styles/tokens.css)                                             |
| Shared classes: buttons, fields, drawers, focus, icons          | [`src/lib/styles/global.css`](src/lib/styles/global.css)                                             |
| Shared components                                               | [`src/lib/components/`](src/lib/components/)                                                         |
| A screen's layout, measurements, and responsive steps           | The screen's route under [`src/routes/`](src/routes/), with a comment where the reason isn't obvious |
| Feature behavior and exact wording                              | The feature's spec under [`specs/`](../specs/)                                                       |

Change this guide when a screen needs a role, pattern, or rule that other screens will share. If it conflicts with a spec or [the constitution](../.specify/memory/constitution.md), they win.

## Overview

Administrators use MailArkiv to confirm that mail is stored and to get it back. The interface reads like an archival ledger: dense, calm, and exact about what it knows. Everything is tuned for scanning long lists and comparing technical evidence by eye.

| Principle                          | What it means on screen                                                                                                | Why                                                                                    |
| ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| Say only what was checked          | Status views list facts with their times and counts. They never say "healthy" or show a summary tick.                  | [Architecture rule 7](../docs/ARCHITECTURE.md) forbids overclaiming                    |
| Evidence looks like evidence       | Hashes use the monospace font. Sizes, counts, and timestamps use tabular numerals.                                     | Administrators compare these values by eye                                             |
| Colour marks state and little else | A state is a small dot beside a word, and the word carries the meaning. One accent hue marks links and focus.          | Colour stays noticeable when something is wrong, and no state depends on seeing colour |
| Mail content is hostile            | File names and mail metadata render as plain text isolated with `<bdi>`, with control characters shown as codes.       | Untrusted input must never spoof layout or run script                                  |
| Density serves scanning            | Small body text and compact rows, with no dividers between rows and no cards around them.                              | Administrators scan long lists                                                         |
| Feedback never moves controls      | Errors, results, and progress appear where they push nothing aside. A control that can't act stays in place, disabled. | A control that moves under the pointer takes the next click by mistake                 |

## Colors

Two OKLCH hues feed both themes: `--hue-neutral` (cool slate) and `--hue-accent` (blue). Use the semantic role tokens below, never raw colour values.

| Token                                       | Use for                                                           |
| ------------------------------------------- | ----------------------------------------------------------------- |
| `--canvas`                                  | The app shell and sidebar, behind everything                      |
| `--panel`                                   | The main work surface and standalone pages                        |
| `--raised`                                  | Drawers, dialogs, and toasts                                      |
| `--scrim`                                   | Backdrop behind a modal drawer or dialog                          |
| `--text-1`                                  | Names, primary values, headings, and the primary button fill      |
| `--text-2`                                  | Button and field labels, secondary values, what happened          |
| `--text-3`                                  | Dates, column heads, fact labels, notes, next steps, short hashes |
| `--text-4`                                  | Form input edges and neutral state dots. Never text               |
| `--line`, `--line-strong`                   | Dividers, and edges of controls that don't need 3:1 contrast      |
| `--fill-1`                                  | Text inputs, drop zones, notice boxes, and row hover              |
| `--fill-2`                                  | Control hover                                                     |
| `--fill-3`                                  | Control press and the current nav item                            |
| `--accent`                                  | Links (`global.css` sets it on every `<a>`)                       |
| `--focus`                                   | Focus rings and active drop-target edges                          |
| `--accent-tint`                             | Active drop-target fill and the glow around a focused input       |
| `--state-ok`, `--state-warn`, `--state-bad` | State dots and invalid input edges only                           |
| `--state-bad-text`, `--state-bad-fill`      | Error text and the navigation problem-count pill                  |

### Theme and contrast

The theme follows `prefers-color-scheme`, with no switch in the app. Check every screen in both themes.

Every pairing meets WCAG 2.2 AA after all translucent layers are composited: 4.5:1 for text, and 3:1 for meaningful non-text (`--text-4` input edges and dots, `--focus`, and state dots). The lines and fills are translucent, so a pair that passes on `--panel` can fail inside a hovered or current item. Measure every new foreground-on-surface pair in Chromium with its fills composited, in both themes. When a pair forces a different role, as `--text-3` on the current nav item's `--fill-3` does, say why in a comment beside it.

## Typography

Inter Variable (`--font-sans`) sets all interface copy. Geist Mono Variable (`--font-mono`) sets hashes and technical codes. Both are bundled at build time ([ADR 0004](../docs/adr/0004-interface-fonts-and-icons.md)), so the app makes no external network requests.

| Role     | Tokens                                                   | Weight                                         | Use for                                         |
| -------- | -------------------------------------------------------- | ---------------------------------------------- | ----------------------------------------------- |
| Label    | `--size-label`, `--leading-label`                        | regular or medium                              | Dates, sizes, column heads, field labels, notes |
| Evidence | `--size-evidence`, `--leading-body`, `--font-mono`       | regular                                        | Full SHA-256 hashes                             |
| Body     | `--size-body`, `--leading-body`                          | regular, or medium for names and button labels | List rows, prose, and controls                  |
| Section  | `--size-section`, `--leading-body`, `--tracking-section` | strong                                         | View titles, drawer headers, section headings   |
| Title    | `--size-title`, `--leading-title`, `--tracking-title`    | strong                                         | Standalone page headings and message subjects   |

The sizes sit close together, so hierarchy comes from weight and the four text colours. A screen rarely needs more than Label, Body, and Section.

- Sizes, counts, and timestamps use `font-variant-numeric: tabular-nums`, and numeric columns align right. Names don't, because tabular numerals widen Inter's hyphen.
- Hashes and codes use `--font-mono` with `font-variant-ligatures: none`. A short hash is the first 8 characters at label size in `--text-3`.
- A full hash is never shortened. It shows in groups of four so two hashes can be compared by eye, and copying it gives the 64 characters without spaces. Use [`FullHash.svelte`](src/lib/components/FullHash.svelte).

## Layout

Signed-in pages pair a sticky sidebar on `--canvas` with one `--panel` surface inset by `--space-8`. The sidebar's logo row and every view's header are `--header-height` tall, so the logo lines up with the view title. Standalone pages (sign-in and full-page errors) sit directly on `--panel` with no sidebar. A standalone form sits in `--form-width`, a fifth of the way down the viewport rather than centred, so a line added below it never moves its fields.

Every signed-in view centres its body in `--content-width`, and everything in the view starts and ends on that column's edges: the title, row text, section labels, and the header's last control. `--header-inset` puts the header's contents there. A row's padding and hover fill extend into the gutter through a negative margin, so the text doesn't step in from the title. A reader should see one edge, not two.

Facts sit in a fixed label column beside their values, on Status, on a source's page, and in drawers, so values start on one edge and can be compared down the page.

Sticky and fixed parts never hide the focused control. A sticky header sets `scroll-padding-top` to its height, and the bottom tab bar and toasts pad the bottom (WCAG 2.4.11).

### Responsive

Every flow works by keyboard and down to 360 px wide, with no horizontal scroll.

| Width             | What changes                                                                                                                                                                                                                                                                                                                                                             |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 1040 px and below | Rows with several columns fold onto two lines and drop the column header row. Two-column rows take one column.                                                                                                                                                                                                                                                           |
| 720 px and below  | The sidebar becomes a bottom bar of `--tab-bar-height` tabs. Gutters narrow to `--space-16`. Controls grow to `--control-height-touch`, and inputs use `--size-input-touch` so iOS Safari doesn't zoom on focus. Rows stack into blocks separated by `--line`. Labels move above their values. Drawers and toasts take the full width, and toasts sit above the tab bar. |

## Elevation & Depth

Hierarchy comes from three tonal surface layers (`--canvas` behind `--panel` behind `--raised`) and 1 px hairline borders (`--line`, `--line-strong`).

A shadow means an element sits above the page:

- `--shadow-panel` frames the main `--panel` surface. In dark mode, surfaces lighten as they rise, so it is only the 1 px `--line` edge.
- `--shadow-pop` lifts modal drawers, dialogs, and toasts on `--raised`, with `--scrim` behind modal ones.
- Nothing else gets a shadow.

Windows High Contrast strips `box-shadow`. Draw every boundary that matters (inputs, notices, toasts) with a `border`, transparent if need be, and pair shadow focus rings with a transparent `outline` so forced-colour modes still paint the edge.

## Shapes

Corner radii stay tight and mechanical, to match the logo's cabinet geometry:

- `--radius-small`: character-code badges and small marks.
- `--radius-medium`: buttons, inputs, navigation items, list rows, and inline notices.
- `--radius-large`: the main `--panel`, drop zones, drawers, dialogs, and toasts.
- Pills (`9999px`): only the navigation problem-count badge.

### Icons and brand marks

Interface icons come from Lucide (`~icons/lucide/...`) through `unplugin-icons`, with `autoInstall` off, so an icon from a set that isn't installed fails the build instead of pulling in a package. They render at `--icon-size`, or `--icon-size-small` in tight places, with a thinner stroke to match the text, and are hidden from screen readers. Give every icon-only button an `aria-label`. No other icon set or font comes in without an ADR ([ADR 0004](../docs/adr/0004-interface-fonts-and-icons.md)).

[`Logo.svelte`](src/lib/components/Logo.svelte) renders the MailArkiv mark: an 18 px horizontal lockup in headers and the sidebar, or a stacked lockup on standalone pages. Place it on whole pixels, because its small tile and grip are drawn to the pixel grid. Never type "MailArkiv" in Inter beside the mark. Full rules are in [the brand guide](../docs/brand/README.md). Custom drawings such as [`CatalogDrawer.svelte`](src/lib/components/CatalogDrawer.svelte) reuse the mark's tile geometry and the semantic surface and text tokens.

## Motion & Micro-interaction

Motion confirms a state change and never decorates it. Nothing loops, and nothing animates while waiting.

- Hover fills appear at once and fade out over `--motion-fade`.
- Drawers slide in decelerating (`--motion-slide`, `--ease-enter`) and leave faster, accelerating (`--motion-exit`, `--ease-exit`). The scrim fades with them, and the drawer keeps its content until it has gone.
- Toasts fade in over `--motion-enter`, and the stack rises a few pixels as it appears.
- Skeleton rows are still. A slow archive must not look busy.
- Under `prefers-reduced-motion: reduce`, nothing animates or fades.

## Voice & Content Strategy

UI copy follows the Writing rules in [AGENTS.md](../AGENTS.md): sentence case, buttons that start with a verb, and failures that say what happened before what to do next. The feature spec sets the exact words. State words and failure categories live in one module per feature, copied from the spec's tables, such as [`src/lib/sources/words.ts`](src/lib/sources/words.ts).

- **State, not verdict.** Name the state and its evidence (`Last successful sync`, `18,342 messages`), never a judgement such as "healthy" or "all good".
- **Numbers.** Counts carry thousands separators and their noun (`1,284 messages`, `12 new`).
- **Times.** Use `2 Nov 2026, 09:14:55 +01:00`, in the viewer's time zone with an explicit UTC offset, wherever a person compares times. Format through [`src/lib/format/time.ts`](src/lib/format/time.ts).
- **Missing values.** An absent untrusted value reads as a placeholder in parentheses, such as `(no file name)`, in `--text-3` at regular weight, so it can't pass for a real value.
- **Work in progress.** A busy control says what it's doing (`Saving…`, `Connecting…`). Buttons never spin.
- **Failed attempts.** Say what happened, then the next step, then where to look if it repeats: `Connect didn't finish within 150 seconds. Nothing was saved. Try again. If this repeats, check the server log.`

## Components

Reuse these before writing new markup. Each entry says what the pattern is for and the rules a screen must keep; the component or class holds the details.

### Buttons

| Variant | Class            | Use                                                   |
| ------- | ---------------- | ----------------------------------------------------- |
| Default | `button`         | Most actions                                          |
| Primary | `button primary` | The view's main action. At most one per view          |
| Ghost   | `button ghost`   | Clear, Close, Copy, Dismiss, and row icon links       |
| Icon    | add `icon-only`  | A square ghost or default button with an `aria-label` |
| Large   | add `large`      | Forms, standalone pages, drawers, and dialogs         |

- A button that can't act is `disabled`. A link, or a submit button that is busy and must keep keyboard focus, uses `aria-disabled="true"` instead.
- An action that toggles, such as Pause and Resume, is one button whose label follows the state, so focus stays on it.
- A destructive or discarding action asks first in a confirmation dialog.

### Form fields and notices

A `field` label wraps a label line above its input. An invalid field gets `aria-invalid="true"` and one line in `--state-bad-text` below it, linked with `aria-describedby`; after a refused submit, focus moves to the first such field. Focus styling overrides the invalid edge, so the active field still changes visibly.

A notice above a form is a `--fill-1` box with `--radius-medium`: what happened in `--text-2`, then the next step in `--text-3`. An action's result goes below its submit button, or at the end of a drawer's body in a `role="status"`, so the fields never move.

### Navigation

The sidebar lists Messages, Sources, and Status, in that order, each with a Lucide icon. An item stays current on every page below it. At 720 px and below the same items become the bottom tabs, icon over label. A status item with active problems carries a pill in `--state-bad-fill` with `--state-bad-text`.

### Lists and rows

- Rows have no dividers and no cards. A row takes `--fill-1` on hover or keyboard focus, and only where the pointer can hover, so a tap leaves nothing lit. A row that opened a drawer stays marked in `--fill-2` until it closes.
- Rows share column tracks through CSS `subgrid`, so metadata columns line up down the list. Size a fixed column for its widest real value.
- A row that opens something is one keyboard stop: its name is the link or button and its hit area covers the row. A secondary action, such as download, is a second stop, an icon in `--text-3` that brightens with its row and runs without opening the row.
- A list built without table markup hides its visual column header from screen readers and gives unlabelled values a visually hidden label.
- Skeleton rows keep the real columns and row heights, so the list doesn't jump when data arrives.
- An empty list says so in `--text-3`. Where the list is how something gets added, its place holds a `--fill-1` box with a heading, what the thing is, and the view's one primary button, as the empty Sources list does.

### State marks

A state is a dot of `--dot-size` beside a state word: `--state-ok`, `--state-warn`, `--state-bad`, or neutral `--text-4`. The word carries the meaning. Where states stack in a column, the dot hangs in the gutter so the words start on one edge; at narrow widths it sits inline.

Status groups and outcome toasts share one order: the dot and state word, the subject, what happened in `--text-2`, and the next step in `--text-3`. See [`SourceState.svelte`](src/lib/components/SourceState.svelte) and [`Toasts.svelte`](src/lib/components/Toasts.svelte).

### Toasts

Toasts sit in a polite live region on `--raised` with `--shadow-pop` and stay until dismissed or replaced, never on a timer. While a modal drawer is open they render inside it, because the page behind is `inert`. Dismissing one moves focus to the next toast or the region, never to `<body>`.

### Failed loads and live data

When a view or drawer can't load its data, [`FailedLoad.svelte`](src/lib/components/FailedLoad.svelte) takes the content's place: what happened, the next step, `Try again`, and `Last tried` with the time, which gives the server log a place to look. Each failed try fades the block in again, so even a quick failure is seen and heard. When a retry succeeds and the button goes, focus moves to the view's heading, or to Close in a drawer.

A view that refreshes itself, such as Sources while a source syncs, starts each reload after the last one finishes and keeps focus where it was. A new state is announced through a visually hidden status, without moving focus. A failed reload shows the failed-load block, because stale numbers could claim work that has ended.

### Drawers and dialogs

A modal drawer is a native `<dialog class="drawer">` opened with `showModal()` on the right edge. Its header names the kind of thing in `--text-3` over which one in `--text-1`, the body scrolls, and a pinned footer keeps the main action in view. `Escape`, Close, Back, and a click on the scrim close it and return focus to what opened it. A press that starts inside the drawer, such as one selecting text, never closes it. A drawer holding edits asks before discarding them. One with a save in flight doesn't close until the save is answered, so a save never ends unseen.

A confirmation is a centred native `<dialog>` in `--dialog-width`. Its title names exactly what it acts on, `Cancel` takes focus first so `Enter` alone changes nothing, and a failure keeps it open with a line below the buttons. See [`RemoveSource.svelte`](src/lib/components/RemoveSource.svelte).

### Drop zone

[`ImportBox.svelte`](src/lib/components/ImportBox.svelte) is a `--fill-1` box with a `--line` edge. Dragging a file over it switches to a `--focus` edge and `--accent-tint` fill. It always keeps an explicit `Choose a file` button, because dropping is an extra way in, not the only one. Each step (ready, chosen, uploading, storing, refused) updates inside the box.

### Untrusted text

Render every name or string that came from mail, an upload, or a mail server through [`UntrustedText.svelte`](src/lib/components/UntrustedText.svelte): plain text in its own `<bdi>`, with control and bidi characters shown as `--radius-small` code badges such as `U+202E`. Never use `{@html}`.

- A long name is cut where the cut hides the least: a file name in the middle, so its start and extension show, and a domain at its front, so the part that names its owner shows.
- When two untrusted strings share a line, such as a sender's name and address or a source and folder, the first is medium `--text-1` and the second regular `--text-2`, each isolated on its own. Either can hold any character, so weight and colour are the only boundary. Say which one gives way first, and cap the other so it can't push its neighbour out.
- A mail server's reply keeps `--font-mono`, so its words don't read as MailArkiv's.

## Do's and Don'ts

- Do edit token values only in [`tokens.css`](src/lib/styles/tokens.css), and re-measure contrast in both themes whenever a value changes.
- Do use `:focus-visible` with `outline: 2px solid var(--focus)` and a `2px` offset, or `-2px` where a container edge would clip the ring.
- Do keep or deliberately move keyboard focus whenever a list refreshes, a step changes, or something closes.
- Do read the closest existing screen and reuse its components before building a new one.
- Do put a screen's measurements in its own CSS, with a comment when the reason isn't obvious.
- Don't claim the archive is "healthy" or use summary ticks. Report what was checked and when.
- Don't rely on colour alone. Every state dot has a state word.
- Don't time out outcome or error toasts, or spin a button during background work.
- Don't render untrusted content with `{@html}`, or echo raw URL paths on error screens.
- Don't add a typeface or icon set without an ADR.
- Don't add screen-specific measurements, copy, or step-by-step behavior to this guide.

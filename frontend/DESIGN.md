# MailArkiv interface design

This guide defines the MailArkiv visual system and says which token role to use where. Exact values live in [`src/lib/styles/tokens.css`](src/lib/styles/tokens.css), so change them there and nowhere else. Feature behavior and exact UI copy come from the specs under [`specs/`](../specs/). If this file conflicts with a spec or [the constitution](../.specify/memory/constitution.md), they win.

## Overview

Administrators use MailArkiv to confirm that mail is stored and to get it back. The interface reads like an archival ledger: dense, calm, and exact about what it knows. Everything is tuned for scanning long lists and comparing technical evidence by eye.

| Principle                          | What it means on screen                                                                                          | Why                                                                                    |
| ---------------------------------- | ---------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| Say only what was checked          | Status views list facts with their times and counts. They never say "healthy" or show a summary tick.            | [Architecture rule 7](../docs/ARCHITECTURE.md) forbids overclaiming                    |
| Evidence looks like evidence       | Hashes use the monospace font. Sizes, counts, and timestamps use tabular numerals.                               | Administrators compare these values by eye                                             |
| Colour marks state and little else | A state is a 6 px dot beside a word, and the word carries the meaning. One accent hue marks links and focus.     | Colour stays noticeable when something is wrong, and no state depends on seeing colour |
| Mail content is hostile            | File names and mail metadata render as plain text isolated with `<bdi>`, with control characters shown as codes. | Untrusted input must never spoof layout or run script                                  |
| Density serves scanning            | 13 px body text and 38 px rows, with no dividers between rows and no cards around them.                          | Administrators scan long lists                                                         |

## Colors

Two OKLCH hues in [`tokens.css`](src/lib/styles/tokens.css), `--hue-neutral` (cool slate) and `--hue-accent` (blue), feed both themes. Use semantic role tokens, never raw colour values.

| Token                                       | Use for                                                                   |
| ------------------------------------------- | ------------------------------------------------------------------------- |
| `--canvas`                                  | The app shell and sidebar, behind everything                              |
| `--panel`                                   | The main work surface and standalone pages                                |
| `--raised`                                  | Drawers, dialogs, and toasts                                              |
| `--scrim`                                   | Backdrop behind a modal drawer or dialog                                  |
| `--text-1`                                  | File names, primary values, headings, and the primary button fill         |
| `--text-2`                                  | Button and field labels, secondary values, counts in the current nav item |
| `--text-3`                                  | Dates, column heads, notes, next steps, short hashes                      |
| `--text-4`                                  | Form input edges and neutral state dots. Never text                       |
| `--line`, `--line-strong`                   | Dividers, and edges of controls that don't need 3:1 contrast              |
| `--fill-1`                                  | Text inputs, drop zones, notice boxes, and row hover                      |
| `--fill-2`                                  | Control hover                                                             |
| `--fill-3`                                  | Control press and the current nav item                                    |
| `--accent`                                  | Links (`global.css` sets it on every `<a>`)                               |
| `--focus`                                   | Focus rings and active drop-target edges                                  |
| `--accent-tint`                             | Active drop-target fill and the glow around a focused input               |
| `--state-ok`, `--state-warn`, `--state-bad` | 6 px state dots and invalid input edges only                              |
| `--state-bad-text`, `--state-bad-fill`      | Error text and the navigation problem-count pill                          |

### Theme and contrast

The theme follows `prefers-color-scheme` in `tokens.css`, with no manual switch in the app. Check every screen in both themes.

Every pairing must meet WCAG 2.2 AA across all composited translucent layers: 4.5:1 for text and 3:1 for meaningful non-text (`--text-4` input borders and neutral dots, `--focus`, and `--state-*` dots).

- `--text-3` on `--fill-3` over `--canvas` falls to 4.44:1 in light mode, so a count inside the current navigation item uses `--text-2`.
- `--state-bad-text` on `--state-bad-fill` stays above 4.5:1 even when its parent navigation item is hovered (`--fill-2`) or current (`--fill-3`).
- Measure any new foreground-on-surface pair in Chromium with all translucent fills composited before shipping it.

## Typography

Inter Variable (`--font-sans`, weights `--weight-regular` 400, `--weight-medium` 510, `--weight-strong` 590) sets all interface copy. Geist Mono Variable (`--font-mono`) sets hashes and technical codes. Both are bundled at build time ([ADR 0004](../docs/adr/0004-interface-fonts-and-icons.md)), so the app makes no external network requests. The bundled Inter omits the `cv01` and `ss03` alternates, so don't set those font features.

| Role     | Size and line                                 | Weight                                         | Use for                                         |
| -------- | --------------------------------------------- | ---------------------------------------------- | ----------------------------------------------- |
| Label    | `--size-label`, `--leading-label` (12/16)     | regular or medium                              | Dates, sizes, column heads, field labels, notes |
| Evidence | `--size-evidence`, `--leading-body` (12.5/20) | regular, mono                                  | Full SHA-256 hashes                             |
| Body     | `--size-body`, `--leading-body` (13/20)       | regular, or medium for names and button labels | List rows, prose, and controls                  |
| Section  | `--size-section`, `--leading-body` (14/20)    | strong, `--tracking-section`                   | View titles, drawer headers, section headings   |
| Title    | `--size-title`, `--leading-title` (20/26)     | strong, `--tracking-title`                     | Standalone page headings and message subjects   |

### Formatting data and copy

- Sizes, counts, and timestamps use `font-variant-numeric: tabular-nums`. Counts include thousands separators (`1,284`), and numeric list columns align right.
- A short hash shows the first 8 characters in `--font-mono` at `--size-label`, coloured `--text-3`.
- A full hash shows all 64 characters as 16 groups of 4 across 4 columns, with `font-variant-ligatures: none` and a button that copies the 64 characters without spaces. Grouping makes the hash comparable by eye; never shorten a full hash. A click selects the whole hash, and a copied selection is the 64 characters too, not 16 lines. Copy turns its icon into a check and shows `Hash copied.` for 2 seconds. A failed copy selects the hash and names the key to press. Each Copy button is described by its short hash, so Status doesn't list identical buttons.
- Untrusted file names use medium weight in `--text-1`, isolated with `<bdi>`. Long names cut from the middle so the start and the extension stay visible. The ending is 12 characters. It grows to as many as 24, or shrinks, to keep the cut out of a run that reads against the name's direction, such as `2026` in a Hebrew name, whose halves would swap. In a column narrower than the ending, the ending loses its front, so the extension shows to the last. Control and bidi characters render as small `--radius-small` code badges such as `U+202E`. A missing name reads `(no file name)` in `--text-3` at regular weight.
- Source names, folder names, user names, and server replies follow the same rules through [`UntrustedText.svelte`](src/lib/components/UntrustedText.svelte), which file names use too.
- Timestamps use `2 Nov 2026, 09:14:55 +01:00`, in the viewer's time zone with an explicit UTC offset, everywhere a person compares times.
- UI copy follows the Writing rules in [AGENTS.md](../AGENTS.md): sentence case, buttons starting with a verb, and failures stating what happened before what to do next.

## Layout

Spacing tokens are named for their pixel values (`--space-2` through `--space-32`) and govern all gaps and padding.

| Token                    | Value  | Use for                                  |
| ------------------------ | ------ | ---------------------------------------- |
| `--control-height`       | 28 px  | Buttons in views and row actions         |
| `--control-height-large` | 32 px  | Form inputs and form or drawer buttons   |
| `--control-height-touch` | 36 px  | Buttons and inputs at 720 px and below   |
| `--row-height`           | 38 px  | Single-line list rows                    |
| `--header-height`        | 52 px  | Sidebar brand row, view headers, drawers |
| `--sidebar-width`        | 224 px | Desktop navigation sidebar               |
| `--tab-bar-height`       | 44 px  | Bottom tab bar at 720 px and below       |
| `--content-width`        | 880 px | Centred reading and status column        |
| `--header-inset`         | 24 px or more | View header side padding that lines up with the content column |
| `--drawer-width`         | 440 px | Right-hand modal detail drawer           |
| `--form-width`           | 300 px | Standalone forms and error messages      |
| `--form-width-wide`      | 440 px | Editing a source                         |
| `--dialog-width`         | 400 px | Confirmation dialogs                     |
| `--toast-width`          | 380 px | Desktop toast notifications              |

Signed-in pages pair a sticky `--sidebar-width` sidebar on `--canvas` with a single `--panel` surface inset by `--space-8`. Both the sidebar's logo row and the view's header are `--header-height` (52 px) tall, aligning the logo with the view title. The Messages header sticks to the top, because it carries the pager, and the page keeps `scroll-padding-top` at its height so focus scrolls clear of it (WCAG 2.4.11). Sign out sits at the foot of the sidebar; if sign-out fails, the error line goes above the button so the button stays under the pointer for the next try. Every signed-in view centres its body in `--content-width`. Its header's line spans the panel, but `--header-inset` pads the header's contents onto the column's edges, so the title starts where the content starts and the last header control ends where it ends. Without it, a wide screen leaves the title at the panel's left, the content in the middle, and the button at the far right.

Standalone pages (sign-in and full-page errors) sit directly on `--panel` with no sidebar, constrained to `--form-width`. Forms sit 20% of the way down the viewport instead of vertically centred, so an error line added below never moves the fields.

### Responsive breakpoints

Every flow must work by keyboard and down to 360 px with no horizontal scroll.

| Width               | Layout adjustment                                                                                                                                                                                                  |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 721 to 1040 px      | Multi-column list rows beside the sidebar take two lines at 54 px (name on line 1, metadata on line 2, count and action across both) and hide the column header row.                                               |
| 720 px and below    | The sidebar becomes a fixed bottom bar of `--tab-bar-height` tabs (icon over label). `scroll-padding-bottom` keeps focused controls clear of the bar and active toasts.                                            |
| 720 px surfaces     | Page gutters narrow to `--space-16`. Drawers fill the width with no corner radius. Toasts fill the width above the bottom tab bar.                                                                                 |
| 720 px controls     | Buttons and inputs grow to `--control-height-touch` (36 px). Inputs use `--size-input-touch` (16 px) so iOS Safari does not zoom on focus.                                                                         |
| 720 px content flow | List rows stack into multi-line blocks separated by `--line`: the name across the full width, then metadata lines with the trailing action beside them. Two-column sections stack labels above values. Drop zones move their button below the text at full width and drop decorative icons. |

## Elevation & Depth

Hierarchy comes from three tonal surface layers (`--canvas` behind `--panel` behind `--raised`) and 1 px hairline borders (`--line`, `--line-strong`).

A shadow means an element sits above the page:

- `--shadow-panel` frames the main `--panel` surface with a 1 px `--line` border and a subtle drop shadow. In dark mode, surfaces lighten as they rise, so `--shadow-panel` is only the 1 px `--line` edge.
- `--shadow-pop` lifts modal drawers, dialogs, and toasts on `--raised`, paired with `--scrim` behind modal dialogs.
- Nothing else gets a shadow.

Windows High Contrast strips `box-shadow`. Draw every boundary that matters (inputs, notices, toasts) with a `border`, and pair shadow focus rings with a transparent `outline` so forced-colour modes still paint the edge.

## Shapes

Corner radii stay tight and mechanical to match the logo's cabinet geometry:

- `--radius-small` (4 px): Inline character-code marks and small badges.
- `--radius-medium` (6 px): Buttons, inputs, navigation items, list rows, and inline notices.
- `--radius-large` (10 px): The main `--panel`, drop zones, drawers, and toasts.
- Pills (`9999px`): Navigation problem-count badges (18 px tall).

### Icons and brand marks

All fonts and icons are bundled at build time ([ADR 0004](../docs/adr/0004-interface-fonts-and-icons.md)).

Interface icons come from Lucide (`~icons/lucide/...`) via `unplugin-icons`, with `autoInstall` off so a mistyped name fails the build. They render at `--icon-size` (16 px), or `--icon-size-small` (14 px) in tight places, with `stroke-width: 1.6` to match 13 px Inter text and `aria-hidden="true"` set at build time. Give icon-only buttons an explicit `aria-label`.

[`Logo.svelte`](src/lib/components/Logo.svelte) renders the MailArkiv mark (a solid tile in `currentColor` with a cut-out drawer grip and outlined Archivo wordmark) as an 18 px horizontal lockup in headers and sidebars, or as a stacked lockup (40 px minimum tile) on standalone pages. The small drawings put the tile and grip on whole pixels, so place the logo on whole pixels too: the sidebar and error page do by their insets, and the stacked lockup centres its tile with `round()` inside a column that [`Standalone.svelte`](src/lib/components/Standalone.svelte) also rounds. Never type "MailArkiv" in Inter beside the mark. Full rules live in [the brand guide](../docs/brand/README.md). Custom drawings such as [`CatalogDrawer.svelte`](src/lib/components/CatalogDrawer.svelte) reuse the mark's tile geometry and semantic surface and text tokens.

## Components

### Button

| Variant | Class            | Look                                        | Use                                             |
| ------- | ---------------- | ------------------------------------------- | ----------------------------------------------- |
| Default | `button`         | 1 px `--line-strong` edge, `--text-2` label | Most actions                                    |
| Primary | `button primary` | `--text-1` fill, `--panel` label            | At most one per view, for its main action       |
| Ghost   | `button ghost`   | No edge                                     | Clear, Close, Copy, Dismiss, and row icon links |
| Icon    | add `icon-only`  | Square, ghost or default, with `aria-label` | Row download, page arrows, Copy, Close, Dismiss |
| Large   | add `large`      | `--control-height-large`                    | Form, standalone, and drawer actions            |

Default and ghost buttons hover to `--fill-2` with `--text-1` and press to `--fill-3`. Primary buttons drop to 86% opacity on hover and 76% on press. Disabled controls use 45% opacity and `pointer-events: none`: use `disabled` on buttons, or `aria-disabled="true"` on links and on busy submit buttons that must keep keyboard focus while a request runs. Buttons never spin; long work shows its progress inline or in a toast.

### Form field and notice

Use the `field` class on a `<label>` wrapping a `<span>` label (`--size-label`, medium, `--text-2`) above an `<input>` (`--control-height-large`, `--fill-1` fill, 1 px `--text-4` border, `--radius-medium`).

Focusing an input clears its fill, sets the border to `--focus`, and adds a 3 px `--accent-tint` ring. Setting `aria-invalid="true"` turns the border to `--state-bad`, with one line in `--state-bad-text` below linked via `aria-describedby`. Focus styling overrides the invalid border so the active field still changes visibly.

Contextual notices above a form use a `--fill-1` box with `--radius-medium` and a 1 px transparent border for forced colours. Action errors from the last attempt stay below the submit button so the form fields never move.

### Navigation item

Navigation links are 30 px tall with `--radius-medium`, medium weight, and a 16 px icon (`--text-3` at rest). Text is `--text-2` at rest, `--fill-2` on hover, and `--fill-3` with `--text-1` when current. A status item with active problems carries an 18 px pill in `--state-bad-fill` with `--state-bad-text`.

### Lists and rows

List rows are `--row-height` (38 px) with `--radius-medium` and no dividers between rows. A row takes `--fill-1` on hover or while it holds keyboard focus, and `--fill-2` from a click until its drawer closes, so a slow load shows the click and the drawer's row stays marked beside it. Hover lights a row only where the pointer can hover, so a tap leaves nothing lit on a touch screen. Rows share column tracks through CSS `subgrid` so fixed metadata columns (such as a 185 px timestamp column sized for the widest date in 12 px tabular Inter, a 62 px byte size, or a 72 px short hash) stay aligned.

A message row names the message by its latest entry. An import shows its file name. A collection shows the source name in `--text-1`, then the folder in `--text-2` at regular weight, each cut in its own middle. The folder gives way first, and the source name takes at most half the column. Any character can appear in either name, so only weight and colour mark where the source name ends. The time is that entry's, and the count counts every entry, imports and collections alike.

When a list uses non-table markup, hide the visual column header row from screen readers and attach visually hidden labels (`Imported` or `Collected`, `Hash`, `3 entries`) to values that do not name themselves. A row with both a detail view and a secondary action has two keyboard stops: `Enter` on the row opens its details (calling `preventDefault()` so focus moving to Close does not immediately close the dialog), and the trailing icon button runs its action without opening the row. That icon rests in `--text-3`, so a column of them doesn't compete with the names, and turns `--text-2` with its row.

Skeleton rows keep the real column widths and responsive row heights without animation, so the list never jumps when data arrives and a slow archive does not look busy. The pager sits at the right of the Messages header: the range and total (`1–50 of 7,000`) in `--text-3` at label size, then ghost icon links to the previous and next page. A turn goes to the top of the page, so the next turn is where the page lands, and the header keeps the pager in view as the list scrolls. An end keeps its arrow, off, so the other never moves, and a single page shows `1–7 of 7` with both off. Screen readers hear the range in words (`Messages 1 to 50 of 7,000`), because some skip the dash. While another page loads, the pager shows that page's range at once and keeps its arrows. Past page 1 the document `<title>` carries the page number, so screen readers announce each turn.

### Drop zone

Staged file-import boxes use `--fill-1` with a 1 px `--line` edge and `--radius-large`, at least 76 px tall. Dragging a file over the zone switches to a 1.5 px `--focus` border and `--accent-tint` fill. Always keep an explicit "Choose a file" button; dropping a file adds to that button and never replaces it. Step changes (ready, file chosen with exact byte size, uploading with progress bar, storing, or browser-side refusal) update in place inside the box.

### Modal drawer

Drawers use a native `<dialog>` with the `drawer` class from `global.css`, opened with `showModal()` on `--raised` (`--drawer-width` wide, attached to the right edge, with `--shadow-pop` and `--scrim`). A `--header-height` header holds the title and Close button, the body scrolls, and a pinned footer keeps primary actions in view. Closing on `Escape`, Close, Back, or a click on the scrim returns focus to the element that opened the drawer. A press that starts inside the drawer, such as one selecting the hash, never closes it. A drawer holding an edited form asks before discarding it, so a stray click can't throw away what was typed.

The header names what the drawer shows in `--text-3` at label size, over which item it shows in `--text-1` at section size, so the drawer says what it's about where it covers the list. The history names the message by its newest entry, as its row does, cut in the middle on one line, and the dialog's accessible name reads `History of statement.eml`. Facts sit in a 96 px label column beside their values, as on Status, and stack at 720 px and below. A hairline separates them from the history. The history's heading carries its count (`Entries 9`), and each entry is a timeline dot beside the time MailArkiv recorded it. An import shows the file name in full and `Manual import by` the account. A collection shows the folder in full, then label-size rows for `Collected from` the source name, `Server`, `User name`, and `Received`, the time the mail server received the message or `Unknown`. A removed source's entries read as they did before. The footer pairs Download, at its natural width at the end, with a note in `--text-3` that names the saved file (`Saves as e33d74d4…acd8.eml`), because FR-021 keeps the supplied name off the download.

### Status items, toasts, and failed loads

Status problem groups and outcome toasts share one structure: a `--dot-size` (6 px) dot (`--state-ok`, `--state-warn`, `--state-bad`, or neutral `--text-4`) beside a state word, followed by the subject in `--text-1`, what happened in `--text-2`, and the next step in `--text-3`.

Toasts sit in a polite live region (`aria-live="polite"`, `--toast-width`, 24 px from viewport edges) on `--raised` with `--shadow-pop`, `--radius-large`, and a 1 px transparent border. While a modal drawer is open, toasts render inside the drawer above its footer so background `inert` does not block them. The toast region publishes its height as `--toast-room` so page and drawer scrolling pad clear of active toasts (WCAG 2.4.11). Toasts stay until dismissed or replaced, with no timer. Dismissing a toast moves focus to the next toast's action or the region so focus never drops to `<body>`.

When a view or drawer cannot load its data, replace the content area with a block that starts where the content would: what happened in `--text-2` and the next step in `--text-3` in a `role="status"`, a default `Try again` button, and `Last tried` with the time at label size in its own `role="status"`, as on the error page. The time gives the server log a place to look, and a failed try fades a new one in, so even a quick failure is seen and heard. A try that lasts 400 ms reads `Trying again…` with `aria-disabled`, so focus stays, and presses while it runs send nothing. A streamed list counts as part of the try. When `Try again` succeeds and removes the button, move focus to the view's heading.

### Sources

The main navigation lists Messages, Sources, and Status, in that order. Sources uses Lucide's mail icon and stays current on `/sources` and every page below it, including source details. It uses the existing sidebar item and bottom tab styles, and remains visible when the server has no key.

A source's state is a dot beside the state word in medium `--text-1`, then what the state is shown with in `--text-2`. The dot hangs beside the first line, so text that wraps starts under the state word. State words and category text come from [`words.ts`](src/lib/sources/words.ts), which copies the spec's tables.

| State           | Dot            | Shown with                                                             |
| --------------- | -------------- | ---------------------------------------------------------------------- |
| Waiting         | `--state-ok`   | `Next sync` and its time                                               |
| Syncing         | `--text-4`     | `Archived 120 of 4,500 found`                                          |
| Retrying        | `--state-warn` | `Timed out. Next attempt` and its time                                 |
| Needs attention | `--state-bad`  | The category label and its next step: `Sign-in rejected. Save the …`   |
| Paused          | `--text-4`     | `Since` and its time                                                   |

The Sources list centres in `--content-width`. Each source is a row, separated from the next by a `--line` hairline, in two columns: who the source is, then a 440 px column for its state and the evidence. The states of every source sit in one column, so a reader finds the one that needs attention by running down it. The whole row opens the source's page, as a message row opens its history: the name is the link, in medium `--text-1`, and its `::after` covers the row. A row takes `--fill-1` on hover or keyboard focus and hides the hairlines beside it. A chevron at the row's end rests in `--text-4` and turns `--text-2` with its row.

Under the name come the mailbox in `--text-3` at label size, worded as on the add form (`anna@example.org on imap.example.org, port 993, TLS`), then the chosen folders in sync order after a 14 px folder icon, separated by commas, each cut in its own middle. Server, user name, and folders rarely change, so they stay quiet. The second column has the state line, then the facts in Status's 136 px label column at label size, labels in `--text-3` and values in `--text-2`: `Last successful sync` (the time and `archived 12`, or `None yet`), `Collected`, and, when any count is above zero, `Problems` (`1 folder unavailable, 2 messages waiting for retry`) after a `--state-warn` dot, as Status marks these problems. Lines sit 4 px apart in both columns, so the mailbox is level with the first fact. A line breaks before a time and never inside it, and a comma stays with the name before it. Only times and counts use tabular numerals, because they widen Inter's hyphen in names such as `Sign-in` or `mail-1.example.org`.

At 1040 px and below a source takes one column: the name, the state line, the mailbox and folders, then the facts, which stack at 720 px and below with 8 px between them. The header shows the count (`6 sources`) in `--text-3` at label size before its button.

With no sources, the list's place holds a box shaped like the Messages drop zone (`--fill-1`, 1 px `--line` edge, `--radius-large`), because it's the other way mail comes in. A section heading reads `No sources yet`. Below it, in `--text-2`: `A source is an IMAP mailbox. MailArkiv first archives every message in the folders you choose, then checks for new mail every 5 minutes. It only reads the mailbox and never changes it.` Then, in `--text-3`: `You'll need the server's address, a user name, and a password.` The text stops at 64 characters a line, and `5 minutes` never breaks. `Add a source` follows as the view's one primary button, so the header has neither count nor button. At 720 px and below the button takes the full width, as in the drop zone. While the server has no key, the notice already says why, so the box gives way to `No sources yet` in `--text-3`, as quiet as the empty Messages list.

When the server started without its key, a notice box (`--fill-1`, `--radius-medium`, transparent border) above the list says what happened in `--text-2`, then the next step in `--text-3`, linking to the key section of the [operator guide](../docs/OPERATIONS.md). The link targets the `#the-key` anchor, so the guide's section must keep the heading `The key`.

A source's page has a header with a `Sources` link in `--text-2`, a chevron, and the source name as its title. The body uses Status's sections, with each heading in the 136 px label column:

- `State`: the state line. A failure adds its category label in medium `--text-1` and facts: `What happened`, `What MailArkiv does`, and `Next step` from the spec's table, then `Failing since`, `Failed attempts`, `Last attempt`, and `Server's reply`, leaving out any that are null. The reply keeps `--text-1` and wraps in full through `UntrustedText`, because it's the evidence an administrator reads.
- `Source`: the list's facts, without `Folders` and `Problems`.
- `Folders`: each chosen folder on its own line, in medium `--text-1`. An unavailable one has a `--state-warn` dot and `Unavailable since` with the time, and the folder category's three facts follow the list once. A renumbered one notes `Renumbered by the server` with the time, `so MailArkiv collected it again`.
- `Waiting for retry` and `Not archived`: the heading carries the total. Each message takes two lines: the folder in medium `--text-1` and the category label in `--text-2`, then the exact size or `Size unknown`, `Received` with the server's time or `Received time unknown`, and `Recorded` with MailArkiv's time, at label size in `--text-3`. The server sends the newest 100, so a larger total adds `Showing the newest 100 of 1,284.` Each category in the list follows it once with its three facts. An empty list reads `None.`

The list and a source's page reload their data every 3 seconds while a source on them syncs, so the archived count climbs as messages are stored. Each reload starts after the last one finished, reloads stop once nothing syncs, and focus stays where it was. A failed reload shows the failed-load block, because stale counts could claim a sync that has ended.

On Status, problem sources come first in the Problems section, above the failure groups. Each has a dot beside its name, which links to its page: `--state-bad` for a failed sync, and `--state-warn` for an unavailable folder or a message waiting for retry, since the rest of the source still collects. Below the name come the category label in `--text-2`, then `Since` with the time and the failed attempts at label size in `--text-3`. A source's problem can predate the latest start, so while any is listed the footnote adds that a source stays listed until a sync clears its problem.

### Adding, changing, and removing a source

The Sources header ends with `Add a source`, a link styled `button primary`. It's left out while the server has no key, because the notice says why nothing can be added, and while there are no sources, because the empty box carries it.

`Add a source` opens a modal drawer over the Sources list, at `/sources?add`, so Back closes it and the address can be shared. Adding is a short task started from the list, and a drawer keeps the list in view, scrolls a long folder list under a pinned `Save`, and matches the form's 440 px width. The address does nothing while the server has no key. A plain click keeps focus on the link, so closing gives it back there; after a direct address, focus goes to the link too. Each opening starts empty, with focus in Server after the fields render.

The header puts `Add a source · Step 1 of 2` in `--text-3` at label size over the step's name at section size, as the history drawer names what it shows over which message. The dialog's accessible name reads both. The footer holds a large default `Cancel` and the step's large primary button at the end, and the body's fields sit in one column with 16 px between them:

1. `Connect to the mailbox`: a line in `--text-3`, `Connect signs in and lists the mailbox's folders. Nothing is saved until you choose them.`, because the first button doesn't save. Then `Server`, `Connection security` (TLS and STARTTLS radios side by side) with an 88 px `Port` beside it, `User name`, and `Password`. The port reads 993 for TLS and 143 for STARTTLS and follows the security until the administrator types a port. A line under the port spans the row, so it doesn't wrap word by word. The drawer opens with focus in `Server`. A check can take 150 seconds, so while it waits `Connect` reads `Connecting…` with `aria-disabled`, keeping focus. `Cancel` still closes the drawer, and the check's answer is dropped, because a check saves nothing.
2. `Choose folders`, whose heading takes focus. A line in `--text-2` names the mailbox (`anna@example.test on imap.example.org, port 993, TLS`), with a `Change details` button that goes back to step one with everything as typed, above a hairline. Then `Name`, which starts as the user name and is described by `Shown in the source list and in each message's history.` Every field Connect sends belongs to step one, and the name, which the check doesn't send, belongs here with the folders. Then the folder checklist: a fieldset with the legend `Folders`, a row per folder in the server's order, and `INBOX` checked. A row is a 16 px checkbox, the name in medium `--text-1` cut in its middle, and the count (`1,204 messages`) at label size in `--text-3` at the row's end. The count describes the checkbox, so the folder name alone is its accessible name. The footer puts a notice box with the removal warning above the buttons, so no folder list can push it out of view: `Archived mail can't be removed in this version.`, in `--text-2`, then `Messages stay in the archive after you remove a folder or the source.` in `--text-3`. `Save` reads `Saving…` while it waits. Until it's answered, `Escape`, Close, `Cancel`, Back, and scrim clicks do nothing, because a save that ends unseen could add a source nobody sees. It opens the new source's page, where the first sync shows.

Closing an untouched add form needs no confirmation. After any field changes or a successful Connect, Escape, Close, Cancel, Back, and a click that starts and ends on the scrim ask `Discard this source?` in a centred native dialog, styled like the remove confirmation. It says `The details you entered will be lost. Nothing has been saved.` `Keep editing` takes focus and closes only the confirmation; Escape does the same. `Discard source` closes the form or continues the requested navigation. Closing returns focus to Add a source. A check's late answer is dropped after leaving. A pending Save blocks in-app navigation; reloading or closing the tab uses the browser's unload warning. Session expiry always opens sign-in, even with edits or a pending Save.

Results go at the end of the body in a `role="status"`, scrolled into view, so the fields never move:

- A failed check: a `--state-bad` dot beside the category label in medium `--text-1`, then facts for `What happened`, `Next step`, and `Server's reply` in full, then `Nothing was saved.`
- An existing source with this server and user name: `A source for this server and user name already exists.` and the link `Open the existing source`.
- No key: the Sources notice's words and link.
- A field the server refused: a line under that field, such as `Enter the server's name or address.`, and focus moves to the first such field, so its line is heard. `Save` checks the name and folders first. A refused connection field after `Save` returns to step one.
- No answer within 150 seconds: `Connect didn't finish within 150 seconds. Nothing was saved. Try again. If this repeats, check the server log.` Any other failure drops `within 150 seconds`. `Save` reads the same with its own name.

On a source's page, `Sync now` and `Pause` sit below the state line and any failure, as default buttons. Pause and Resume are one button whose label follows the state, so focus stays on it. `Sync now` is disabled while paused. An action reloads the page as soon as it's answered, which also drops a reload that started before it and would show the old state. A visually hidden status says each new state (`State: Paused`) without moving focus, whether an action or a reload brought it. A refusal shows below the buttons in `--text-2`: `A sync is already running.` or `The source is paused. Resume it to sync.`

`Edit` and `Remove` sit below the source's facts. `Edit` replaces the page's body with a form headed `Edit source`, which takes focus. It has the add form's fields, except that `Server` and `User name` show as facts with `These can't change. To collect another mailbox, add a source.` `Password` starts empty, described by `Leave empty to keep the stored password.` `Folders` lists the source's folders, checked, then `Add folders`, which signs in with the form's details and appends the server's other folders, unchecked, with their counts. It stays, so focus does, and a status line below it says what it added (`2 more folders listed, unchecked.`). Once it has listed them, the removal warning shows above `Save`. `Save` and `Cancel` both return to the page with focus on `Edit`. A failed check shows as on the add form, ending `Nothing was changed.`

`Remove` opens a confirmation: a native `<dialog>` opened with `showModal()`, centred, `--dialog-width` (400 px) wide, on `--raised` with `--radius-large`, `--shadow-pop`, and `--scrim`. Its title names the source at section size (`Remove Anna?`). The text in `--text-2` says that collection stops and the stored password is deleted, and that collected messages stay in the archive with their history naming the source. `Cancel` (large default) takes focus first, so `Enter` alone removes nothing, then `Remove source` (large primary) reads `Removing…` while it waits. `Escape` and `Cancel` close it and return focus to `Remove`. Success opens the Sources list. A failure keeps the dialog open with a line below its buttons. At 720 px and below the dialog keeps 16 px from each edge.

## Motion

Motion confirms a state change and never decorates it:

- Hover fills appear immediately and fade out over `--motion-fade` (140 ms).
- Drawers slide in over `--motion-slide` (200 ms) with `--ease-enter`, and out over `--motion-exit` (150 ms) with `--ease-exit`. The scrim fades with them, and the drawer keeps its content until it has gone.
- Toasts rise 8 px and fade in over `--motion-enter` (160 ms) with `--ease-enter`.
- Under `prefers-reduced-motion: reduce`, remove all animations and transitions.

## Do's and Don'ts

- Do edit token values only in [`src/lib/styles/tokens.css`](src/lib/styles/tokens.css), and re-measure WCAG 2.2 AA contrast (4.5:1 text, 3:1 non-text) across composited layers in both themes whenever a value changes.
- Do use `:focus-visible` with `outline: 2px solid var(--focus)` and a `2px` offset (`-2px` on list rows and bottom tabs so container edges never clip the focus ring).
- Do preserve or intentionally move keyboard focus whenever a list refreshes, a step changes, or a notification is dismissed.
- Do isolate untrusted strings (file names, subjects, addresses) in `<bdi>` as plain text, and never render untrusted content with `{@html}` or echo raw URL paths on error screens.
- Don't claim the archive is "healthy" or use summary checkmarks; report only what was verified and when.
- Don't rely on colour alone to communicate status; always pair a state dot with a state word.
- Don't auto-dismiss outcome or error toasts on a timer, or spin buttons during background work.
- Don't add a new typeface or icon package without an ADR.

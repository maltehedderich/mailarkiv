# MailArkiv interface design

This guide says which token to use where, and why. Exact values live in [`src/lib/styles/tokens.css`](src/lib/styles/tokens.css), so change them there and nowhere else.

It covers the Slice 1 screens: sign-in, the import list with the import box, file history, and status, plus the toasts that report imports and downloads. Behavior and failure wording come from [the Slice 1 spec](../specs/001-import-browse-retrieve/spec.md). If this file conflicts with the spec or [the constitution](../.specify/memory/constitution.md), they win.

## Character

Administrators use MailArkiv to confirm that mail is stored and to get it back. The interface reads like a precise record: dense, calm, and exact about what it knows.

| Rule                               | What it means on screen                                                                                        | Why                                                                                    |
| ---------------------------------- | -------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| Say only what was checked          | Status lists facts with their times. It never says "healthy" and has no summary tick.                          | [Architecture rule 7](../docs/ARCHITECTURE.md) and FR-027 forbid overclaiming          |
| Evidence looks like evidence       | Hashes use the mono font. Sizes, counts, and times use tabular numerals.                                       | People compare these values by eye                                                     |
| Colour marks state and little else | A state is a 6 px dot beside a word. The word carries the meaning. One accent hue is used for focus and links. | Colour stays noticeable when something is wrong, and no state depends on seeing colour |
| Mail content is hostile            | File names are plain text, with control and bidi characters shown as codes.                                    | FR-021                                                                                 |
| Density serves scanning            | 13 px text, 38 px rows, no separators between rows, no cards around rows.                                      | Administrators scan long lists                                                         |

## Colour

Two hues in OKLCH, `--hue-neutral` and `--hue-accent`, feed both themes. Use roles, never raw colours.

| Token                                       | Use for                                                                   |
| ------------------------------------------- | ------------------------------------------------------------------------- |
| `--canvas`                                  | The shell and sidebar, behind everything                                  |
| `--panel`                                   | The main work surface and standalone pages                                |
| `--raised`                                  | Drawers and dialogs                                                       |
| `--scrim`                                   | Behind a drawer or dialog                                                 |
| `--text-1`                                  | File names, values, headings, and the primary button fill                 |
| `--text-2`                                  | Button and field labels, secondary values, counts in the current nav item |
| `--text-3`                                  | Dates, column heads, notes, next steps, short hashes                      |
| `--text-4`                                  | Form input edges and decorative marks. Never text                         |
| `--line`, `--line-strong`                   | Dividers, and edges of controls that don't need 3:1                       |
| `--fill-1`                                  | Inputs, the import box, and row hover                                     |
| `--fill-2`                                  | Control hover                                                             |
| `--fill-3`                                  | Control press and the current nav item                                    |
| `--accent`                                  | Links. `global.css` sets it on every `<a>`                                |
| `--focus`                                   | Focus rings and the drop-target edge                                      |
| `--accent-tint`                             | The drop-target fill and the glow around a focused input                  |
| `--state-ok`, `--state-warn`, `--state-bad` | State dots only                                                           |
| `--state-bad-text`, `--state-bad-fill`      | Error text, and the problem-count badge                                   |

### Approved pairs

FR-041 requires WCAG 2.2 AA: 4.5:1 for text and 3:1 for meaningful non-text. Use these pairs, and measure any new one first. The values were measured from pixels Chromium painted, with every translucent layer composited in order.

| Text               | On                                                | Light | Dark  |
| ------------------ | ------------------------------------------------- | ----- | ----- |
| `--text-1`         | `--panel`                                         | 17.32 | 16.49 |
| `--text-1`         | `--raised`                                        | 17.46 | 14.87 |
| `--text-1`         | `--fill-1` on `--panel`                           | 16.18 | 15.22 |
| `--text-1`         | `--fill-2` on `--panel` (button hover)            | 15.31 | 13.84 |
| `--text-1`         | `--fill-3` on `--panel` (button press)            | 14.24 | 12.09 |
| `--text-1`         | `--fill-2` on `--raised` (button hover)           | 15.44 | 12.09 |
| `--text-1`         | `--fill-3` on `--canvas`                          | 12.85 | 13.33 |
| `--text-2`         | `--panel`                                         | 10.53 | 11.21 |
| `--text-2`         | `--raised`                                        | 10.61 | 10.11 |
| `--text-2`         | `--canvas`                                        | 9.48  | 11.93 |
| `--text-2`         | `--fill-1` on `--panel`                           | 9.83  | 10.35 |
| `--text-2`         | `--fill-3` on `--canvas`                          | 7.81  | 9.07  |
| `--text-3`         | `--panel`                                         | 5.98  | 6.82  |
| `--text-3`         | `--raised`                                        | 6.03  | 6.15  |
| `--text-3`         | `--canvas`                                        | 5.39  | 7.26  |
| `--text-3`         | `--fill-1` on `--panel`                           | 5.59  | 6.30  |
| `--text-3`         | `--fill-2` on `--panel`                           | 5.29  | 5.73  |
| `--panel`          | `--text-1` (primary button)                       | 17.32 | 16.49 |
| `--panel`          | `--text-1` at 86% (primary hover)                 | 11.33 | 12.33 |
| `--panel`          | `--text-1` at 76% (primary press)                 | 7.92  | 9.84  |
| `--accent`         | `--panel`                                         | 6.50  | 9.31  |
| `--accent`         | `--raised`                                        | 6.55  | 8.40  |
| `--state-bad-text` | `--panel`                                         | 7.72  | 8.10  |
| `--state-bad-text` | `--raised`                                        | 7.78  | 7.30  |
| `--state-bad-text` | `--canvas`                                        | 6.95  | 8.62  |
| `--state-bad-text` | `--fill-1` on `--panel` (refusal in the box)      | 7.21  | 7.48  |
| `--state-bad-text` | `--state-bad-fill` on `--canvas`                  | 5.54  | 7.05  |
| `--state-bad-text` | `--state-bad-fill` on `--fill-2` on `--canvas`    | 4.92  | 5.82  |
| `--state-bad-text` | `--state-bad-fill` on `--fill-3` on `--canvas`    | 4.62  | 5.18  |

The last two are the problem count while Status is hovered or current. They set `--state-bad-text` in light to L 0.46: at 0.48 the current item measured 4.23.

| Non-text                                | On                      | Light | Dark |
| --------------------------------------- | ----------------------- | ----- | ---- |
| `--text-4` (input edge, neutral dot)    | `--panel`               | 3.35  | 3.95 |
| `--text-4` (neutral dot in the outcome) | `--fill-1` on `--panel` | 3.13  | 3.64 |
| `--text-4` (timeline dot)               | `--raised`              | 3.37  | 3.56 |
| `--focus`                               | `--panel`               | 4.40  | 7.53 |
| `--focus`                               | `--raised`              | 4.43  | 6.79 |
| `--focus`                               | `--canvas`              | 3.96  | 8.02 |
| `--focus` (Choose a file in the box)    | `--fill-1` on `--panel` | 4.11  | 6.96 |
| `--state-ok`                            | `--panel`               | 3.70  | 8.08 |
| `--state-ok`                            | `--raised`              | 3.73  | 7.29 |
| `--state-ok`                            | `--fill-1` on `--panel` | 3.46  | 7.46 |
| `--state-warn`                          | `--panel`               | 3.31  | 9.66 |
| `--state-warn`                          | `--raised`              | 3.34  | 8.71 |
| `--state-warn`                          | `--fill-1` on `--panel` | 3.09  | 8.92 |
| `--state-bad`                           | `--panel`               | 4.68  | 6.35 |
| `--state-bad`                           | `--raised`              | 4.72  | 5.73 |
| `--state-bad`                           | `--fill-1` on `--panel` | 4.37  | 5.87 |

`--text-3` on `--fill-3` on `--canvas` fails in light at 4.44, so a count in the current nav item uses `--text-2`.

### Theme

The theme follows the system setting through `prefers-color-scheme` in `tokens.css`. Slice 1 has no switch in the app, because nothing requires one (constitution principle I). Check every screen in both themes. A dark surface gets lighter as it rises, so `--shadow-panel` in dark is only an edge.

## Type

| Role     | Size and line                                 | Weight                                         | Use for                                                |
| -------- | --------------------------------------------- | ---------------------------------------------- | ------------------------------------------------------ |
| Label    | `--size-label`, `--leading-label` (12/16)     | regular or medium                              | Dates, sizes, column heads, field labels, notes        |
| Evidence | `--size-evidence`, `--leading-body` (12.5/20) | regular, mono                                  | The full hash                                          |
| Body     | `--size-body`, `--leading-body` (13/20)       | regular, or medium for names and button labels | Everything else                                        |
| Section  | `--size-section`, `--leading-body` (14/20)    | strong, `--tracking-section`                   | View titles, status headings, standalone page headings |

Inter sets everything except hashes. Its variable file gives the 510 and 590 weights in the tokens. Geist Mono sets hashes, and it has no Greek, which hashes don't need. Linear's `cv01` and `ss03` alternates aren't in the bundled Inter, so don't add those feature settings: they do nothing.

- **Numbers:** sizes and counts use `font-variant-numeric: tabular-nums` and align right. Times use tabular numerals too.
- **Short hash:** the first 8 characters, in the mono font at `--size-label`, coloured `--text-3`.
- **Full hash:** all 64 characters as 16 groups of 4, in 4 columns, with `font-variant-ligatures: none`. A copy button next to it copies the 64 characters without spaces. Groups make the hash comparable by eye, so don't shorten it (FR-020).
- **File names:** the medium weight in `--text-1`, isolated with `<bdi>`. Long names lose their middle, so the start and the extension both stay visible (FR-021). A control or bidi character shows as a small code such as `U+202E`. A missing name is `(no file name)` in `--text-3` at regular weight.
- **Dates:** `2 Nov 2026, 09:14:55 +01:00`, in the viewer's time zone with its UTC offset (FR-043). Use the same form everywhere a person compares times.

## Space, size, and shape

Space tokens are named for their pixel value: `--space-2` through `--space-32`. Use them for all gaps and padding.

| Token                    | Value  | Use for                                |
| ------------------------ | ------ | -------------------------------------- |
| `--control-height`       | 28 px  | Buttons in views and row actions       |
| `--control-height-large` | 32 px  | Form inputs and form or drawer buttons |
| `--control-height-touch` | 36 px  | Buttons and inputs at 720 px and below |
| `--row-height`           | 38 px  | List rows                              |
| `--header-height`        | 52 px  | View and drawer headers                |
| `--sidebar-width`        | 224 px | Sidebar                                |
| `--content-width`        | 880 px | Centred content column                 |
| `--drawer-width`         | 440 px | File history                           |
| `--form-width`           | 300 px | Sign-in                                |
| `--toast-width`          | 380 px | Toasts on the page                     |

Radii: `--radius-small` for codes and small marks, `--radius-medium` for controls and rows, `--radius-large` for panels, the import box, and the drawer.

A shadow means something sits above the page. The panel gets `--shadow-panel`, drawers, dialogs, and toasts get `--shadow-pop`, and nothing else gets one.

## Layout

Signed-in pages have a sidebar on `--canvas` and one `--panel` beside it, inset 8 px. The sidebar stays in view while the page scrolls, with Sign out at its foot. A view has a 52 px header with its title, then a scrolling body. Import and status centre their content in `--content-width`. The page itself never scrolls sideways.

Sign-in is standalone: a `--form-width` column, centred, on `--panel`, with no sidebar.

| At 720 px and below | Change                                                                                                                                       |
| ------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Sidebar             | Becomes a bottom bar of 44 px tabs, icon and label. The problem count stays on Status, and Sign out becomes an icon tab with an `aria-label` |
| Import list         | The header row goes. Each row stacks as name and count, then time and size, then hash and download, with a line between rows                 |
| Import box          | The button moves below the text and fills the width. The file icon goes                                                                      |
| View header         | Title and count stay on one line. The update time moves to its own line below                                                                |
| Status sections     | Label moves above content. Values move below their label and align left                                                                      |
| Drawer              | Full width, no radius                                                                                                                        |
| Buttons and inputs  | `--control-height-touch`. Input text grows to `--size-input-touch`, because iOS Safari zooms into anything smaller                           |
| Page gutters        | 16 px                                                                                                                                        |

SC-008 requires the whole flow by keyboard and at 360 px with no sideways scroll, so test at 360 px.

## Components

### Button

| Variant | Class                    | Look                                        | Use                                              |
| ------- | ------------------------ | ------------------------------------------- | ------------------------------------------------ |
| Default | `button`                 | 1 px `--line-strong` edge, `--text-2` label | Most actions                                     |
| Primary | `button primary`         | `--text-1` fill, `--panel` label            | One per view, for the action the view exists for |
| Ghost   | `button ghost`           | No edge                                     | Clear, Close, and icon buttons                   |
| Icon    | add `icon-only`          | Square, ghost or default, with `aria-label` | Row download, Copy, Close, Dismiss               |
| Large   | add `large`              | `--control-height-large`                    | Form and drawer buttons                          |

Default and ghost hover to `--fill-2` with `--text-1`, and press to `--fill-3`. Primary drops to 86% opacity on hover and 76% on press. Disabled is 45% opacity with no pointer events. Use `disabled` on a button and `aria-disabled="true"` on a link, or on a button that must keep focus while busy. Long work shows in the import box, so buttons don't spin.

### Form field

Label above in the label size, medium, `--text-2`. The input is `--control-height-large` on `--fill-1`, with a 1 px `--text-4` border. Focus clears the fill and adds a `--focus` edge with a 3 px `--accent-tint` glow. An error is `aria-invalid="true"` on the input: it sets the edge to `--state-bad`, and one line in `--state-bad-text` below names the problem through `aria-describedby`. Focus wins over an error, so the focused field still changes.

Use the `field` class on a `<label>` that holds a `<span>` and the `<input>`.

### Navigation item

30 px tall, medium weight, 16 px icon. Rest is `--text-2` with a `--text-3` icon. Hover is `--fill-2`. Current is `--fill-3` in `--text-1`. The Status item carries the problem count on every signed-in page (FR-039): an 18 px pill, `--state-bad-fill` with `--state-bad-text`.

### Import box

Uses `--fill-1` with a `--line` edge, `--radius-large`, at least 76 px tall. It holds a file icon, text, and the Choose a file button. Dropping a file adds to that button and never replaces it (FR-041). A dragged file sets a 1.5 px `--focus` edge and `--accent-tint` fill.

| Step                   | Shows                                                                                         |
| ---------------------- | --------------------------------------------------------------------------------------------- |
| Ready                  | "Drop one .eml file here" and "Up to 150 MB. Imported mail can't be removed in this version." |
| Chosen                 | The name and exact bytes, the removal warning again, and Import (FR-034)                      |
| Uploading              | "Uploading, 42%" and a progress bar. Import is off (FR-035)                                   |
| Storing                | A separate storing state with no percentage (FR-035)                                          |
| Refused in the browser | A message, with nothing uploaded (FR-036)                                                     |

### Toast

A toast reports a finished import, a download in progress, or a download that failed. Toasts sit in one region named "Notifications" at the bottom right, `--toast-width` wide and 24 px from the edges. At 720 px and below they fill the width above the bottom tab bar, with 16 px gutters. Each is on `--raised` with `--shadow-pop` and `--radius-large`, plus a transparent 1 px border so forced colours still draw an edge.

A toast reads like a problem on Status: a `--dot-size` dot and the state word, then the file name, then what happened in `--text-2` and the next step in `--text-3`. Use the spec's words.

| Toast            | Dot            | Shows                                                                          |
| ---------------- | -------------- | ------------------------------------------------------------------------------ |
| Archived         | `--state-ok`   | Name, size, and "Open import history"                                          |
| Already archived | `--text-4`     | Name, what happened, and "Open import history"                                 |
| Restored         | `--state-warn` | Name, what happened, and "Open import history"                                 |
| Refused          | `--state-bad`  | Name and the reason                                                            |
| Failed           | `--state-bad`  | Name, the reason, and the next step                                            |
| Unconfirmed      | `--state-warn` | Name and what happened                                                         |
| Downloading      | `--text-4`     | Name, a progress bar with its percentage, and Cancel                           |
| Download waiting | `--text-4`     | Name, "Waiting for the previous download to finish.", and Cancel              |
| Download not started | `--text-4` | Name, "Another download is already waiting.", and how to try again            |
| Download canceled | `--text-4`    | Name and "Nothing was saved."                                                 |
| Download failed  | `--state-bad`  | Name, "This message wasn't downloaded." with the reason, and the next step     |

- **No timer.** A toast stays until it's dismissed or replaced, because a result that disappears breaks FR-023 and FR-032. Starting an import removes the last import's toast. Trying a download again replaces that download's toast.
- **Downloading** appears only after 400 ms, so a quick download shows nothing. A waiting download appears immediately and keeps its notification when its transfer starts. A failure replaces progress in place.
- **Download limits:** one transfer and one waiter per tab. Both offer Cancel. Keep focus on the notification's control when Cancel becomes Dismiss, and move it to another notification or the region when a focused notification disappears.
- **In the drawer:** the drawer is a modal dialog, so the page behind it is inert. A toast raised while it's open shows inside it, above the footer, and moves to the page when it closes.
- **Focus stays visible:** the region sets `--toast-room` to the space it covers. The page and the drawer body add that as bottom padding and `scroll-padding-bottom`, so the last row and a focused control scroll clear of the toasts (WCAG 2.4.11).
- **Screen readers:** the list is `aria-live="polite"`. The bar is a `progressbar` named "Download", and its percentage text is hidden from them.
- **Dismiss** is a ghost icon button labelled "Dismiss". It moves focus to the next toast's control, or to the region when none is left, so focus never drops to the page.

### Import list row

Columns: file name, latest time (185 px), size (62 px), short hash (72 px), count (40 px), download (28 px). 185 px fits the widest time, `22 May 2026, 09:14:55 +05:30`, measured in Chromium in Inter at 12 px with tabular numerals. Measure again if the date form or the font changes. The count shows as `3×`, only above 1 (FR-019). Rows are `--row-height`, `--radius-medium`, with no separators. Hover is `--fill-1`.

Every row has two keyboard stops: the row opens its history on Enter, and its download button saves the file. The row's accessible name must include the file name. Clicking the download button must not also open the history.

Show "1–7 of 7" and Previous and Next under the list. A fresh install shows the import box above "No mail yet" and has no tour or sample mail.

### File history drawer

A native modal `<dialog>` on `--raised`, `--drawer-width` wide, attached to the right, with `--shadow-pop` and a `--scrim`. The header holds the title, "Import history", and Close. The body holds the full hash, the size in exact bytes, and every observation as a timeline with no cap (FR-020). Each observation shows source type, time, account, and file name (FR-014). Download is pinned in a footer, so it stays visible on a long history.

### Status

Sections stack with a `--line` above each: a 136 px label column, then content. A problem has its state word and dot with the time, then what happened in `--text-2`, then the next step in `--text-3`. Use the category names and next steps from FR-038. The first line says the screen covers the time since the latest start and that older failures are in the server log (FR-027). No action here changes the archive in Slice 1.

### Sign-in

Standalone. Email address, password, and a full-width primary Sign in. A standing note says "Accounts and passwords are managed on the server. Operator guide: docs/OPERATIONS.md", with the path as the link's text, pointing to the guide on GitHub. The path stays readable on a network that can't reach GitHub. When a session ended in this tab, a line above the form says "Your session ended. Sign in again to continue."

### Loading

Skeleton rows keep the real columns. They don't animate, so a slow archive doesn't look busy.

### Failed load

Data that didn't arrive leaves one block in place of the content: what happened in `--text-2`, the next step in `--text-3`, and a default button to try again. An action that didn't finish shows one line in `--state-bad-text` beside its control. Both sit in a `role="status"` element.

## Accessibility and keyboard

- **Focus:** every interactive element uses `:focus-visible` with `outline: 2px solid var(--focus)` and a 2 px offset. Rows use a −2 px offset, so scrolling containers don't clip it.
- **Opening with a key:** call `preventDefault()` on the key press that opens a drawer. Otherwise focus moves to Close and the same Enter closes it again.
- **Drawers:** use `<dialog>` with `showModal()`, close on Escape, and return focus to the row that opened it.
- **Re-rendering:** updating a list or the import box must not drop focus. Keep the focused element, or move focus on purpose.
- **Icons:** the Vite config marks each icon `aria-hidden`. Give an icon-only button its own `aria-label`.
- **State:** never rely on colour alone. Pair each dot with its word.
- **Forced colours:** Windows High Contrast removes box shadows. Draw an edge that matters with a border, and pair a shadow focus ring with a transparent outline, which forced colours paints.
- **Headings:** choose levels for document structure and style them by role.

## Motion

Motion confirms a change and never decorates it. A hover fill appears at once and fades over `--motion-fade` (140 ms) when it leaves. The drawer slides in over `--motion-slide` (200 ms) with `--ease-enter`. A toast rises 8 px and fades in over `--motion-enter` (160 ms) with `--ease-enter`. With `prefers-reduced-motion: reduce`, remove animations and transitions.

## Fonts and icons

Both fonts and every icon are bundled at build time, so the app makes no external request for them. [ADR 0004](../docs/adr/0004-interface-fonts-and-icons.md) has the packages and pinned versions.

```svelte
<!-- src/routes/+layout.svelte -->
<script>
  import '@fontsource-variable/inter/wght.css';
  import '@fontsource-variable/geist-mono/wght.css';
  import '#lib/styles/tokens.css';
</script>
```

```ts
// In the Vite plugins, after sveltekit()
Icons({
    compiler: "svelte",
    scale: 1,
    defaultClass: "icon",
    iconCustomizer: (_collection, _icon, props) => {
        props["aria-hidden"] = "true";
    },
});
```

```css
/* Global styles. Lucide draws at 2 px, which is heavy beside 13 px text. */
.icon {
    display: block;
    flex: none;
    width: var(--icon-size);
    height: var(--icon-size);
}
.icon * {
    stroke-width: 1.6;
}
```

Use Lucide for every icon, imported as `~icons/lucide/download`, at `--icon-size`, or `--icon-size-small` in tight places. The stroke width has to come from CSS, because Lucide sets it on the SVG's `<g>` element. Leave `autoInstall` off, so a mistyped icon fails the build instead of installing a package.

Slice 1 uses `archive`, `upload`, `activity`, `file-up`, `file-text`, `download`, `x`, `arrow-left`, `log-out`, and `copy`.

The MailArkiv logo is the one drawing that isn't from Lucide: a solid tile with a drawer grip cut out of it, and the name in outlined Archivo. [`Logo.svelte`](src/lib/components/Logo.svelte) holds the 18 px version, used in the sidebar and on sign-in. It fills with `currentColor` and the grip is a real hole, so both themes use the same drawing.

Don't type the name beside the mark in Inter. Archivo appears only as these outlines, so the app ships no extra font.

The browser tab uses `static/favicon.svg`, which follows the theme, and `static/favicon.ico` where SVG isn't supported. [The brand guide](../docs/brand/README.md) has the other sizes and layouts, clear space, and minimum sizes.

## Writing

Follow the Writing section in [AGENTS.md](../AGENTS.md).

- Use sentence case. Start buttons with a verb.
- A failure says what happened, then what to do next.
- Say what's known and nothing more, as in "1,204 messages are waiting" rather than "All good."
- Use the spec's exact words for outcomes and failures.

## Later slices

Search, the message reader, and delete use these values. Add each to `tokens.css` in the slice that needs it, then measure its pairs.

| Token            | Light                               | Dark                                 | Use                                     |
| ---------------- | ----------------------------------- | ------------------------------------ | --------------------------------------- |
| `--hit`          | `oklch(0.94 0.065 95 / 0.9)`        | `oklch(0.8 0.1 92 / 0.2)`            | Search match highlight                  |
| `--tick`         | `oklch(0.5 0.11 var(--hue-accent))` | `oklch(0.52 0.11 var(--hue-accent))` | Selected checkbox                       |
| `--sheet-dim`    | `1`                                 | `0.93`                               | Brightness of the white message preview |
| `--bad-solid`    | `oklch(0.52 0.18 27)`               | `oklch(0.52 0.17 27)`                | Delete button fill, with white text     |
| `--warn-text`    | `oklch(0.47 0.1 70)`                | `oklch(0.82 0.11 78)`                | Warning text                            |
| Title type       | 20/26, 590, −0.014em                | Same                                 | Message subject                         |

From 1041 px up, the archive list has one-line rows. At 1040 px and below, rows take two lines at 54 px and the reader stacks over its details.

For the message reader, keep mail on a light sheet in both themes, inside the sandboxed iframe required by [the architecture](../docs/ARCHITECTURE.md#security-and-privacy-from-the-first-slice). Place the label identifying mail content outside that sheet. Show senders as written, without avatars: a From header is only a claim, and fetching a picture would make a remote request.

For permanent delete, explain what remains in backups and that re-delivery can archive the message again. Focus Cancel first. Use the red button only in the confirmation dialog.

## Changing this system

To change a value, edit `tokens.css`, measure again, and update the pair tables here in the same change. A new font or icon set needs an ADR.

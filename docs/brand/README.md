# MailArkiv brand files

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="logo/lockup-horizontal-on-dark.svg">
  <img alt="MailArkiv" src="logo/lockup-horizontal-on-light.svg" width="221" height="48">
</picture>

Use these files as they are: a solid tile with a drawer grip cut out of it, next to the name set in Archivo. The SVGs in `logo/` are the masters. Everything else here is exported from them, so re-export after any change to the drawing.

The app uses copies of these: the 18 px lockup in `frontend/src/lib/components/Logo.svelte` and the favicons in `frontend/static/`. Update the copies when a drawing here changes.

## Pick a logo file

| You need | File | Use it at |
| --- | --- | --- |
| Name and mark, side by side | `logo/lockup-horizontal.svg` | 24 px tall and up |
| The same, small | `logo/lockup-horizontal-small.svg` | 18 to 23 px tall |
| Name under the mark | `logo/lockup-stacked.svg` | Tile 40 px and up |
| Mark alone | `logo/mark.svg` | 20 px and up |
| Mark alone, small | `logo/mark-18.svg` | 18 to 19 px |
| Mark alone, tab size | `logo/mark-16.svg` | 16 to 17 px |
| Name alone | `logo/wordmark.svg` | Caps 9 px and up |

The small drawings are not reductions. They are redrawn so the grip is 3 px tall on whole pixels, where a plain reduction of the master would give 2.5 px at 16 px and blur.

Plain files fill with `currentColor`, so they follow the theme when inlined. For an `<img>` or a document, use the `-on-light` or `-on-dark` version of the mark, wordmark, or either full-size lockup. `logo/png/` has large exports for tools that can't take SVG.

## Space and size

Keep clear space of a quarter of the tile's height on every side. A 24 px tile needs 6 px. That's the same distance as the top of the tile to the top of the grip, so you can measure it from the mark.

Don't go below the sizes in the table. Under them, the word or the grip stops being legible.

## Colour

The logo uses one colour at a time and no accent.

| Name | Hex | Token | Use |
| --- | --- | --- | --- |
| Ink | `#161a20` | `--text-1`, light | Logo on light grounds |
| Paper | `#fefeff` | `--panel`, light | Light ground, field of the app icon |
| Dark ink | `#f3f3f5` | `--text-1`, dark | Logo on dark grounds |
| Dark ground | `#0c0d0f` | `--canvas`, dark | Dark ground |
| Lifted field | `#2a2d33` | None | Field of the dark iOS icon only |

Ink on Paper has a contrast of 17.32:1, and Dark ink on Dark ground 17.54:1. The grip is a real hole, so it shows whatever is behind the logo. Don't fill it with a second colour.

## Don't

- Add an envelope or any other symbol to the mark.
- Centre the grip in the tile. It becomes a minus sign.
- Round the grip's ends into a pill. Same problem.
- Outline the tile.
- Type the name in Inter next to the mark. Use the supplied lockup.

## How it's drawn

On a 24-unit tile:

- The corner radius is 4.5.
- The grip is 15 wide, the same as the tile's flat top edge, and 3.75 tall.
- The grip's top edge sits at 6, a quarter of the way down.
- The grip's lower corners have a radius of 3, and its top corners 0.25.

The wordmark is Archivo at width 110: "Mail" at weight 400, "Arkiv" at weight 640. Tracking is -8/1000 em, with 5 extra units between the l and the A when caps are scaled to 100 units. Both i dots are replaced by the grip's shape, 35% wider than Archivo's dot.

In the horizontal lockup, caps are half the tile's height and the cap line sits on the grip's top edge. The gap between tile and word is 7 units on the 24 grid and 6 on the 18 grid. The stacked lockup makes the word 49.78 units wide under a 24-unit tile, with a gap of 6.

Archivo appears only in the logo, as outlines, so no font file ships. It is copyright 2020 The Archivo Project Authors and licensed under the SIL Open Font License 1.1. The interface stays in Inter and hashes in Geist Mono.

## App icons

Icons that fill a square show the whole mark at 62% of the icon on a Paper field. A circle or rounded mask then leaves the drawer whole, and the icon stands apart from dark neighbours and dark wallpaper.

| Platform | Files | Notes |
| --- | --- | --- |
| Browser tab | `favicon/favicon.svg`, `favicon.ico`, `favicon-16.png`, `-32`, `-48` | The SVG follows the theme. The ICO and PNGs don't: on a dark tab strip, their ink tile fades and only the grip shows. |
| Apple touch | `favicon/apple-touch-icon.png` | 180 px, opaque |
| PWA | `pwa/icon-192.png`, `icon-512.png` | Rounded tile on transparency |
| PWA, maskable | `pwa/icon-maskable-192.png`, `-512` | The tile's corners reach 39% of the icon's width from the centre. The safe radius is 40%. |
| iOS | `ios/icon-1024.png`, `-dark`, `-tinted` | Square and opaque. iOS applies the mask. |
| Android | `android/ic_launcher_foreground`, `_background`, `_monochrome`, as `.svg` and `.png` | 108 dp layers. A 46 dp tile reaches 29 dp from the centre, inside the 33 dp safe radius. |

MailArkiv has no iOS or Android app today, so those two sets are for later.

For the web, copy the files to the site root and add:

```html
<link rel="icon" href="/favicon.ico" sizes="32x32">
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="/apple-touch-icon.png">
<link rel="manifest" href="/manifest.webmanifest">
```

```json
{
  "name": "MailArkiv",
  "icons": [
    { "src": "/icon-192.png", "sizes": "192x192", "type": "image/png" },
    { "src": "/icon-512.png", "sizes": "512x512", "type": "image/png" },
    { "src": "/icon-maskable-192.png", "sizes": "192x192", "type": "image/png", "purpose": "maskable" },
    { "src": "/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable" }
  ]
}
```

## Before this goes public

- Check the name. [MailArchiva](https://www.capterra.com/p/129318/MailArchiva/) is an email-archiving product with almost the same name. Nobody has searched a trademark register yet.
- Run a doodle test. Nobody has tried drawing the mark from memory yet.

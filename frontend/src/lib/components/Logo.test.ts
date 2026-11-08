import { readFileSync } from "node:fs";

import { describe, expect, test } from "vitest";

// The app's logo and icons are copies of the masters in docs/brand/, and the
// brand guide asks for the copies to follow any change to a drawing. These
// tests fail when one doesn't.

const BRAND = new URL("../../../../docs/brand/", import.meta.url);
const STATIC = new URL("../../../static/", import.meta.url);
const LOGO = readFileSync(new URL("Logo.svelte", import.meta.url), "utf8");

function brand(path: string): string {
  return readFileSync(new URL(path, BRAND), "utf8");
}

function attributes(svg: string, name: string): string[] {
  return [...svg.matchAll(new RegExp(` ${name}="([^"]+)"`, "g"))].map(
    (match) => match[1],
  );
}

/** Width and height from a PNG's header. */
function pngSize(bytes: Buffer): string {
  return `${bytes.readUInt32BE(16)}x${bytes.readUInt32BE(20)}`;
}

describe("Logo.svelte", () => {
  test.each([
    ["horizontal", "logo/lockup-horizontal-small.svg"],
    ["stacked", "logo/lockup-stacked.svg"],
  ])("draws the %s lockup as %s does", (_layout, file) => {
    const master = brand(file);
    for (const value of [
      ...attributes(master, "viewBox"),
      ...attributes(master, "transform"),
      ...attributes(master, "d"),
    ]) {
      expect(LOGO).toContain(value);
    }
  });

  // Pixels per unit: the small lockup is drawn in pixels at 18 px, and the
  // stacked one puts its 24-unit tile at 40 px.
  test.each([
    ["logo/lockup-horizontal-small.svg", ".logo {", 1],
    ["logo/lockup-stacked.svg", ".stacked {", 40 / 24],
  ])("sizes %s at its drawn scale", (file, rule, scale) => {
    const [, , width, height] = attributes(brand(file), "viewBox")[0]
      .split(" ")
      .map(Number);
    const start = LOGO.indexOf(rule);
    const block = LOGO.slice(start, LOGO.indexOf("}", start));
    expect(block).toContain(`width: ${+(width * scale).toFixed(3)}px;`);
    expect(block).toContain(`height: ${+(height * scale).toFixed(3)}px;`);
  });
});

describe("static/", () => {
  test.each([
    ["favicon.svg", "favicon/favicon.svg"],
    ["favicon.ico", "favicon/favicon.ico"],
    ["apple-touch-icon.png", "favicon/apple-touch-icon.png"],
    ["icon-192.png", "pwa/icon-192.png"],
    ["icon-512.png", "pwa/icon-512.png"],
    ["icon-maskable-192.png", "pwa/icon-maskable-192.png"],
    ["icon-maskable-512.png", "pwa/icon-maskable-512.png"],
  ])("holds %s as exported to docs/brand/%s", (copy, master) => {
    expect(readFileSync(new URL(copy, STATIC))).toEqual(
      readFileSync(new URL(master, BRAND)),
    );
  });

  test("lists manifest icons that exist at the sizes it claims", () => {
    const manifest = JSON.parse(
      readFileSync(new URL("manifest.webmanifest", STATIC), "utf8"),
    ) as { icons: { src: string; sizes: string }[] };
    expect(manifest.icons.length).toBeGreaterThan(0);
    for (const icon of manifest.icons) {
      const bytes = readFileSync(new URL(icon.src.slice(1), STATIC));
      expect(pngSize(bytes)).toBe(icon.sizes);
    }
  });
});

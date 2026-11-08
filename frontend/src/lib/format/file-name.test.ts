import { describe, expect, test } from "vitest";

import { displayName, type DisplayName, type NamePart } from "./file-name";

// A supplied file name is hostile. It shows as plain text, control and bidi
// characters show as codes, a long name loses its middle so its ending stays
// visible, and a missing name is "(no file name)".

/** The whole name's parts, with neighbouring text joined. */
function parts(display: DisplayName): NamePart[] {
  if (display.kind === "missing") throw new Error("the name is missing");
  const joined: NamePart[] = [];
  for (const part of [...display.start, ...display.end]) {
    const last = joined.at(-1);
    if (part.kind === "text" && last?.kind === "text") {
      joined[joined.length - 1] = { kind: "text", text: last.text + part.text };
    } else {
      joined.push(part);
    }
  }
  return joined;
}

function join(list: NamePart[]): string {
  return list.map((part) => part.text).join("");
}

function named(name: string) {
  const display = displayName(name);
  if (display.kind !== "name") throw new Error(`${name} shows as missing`);
  return display;
}

const text = (value: string): NamePart => ({ kind: "text", text: value });
const code = (value: string): NamePart => ({ kind: "code", text: value });

// Unicode's Bidi_Control characters, then C0, DEL, C1, and the line and
// paragraph separators.
const NEUTRALIZED = [
  ["U+202E", "\u202E"],
  ["U+202D", "\u202D"],
  ["U+202A", "\u202A"],
  ["U+202B", "\u202B"],
  ["U+202C", "\u202C"],
  ["U+200E", "\u200E"],
  ["U+200F", "\u200F"],
  ["U+061C", "\u061C"],
  ["U+2066", "\u2066"],
  ["U+2067", "\u2067"],
  ["U+2068", "\u2068"],
  ["U+2069", "\u2069"],
  ["U+0000", "\u0000"],
  ["U+0007", "\u0007"],
  ["U+0009", "\t"],
  ["U+000A", "\n"],
  ["U+000D", "\r"],
  ["U+001B", "\u001B"],
  ["U+007F", "\u007F"],
  ["U+0085", "\u0085"],
  ["U+009B", "\u009B"],
  ["U+2028", "\u2028"],
  ["U+2029", "\u2029"],
] as const;

describe("control and bidi characters become codes", () => {
  test.each(NEUTRALIZED)("%s", (shown, character) => {
    const display = named(`invoice${character}fdp.eml`);
    expect(parts(display)).toEqual([
      text("invoice"),
      code(shown),
      text("fdp.eml"),
    ]);
    expect(display.text).toBe(`invoice${shown}fdp.eml`);
  });

  test("a line break shows both of its characters", () => {
    expect(parts(named("a\r\nb.eml"))).toEqual([
      text("a"),
      code("U+000D"),
      code("U+000A"),
      text("b.eml"),
    ]);
  });

  test("a name that is only an override", () => {
    expect(parts(named("\u202E"))).toEqual([code("U+202E")]);
  });

  test("no raw control or bidi character is left in any part", () => {
    const all = NEUTRALIZED.map(([, character]) => character).join("");
    const display = named(`${"x".repeat(40)}${all}.eml`);
    for (const part of [...display.start, ...display.end]) {
      expect(part.text).not.toMatch(/[\p{Cc}\p{Bidi_Control}\u2028\u2029]/u);
    }
    expect(display.text).not.toMatch(/[\p{Cc}\p{Bidi_Control}\u2028\u2029]/u);
  });
});

describe("other characters stay as they are", () => {
  test.each([
    ["HTML", '<img src=x onerror="alert(1)">.eml'],
    ["a script tag", "<script>alert(1)</script>.eml"],
    ["an entity", "&lt;b&gt;.eml"],
    ["a path", "../../etc/passwd"],
    ["a Windows path", "..\\..\\boot.ini"],
    ["quotes", `"quoted" 'name'.eml`],
    ["right-to-left letters", "חשבונית.eml"],
    ["an emoji sequence", "👩\u200D👩\u200D👧 family.eml"],
  ])("%s", (_name, name) => {
    const display = named(name);
    expect(parts(display)).toEqual([text(name)]);
    expect(display.text).toBe(name);
  });
});

describe("a long name", () => {
  const name = `${"quarterly-statement-".repeat(8)}final.eml`;

  test("splits into a start and an ending that keeps the extension", () => {
    const { start, end } = named(name);
    expect(join(start)).not.toBe("");
    expect(join(end)).toMatch(/\.eml$/);
    expect(join(start) + join(end)).toBe(name);
  });

  test("keeps a code in its ending", () => {
    const { end } = named(`${"x".repeat(40)}\u202Elmth.exe`);
    expect(end).toContainEqual(code("U+202E"));
    expect(join(end)).toMatch(/lmth\.exe$/);
  });

  // A cut inside a cluster would draw its halves as separate symbols.
  test("is never cut inside a character cluster", () => {
    for (let tail = 0; tail < 24; tail++) {
      const family = "👩\u200D👩\u200D👧";
      const name = `${"a".repeat(30)}${family}${"b".repeat(tail)}.eml`;
      const { start, end } = named(name);
      expect(join(start) + join(end)).toBe(name);
      expect(join(start)).not.toMatch(/\u200D$|\uD83D$/);
      expect(join(end)).not.toMatch(/^\u200D|^[\uDC00-\uDFFF]/);
    }
  });

  test("keeps 12 clusters when the name reads one way", () => {
    const { end } = named(name);
    expect(join(end)).toBe("nt-final.eml");
  });
});

// The halves are laid out apart, so a cut inside a run that reads against the
// name's direction swaps the run's halves on screen. The cut moves to the
// nearest place outside the run.
describe("a long name with mixed directions", () => {
  test("isn't cut inside a number in a right-to-left name", () => {
    // 12 from the end falls between "202" and "6", which drew "6202".
    const { end } = named(
      "\u05D7\u05E9\u05D1\u05D5\u05E0\u05D9\u05EA \u05DC\u05D7\u05D5\u05D3\u05E9 \u05E0\u05D5\u05D1\u05DE\u05D1\u05E8 2026 - \u05E1\u05D5\u05E4\u05D9.eml",
    );
    expect(join(end)).toBe(" - \u05E1\u05D5\u05E4\u05D9.eml");
  });

  test("isn't cut inside a Latin word in a right-to-left name", () => {
    const { end } = named(
      "\u05D7\u05E9\u05D1\u05D5\u05E0\u05D9\u05EA \u05DC\u05D7\u05D5\u05D3\u05E9 \u05E0\u05D5\u05D1\u05DE\u05D1\u05E8 final-report.eml",
    );
    expect(join(end)).toBe("final-report.eml");
  });

  test("isn't cut inside right-to-left words in a left-to-right name", () => {
    const { end } = named(
      "Rechnung \u05D7\u05E9\u05D1\u05D5\u05E0\u05D9\u05EA \u05DC\u05D7\u05D5\u05D3\u05E9 \u05E0\u05D5\u05D1\u05DE\u05D1\u05E8.eml",
    );
    expect(join(end)).toBe(".eml");
  });

  test("isn't cut between right-to-left words and the digits after them", () => {
    const { start, end } = named(
      "Scan 2 \u05E4\u05D1\u05E8\u05D5\u05D0\u05E8 2026 final.eml",
    );
    expect(join(end)).toBe(" final.eml");
    expect(join(start)).toBe(
      "Scan 2 \u05E4\u05D1\u05E8\u05D5\u05D0\u05E8 2026",
    );
  });

  test("keeps the first cut when no cut keeps the order", () => {
    const hebrew = "\u05D0".repeat(40);
    const { end } = named(`report ${hebrew}`);
    expect(join(end)).toBe("\u05D0".repeat(12));
  });
});

describe("a missing name", () => {
  test("shows as (no file name)", () => {
    expect(displayName(null)).toEqual({
      kind: "missing",
      text: "(no file name)",
    });
  });

  // Only a direct request can store an empty name. It has nothing to show.
  test("an empty name shows the same way", () => {
    expect(displayName("")).toEqual({
      kind: "missing",
      text: "(no file name)",
    });
  });
});

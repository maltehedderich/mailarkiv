import { describe, expect, test } from "vitest";

import { displayText, type DisplayText, type TextPart } from "./untrusted";

// Folder names, source names, and server replies come from outside. They show
// as plain text, control and bidi characters show as codes, and long text
// loses its middle so its ending stays visible. The rule is the one file names
// follow, so file-name.test.ts covers the cut in more detail.

/** The whole text's parts, with neighbouring text joined. */
function parts(display: DisplayText): TextPart[] {
  const joined: TextPart[] = [];
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

function join(list: TextPart[]): string {
  return list.map((part) => part.text).join("");
}

const text = (value: string): TextPart => ({ kind: "text", text: value });
const code = (value: string): TextPart => ({ kind: "code", text: value });

describe("control and bidi characters become codes", () => {
  test.each([
    ["U+202E", "\u202E"],
    ["U+0000", "\u0000"],
    ["U+2028", "\u2028"],
  ])("%s", (shown, character) => {
    const display = displayText(`Ordner${character}Arbeit`);
    expect(parts(display)).toEqual([
      text("Ordner"),
      code(shown),
      text("Arbeit"),
    ]);
    expect(display.text).toBe(`Ordner${shown}Arbeit`);
  });

  test("a server reply keeps no raw control character", () => {
    const display = displayText(
      "NO [AUTHENTICATIONFAILED] Invalid\r\n\u202Ecredentials\u0007",
    );
    expect(display.text).toBe(
      "NO [AUTHENTICATIONFAILED] InvalidU+000DU+000AU+202EcredentialsU+0007",
    );
    for (const part of [...display.start, ...display.end]) {
      expect(part.text).not.toMatch(/[\p{Cc}\p{Bidi_Control}\u2028\u2029]/u);
    }
  });
});

describe("other text stays as it is", () => {
  test.each([
    ["a German folder name", "Entwürfe"],
    [
      "a Hebrew folder name",
      "\u05D3\u05D5\u05D0\u05E8 \u05E0\u05DB\u05E0\u05E1",
    ],
    ["markup", "<b>INBOX</b>"],
    ["a folder path", "Archiv/2026"],
  ])("%s", (_name, value) => {
    const display = displayText(value);
    expect(parts(display)).toEqual([text(value)]);
    expect(display.text).toBe(value);
  });

  test("short text has no separate ending", () => {
    const display = displayText("Entwürfe");
    expect(display.start).toEqual([text("Entwürfe")]);
    expect(display.end).toEqual([]);
  });
});

describe("long text", () => {
  const folder = "Archiv/2026/Projekte/Kunden/Rechnungen/November";

  test("keeps its start and its last 12 characters apart", () => {
    const { start, end } = displayText(folder);
    expect(join(end)).toBe("gen/November");
    expect(join(start)).toBe("Archiv/2026/Projekte/Kunden/Rechnun");
  });

  test("is never cut inside a character cluster", () => {
    const value = `${"a".repeat(30)}\u00E4\u0308${"b".repeat(11)}`;
    const { start, end } = displayText(value);
    expect(join(start) + join(end)).toBe(value);
    expect(join(end)).toBe(`\u00E4\u0308${"b".repeat(11)}`);
  });
});

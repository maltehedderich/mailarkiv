import { describe, expect, test } from "vitest";

import { MARKER, type Fetch } from "#lib/api.js";

import { cleanFileName, fetchAttachment } from "./attachment";

// The names follow the data model's file name steps, worked out by hand. The
// server cleans the name it sends the same way.
describe("cleanFileName", () => {
  test.each([
    ["an ordinary name", "Vertrag_2026.pdf", "2", "file", "Vertrag_2026.pdf"],
    ["non-ASCII stays", "Prüfbericht.pdf", "2", "file", "Prüfbericht.pdf"],
    ["a path", "../../etc/passwd", "2", "file", "etcpasswd"],
    [
      "backslashes",
      "C:\\Temp\\Rechnung.pdf",
      "2",
      "file",
      "C:TempRechnung.pdf",
    ],
    ["markup in the name", "<b>x</b>.html", "2", "file", "<b>x<b>.html"],
    [
      "right-to-left override",
      "Rechnung\u202efdp.exe",
      "2",
      "file",
      "Rechnungfdp.exe",
    ],
    ["C0 controls", "a\u0000b\u0009c\u001fd.txt", "2", "file", "abcd.txt"],
    ["delete", "a\u007fb.txt", "2", "file", "ab.txt"],
    ["C1 controls", "a\u0080b\u009fc.txt", "2", "file", "abc.txt"],
    [
      "bidirectional controls",
      "\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069Plan.txt",
      "2",
      "file",
      "Plan.txt",
    ],
    [
      "spaces and dots at both ends",
      "  . Bericht.pdf .  ",
      "2",
      "file",
      "Bericht.pdf",
    ],
    [
      "dots and spaces inside stay",
      "Q3 . final.pdf",
      "2",
      "file",
      "Q3 . final.pdf",
    ],
    ["no name", null, "2", "file", "attachment-2"],
    ["nothing left", "/.. ./", "1.2", "file", "attachment-1.2"],
    ["controls only", "\u202e\u0007", "3", "file", "attachment-3"],
    [
      "a message gains .eml",
      "Weitergeleitet",
      "2",
      "message",
      "Weitergeleitet.eml",
    ],
    ["a message with .eml", "Nachricht.EML", "2", "message", "Nachricht.EML"],
    ["a message with no name", null, "3", "message", "attachment-3.eml"],
    [
      "a file doesn't gain .eml",
      "Weitergeleitet",
      "2",
      "file",
      "Weitergeleitet",
    ],
    [
      "an embedded image",
      "image001.png",
      "1.2",
      "embedded_image",
      "image001.png",
    ],
  ] as const)("%s", (_name, name, position, kind, want) => {
    expect(cleanFileName(name, position, kind)).toBe(want);
  });

  const bytes = (s: string) => new TextEncoder().encode(s).byteLength;

  test.each([
    // 1 + 294 + 1 + 4 = 300 bytes. The extension keeps 4, which leaves 196
    // for the rest: "a" and 97 "ä" take 195, and a 98th would end at 197.
    [
      "300 bytes on a character boundary",
      "a" + "ä".repeat(147) + "b.pdf",
      "file",
      "a" + "ä".repeat(97) + ".pdf",
    ],
    ["no extension", "x".repeat(300), "file", "x".repeat(200)],
    // 198 bytes and .eml make 202.
    [
      "a message's added .eml",
      "x".repeat(198),
      "message",
      "x".repeat(196) + ".eml",
    ],
    [
      "200 bytes stay",
      "x".repeat(196) + ".pdf",
      "file",
      "x".repeat(196) + ".pdf",
    ],
    // An emoji is two UTF-16 units and 4 bytes; it goes whole or not at all.
    [
      "a character outside the BMP",
      "x".repeat(198) + "😀",
      "file",
      "x".repeat(198),
    ],
  ] as const)("shortens: %s", (_name, name, kind, want) => {
    const got = cleanFileName(name, "2", kind);
    expect(got).toBe(want);
    expect(bytes(got)).toBeLessThanOrEqual(200);
  });
});

describe("fetchAttachment", () => {
  test("reads the part's address with the original download's reader", async () => {
    const calls: string[] = [];
    const fetch: Fetch = async (input) => {
      calls.push(String(input));
      return new Response("%PDF-1.4", {
        status: 200,
        headers: { [MARKER]: "1", "Content-Length": "8" },
      });
    };
    const result = await fetchAttachment(17, "2", () => {}, fetch);
    expect(calls).toEqual(["/api/messages/17/attachments/2"]);
    expect(result.kind).toBe("ok");
    if (result.kind === "ok") expect(await result.file.text()).toBe("%PDF-1.4");
  });

  test("uses the same failure table", async () => {
    const fetch: Fetch = async () =>
      new Response(JSON.stringify({ error: "stored_original_damaged" }), {
        status: 500,
        headers: { [MARKER]: "1" },
      });
    expect(await fetchAttachment(17, "2", () => {}, fetch)).toEqual({
      kind: "failed",
      reason: "stored_original_damaged",
    });
  });
});

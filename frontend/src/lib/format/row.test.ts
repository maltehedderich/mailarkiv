import { describe, expect, test } from "vitest";

import type { Message, Processing } from "#lib/api.js";

import { rowDisplay } from "./row";

// A row shows what processing read, or, before it has or when it couldn't,
// the message's capture data and its state. Placeholders for a missing
// sender or subject belong only to rows processing read.

function message(
  processing: Processing,
  change: Partial<Message> = {},
): Message {
  const summarized =
    processing === "processed" || processing === "processed_with_problems";
  return {
    id: 17,
    contentHash: "ea9449bf".padEnd(64, "0"),
    size: 18_234,
    processing,
    listDate: "2026-11-02T08:14:55.000Z",
    listDateIsArchiveTime: !summarized,
    sender: summarized
      ? { name: "Anna Schmidt", address: "anna@example.com", more: 0 }
      : null,
    subject: summarized ? "Quartalsbericht Q3" : null,
    hasAttachments: false,
    observationCount: 1,
    latest: {
      sourceType: "manual_import",
      observedAt: "2026-11-06T15:50:52.123Z",
      accountEmail: "admin@example.test",
      fileName: "q3.eml",
    },
    ...change,
  };
}

describe("rowDisplay", () => {
  test("a processed row shows its sender and subject, with no mark", () => {
    expect(rowDisplay(message("processed"))).toEqual({
      kind: "summary",
      sender: {
        kind: "sender",
        name: {
          text: "Anna Schmidt",
          start: [{ kind: "text", text: "Anna Schmidt" }],
          end: [],
        },
        address: {
          text: "anna@example.com",
          start: [{ kind: "text", text: "anna" }],
          end: [{ kind: "text", text: "@example.com" }],
        },
        more: null,
      },
      subject: {
        kind: "subject",
        subject: {
          text: "Quartalsbericht Q3",
          start: [{ kind: "text", text: "Quartalsbericht Q3" }],
          end: [],
        },
      },
      label: "Quartalsbericht Q3",
      problems: null,
    });
  });

  test("a row processed with problems carries the problem mark", () => {
    expect(rowDisplay(message("processed_with_problems"))).toMatchObject({
      kind: "summary",
      subject: { kind: "subject", subject: { text: "Quartalsbericht Q3" } },
      problems: "Processed with problems",
    });
  });

  test("a row not processed yet shows its capture data and its state", () => {
    expect(rowDisplay(message("not_processed"))).toEqual({
      kind: "capture",
      entry: {
        sourceType: "manual_import",
        observedAt: "2026-11-06T15:50:52.123Z",
        accountEmail: "admin@example.test",
        fileName: "q3.eml",
      },
      label: "q3.eml",
      state: "Not processed yet",
      tone: "neutral",
    });
  });

  test("a row that couldn't be read shows its capture data and its state", () => {
    expect(rowDisplay(message("unreadable"))).toMatchObject({
      kind: "capture",
      label: "q3.eml",
      state: "Couldn't be read",
      tone: "warn",
    });
  });

  test("a summary row without sender or subject shows placeholders", () => {
    const shown = rowDisplay(
      message("processed", { sender: null, subject: null }),
    );
    expect(shown).toMatchObject({
      kind: "summary",
      sender: { kind: "missing", text: "(no sender)" },
      subject: { kind: "missing", text: "(no subject)" },
      label: "(no subject)",
    });
  });

  test("a row without a summary shows no placeholder", () => {
    for (const processing of ["not_processed", "unreadable"] as const) {
      const shown = JSON.stringify(rowDisplay(message(processing)));
      expect(shown).not.toContain("(no sender)");
      expect(shown).not.toContain("(no subject)");
    }
  });

  test("a subject's control and bidi characters become badges in the label too", () => {
    const shown = rowDisplay(
      message("processed", { subject: "Rechnung‮fdp.exe\u0000" }),
    );
    expect(shown).toMatchObject({
      subject: {
        kind: "subject",
        subject: {
          start: [
            { kind: "text", text: "Rechnung" },
            { kind: "code", text: "U+202E" },
            { kind: "text", text: "fdp.exe" },
            { kind: "code", text: "U+0000" },
          ],
          end: [],
        },
      },
      label: "RechnungU+202Efdp.exeU+0000",
    });
  });

  test("an import without a file name is labelled as the list names it", () => {
    const shown = rowDisplay(
      message("unreadable", {
        latest: {
          sourceType: "manual_import",
          observedAt: "2026-11-06T15:50:52.123Z",
          accountEmail: "admin@example.test",
          fileName: null,
        },
      }),
    );
    expect(shown).toMatchObject({ kind: "capture", label: "(no file name)" });
  });
});

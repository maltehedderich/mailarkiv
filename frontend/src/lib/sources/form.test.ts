import { describe, expect, test } from "vitest";

import {
  chosenNames,
  currentChoice,
  DEFAULT_PORTS,
  firstChoice,
  isValidName,
  parsePort,
  portAfterSwitch,
  trimSourceField,
  widenChoice,
} from "./form";

// Expected values come from the spec: the port defaults to 993 for TLS and
// 143 for STARTTLS, and the inbox is preselected.

describe("DEFAULT_PORTS", () => {
  test("are 993 for TLS and 143 for STARTTLS", () => {
    expect(DEFAULT_PORTS).toEqual({ tls: 993, starttls: 143 });
  });
});

describe("portAfterSwitch", () => {
  test.each([
    ["993", "tls", "starttls", "143"],
    ["143", "starttls", "tls", "993"],
    ["", "tls", "starttls", "143"],
    [" ", "starttls", "tls", "993"],
  ] as const)(
    "%j follows a switch from %s to %s, to %j",
    (port, from, to, want) => {
      expect(portAfterSwitch(port, from, to)).toBe(want);
    },
  );

  test.each([
    ["1993", "tls", "starttls"],
    ["41923", "starttls", "tls"],
    // The other mode's default, typed while this mode was chosen.
    ["993", "starttls", "tls"],
    ["143", "tls", "starttls"],
  ] as const)("a typed %j stays when %s switches to %s", (port, from, to) => {
    expect(portAfterSwitch(port, from, to)).toBe(port);
  });
});

describe("parsePort", () => {
  test.each([
    ["993", 993],
    [" 143 ", 143],
    ["\u200B\u0085 993\uFEFF", 993],
    ["65535", 65535],
    ["0", 0],
  ])("reads %j as %d", (text, want) => {
    expect(parsePort(text)).toBe(want);
  });

  // The server refuses null with invalid_fields naming the port.
  test.each(["", "abc", "99.5", "+5", "1e3", "-1", "99999999999999999999"])(
    "reads %j as null",
    (text) => {
      expect(parsePort(text)).toBeNull();
    },
  );
});

describe("trimSourceField", () => {
  test.each([
    [
      " \t\u0085\u00a0\u200B\uFEFFanna@example.test\uFEFF\u200B\n ",
      "anna@example.test",
    ],
    [" \u200B\uFEFF ", ""],
    [" Anna Office ", "Anna Office"],
    ["a\u200Bb\u200Dc", "a\u200Bb\u200Dc"],
  ])("normalizes %j to %j", (input, expected) => {
    expect(trimSourceField(input)).toBe(expected);
  });
});

describe("firstChoice", () => {
  test("keeps the server's order and checks INBOX only", () => {
    expect(
      firstChoice([
        { name: "INBOX", messages: 1204 },
        { name: "Gesendete Objekte", messages: 311 },
        { name: "Archiv/2025", messages: null },
      ]),
    ).toEqual([
      { name: "INBOX", messages: 1204, chosen: true },
      { name: "Gesendete Objekte", messages: 311, chosen: false },
      { name: "Archiv/2025", messages: null, chosen: false },
    ]);
  });

  test("checks nothing when the server lists no INBOX", () => {
    expect(firstChoice([{ name: "Archiv", messages: 2 }])).toEqual([
      { name: "Archiv", messages: 2, chosen: false },
    ]);
  });
});

describe("currentChoice", () => {
  test("lists a source's folders in sync order, all chosen, with no counts", () => {
    expect(currentChoice(["INBOX", "Projekte"])).toEqual([
      { name: "INBOX", messages: null, chosen: true },
      { name: "Projekte", messages: null, chosen: true },
    ]);
  });
});

describe("widenChoice", () => {
  test("keeps each folder's choice, adds counts, and appends the server's others unchosen", () => {
    const current = [
      { name: "INBOX", messages: null, chosen: true },
      // Gone from the server, so unavailable. It stays until unchecked.
      { name: "Projekte", messages: null, chosen: true },
      // Unchecked in this form before the check.
      { name: "Sent", messages: null, chosen: false },
    ];
    expect(
      widenChoice(current, [
        { name: "INBOX", messages: 3 },
        { name: "Sent", messages: 2 },
        { name: "Drafts", messages: null },
        { name: "Archiv", messages: 1 },
      ]),
    ).toEqual([
      { name: "INBOX", messages: 3, chosen: true },
      { name: "Projekte", messages: null, chosen: true },
      { name: "Sent", messages: 2, chosen: false },
      { name: "Drafts", messages: null, chosen: false },
      { name: "Archiv", messages: 1, chosen: false },
    ]);
  });
});

describe("chosenNames", () => {
  test("lists the chosen folders in the form's order", () => {
    expect(
      chosenNames([
        { name: "INBOX", messages: 3, chosen: true },
        { name: "Sent", messages: 2, chosen: false },
        { name: "Archiv", messages: 1, chosen: true },
      ]),
    ).toEqual(["INBOX", "Archiv"]);
  });
});

// The data model: a name is 1 to 100 code points after trimming spaces.
describe("isValidName", () => {
  test.each([
    ["Anna"],
    [" Anna "],
    ["a".repeat(100)],
    // 100 code points, 200 UTF-16 units.
    ["😀".repeat(100)],
  ])("accepts %j", (name) => {
    expect(isValidName(name)).toBe(true);
  });

  test.each([[""], ["   "], ["a".repeat(101)], ["😀".repeat(101)]])(
    "refuses %j",
    (name) => {
      expect(isValidName(name)).toBe(false);
    },
  );
});

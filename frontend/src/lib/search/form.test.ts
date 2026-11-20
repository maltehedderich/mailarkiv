import { describe, expect, test } from "vitest";

import { emptyForm, filterCount, readForm, type SearchForm } from "./form";

function form(changes: Partial<SearchForm>): SearchForm {
  return { ...emptyForm(), ...changes };
}

test("every field reaches the query, trimmed, with the days as instants", () => {
  expect(
    readForm(
      form({
        words: " quartals müller ",
        from: "anna@example.com",
        to: " example.com",
        subject: "Planung",
        attachment: "vertrag",
        firstDay: "2026-03-29",
        lastDay: "2026-03-30",
        hasAttachments: true,
      }),
      3,
    ),
  ).toEqual({
    kind: "search",
    query: {
      words: "quartals müller",
      from: "anna@example.com",
      to: "example.com",
      subject: "Planung",
      attachment: "vertrag",
      listDateFrom: "2026-03-29T00:00:00.000+01:00",
      listDateBefore: "2026-03-31T00:00:00.000+02:00",
      hasAttachments: true,
      page: 3,
    },
  });
});

test("words alone search from the first page with every filter empty", () => {
  expect(readForm(form({ words: "quartals" }))).toEqual({
    kind: "search",
    query: {
      words: "quartals",
      from: "",
      to: "",
      subject: "",
      attachment: "",
      listDateFrom: null,
      listDateBefore: null,
      hasAttachments: false,
      page: 1,
    },
  });
});

describe("a form with nothing to search for shows the list without a request", () => {
  test.each([
    ["empty", {}],
    ["spaces", { words: "   " }],
    ["punctuation", { words: "?! -- „“" }],
    ["punctuation in filters too", { words: "?!", from: "@", subject: "--" }],
  ])("%s", (_, changes: Partial<SearchForm>) => {
    expect(readForm(form(changes))).toEqual({ kind: "list" });
  });
});

describe("one filter is enough to search", () => {
  test.each([
    ["From", { from: "anna" }],
    ["To", { to: "example.com" }],
    ["Subject", { subject: "Planung" }],
    ["Attachment name", { attachment: "pdf" }],
    ["First day", { firstDay: "2026-03-28" }],
    ["Last day", { lastDay: "2026-03-28" }],
    ["Has attachments", { hasAttachments: true }],
    ["a letter in another script", { words: "?!", subject: "ä" }],
    ["a digit", { words: "2026" }],
  ])("%s", (_, changes: Partial<SearchForm>) => {
    expect(readForm(form(changes)).kind).toBe("search");
  });
});

test("a last day before the first day is refused", () => {
  expect(
    readForm(form({ firstDay: "2026-03-30", lastDay: "2026-03-29" })),
  ).toEqual({
    kind: "invalid",
    problem: "The last day is before the first day.",
  });
  expect(
    readForm(form({ firstDay: "2026-03-30", lastDay: "2026-03-30" })).kind,
  ).toBe("search");
});

test("the filter count counts each filter with something in it", () => {
  expect(filterCount(emptyForm())).toBe(0);
  expect(filterCount(form({ words: "quartals" }))).toBe(0);
  expect(filterCount(form({ from: "anna", subject: "  " }))).toBe(1);
  expect(
    filterCount(
      form({
        from: "a",
        to: "b",
        subject: "c",
        attachment: "d",
        firstDay: "2026-03-29",
        lastDay: "2026-03-30",
        hasAttachments: true,
      }),
    ),
  ).toBe(7);
});

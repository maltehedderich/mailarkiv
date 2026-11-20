import { describe, expect, test } from "vitest";

import { historyHref } from "./history";

// A history opens over the list it was opened from, which keeps its page. From
// anywhere else it opens over the message list's first page.

describe("historyHref", () => {
  test("the message list keeps its page", () => {
    expect(historyHref(new URL("http://localhost/?page=3"), 17)).toBe(
      "/?page=3&message=17",
    );
  });

  test("the problem list keeps its page", () => {
    expect(historyHref(new URL("http://localhost/problems?page=2"), 17)).toBe(
      "/problems?page=2&message=17",
    );
  });

  test("another history replaces the open one", () => {
    expect(
      historyHref(new URL("http://localhost/problems?message=4"), 17),
    ).toBe("/problems?message=17");
  });

  test("anywhere else opens the message list", () => {
    expect(historyHref(new URL("http://localhost/status"), 17)).toBe(
      "/?message=17",
    );
  });
});

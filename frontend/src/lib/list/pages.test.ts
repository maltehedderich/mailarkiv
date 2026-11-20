import { describe, expect, test } from "vitest";

import { pastTheEndHref, wantedPage } from "./pages";

// A paged list reads its page from the address, and a page past the end moves
// back to the last page, keeping every other parameter.

describe("wantedPage", () => {
  test.each([
    [null, 1],
    ["1", 1],
    ["12", 12],
    ["0", null],
    ["01", null],
    ["-1", null],
    ["two", null],
    ["", null],
  ])("%j asks for %j", (param, page) => {
    expect(wantedPage(param)).toBe(page);
  });
});

describe("pastTheEndHref", () => {
  const empty = { items: [], pageSize: 50 };

  test("a page with rows stays", () => {
    expect(
      pastTheEndHref(new URL("http://localhost/problems?page=2"), {
        items: [{}],
        pageSize: 50,
        total: 51,
      }),
    ).toBeNull();
  });

  test("an empty list stays", () => {
    expect(
      pastTheEndHref(new URL("http://localhost/?page=3"), {
        ...empty,
        total: 0,
      }),
    ).toBeNull();
  });

  test("moves back to the last page", () => {
    expect(
      pastTheEndHref(new URL("http://localhost/problems?page=9&message=4"), {
        ...empty,
        total: 101,
      }),
    ).toBe("/problems?page=3&message=4");
  });

  test("page 1 has no parameter", () => {
    expect(
      pastTheEndHref(new URL("http://localhost/?page=2"), {
        ...empty,
        total: 50,
      }),
    ).toBe("/");
  });
});

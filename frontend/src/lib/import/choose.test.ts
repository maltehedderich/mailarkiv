import { describe, expect, test } from "vitest";

import { choose } from "./choose";

// FR-009 and FR-036: the browser takes one file of 1 to 150,000,000 bytes, and
// refuses an empty file, a larger one, or several at once before any upload.

const LIMIT = 150_000_000;

/** A real file of a few bytes. */
function smallFile(content: string, name = "message.eml"): File {
  return new File([content], name, { type: "message/rfc822" });
}

/**
 * A file that reports `size` bytes. Building a real one would hold 150 MB in
 * memory, and choose reads only the size.
 */
function fileOfSize(size: number, name = "large.eml"): File {
  const file = new File([], name, { type: "message/rfc822" });
  Object.defineProperty(file, "size", { value: size });
  return file;
}

describe("one file", () => {
  test("with content is chosen", () => {
    const file = smallFile("Subject: Hello\r\n\r\nHi\r\n");
    expect(choose([file])).toEqual({ kind: "chosen", file });
  });

  test("of exactly 150,000,000 bytes is chosen", () => {
    const file = fileOfSize(LIMIT);
    expect(choose([file])).toEqual({ kind: "chosen", file });
  });

  test("of 150,000,001 bytes is refused as too large", () => {
    expect(choose([fileOfSize(LIMIT + 1)])).toEqual({
      kind: "refused",
      reason: "too_large",
    });
  });

  test("of 0 bytes is refused as empty", () => {
    expect(choose([smallFile("")])).toEqual({
      kind: "refused",
      reason: "empty",
    });
  });
});

describe("several files", () => {
  test("two valid files are refused, and neither is picked", () => {
    expect(choose([smallFile("a: 1\r\n"), smallFile("b: 2\r\n")])).toEqual({
      kind: "refused",
      reason: "several",
    });
  });

  test("three files are refused", () => {
    const files = ["a", "b", "c"].map((name) => smallFile("x: 1\r\n", name));
    expect(choose(files)).toEqual({ kind: "refused", reason: "several" });
  });

  // The count is the problem the administrator has to fix first.
  test("an empty and an oversized file are refused as several", () => {
    expect(choose([smallFile(""), fileOfSize(LIMIT + 1)])).toEqual({
      kind: "refused",
      reason: "several",
    });
  });
});

// A closed file dialog, or a drop with no files in it, such as dragged text.
test("no files choose nothing", () => {
  expect(choose([])).toEqual({ kind: "none" });
});

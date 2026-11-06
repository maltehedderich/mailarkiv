import { describe, expect, test } from "vitest";

import { formatBytes, formatSize } from "./size";

// T050: decimal units, as in "31 B", "48 KB", "1.2 MB", and "150 MB", and
// exact bytes as "150,000,000 bytes".
describe("formatSize", () => {
  test.each([
    [31, "31 B"],
    [48_123, "48 KB"],
    [1_234_567, "1.2 MB"],
    [150_000_000, "150 MB"],
  ])("%i bytes is %s", (bytes, expected) => {
    expect(formatSize(bytes)).toBe(expected);
  });

  // 1 KB is 1,000 bytes, not 1,024.
  test.each([
    [0, "0 B"],
    [999, "999 B"],
    [1_000, "1 KB"],
    [1_024, "1 KB"],
    [1_500, "1.5 KB"],
    [9_960, "10 KB"],
    [999_999, "1 MB"],
    [1_000_000, "1 MB"],
  ])("%i bytes is %s at the unit edges", (bytes, expected) => {
    expect(formatSize(bytes)).toBe(expected);
  });
});

describe("formatBytes", () => {
  test.each([
    [150_000_000, "150,000,000 bytes"],
    [31, "31 bytes"],
    [1, "1 byte"],
    [1_000, "1,000 bytes"],
  ])("%i is %s", (bytes, expected) => {
    expect(formatBytes(bytes)).toBe(expected);
  });
});

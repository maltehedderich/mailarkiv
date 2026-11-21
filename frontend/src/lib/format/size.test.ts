import { describe, expect, test } from "vitest";

import { formatBinarySize, formatBytes, formatSize } from "./size";

// Decimal units, as in "31 B", "48 KB", "1.2 MB", and "150 MB", and exact
// bytes as "150,000,000 bytes".
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

// The search thresholds are set in binary units, so the screen shows them as
// set: 2 GiB, not 2.1 GB.
describe("formatBinarySize", () => {
  test.each([
    [2_147_483_648, "2 GiB"],
    [3_221_225_472, "3 GiB"],
    [1_610_612_736, "1.5 GiB"],
    [41_234_567_890, "38 GiB"],
    [104_857_600, "100 MiB"],
    [1_023, "1023 B"],
    [1_024, "1 KiB"],
  ])("%i bytes is %s", (bytes, expected) => {
    expect(formatBinarySize(bytes)).toBe(expected);
  });
});

import { describe, expect, test } from "vitest";

import {
  formatOffset,
  formatTime,
  formatTimeToday,
  untilMidnight,
} from "./time";

// `mise run frontend:test` sets TZ=Europe/Berlin. Expected values come from
// Europe/Berlin uses +01:00 in winter and +02:00 in summer.
describe("formatTime", () => {
  test("shows an absolute time with the UTC offset", () => {
    expect(formatTime("2026-11-02T08:14:55.000Z")).toBe(
      "2 Nov 2026, 09:14:55 +01:00",
    );
  });

  test("uses the offset in force on that date", () => {
    expect(formatTime("2026-07-02T08:14:55.000Z")).toBe(
      "2 Jul 2026, 10:14:55 +02:00",
    );
  });

  test("moves to the next year in the viewer's zone", () => {
    expect(formatTime("2026-12-31T23:05:09.000Z")).toBe(
      "1 Jan 2027, 00:05:09 +01:00",
    );
  });
});

describe("formatOffset", () => {
  test.each([
    [60, "+01:00"],
    [-300, "-05:00"],
    [330, "+05:30"],
    [0, "+00:00"],
  ])("%i minutes east of UTC is %s", (minutes, expected) => {
    expect(formatOffset(minutes)).toBe(expected);
  });
});

describe("formatTimeToday", () => {
  const now = new Date("2026-11-14T15:00:00.000Z");

  test("drops the date on the viewer's current day", () => {
    expect(formatTimeToday("2026-11-14T15:09:34.000Z", now)).toBe(
      "16:09:34 +01:00",
    );
  });

  test("keeps the date on another day", () => {
    expect(formatTimeToday("2026-11-13T15:09:34.000Z", now)).toBe(
      "13 Nov 2026, 16:09:34 +01:00",
    );
  });

  test("uses the viewer's day, not the UTC one", () => {
    // 23:30 UTC on the 14th is 00:30 on the 15th in Berlin.
    expect(formatTimeToday("2026-11-14T23:30:00.000Z", now)).toBe(
      "15 Nov 2026, 00:30:00 +01:00",
    );
  });
});

describe("untilMidnight", () => {
  test("counts to the viewer's next midnight", () => {
    expect(untilMidnight(new Date("2026-11-14T22:30:00.000Z"))).toBe(
      30 * 60 * 1000,
    );
  });
});

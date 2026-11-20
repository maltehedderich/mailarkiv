import { expect, test } from "vitest";

import { dayRange } from "./dates";

// mise runs these with TZ=Europe/Berlin, which changes to summer time on
// 2026-03-29 and back on 2026-10-25.

test("a one-day range runs from that midnight to the next, each with its own offset", () => {
  expect(dayRange("2026-03-29", "2026-03-29")).toEqual({
    listDateFrom: "2026-03-29T00:00:00.000+01:00",
    listDateBefore: "2026-03-30T00:00:00.000+02:00",
  });
  expect(dayRange("2026-10-25", "2026-10-25")).toEqual({
    listDateFrom: "2026-10-25T00:00:00.000+02:00",
    listDateBefore: "2026-10-26T00:00:00.000+01:00",
  });
});

test("the last day is included, so the range ends at the next midnight", () => {
  expect(dayRange("2026-01-31", "2026-12-31")).toEqual({
    listDateFrom: "2026-01-31T00:00:00.000+01:00",
    listDateBefore: "2027-01-01T00:00:00.000+01:00",
  });
});

test("an empty first or last day has no bound", () => {
  expect(dayRange("", "2026-03-28")).toEqual({
    listDateFrom: null,
    listDateBefore: "2026-03-29T00:00:00.000+01:00",
  });
  expect(dayRange("2026-10-26", "")).toEqual({
    listDateFrom: "2026-10-26T00:00:00.000+01:00",
    listDateBefore: null,
  });
  expect(dayRange("", "")).toEqual({
    listDateFrom: null,
    listDateBefore: null,
  });
});

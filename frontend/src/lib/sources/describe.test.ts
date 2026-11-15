import { describe, expect, test } from "vitest";

import { SOURCE_STATES, SYNC_CATEGORIES } from "#lib/api.js";

import { problemSummary, problemTone, stateTone } from "./describe";

// The expected words and tones are the design guide's Sources section.

describe("stateTone", () => {
  test.each([
    ["waiting", "ok"],
    ["syncing", "neutral"],
    ["retrying", "warn"],
    ["needs_attention", "bad"],
    ["paused", "neutral"],
  ] as const)("%s has a %s dot", (state, tone) => {
    expect(stateTone(state)).toBe(tone);
  });

  test("covers every state", () => {
    for (const state of SOURCE_STATES) expect(stateTone(state)).toBeTruthy();
  });
});

describe("problemTone", () => {
  test.each(SYNC_CATEGORIES)("a sync failure, %s, is bad", (category) => {
    expect(problemTone(category)).toBe("bad");
  });

  test.each(["folder_unavailable", "message_unavailable"] as const)(
    "%s is a warning",
    (category) => {
      expect(problemTone(category)).toBe("warn");
    },
  );
});

describe("problemSummary", () => {
  test("is null with no problems", () => {
    expect(
      problemSummary({
        unavailableFolders: 0,
        waitingForRetry: 0,
        notArchived: 0,
      }),
    ).toBeNull();
  });

  test("names one of each in the singular", () => {
    expect(
      problemSummary({
        unavailableFolders: 1,
        waitingForRetry: 1,
        notArchived: 1,
      }),
    ).toBe(
      "1 folder unavailable, 1 message waiting for retry, 1 message not archived",
    );
  });

  test("names only the counts above zero, with separators", () => {
    expect(
      problemSummary({
        unavailableFolders: 0,
        waitingForRetry: 1284,
        notArchived: 2,
      }),
    ).toBe("1,284 messages waiting for retry, 2 messages not archived");
    expect(
      problemSummary({
        unavailableFolders: 3,
        waitingForRetry: 0,
        notArchived: 0,
      }),
    ).toBe("3 folders unavailable");
  });
});

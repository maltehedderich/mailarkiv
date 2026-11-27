import { describe, expect, test } from "vitest";

import type { SearchStatus } from "#lib/api.js";

import {
  canRebuild,
  INDEXING_WORDS,
  pausedNote,
  progressNote,
  SEARCH_WORDS,
} from "./search";

const GIB = 1024 ** 3;

function search(
  change: Partial<Omit<SearchStatus, "indexing">> & {
    indexing?: Partial<SearchStatus["indexing"]>;
  } = {},
): SearchStatus {
  return {
    state: "available",
    nextStartAt: null,
    needsRebuild: null,
    restarts: 0,
    rebuild: null,
    ...change,
    indexing: {
      state: "up_to_date",
      waiting: 0,
      done: null,
      toDo: null,
      freeBytes: 40 * GIB,
      pauseBelowBytes: 2 * GIB,
      resumeAtBytes: 3 * GIB,
      ...change.indexing,
    },
  };
}

// Each state shows as a word with a dot. Only Up to date and Available read
// as fine; work in progress is neutral, and the rest ask for attention.
describe("state words", () => {
  test("search", () => {
    expect(SEARCH_WORDS).toEqual({
      available: { word: "Available", tone: "ok" },
      unavailable: { word: "Unavailable", tone: "bad" },
      needs_rebuild: { word: "Needs rebuild", tone: "bad" },
    });
  });

  test("indexing", () => {
    expect(INDEXING_WORDS).toEqual({
      up_to_date: { word: "Up to date", tone: "ok" },
      catching_up: { word: "Catching up", tone: "neutral" },
      building: { word: "Building", tone: "neutral" },
      rebuilding: { word: "Rebuilding", tone: "neutral" },
      paused: { word: "Paused", tone: "warn" },
      stopped: { word: "Stopped", tone: "bad" },
    });
  });
});

// Rebuild shows only where the server would start one.
describe("canRebuild", () => {
  test.each<[string, SearchStatus, boolean]>([
    ["available and up to date", search(), true],
    [
      "catching up",
      search({ indexing: { state: "catching_up", waiting: 3 } }),
      true,
    ],
    [
      "needs rebuild",
      search({
        state: "needs_rebuild",
        needsRebuild: { cause: "index_damaged", since: "2026-11-21T10:00:00Z" },
        indexing: { state: "stopped" },
      }),
      true,
    ],
    [
      "a rebuild runs",
      search({
        indexing: { state: "rebuilding", done: 0, toDo: 3 },
        rebuild: {
          id: 4,
          startedAt: "2026-11-21T10:00:00Z",
          startedBy: "admin@example.test",
        },
      }),
      false,
    ],
    [
      "the first index builds",
      search({ indexing: { state: "building", done: 1, toDo: 3 } }),
      false,
    ],
    [
      "unavailable",
      search({
        state: "unavailable",
        nextStartAt: "2026-11-21T10:00:00Z",
        indexing: { state: "stopped" },
      }),
      false,
    ],
  ])("%s: %s", (_name, status, expected) => {
    expect(canRebuild(status)).toBe(expected);
  });
});

describe("progressNote", () => {
  test("counts with separators", () => {
    expect(
      progressNote(search({ indexing: { done: 1_200_000, toDo: 5_000_000 } })),
    ).toBe("1,200,000 of 5,000,000 messages");
  });

  test("one message", () => {
    expect(progressNote(search({ indexing: { done: 0, toDo: 1 } }))).toBe(
      "0 of 1 message",
    );
  });

  test("none while no index builds", () => {
    expect(progressNote(search())).toBeNull();
  });
});

describe("pausedNote", () => {
  test("names the free space and both thresholds", () => {
    expect(
      pausedNote(
        search({ indexing: { state: "paused", freeBytes: 1.5 * GIB } }),
      ),
    ).toBe("1.5 GiB free, below 2 GiB. Indexing resumes at 3 GiB.");
  });

  test("without a reading", () => {
    expect(
      pausedNote(search({ indexing: { state: "paused", freeBytes: null } })),
    ).toBe("Free space is below 2 GiB. Indexing resumes at 3 GiB.");
  });
});

import { describe, expect, test } from "vitest";

import type { FailureDetail } from "#lib/api.js";

import { detailFacts, NEEDS_REBUILD_WORDS } from "./detail";

const NONE: FailureDetail = {
  exitStatus: null,
  signal: null,
  cause: null,
  operation: null,
  generationId: null,
  rebuildId: null,
  firstMessageId: null,
  lastMessageId: null,
  freeBytes: null,
};

// A failure's detail shows as fixed words with IDs and sizes, one fact each.
describe("detailFacts", () => {
  test.each<[string, Partial<FailureDetail>, string[]]>([
    ["a signal", { signal: 9 }, ["Signal 9"]],
    ["an exit status", { exitStatus: 0 }, ["Exit status 0"]],
    ["a missing program", { cause: "missing" }, ["Program missing"]],
    ["the operation", { operation: "search" }, ["During search"]],
    ["maintenance", { operation: "maintain" }, ["During maintenance"]],
    [
      "a failed batch",
      {
        generationId: 3,
        firstMessageId: 17,
        lastMessageId: 40,
        cause: "no_space",
      },
      ["No space left", "Index 3", "Messages 17–40"],
    ],
    [
      "a batch of one",
      {
        generationId: 3,
        firstMessageId: 17,
        lastMessageId: 17,
        cause: "other",
      },
      ["Other error", "Index 3", "Message 17"],
    ],
    ["free space", { freeBytes: 1_610_612_736 }, ["1.5 GiB free"]],
    [
      "a failed rebuild",
      { rebuildId: 4, generationId: 5 },
      ["Rebuild 4", "Index 5"],
    ],
    ["a damaged index", { cause: "index_damaged" }, ["Index damaged"]],
  ])("%s", (_name, detail, expected) => {
    expect(detailFacts({ ...NONE, ...detail })).toEqual(expected);
  });

  test("no detail has no facts", () => {
    expect(detailFacts(null)).toEqual([]);
  });
});

describe("NEEDS_REBUILD_WORDS", () => {
  test("names each cause as its failure category does", () => {
    expect(NEEDS_REBUILD_WORDS).toEqual({
      search_process_stopped: "Search process stopped",
      index_maintenance_failed: "Index maintenance failed",
      index_damaged: "Index damaged",
    });
  });
});

import { describe, expect, test } from "vitest";

import type { FailureEvent } from "#lib/api.js";

import { groupFailures } from "./groups";

function event(
  at: string,
  category: FailureEvent["category"],
  restored = false,
): FailureEvent {
  return {
    at,
    category,
    messageId: null,
    contentHash: null,
    source: null,
    restored,
    repeats: 1,
    lastAt: at,
    detail: null,
  };
}

function repeated(
  at: string,
  category: FailureEvent["category"],
  repeats: number,
  lastAt: string,
): FailureEvent {
  return { ...event(at, category), repeats, lastAt };
}

function messageEvent(
  at: string,
  category: FailureEvent["category"],
  sourceId: number,
): FailureEvent {
  return {
    ...event(at, category),
    source: { id: sourceId, name: `Source ${sourceId}` },
  };
}

describe("groupFailures", () => {
  test("no events make no groups", () => {
    expect(groupFailures([])).toEqual([]);
  });

  test("events of one category share a group, in the order they happened", () => {
    const first = event("2026-11-06T15:10:00Z", "storage_full");
    const second = event("2026-11-06T15:20:00Z", "storage_full");
    expect(groupFailures([first, second])).toEqual([
      {
        key: "storage_full",
        category: "storage_full",
        restored: false,
        events: [first, second],
        count: 2,
        lastAt: "2026-11-06T15:20:00Z",
      },
    ]);
  });

  // The status screen shows a restored event's note in place of the next step.
  test("a restored event gets its own group", () => {
    const damaged = event("2026-11-06T15:18:00Z", "stored_original_damaged");
    const restored = event(
      "2026-11-06T15:20:00Z",
      "stored_original_damaged",
      true,
    );
    const groups = groupFailures([damaged, restored]);
    expect(groups.map(({ key }) => key)).toEqual([
      "stored_original_damaged",
      "stored_original_damaged-restored",
    ]);
    expect(groups[1].events).toEqual([restored]);
  });

  test("groups keep the order of their first event", () => {
    const groups = groupFailures([
      event("2026-11-06T15:00:00Z", "temp_file_not_removed"),
      event("2026-11-06T15:10:00Z", "storage_full"),
      event("2026-11-06T15:12:00Z", "temp_file_not_removed"),
      event("2026-11-06T15:14:00Z", "catalog_write_failed"),
    ]);
    expect(groups.map(({ key }) => key)).toEqual([
      "temp_file_not_removed",
      "storage_full",
      "catalog_write_failed",
    ]);
    expect(groups[0].events).toHaveLength(2);
  });
});

describe("groupFailures with message events", () => {
  // A message event names its source in its own line, so events from
  // different sources share their category's group.
  test("group by category like other events", () => {
    const anna = messageEvent("2026-11-08T09:00:00Z", "message_too_large", 3);
    const office = messageEvent("2026-11-08T09:05:00Z", "message_too_large", 5);
    const removed = messageEvent("2026-11-08T09:10:00Z", "message_removed", 3);
    const full = event("2026-11-08T09:12:00Z", "storage_full");
    expect(groupFailures([anna, office, removed, full])).toEqual([
      {
        key: "message_too_large",
        category: "message_too_large",
        restored: false,
        events: [anna, office],
        count: 2,
        lastAt: "2026-11-08T09:05:00Z",
      },
      {
        key: "message_removed",
        category: "message_removed",
        restored: false,
        events: [removed],
        count: 1,
        lastAt: "2026-11-08T09:10:00Z",
      },
      {
        key: "storage_full",
        category: "storage_full",
        restored: false,
        events: [full],
        count: 1,
        lastAt: "2026-11-08T09:12:00Z",
      },
    ]);
  });
});

describe("groupFailures with repeats", () => {
  // The server merges a run of one search failure into one event, which
  // stays one entry. The group counts every failure it stands for.
  test("a repeated event is one entry, counted with its repeats", () => {
    const crashes = repeated(
      "2026-11-15T09:00:01Z",
      "search_process_stopped",
      3,
      "2026-11-15T09:00:09Z",
    );
    expect(groupFailures([crashes])).toEqual([
      {
        key: "search_process_stopped",
        category: "search_process_stopped",
        restored: false,
        events: [crashes],
        count: 3,
        lastAt: "2026-11-15T09:00:09Z",
      },
    ]);
  });

  // Another category between two runs ends the first, so one category can
  // have several events. The last time is the newest of any of them.
  test("runs of one category add up, and the last time is the newest", () => {
    const first = repeated(
      "2026-11-15T09:00:01Z",
      "search_process_stopped",
      3,
      "2026-11-15T09:00:09Z",
    );
    const between = event("2026-11-15T09:01:00Z", "index_write_failed");
    const second = repeated(
      "2026-11-15T09:02:00Z",
      "search_process_stopped",
      2,
      "2026-11-15T09:02:05Z",
    );
    const groups = groupFailures([first, between, second]);
    expect(
      groups.map(({ key, count, lastAt }) => ({ key, count, lastAt })),
    ).toEqual([
      {
        key: "search_process_stopped",
        count: 5,
        lastAt: "2026-11-15T09:02:05Z",
      },
      {
        key: "index_write_failed",
        count: 1,
        lastAt: "2026-11-15T09:01:00Z",
      },
    ]);
    expect(groups[0].events).toEqual([first, second]);
  });
});

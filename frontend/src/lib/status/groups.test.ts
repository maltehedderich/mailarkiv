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
  };
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
      },
      {
        key: "message_removed",
        category: "message_removed",
        restored: false,
        events: [removed],
      },
      {
        key: "storage_full",
        category: "storage_full",
        restored: false,
        events: [full],
      },
    ]);
  });
});

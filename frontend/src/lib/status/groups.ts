import type { FailureCategory, FailureEvent } from "#lib/api.js";

/** Failure events that share a category and whether they were restored. */
export interface FailureGroup {
  /** Unique on the page, for keyed lists and heading ids. */
  key: string;
  category: FailureCategory;
  /** A restored event shows that the copy was restored, not the next step. */
  restored: boolean;
  /** In the order they happened. A repeated event is one entry. */
  events: FailureEvent[];
  /** Every failure the group stands for, repeats included. */
  count: number;
  /** The newest failure's time. */
  lastAt: string;
}

/**
 * Groups failure events, so a category's meaning and next step show once
 * above every time it happened. Groups keep the order of their first event.
 */
export function groupFailures(failures: FailureEvent[]): FailureGroup[] {
  const groups = new Map<string, FailureGroup>();
  for (const event of failures) {
    const key = `${event.category}${event.restored ? "-restored" : ""}`;
    let group = groups.get(key);
    if (group === undefined) {
      group = {
        key,
        category: event.category,
        restored: event.restored,
        events: [],
        count: 0,
        lastAt: event.lastAt,
      };
      groups.set(key, group);
    }
    group.events.push(event);
    group.count += event.repeats;
    if (Date.parse(event.lastAt) > Date.parse(group.lastAt)) {
      group.lastAt = event.lastAt;
    }
  }
  return [...groups.values()];
}

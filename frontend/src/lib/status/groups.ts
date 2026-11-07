import type { FailureCategory, FailureEvent } from "#lib/api.js";

/** Failure events that share a category and whether they were restored. */
export interface FailureGroup {
  /** Unique on the page, for keyed lists and heading ids. */
  key: string;
  category: FailureCategory;
  /** A restored event shows that the copy was restored, not the next step. */
  restored: boolean;
  /** In the order they happened. */
  events: FailureEvent[];
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
      };
      groups.set(key, group);
    }
    group.events.push(event);
  }
  return [...groups.values()];
}

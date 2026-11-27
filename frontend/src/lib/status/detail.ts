import type {
  FailureCause,
  FailureDetail,
  NeedsRebuildCause,
  SearchOperation,
} from "#lib/api.js";
import { formatBinarySize } from "#lib/format/size.js";

// A Record over each closed union makes a new word fail type checking until
// it has its words here.

const CAUSES: Record<FailureCause, string> = {
  missing: "Program missing",
  not_executable: "Program not executable",
  bad_hello: "Program answered wrongly at its start",
  no_space: "No space left",
  permission_denied: "Permission denied",
  other: "Other error",
  search_process_stopped: "Search process stopped",
  index_maintenance_failed: "Index maintenance failed",
  index_damaged: "Index damaged",
};

const OPERATIONS: Record<SearchOperation, string> = {
  open: "During opening",
  index: "During indexing",
  search: "During search",
  maintain: "During maintenance",
  close: "During closing",
  size: "During the size check",
};

/** Why search needs a rebuild, named as its failure category is. */
export const NEEDS_REBUILD_WORDS: Record<NeedsRebuildCause, string> = {
  search_process_stopped: CAUSES.search_process_stopped,
  index_maintenance_failed: CAUSES.index_maintenance_failed,
  index_damaged: CAUSES.index_damaged,
};

/**
 * A search failure's detail as short facts, such as "Signal 9" and
 * "Messages 17–40". They hold IDs, sizes, and fixed words only.
 */
export function detailFacts(detail: FailureDetail | null): string[] {
  if (detail === null) return [];
  const facts: string[] = [];
  if (detail.exitStatus !== null)
    facts.push(`Exit status ${detail.exitStatus}`);
  if (detail.signal !== null) facts.push(`Signal ${detail.signal}`);
  if (detail.cause !== null) facts.push(CAUSES[detail.cause]);
  if (detail.operation !== null) facts.push(OPERATIONS[detail.operation]);
  if (detail.rebuildId !== null) facts.push(`Rebuild ${detail.rebuildId}`);
  if (detail.generationId !== null) facts.push(`Index ${detail.generationId}`);
  const first = detail.firstMessageId;
  const last = detail.lastMessageId;
  if (first !== null && last !== null) {
    facts.push(
      first === last ? `Message ${first}` : `Messages ${first}–${last}`,
    );
  }
  if (detail.freeBytes !== null) {
    facts.push(`${formatBinarySize(detail.freeBytes)} free`);
  }
  return facts;
}

import type { Observation } from "#lib/api.js";

/**
 * The words that name a message by one of its entries, as its row does: an
 * import's file name, or a collection's source name and folder. They are
 * untrusted text. Null is an import that supplied no file name.
 */
export function entryName(entry: Observation): string | null {
  if (entry.sourceType === "manual_import") return entry.fileName;
  return `${entry.source.name}, ${entry.folder}`;
}

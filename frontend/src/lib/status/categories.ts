import type { FailureCategory } from "#lib/api.js";

// FR-038's words for each failure category, word for word. A Record over the
// closed union, so a new category fails svelte-check until it has its words.

export interface CategoryWords {
  /** The state word on the status screen. */
  name: string;
  /** What happened. */
  meaning: string;
  /** What to do next. */
  nextStep: string;
}

export const CATEGORIES: Record<FailureCategory, CategoryWords> = {
  storage_full: {
    name: "Storage full",
    meaning: "The disk holding the archive ran out of space during an import.",
    nextStep: "Free disk space, then import the file again.",
  },
  storage_write_failed: {
    name: "Storage write failed",
    meaning:
      "The server could not write to its storage location, such as through denied permissions.",
    nextStep: "Fix the storage location, then import the file again.",
  },
  catalog_write_failed: {
    name: "Catalog write failed",
    meaning: "The original was stored but its catalog entry was not written.",
    nextStep:
      "Import the same file again. If this repeats, check the server log.",
  },
  stored_original_missing: {
    name: "Stored original missing",
    meaning: "A download or repeat import found no stored file.",
    nextStep:
      "Import the same file again, if you still have it, to restore the original.",
  },
  stored_original_damaged: {
    name: "Stored original damaged",
    meaning:
      "The stored file can't be read or does not match its content hash.",
    nextStep:
      "Import the same file again, if you still have it, to restore the original.",
  },
  temp_file_not_removed: {
    name: "Temporary file not removed",
    meaning: "Recovery could not delete a leftover temporary file.",
    nextStep:
      "Check the permissions on the storage location. The file is harmless, and recovery tries again at the next start.",
  },
};

/** The spec's definition of an unreferenced original, from its glossary. */
export const UNREFERENCED_MEANING =
  "A stored original with no catalog entry, usually left by a crash between storage and catalog writes.";

/** FR-038's next step for an unreferenced original, which isn't a failure. */
export const UNREFERENCED_NEXT_STEP =
  "Importing the same file again puts it back in the list, and nothing is deleted.";

/** Shown in place of the next step once an import replaced the original. */
export const RESTORED_NOTE =
  "The stored copy was restored from an imported file.";

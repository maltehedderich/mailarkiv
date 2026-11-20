import type { FailureCategory } from "#lib/api.js";

// A Record over the closed union makes a new category fail type checking
// until it has a name, explanation, and next step.

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
  message_too_large: {
    name: "Message too large",
    meaning:
      "A message is over 150,000,000 bytes, so it wasn't archived. MailArkiv doesn't retry it.",
    nextStep: "Keep the message in the mailbox, or save it elsewhere.",
  },
  message_removed: {
    name: "Removed before it was archived",
    meaning:
      "A message waiting for retry was removed from the mailbox. MailArkiv doesn't retry it.",
    nextStep: "None. The archive doesn't have this message.",
  },
  search_process_stopped: {
    name: "Search process stopped",
    meaning: "The search process exited or crashed.",
    nextStep: "None if it passes. If it repeats, check the server log.",
  },
  search_process_did_not_start: {
    name: "Search process didn't start",
    meaning:
      "The search process couldn't be started, such as when its program is missing.",
    nextStep: "Check the installation and the server log.",
  },
  search_process_did_not_answer: {
    name: "Search process didn't answer",
    meaning: "A call passed its time limit.",
    nextStep: "If it repeats, check the server log and the host's free memory.",
  },
  index_write_failed: {
    name: "Index write failed",
    meaning:
      "A batch couldn't be saved to the index, such as through a denied write.",
    nextStep: "Fix the index location, such as its permissions.",
  },
};

export const UNREFERENCED_MEANING =
  "A stored original with no catalog entry, usually left by a crash between storage and catalog writes.";

/** An unreferenced original is recoverable and isn't a failure. */
export const UNREFERENCED_NEXT_STEP =
  "Importing the same file again puts it back in the list, and nothing is deleted.";

/** Shown in place of the next step once an import replaced the original. */
export const RESTORED_NOTE =
  "The stored copy was restored from an imported file.";

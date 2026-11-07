import type { DownloadFailure } from "#lib/download/original.js";
import type {
  FailedReason,
  ImportResult,
  RefusedReason,
} from "#lib/import/classify.js";
import { CATEGORIES } from "#lib/status/categories.js";

import type { Toast } from "./store.svelte";

// What each toast says, from the spec's words. DESIGN.md's Toast section has
// the layout.

/** DESIGN.md's dot colour for each state. */
export type Tone = "ok" | "neutral" | "warn" | "bad";

export interface ToastWords {
  tone: Tone;
  /** The state word, such as "Archived". */
  word: string;
  /** The file name, shown as untrusted text. Null is "(no file name)". */
  name: string | null;
  /** The stored size, after the name. */
  size?: number;
  /** What happened, in `--text-2`. */
  what?: string;
  /** What to do next, in `--text-3`. */
  next?: string;
  /** The message whose history the toast links to. */
  historyId?: number;
  /** A running download's progress, from 0 to 1. */
  fraction?: number;
}

type Outcome = ImportResult["outcome"];

// FR-032's words, and DESIGN.md's dot for each outcome.
const OUTCOMES: Record<Outcome, { word: string; tone: Tone }> = {
  archived: { word: "Archived", tone: "ok" },
  already_archived: { word: "Already archived", tone: "neutral" },
  restored: { word: "Restored", tone: "warn" },
  refused: { word: "Refused", tone: "bad" },
  failed: { word: "Failed", tone: "bad" },
  unconfirmed: { word: "Unconfirmed", tone: "warn" },
};

const REFUSED: Record<RefusedReason, string> = {
  empty: "This file is empty.",
  too_large: "This file is over the 150 MB limit.",
  no_mail_header: "This file doesn't start with a mail header line.",
};

// FR-038's meaning and next step for a capture failure. FR-033 names the
// proxy's limit, and a marked 403 is MailArkiv refusing the request.
const FAILED: Record<FailedReason, { meaning: string; nextStep: string }> = {
  storage_full: CATEGORIES.storage_full,
  storage_write_failed: CATEGORIES.storage_write_failed,
  catalog_write_failed: CATEGORIES.catalog_write_failed,
  proxy_limit: {
    meaning: "The reverse proxy's upload limit refused it.",
    nextStep:
      "Ask the operator to let the reverse proxy accept uploads up to 150 MB.",
  },
  refused_by_server: {
    meaning: "MailArkiv refused the request.",
    nextStep: "Try again. If this repeats, check the server log.",
  },
};

const UNCONFIRMED =
  "The file may be archived, and importing it again is safe and stores no second copy.";

/** FR-023's words. */
export const NOT_DOWNLOADED = "This message wasn't downloaded.";

const TRY_AGAIN = "Try again. If this repeats, check the server log.";

// FR-038's two stored-original rows, and what a broken transfer says.
const DOWNLOAD_FAILED: Record<
  DownloadFailure,
  { meaning?: string; nextStep: string }
> = {
  stored_original_missing: CATEGORIES.stored_original_missing,
  stored_original_damaged: CATEGORIES.stored_original_damaged,
  interrupted: {
    meaning: "The download stopped before the whole file arrived.",
    nextStep: TRY_AGAIN,
  },
  unavailable: { nextStep: TRY_AGAIN },
};

export function toastWords(toast: Toast): ToastWords {
  switch (toast.kind) {
    case "import":
      return importWords(toast.fileName, toast.result);
    case "downloading":
      return {
        tone: "neutral",
        word: "Downloading",
        name: toast.fileName,
        fraction: toast.fraction,
      };
    case "download_waiting":
      return {
        tone: "neutral",
        word: "Download waiting",
        name: toast.fileName,
        what: "Waiting for the previous download to finish.",
      };
    case "download_busy":
      return {
        tone: "neutral",
        word: "Download not started",
        name: toast.fileName,
        what: "Another download is already waiting.",
        next: "Wait for it to finish, or cancel it and try again.",
      };
    case "download_canceled":
      return {
        tone: "neutral",
        word: "Download canceled",
        name: toast.fileName,
        what: "Nothing was saved.",
      };
    case "download_failed": {
      const { meaning, nextStep } = DOWNLOAD_FAILED[toast.reason];
      return {
        tone: "bad",
        word: "Download failed",
        name: toast.fileName,
        what:
          meaning === undefined
            ? NOT_DOWNLOADED
            : `${NOT_DOWNLOADED} ${meaning}`,
        next: nextStep,
      };
    }
  }
}

function importWords(fileName: string, result: ImportResult): ToastWords {
  const { word, tone } = OUTCOMES[result.outcome];
  switch (result.outcome) {
    case "archived":
      return {
        tone,
        word,
        name: result.message.fileName,
        size: result.message.size,
        historyId: result.message.id,
      };
    case "already_archived":
      return {
        tone,
        word,
        name: result.message.fileName,
        what: "This import was added to the history, and no second copy was stored.",
        historyId: result.message.id,
      };
    case "restored":
      return {
        tone,
        word,
        name: result.message.fileName,
        what: "The stored copy was restored from this file.",
        historyId: result.message.id,
      };
    case "refused":
      return {
        tone,
        word,
        name: fileName,
        what: `${REFUSED[result.reason]} Nothing was stored.`,
      };
    case "failed": {
      const { meaning, nextStep } = FAILED[result.reason];
      return {
        tone,
        word,
        name: fileName,
        what: `The file is not archived. ${meaning}`,
        next: nextStep,
      };
    }
    case "unconfirmed":
      return { tone, word, name: fileName, what: UNCONFIRMED };
  }
}

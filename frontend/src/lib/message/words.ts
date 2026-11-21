import type {
  AttachmentKind,
  ContentFailure,
  DateReason,
  MessageProblem,
  Processing,
  ProblemKind,
} from "#lib/api.js";
import { CATEGORIES } from "#lib/status/categories.js";

// The message view's words. Problem names copy the data model's table.

export const PROBLEM_NAMES: Record<ProblemKind, string> = {
  unknown_charset: "Unknown character set",
  invalid_encoding: "Invalid encoding",
  malformed_header: "Malformed header",
  malformed_structure: "Malformed structure",
  cut_short: "Message cut short",
  too_many_parts: "Too many parts",
  nested_too_deeply: "Nested too deeply",
  body_cut: "Body cut at the size limit",
  took_too_long: "Processing took too long",
};

/** Where a problem is: "Part 2", or "Whole message". */
export function problemPart(problem: MessageProblem): string {
  return problem.position === null
    ? "Whole message"
    : `Part ${problem.position}`;
}

export const STATES: Record<
  Processing,
  { word: string; tone: "ok" | "neutral" | "warn" }
> = {
  processed: { word: "Processed", tone: "ok" },
  processed_with_problems: { word: "Processed with problems", tone: "warn" },
  not_processed: { word: "Not processed yet", tone: "neutral" },
  unreadable: { word: "Couldn't be read", tone: "warn" },
};

/** Why the date shown is the first archive time. */
export function archiveTimeReason(
  processing: Processing,
  reason: DateReason | null,
): string {
  if (processing === "not_processed") {
    return "Processing hasn't read the message yet, so this is when MailArkiv first archived it.";
  }
  if (processing === "unreadable") {
    return "Processing couldn't read the message, so this is when MailArkiv first archived it.";
  }
  switch (reason) {
    case "missing":
      return "The message has no Date header, so this is when MailArkiv first archived it.";
    case "unreadable":
      return "MailArkiv couldn't read the Date header, so this is when it first archived the message.";
    case "ahead":
    case null:
      return "The Date header is more than 24 hours after MailArkiv first archived the message, so this is the archive time.";
  }
}

export const KIND_NOTES: Record<AttachmentKind, string | null> = {
  file: null,
  message: "Attached message",
  embedded_image: "Embedded image",
};

export const NOT_READ =
  "Processing hasn't read this message, so its body and attachments can't be shown. The original downloads unchanged.";

const TRY_AGAIN = "Try again. If this repeats, check the server log.";

export const CONTENT_FAILED: Record<
  ContentFailure | "unavailable",
  { what: string; next: string }
> = {
  not_found: {
    what: "This message isn't in the archive.",
    next: "Open it again from the message list.",
  },
  stored_original_missing: {
    what: "This message can't be shown, because its stored original is missing.",
    next: CATEGORIES.stored_original_missing.nextStep,
  },
  stored_original_damaged: {
    what: "This message can't be shown, because its stored original can't be read or doesn't match its content hash.",
    next: CATEGORIES.stored_original_damaged.nextStep,
  },
  busy: {
    what: "Both workers that read messages for the view are busy.",
    next: "Try again in a moment.",
  },
  could_not_read: {
    what: "Reading this message failed or took longer than 60 seconds.",
    next: TRY_AGAIN,
  },
  unavailable: {
    what: "This page couldn't be loaded.",
    next: TRY_AGAIN,
  },
};

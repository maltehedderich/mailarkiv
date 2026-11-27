import type { Message, Observation } from "#lib/api.js";

import { entryName } from "./entry.js";
import { displayName } from "./file-name.js";
import { senderDisplay, type SenderDisplay } from "./sender.js";
import { displayParts, type DisplayText } from "./untrusted.js";

export type SubjectDisplay =
  { kind: "subject"; subject: DisplayText } | { kind: "missing"; text: string };

export type RowDisplay =
  | {
      /** Processing read the message, so the row shows what it found. */
      kind: "summary";
      sender: SenderDisplay;
      subject: SubjectDisplay;
      /** What the row's link and download are named by, codes in place. */
      label: string;
      /** The problem mark's words, or null for a message read whole. */
      problems: string | null;
    }
  | {
      /** The message names itself by its latest entry, beside its state. */
      kind: "capture";
      entry: Observation;
      label: string;
      state: string;
      /** The state dot: neutral while waiting, warn when it couldn't be read. */
      tone: "neutral" | "warn";
    };

/** What a message's row shows, by its processing state. */
export function rowDisplay(message: Message): RowDisplay {
  switch (message.processing) {
    case "processed":
    case "processed_with_problems": {
      const subject: SubjectDisplay =
        message.subject === null
          ? { kind: "missing", text: "(no subject)" }
          : { kind: "subject", subject: displayParts(message.subject) };
      return {
        kind: "summary",
        sender: senderDisplay(message.sender),
        subject,
        label: subject.kind === "missing" ? subject.text : subject.subject.text,
        problems:
          message.processing === "processed_with_problems"
            ? "Processed with problems"
            : null,
      };
    }
    case "not_processed":
    case "unreadable":
      return {
        kind: "capture",
        entry: message.latest,
        label: displayName(entryName(message.latest)).text,
        state:
          message.processing === "not_processed"
            ? "Not processed yet"
            : "Couldn't be read",
        tone: message.processing === "not_processed" ? "neutral" : "warn",
      };
  }
}

import type {
  ProblemCounts,
  Security,
  SourceProblemCategory,
  SourceState,
} from "#lib/api.js";

/** A state dot's colour role. Neutral uses `--text-4`. */
export type Tone = "ok" | "warn" | "bad" | "neutral";

const STATE_TONES: Record<SourceState, Tone> = {
  waiting: "ok",
  syncing: "neutral",
  retrying: "warn",
  needs_attention: "bad",
  paused: "neutral",
};

export function stateTone(state: SourceState): Tone {
  return STATE_TONES[state];
}

/**
 * A failed sync collected nothing, so it's bad. An unavailable folder or a
 * message waiting for retry leaves the rest of the source collecting.
 */
export function problemTone(category: SourceProblemCategory): Tone {
  return category === "folder_unavailable" || category === "message_unavailable"
    ? "warn"
    : "bad";
}

export const SECURITY_WORDS: Record<Security, string> = {
  tls: "TLS",
  starttls: "STARTTLS",
};

const numbers = new Intl.NumberFormat("en-US");

/** Names each count above zero, or returns null when there are none. */
export function problemSummary(problems: ProblemCounts): string | null {
  const parts = [
    counted(problems.unavailableFolders, "folder", "folders", "unavailable"),
    counted(
      problems.waitingForRetry,
      "message",
      "messages",
      "waiting for retry",
    ),
    counted(problems.notArchived, "message", "messages", "not archived"),
  ].filter((part) => part !== null);
  return parts.length === 0 ? null : parts.join(", ");
}

function counted(
  count: number,
  one: string,
  many: string,
  what: string,
): string | null {
  if (count === 0) return null;
  return `${numbers.format(count)} ${count === 1 ? one : many} ${what}`;
}

/**
 * How long a page waits between reloads while a source on it syncs. Archive
 * keeps each source's collected count as a counter, so a reload is cheap.
 */
export const REFRESH_MS = 3000;

/** The dependency a sources page's load declares, so a reload reruns only it. */
export const SOURCES_DATA = "app:sources";

/** The operator guide's section on the key, which the no-key notice links. */
export const KEY_GUIDE_URL =
  "https://github.com/maltehedderich/mailarkiv/blob/main/docs/OPERATIONS.md#the-key";

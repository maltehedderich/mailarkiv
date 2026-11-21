import type { IndexingState, SearchState, SearchStatus } from "#lib/api.js";
import { formatBinarySize } from "#lib/format/size.js";

/** The dot's colour: fine, work in progress, a warning, or a problem. */
export type Tone = "ok" | "neutral" | "warn" | "bad";

export interface StateWords {
  word: string;
  tone: Tone;
}

export const SEARCH_WORDS: Record<SearchState, StateWords> = {
  available: { word: "Available", tone: "ok" },
  unavailable: { word: "Unavailable", tone: "bad" },
  needs_rebuild: { word: "Needs rebuild", tone: "bad" },
};

export const INDEXING_WORDS: Record<IndexingState, StateWords> = {
  up_to_date: { word: "Up to date", tone: "ok" },
  catching_up: { word: "Catching up", tone: "neutral" },
  building: { word: "Building", tone: "neutral" },
  rebuilding: { word: "Rebuilding", tone: "neutral" },
  paused: { word: "Paused", tone: "warn" },
  stopped: { word: "Stopped", tone: "bad" },
};

/**
 * Whether the server would start a rebuild: not while one runs or the first
 * index builds, and not while no search process runs. A paused first build
 * can't be told apart here, so the server's refusal covers it.
 */
export function canRebuild(search: SearchStatus): boolean {
  if (search.rebuild !== null) return false;
  switch (search.state) {
    case "needs_rebuild":
      return true;
    case "unavailable":
      return false;
    case "available":
      return search.indexing.state !== "building";
  }
}

const numbers = new Intl.NumberFormat("en-US");

/** How far a first build or a rebuild is, or null while none runs. */
export function progressNote(search: SearchStatus): string | null {
  const { done, toDo } = search.indexing;
  if (done === null || toDo === null) return null;
  return `${numbers.format(done)} of ${numbers.format(toDo)} ${toDo === 1 ? "message" : "messages"}`;
}

/** Why indexing paused, and when it goes on. */
export function pausedNote(search: SearchStatus): string {
  const { freeBytes, pauseBelowBytes, resumeAtBytes } = search.indexing;
  const below = formatBinarySize(pauseBelowBytes);
  const free =
    freeBytes === null
      ? `Free space is below ${below}.`
      : `${formatBinarySize(freeBytes)} free, below ${below}.`;
  return `${free} Indexing resumes at ${formatBinarySize(resumeAtBytes)}.`;
}

import type { DownloadFailure } from "#lib/download/original.js";
import type { ImportResult } from "#lib/import/classify.js";

// Results stay until dismissed or replaced by a newer toast with the same
// key, so they can't disappear before the administrator reads them.

export type Toast =
  /** The latest import's result, with the name of the file sent. */
  | { kind: "import"; fileName: string; result: ImportResult }
  /** A download that has run long enough to show. */
  | {
      kind: "downloading";
      messageId: number;
      fileName: string | null;
      /** The share received so far, from 0 to 1. */
      fraction: number;
      cancel: () => void;
    }
  | {
      kind: "download_waiting";
      messageId: number;
      fileName: string | null;
      cancel: () => void;
    }
  | {
      kind: "download_busy" | "download_canceled";
      messageId: number;
      fileName: string | null;
    }
  /** A download that saved nothing. */
  | {
      kind: "download_failed";
      messageId: number;
      fileName: string | null;
      reason: DownloadFailure;
    };

/**
 * The history drawer is a modal dialog, which makes the rest of the page inert.
 * A toast raised while it's open shows inside it, and moves to the page when it
 * closes.
 */
export type Place = "page" | "drawer";

export interface Shown {
  key: string;
  place: Place;
  toast: Toast;
}

/** There is one import result at a time. */
export const IMPORT_KEY = "import";

/** One toast per message, so trying again replaces the last failure. */
export function downloadKey(messageId: number): string {
  return `download-${messageId}`;
}

let shown = $state<Shown[]>([]);
let drawerOpen = false;

function keyOf(toast: Toast): string {
  return toast.kind === "import" ? IMPORT_KEY : downloadKey(toast.messageId);
}

/** Shows a toast, in place of any with the same key. */
export function showToast(toast: Toast): void {
  const key = keyOf(toast);
  const entry: Shown = { key, place: drawerOpen ? "drawer" : "page", toast };
  const index = shown.findIndex((item) => item.key === key);
  if (index === -1) shown.push(entry);
  else shown[index] = entry;
}

export function dismissToast(key: string): void {
  const index = shown.findIndex((item) => item.key === key);
  if (index !== -1) shown.splice(index, 1);
}

/** The toasts for one place, oldest first. */
export function toastsIn(place: Place): Shown[] {
  return shown.filter((item) => item.place === place);
}

/** The drawer calls this as it opens and closes. */
export function setDrawerOpen(open: boolean): void {
  drawerOpen = open;
  if (open) return;
  for (const item of shown) item.place = "page";
}

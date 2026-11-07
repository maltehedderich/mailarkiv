import { field, isMarked, type Fetch } from "#lib/api.js";

// FR-022 and FR-023: the page fetches the stored original itself, so it learns
// why a download failed and can say so in a toast. A file is saved only after
// every byte has arrived.

/**
 * Gives up after this long with no answer, or this long between two pieces of
 * the file. The server can take a while to hash a large file before it answers.
 */
export const STALL_MS = 120_000;

/** Why the file wasn't saved. */
export type DownloadFailure =
  /** FR-038's two stored-original rows, as the server names them. */
  | "stored_original_missing"
  | "stored_original_damaged"
  /** The transfer broke after it started. */
  | "interrupted"
  /** No answer, or one this client doesn't recognize. */
  | "unavailable";

export type Fetched =
  | { kind: "ok"; file: Blob }
  | { kind: "session_ended" }
  | { kind: "canceled" }
  | { kind: "failed"; reason: DownloadFailure };

/** Reads an answer that isn't a marked 200, from the contract's table. */
export function classifyRefusal(
  status: number,
  marked: boolean,
  body: unknown,
): Exclude<Fetched, { kind: "ok" }> {
  if (!marked) return { kind: "failed", reason: "unavailable" };
  if (status === 401) return { kind: "session_ended" };
  const code = field(body, "error");
  // An unknown ID has no stored file either, as far as the reader can tell.
  if (
    status === 404 &&
    (code === "not_found" || code === "stored_original_missing")
  ) {
    return { kind: "failed", reason: "stored_original_missing" };
  }
  if (status === 500 && code === "stored_original_damaged") {
    return { kind: "failed", reason: "stored_original_damaged" };
  }
  return { kind: "failed", reason: "unavailable" };
}

/**
 * Fetches one message's stored original. `onProgress` gets the share that has
 * arrived, from 0 to 1, when the server sent a length.
 */
export async function fetchOriginal(
  id: number,
  onProgress: (fraction: number) => void,
  fetch: Fetch = globalThis.fetch,
  signal?: AbortSignal,
): Promise<Fetched> {
  const controller = new AbortController();
  const cancel = () => controller.abort();
  signal?.addEventListener("abort", cancel, { once: true });
  if (signal?.aborted) controller.abort();
  let timer = setTimeout(() => controller.abort(), STALL_MS);
  const wait = () => {
    clearTimeout(timer);
    timer = setTimeout(() => controller.abort(), STALL_MS);
  };

  try {
    if (signal?.aborted) return { kind: "canceled" };
    let response: Response;
    try {
      response = await fetch(`/api/messages/${id}/original`, {
        signal: controller.signal,
      });
    } catch {
      if (signal?.aborted) return { kind: "canceled" };
      return { kind: "failed", reason: "unavailable" };
    }

    if (!isMarked(response) || response.status !== 200) {
      let body: unknown = null;
      try {
        body = await response.json();
      } catch {
        // Not JSON, so the status and the marker decide.
      }
      if (signal?.aborted) return { kind: "canceled" };
      return classifyRefusal(response.status, isMarked(response), body);
    }

    const length = Number(response.headers.get("Content-Length"));
    const total = Number.isSafeInteger(length) && length > 0 ? length : null;
    const reader = response.body?.getReader();
    if (reader === undefined) return { kind: "failed", reason: "unavailable" };

    const stopReading = () => {
      void reader.cancel().catch(() => {});
    };
    controller.signal.addEventListener("abort", stopReading, { once: true });
    if (controller.signal.aborted) stopReading();
    const pieces: Uint8Array<ArrayBuffer>[] = [];
    let received = 0;
    try {
      for (;;) {
        wait();
        const { done, value } = await reader.read();
        if (signal?.aborted) return { kind: "canceled" };
        if (controller.signal.aborted)
          return { kind: "failed", reason: "interrupted" };
        if (done) break;
        pieces.push(value);
        received += value.byteLength;
        if (total !== null) onProgress(Math.min(received / total, 1));
      }
    } catch {
      // The server ended the connection, the network dropped, or it stalled.
      if (signal?.aborted) return { kind: "canceled" };
      return { kind: "failed", reason: "interrupted" };
    } finally {
      // Canceling a reader drops any queued chunks. An errored stream can
      // reject cancellation; the failed result above already reports it.
      await reader.cancel().catch(() => {});
      reader.releaseLock();
      controller.signal.removeEventListener("abort", stopReading);
    }
    // Browsers already fail a body shorter than its length. This also catches
    // one that is longer.
    if (total !== null && received !== total) {
      return { kind: "failed", reason: "interrupted" };
    }
    return {
      kind: "ok",
      file: new Blob(pieces, { type: "application/octet-stream" }),
    };
  } finally {
    clearTimeout(timer);
    signal?.removeEventListener("abort", cancel);
  }
}

let savedURL: string | null = null;
let savedTimer: ReturnType<typeof setTimeout> | undefined;

/** Hands a file to the browser to save under `name`, never to display. */
export function saveFile(file: Blob, name: string): void {
  // Keep at most one completed payload alive alongside the active transfer.
  if (savedURL !== null) URL.revokeObjectURL(savedURL);
  clearTimeout(savedTimer);
  const url = URL.createObjectURL(file);
  savedURL = url;
  const link = document.createElement("a");
  link.href = url;
  link.download = name;
  link.click();
  // Some browsers read the file only after the click returns. FileSaver.js
  // waits 40 seconds before it lets the file go, for the same reason.
  savedTimer = setTimeout(() => {
    URL.revokeObjectURL(url);
    savedURL = null;
  }, 40_000);
}

import { goto } from "$app/navigation";
import { page } from "$app/state";

import { signInPath } from "#lib/session.js";
import {
  dismissToast,
  downloadKey,
  showToast,
} from "#lib/toasts/store.svelte.js";

import { fetchOriginal, saveFile } from "./original";

/** A download that ends sooner than this shows no progress, so it can't flash. */
const QUIET_MS = 400;

export interface Downloadable {
  id: number;
  contentHash: string;
  /** The name shown in the toast, as the row names the message. */
  fileName: string | null;
}

let active: number | null = null;
let waiting: { id: number; start: () => void } | null = null;

/** One buffered transfer and one waiting request per tab; repeats do nothing. */
export async function startDownload({
  id,
  contentHash,
  fileName,
}: Downloadable): Promise<void> {
  if (active === id || waiting?.id === id) return;
  if (active !== null && waiting !== null) {
    showToast({ kind: "download_busy", messageId: id, fileName });
    return;
  }

  const wasWaiting = active !== null;
  const controller = new AbortController();
  const cancel = () => controller.abort();
  if (active !== null) {
    const admitted = await new Promise<boolean>((resolve) => {
      const cancelWaiting = () => {
        waiting = null;
        resolve(false);
      };
      controller.signal.addEventListener("abort", cancelWaiting, {
        once: true,
      });
      waiting = {
        id,
        start: () => {
          controller.signal.removeEventListener("abort", cancelWaiting);
          resolve(true);
        },
      };
      showToast({ kind: "download_waiting", messageId: id, fileName, cancel });
    });
    if (!admitted) {
      showToast({ kind: "download_canceled", messageId: id, fileName });
      return;
    }
  }
  active = id;

  let percent = 0;
  let showing = wasWaiting;
  const progress = () =>
    showToast({
      kind: "downloading",
      messageId: id,
      fileName,
      fraction: percent / 100,
      cancel,
    });
  if (showing) progress();
  const timer = setTimeout(() => {
    showing = true;
    progress();
  }, QUIET_MS);

  try {
    const result = await fetchOriginal(
      id,
      (fraction) => {
        const next = Math.floor(fraction * 100);
        if (next === percent) return;
        percent = next;
        if (showing) progress();
      },
      globalThis.fetch,
      controller.signal,
    );
    clearTimeout(timer);

    switch (result.kind) {
      case "ok":
        dismissToast(downloadKey(id));
        saveFile(result.file, `${contentHash}.eml`);
        return;
      case "canceled":
        showToast({ kind: "download_canceled", messageId: id, fileName });
        return;
      case "session_ended":
        dismissToast(downloadKey(id));
        await goto(signInPath(page.url));
        return;
      case "failed":
        showToast({
          kind: "download_failed",
          messageId: id,
          fileName,
          reason: result.reason,
        });
    }
  } finally {
    clearTimeout(timer);
    // Reserve the slot before waking the waiter so a new click cannot pass it.
    active = waiting?.id ?? null;
    const next = waiting;
    waiting = null;
    next?.start();
  }
}

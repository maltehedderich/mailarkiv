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
  /** The name shown in the toast. Null when the import supplied none. */
  fileName: string | null;
}

const running = new Set<number>();

/**
 * Downloads one message and saves it as `<content hash>.eml` (FR-022). A
 * failure shows a toast that stays until it's dismissed (FR-023). A second
 * press while the same message downloads does nothing.
 */
export async function startDownload({
  id,
  contentHash,
  fileName,
}: Downloadable): Promise<void> {
  if (running.has(id)) return;
  running.add(id);

  let percent = 0;
  let showing = false;
  const progress = () =>
    showToast({
      kind: "downloading",
      messageId: id,
      fileName,
      fraction: percent / 100,
    });
  const timer = setTimeout(() => {
    showing = true;
    progress();
  }, QUIET_MS);

  const result = await fetchOriginal(id, (fraction) => {
    // Whole percents, so a large file doesn't redraw the toast for every piece.
    const next = Math.floor(fraction * 100);
    if (next === percent) return;
    percent = next;
    if (showing) progress();
  });
  clearTimeout(timer);
  running.delete(id);

  switch (result.kind) {
    case "ok":
      dismissToast(downloadKey(id));
      saveFile(result.file, `${contentHash}.eml`);
      return;
    case "session_ended":
      // FR-040: the action opens sign-in, which returns to this page.
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
}

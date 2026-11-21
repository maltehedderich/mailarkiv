import type { Fetch } from "#lib/api.js";

import { fetchFile, type Fetched } from "./save";

export {
  classifyRefusal,
  saveFile,
  STALL_MS,
  type DownloadFailure,
  type Fetched,
} from "./save";

/**
 * Fetches one message's stored original. `onProgress` gets the share that has
 * arrived, from 0 to 1, when the server sent a length.
 */
export function fetchOriginal(
  id: number,
  onProgress: (fraction: number) => void,
  fetch: Fetch = globalThis.fetch,
  signal?: AbortSignal,
): Promise<Fetched> {
  return fetchFile(`/api/messages/${id}/original`, onProgress, fetch, signal);
}

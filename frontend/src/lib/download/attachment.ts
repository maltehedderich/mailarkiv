import {
  attachmentAddress,
  type AttachmentKind,
  type Fetch,
} from "#lib/api.js";

import { fetchFile, type Fetched } from "./save";

/** The most bytes of a saved attachment's name. */
const MAX_NAME_BYTES = 200;

// C0 and C1 controls with delete, the bidirectional controls that can make a
// name read as another, and path separators.
const REMOVED = /[\u0000-\u001f\u007f-\u009f؜‎‏‪-‮⁦-⁩/\\]/gu;

/**
 * The name an attachment saves under, by the data model's steps. The server
 * names the file the same way when its address is opened directly.
 */
export function cleanFileName(
  name: string | null,
  position: string,
  kind: AttachmentKind,
): string {
  let cleaned = (name ?? "").replace(REMOVED, "").replace(/^[ .]+|[ .]+$/g, "");
  if (cleaned === "") cleaned = `attachment-${position}`;
  if (kind === "message" && !cleaned.toLowerCase().endsWith(".eml")) {
    cleaned += ".eml";
  }
  return shortenKeepingExtension(cleaned, MAX_NAME_BYTES);
}

const encoder = new TextEncoder();

function byteLength(text: string): number {
  return encoder.encode(text).byteLength;
}

/**
 * Cuts `name` to `limit` UTF-8 bytes on a character boundary. The extension,
 * from the last dot, stays when it leaves room for a byte before it.
 */
function shortenKeepingExtension(name: string, limit: number): string {
  if (byteLength(name) <= limit) return name;
  let stem = name;
  let extension = "";
  const dot = name.lastIndexOf(".");
  if (dot > 0 && byteLength(name.slice(dot)) < limit) {
    stem = name.slice(0, dot);
    extension = name.slice(dot);
  }
  let room = limit - byteLength(extension);
  let kept = "";
  for (const character of stem) {
    room -= byteLength(character);
    if (room < 0) break;
    kept += character;
  }
  return kept + extension;
}

/** Fetches one attachment with the original download's reader and failures. */
export function fetchAttachment(
  messageId: number,
  position: string,
  onProgress: (fraction: number) => void,
  fetch: Fetch = globalThis.fetch,
  signal?: AbortSignal,
): Promise<Fetched> {
  return fetchFile(
    attachmentAddress(messageId, position),
    onProgress,
    fetch,
    signal,
  );
}

// What the browser does with the files chosen or dropped, before any byte is
// sent. The server still checks the file.

/** The server's limit, which a file may reach but not pass. */
export const MAX_IMPORT_BYTES = 150_000_000;

export type BrowserRefusal = "several" | "empty" | "too_large";

export type Choice =
  | { kind: "chosen"; file: File }
  | { kind: "refused"; reason: BrowserRefusal }
  /** A closed file dialog, or a drop that held no files. */
  | { kind: "none" };

export function choose(files: readonly File[]): Choice {
  if (files.length === 0) return { kind: "none" };
  // Never pick one of several: the administrator decides which.
  if (files.length > 1) return { kind: "refused", reason: "several" };
  const [file] = files;
  if (file.size === 0) return { kind: "refused", reason: "empty" };
  if (file.size > MAX_IMPORT_BYTES) {
    return { kind: "refused", reason: "too_large" };
  }
  return { kind: "chosen", file };
}

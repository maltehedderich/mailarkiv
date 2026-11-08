import type { BrowserRefusal } from "./choose";

/**
 * Where the import box is, from DESIGN.md's step table. The page owns it, so
 * the box only shows it. Uploading and storing are the busy steps.
 */
export type ImportStep =
  | { kind: "ready" }
  /** Chosen, and nothing sent until Import is pressed. */
  | { kind: "chosen"; file: File }
  /** The share of the file sent so far, from 0 to 1. */
  | { kind: "uploading"; file: File; fraction: number }
  /** Every byte has left, and the server is storing the file. */
  | { kind: "storing"; file: File }
  /** Refused before any byte was sent. */
  | { kind: "refused"; reason: BrowserRefusal };

export function isBusy(step: ImportStep): boolean {
  return step.kind === "uploading" || step.kind === "storing";
}

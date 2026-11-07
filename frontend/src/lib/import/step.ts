import type { BrowserRefusal } from "./choose";

/**
 * Where the import box is, from DESIGN.md's step table. The page owns it, so
 * the box only shows it. Uploading and storing are the busy steps (FR-035).
 */
export type ImportStep =
  | { kind: "ready" }
  /** FR-034: chosen, and nothing sent until Import is pressed. */
  | { kind: "chosen"; file: File }
  /** The share of the file sent so far, from 0 to 1. */
  | { kind: "uploading"; file: File; fraction: number }
  /** Every byte has left, and the server is storing the file. */
  | { kind: "storing"; file: File }
  /** FR-036: refused before any byte was sent. */
  | { kind: "refused"; reason: BrowserRefusal };

export function isBusy(step: ImportStep): boolean {
  return step.kind === "uploading" || step.kind === "storing";
}

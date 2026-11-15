// A supplied file name follows the rules for untrusted text, and a missing
// one gets its own words.

import { displayText, type DisplayText, type TextPart } from "./untrusted";

export type NamePart = TextPart;

export type DisplayName =
  | { kind: "missing"; text: "(no file name)" }
  | ({ kind: "name" } & DisplayText);

export function displayName(name: string | null): DisplayName {
  // An empty name, which only a direct request can store, has nothing to show.
  if (name === null || name === "") {
    return { kind: "missing", text: "(no file name)" };
  }
  return { kind: "name", ...displayText(name) };
}

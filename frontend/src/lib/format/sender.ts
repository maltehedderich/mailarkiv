import type { Sender } from "#lib/api.js";

import { displayParts, type DisplayText } from "./untrusted.js";

export type SenderDisplay =
  | {
      kind: "sender";
      /** Cut at its end. Null when the address shows alone. */
      name: DisplayText | null;
      /** Cut before its @, so the domain stays in view. */
      address: DisplayText | null;
    }
  | { kind: "missing"; text: string };

/**
 * A row's sender. A name can hold "@" and "<", so it never shares a part with
 * the address, and only the parts' styles show where the name ends.
 */
export function senderDisplay(sender: Sender | null): SenderDisplay {
  if (sender === null || (sender.name === null && sender.address === null)) {
    return { kind: "missing", text: "(no sender)" };
  }
  const { name, address } = sender;
  const sameAsAddress =
    name !== null &&
    address !== null &&
    name.toLowerCase() === address.toLowerCase();
  return {
    kind: "sender",
    name: name === null || sameAsAddress ? null : displayParts(name),
    address: address === null ? null : splitAddress(address),
  };
}

// The last @, because a quoted local part such as "ceo@example.com" can hold
// one, and a domain can't.
function splitAddress(address: string): DisplayText {
  const at = address.lastIndexOf("@");
  if (at === -1) return displayParts(address);
  return displayParts(address.slice(0, at), address.slice(at));
}

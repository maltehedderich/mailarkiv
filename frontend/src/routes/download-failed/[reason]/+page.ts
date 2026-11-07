import { error } from "@sveltejs/kit";

import type { EntryGenerator, PageLoad } from "./$types";

// The server sends these two files in place of a failed download, from whatever
// path the download was at. They hold no script, so they have nothing to fail.
export const prerender = true;
export const ssr = true;
export const csr = false;

export const entries: EntryGenerator = () => [
  { reason: "missing" },
  { reason: "damaged" },
];

// FR-038's two stored-original rows, word for word.
const REASONS = new Map([
  ["missing", "A download or repeat import found no stored file."],
  [
    "damaged",
    "The stored file can't be read or does not match its content hash.",
  ],
]);
const NEXT_STEP =
  "Import the same file again, if you still have it, to restore the original.";

export const load: PageLoad = ({ params }) => {
  const reason = REASONS.get(params.reason);
  if (reason === undefined) error(404);
  return { reason, nextStep: NEXT_STEP };
};

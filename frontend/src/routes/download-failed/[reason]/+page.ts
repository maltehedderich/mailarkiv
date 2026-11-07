import { error } from "@sveltejs/kit";

import { CATEGORIES } from "#lib/status/categories.js";

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

// FR-038's two stored-original rows.
const REASONS = new Map([
  ["missing", CATEGORIES.stored_original_missing],
  ["damaged", CATEGORIES.stored_original_damaged],
]);

export const load: PageLoad = ({ params }) => {
  const category = REASONS.get(params.reason);
  if (category === undefined) error(404);
  return { reason: category.meaning, nextStep: category.nextStep };
};

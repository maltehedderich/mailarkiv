import { redirect } from "@sveltejs/kit";

import { readSource } from "#lib/api.js";
import { signInPath } from "#lib/session.js";
import { SOURCES_DATA } from "#lib/sources/describe.js";

import type { PageLoad } from "./$types";

export const load: PageLoad = async ({ depends, fetch, params, url }) => {
  depends(SOURCES_DATA);
  const source = await readSource(params.id, fetch);
  if (source.kind === "session_ended") redirect(307, signInPath(url));
  return { source };
};

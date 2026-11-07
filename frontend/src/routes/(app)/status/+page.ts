import { redirect } from "@sveltejs/kit";

import { getStatus } from "#lib/api.js";
import { signInPath } from "#lib/session.js";

import type { PageLoad } from "./$types";

export const load: PageLoad = async ({ fetch, url }) => {
  const status = await getStatus(fetch);
  if (status.kind === "session_ended") redirect(307, signInPath(url));
  return { status };
};

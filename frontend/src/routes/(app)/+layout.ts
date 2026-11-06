import { redirect } from "@sveltejs/kit";

import { getSession } from "#lib/api.js";

import type { LayoutLoad } from "./$types";

// Every signed-in page reads the session at load (FR-039). Only a marked 401
// opens sign-in. Anything else that isn't success shows the failed-load state.
export const load: LayoutLoad = async ({ fetch }) => {
  const session = await getSession(fetch);
  if (session.kind === "session_ended") redirect(307, "/sign-in");
  return { session };
};

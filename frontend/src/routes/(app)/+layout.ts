import { redirect } from "@sveltejs/kit";

import { getSession } from "#lib/api.js";
import { markSignedIn, signInPath } from "#lib/session.js";

import type { LayoutLoad } from "./$types";

// Every signed-in page reads the session at load (FR-039). Only a marked 401
// opens sign-in. For anything else that isn't success, the page shows the
// failed-load state, and no count: a failed read must not look like zero.
export const load: LayoutLoad = async ({ fetch, url }) => {
  const session = await getSession(fetch);
  if (session.kind === "session_ended") redirect(307, signInPath(url));
  if (session.kind === "ok") markSignedIn();
  return { session };
};

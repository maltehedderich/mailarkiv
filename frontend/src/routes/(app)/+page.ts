import { redirect } from "@sveltejs/kit";

import { listMessages } from "#lib/api.js";

import type { PageLoad } from "./$types";

// ?page=N goes to the server as typed. A bad value comes back as a 400 and
// shows the failed-load state.
export const load: PageLoad = async ({ fetch, url }) => {
  const messages = await listMessages(url.searchParams.get("page"), fetch);
  if (messages.kind === "session_ended") redirect(307, "/sign-in");
  return { messages };
};

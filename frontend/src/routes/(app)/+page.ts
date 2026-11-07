import { redirect } from "@sveltejs/kit";

import { getMessage, listMessages } from "#lib/api.js";
import { signInPath } from "#lib/session.js";

import type { PageLoad } from "./$types";

// ?page=N goes to the server as typed. A bad value comes back as a 400 and
// shows the failed-load state. ?message=N opens that message's history, so Back
// and the return after sign-in reach it.
export const load: PageLoad = async ({ fetch, url }) => {
  const messageId = url.searchParams.get("message");
  const [messages, history] = await Promise.all([
    listMessages(url.searchParams.get("page"), fetch),
    messageId === null ? null : getMessage(messageId, fetch),
  ]);
  if (messages.kind === "session_ended" || history?.kind === "session_ended") {
    redirect(307, signInPath(url));
  }
  return { messages, history };
};

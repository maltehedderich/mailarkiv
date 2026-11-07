import { redirect } from "@sveltejs/kit";

import { getMessage, listMessages } from "#lib/api.js";
import { signInPath } from "#lib/session.js";

import type { PageLoad } from "./$types";

// ?page=N goes to the server as typed. A bad value comes back as a 400 and
// shows the failed-load state. ?message=N opens that message's history, so Back
// and the return after sign-in reach it.
//
// The list streams: its promise isn't awaited here, so the page can keep the
// rows it has, or show skeleton rows, while a page loads. The history is
// awaited, so the drawer opens with its data.
export const load: PageLoad = async ({ fetch, url }) => {
  const pageParam = url.searchParams.get("page");
  const messages = listMessages(pageParam, fetch);
  const messageId = url.searchParams.get("message");
  const history =
    messageId === null ? null : await getMessage(messageId, fetch);
  if (history?.kind === "session_ended") redirect(307, signInPath(url));
  return { pageParam, messages, history };
};

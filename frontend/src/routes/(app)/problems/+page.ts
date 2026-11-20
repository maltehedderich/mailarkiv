import { redirect } from "@sveltejs/kit";

import { getMessage, listProcessingProblems } from "#lib/api.js";
import { signInPath } from "#lib/session.js";

import type { PageLoad } from "./$types";

// As the message list: ?page=N goes to the server as typed, the list streams,
// and ?message=N opens that message's history over it.
export const load: PageLoad = async ({ fetch, url }) => {
  const pageParam = url.searchParams.get("page");
  const messages = listProcessingProblems(pageParam, fetch);
  const messageId = url.searchParams.get("message");
  const history =
    messageId === null ? null : await getMessage(messageId, fetch);
  if (history?.kind === "session_ended") redirect(307, signInPath(url));
  return { pageParam, messages, history };
};

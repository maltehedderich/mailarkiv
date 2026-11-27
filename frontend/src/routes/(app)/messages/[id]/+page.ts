import { redirect } from "@sveltejs/kit";

import { getMessage, getMessageContent, getMessageText } from "#lib/api.js";
import { signInPath } from "#lib/session.js";

import type { PageLoad } from "./$types";

// The load reads only the ID, so ?history opening the drawer doesn't read the
// message again or drop the choice to load images. The text is fetched here
// only when it's the body shown first; the page fetches it on a switch.
export const load: PageLoad = async ({ fetch, params, untrack, url }) => {
  const content = await getMessageContent(params.id, fetch);
  if (content.kind === "session_ended") {
    redirect(
      307,
      untrack(() => signInPath(url)),
    );
  }
  const message = content.kind === "ok" ? content.value : null;
  const text =
    message?.parsed?.html === null && message.parsed.text !== null
      ? getMessageText(message.id, fetch)
      : null;

  // A message processing hasn't read is named by its newest entry, as its row
  // is. When the view can't read the message, the history still gives the
  // hash its original downloads under.
  const capture =
    message === null || message.parsed === null
      ? await getMessage(params.id, fetch)
      : null;
  if (capture?.kind === "session_ended") {
    redirect(
      307,
      untrack(() => signInPath(url)),
    );
  }
  const history = capture?.kind === "ok" ? capture.value : null;
  const original = message ?? history;
  return {
    id: params.id,
    content,
    text,
    entry: history?.observations[0] ?? null,
    original:
      original === null
        ? null
        : { id: original.id, contentHash: original.contentHash },
  };
};

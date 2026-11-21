import {
  previewAddress,
  type MessageContent,
  type ParsedMessage,
} from "#lib/api.js";

// Which body the message view shows, and whether it has loaded images from
// the internet. The choice belongs to one view of one message: opening
// another message, or the same one again, starts over.

export type Body = "html" | "text";

export interface BodyView {
  messageId: number;
  /** The bodies the message has, HTML first. */
  choices: Body[];
  /** Null when the message has neither body. */
  shown: Body | null;
  imagesLoaded: boolean;
}

export const NO_TEXT = "This message has no text.";

export const REMOTE_IMAGES = {
  what: "Images from the internet aren't loaded.",
  why: "Loading them can tell the sender when and from which network address this message was opened. They weren't archived, so they may differ from what arrived or be gone.",
  action: "Load images",
};

export const EMBEDDED_LEFT_OUT =
  "Some embedded images are only in the attachment list, because together they pass the 10 MB the preview shows.";

export const BODY_CUT = {
  what: "This body is cut at 2,000,000 bytes. The rest is only in the original.",
  action: "Download original",
};

export function openBody(
  messageId: number,
  parsed: ParsedMessage | null,
): BodyView {
  const choices: Body[] = [];
  if (parsed?.html) choices.push("html");
  if (parsed?.text) choices.push("text");
  return { messageId, choices, shown: choices[0] ?? null, imagesLoaded: false };
}

export function showBody(view: BodyView, body: Body): BodyView {
  return view.choices.includes(body) ? { ...view, shown: body } : view;
}

export function loadImages(view: BodyView): BodyView {
  return { ...view, imagesLoaded: true };
}

/** The preview frame's address, or null while the HTML isn't shown. */
export function frameAddress(view: BodyView): string | null {
  return view.shown === "html"
    ? previewAddress(view.messageId, view.imagesLoaded)
    : null;
}

/**
 * The view's state for a load. A choice made for one load stays with it, and
 * every new load, even of the same message, starts afresh.
 */
export function bodyFor(
  kept: { content: MessageContent; body: BodyView } | null,
  content: MessageContent,
): BodyView {
  return kept?.content === content
    ? kept.body
    : openBody(content.id, content.parsed);
}

export interface Notices {
  remoteImages: boolean;
  embeddedImagesLeftOut: boolean;
  truncated: boolean;
}

/** The notices beside the body shown. */
export function notices(view: BodyView, parsed: ParsedMessage): Notices {
  const html = view.shown === "html" ? parsed.html : null;
  const shown = view.shown === "text" ? parsed.text : html;
  return {
    remoteImages: html?.remoteImages === true && !view.imagesLoaded,
    embeddedImagesLeftOut: html?.embeddedImagesLeftOut === true,
    truncated: shown?.truncated === true,
  };
}

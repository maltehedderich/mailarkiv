import { describe, expect, test } from "vitest";

import type { MessageContent, ParsedMessage } from "#lib/api.js";

import {
  bodyFor,
  frameAddress,
  loadImages,
  NO_TEXT,
  notices,
  openBody,
  showBody,
} from "./view";

const HASH = "ea9449bf2f2e4ba3d3c4f3d61a1f6b8e0c3e5f8b0a9d2c7e6f5a4b3c2d1e0f9a";

function parsed(change: Partial<ParsedMessage> = {}): ParsedMessage {
  return {
    subject: "Quartalsbericht Q3",
    from: [{ name: "Anna Schmidt", address: "anna@example.com" }],
    to: [],
    cc: [],
    bcc: [],
    encrypted: false,
    html: {
      truncated: false,
      remoteImages: false,
      embeddedImagesLeftOut: false,
    },
    text: { truncated: false },
    attachments: [],
    ...change,
  };
}

function content(
  id: number,
  change: Partial<ParsedMessage> = {},
): MessageContent {
  return {
    id,
    contentHash: HASH,
    size: 1200,
    processing: "processed",
    problems: [],
    listDate: "2026-11-02T08:14:55.000Z",
    dateReason: null,
    dateHeader: "Mon, 2 Nov 2026 09:14:55 +0100",
    parsed: parsed(change),
  };
}

describe("the body shown", () => {
  test("is the HTML when the message has both, and the text can be chosen", () => {
    const body = openBody(17, parsed());
    expect(body.shown).toBe("html");
    expect(body.choices).toEqual(["html", "text"]);
    const text = showBody(body, "text");
    expect(text.shown).toBe("text");
    expect(showBody(text, "html").shown).toBe("html");
  });

  test("is the HTML alone, with nothing to choose", () => {
    const body = openBody(17, parsed({ text: null }));
    expect(body.shown).toBe("html");
    expect(body.choices).toEqual(["html"]);
    expect(showBody(body, "text").shown).toBe("html");
  });

  test("is the text when there is no HTML", () => {
    const body = openBody(17, parsed({ html: null }));
    expect(body.shown).toBe("text");
    expect(body.choices).toEqual(["text"]);
    expect(frameAddress(body)).toBeNull();
  });

  test("is neither when the message has no body", () => {
    const body = openBody(17, parsed({ html: null, text: null }));
    expect(body.shown).toBeNull();
    expect(body.choices).toEqual([]);
    expect(NO_TEXT).toBe("This message has no text.");
  });

  test("is neither for a message processing didn't read", () => {
    expect(openBody(17, null).shown).toBeNull();
  });
});

describe("images from the internet", () => {
  const remote = parsed({
    html: {
      truncated: false,
      remoteImages: true,
      embeddedImagesLeftOut: false,
    },
  });

  test("start not loaded, and the preview asks for none", () => {
    const body = openBody(17, remote);
    expect(body.imagesLoaded).toBe(false);
    expect(frameAddress(body)).toBe("/api/messages/17/html");
    expect(notices(body, remote).remoteImages).toBe(true);
  });

  test("load after the choice, and only then does the address ask for them", () => {
    const body = loadImages(openBody(17, remote));
    expect(body.imagesLoaded).toBe(true);
    expect(frameAddress(body)).toBe("/api/messages/17/html?images=1");
    expect(notices(body, remote).remoteImages).toBe(false);
  });

  test("stay loaded while the view switches to the text and back", () => {
    const body = showBody(loadImages(openBody(17, remote)), "text");
    expect(frameAddress(body)).toBeNull();
    expect(frameAddress(showBody(body, "html"))).toBe(
      "/api/messages/17/html?images=1",
    );
  });

  test("reset when the message ID changes", () => {
    const first = content(17, remote);
    const kept = { content: first, body: loadImages(openBody(17, remote)) };
    expect(bodyFor(kept, first).imagesLoaded).toBe(true);
    const other = bodyFor(kept, content(18, remote));
    expect(other.imagesLoaded).toBe(false);
    expect(frameAddress(other)).toBe("/api/messages/18/html");
  });

  test("reset when the same message opens again", () => {
    const kept = {
      content: content(17, remote),
      body: loadImages(openBody(17, remote)),
    };
    expect(bodyFor(kept, content(17, remote)).imagesLoaded).toBe(false);
  });

  test("give no notice when the HTML has none", () => {
    expect(notices(openBody(17, parsed()), parsed()).remoteImages).toBe(false);
  });
});

describe("notices", () => {
  test("say embedded images were left out only beside the HTML", () => {
    const leftOut = parsed({
      html: {
        truncated: false,
        remoteImages: false,
        embeddedImagesLeftOut: true,
      },
    });
    const body = openBody(17, leftOut);
    expect(notices(body, leftOut).embeddedImagesLeftOut).toBe(true);
    expect(notices(showBody(body, "text"), leftOut).embeddedImagesLeftOut).toBe(
      false,
    );
  });

  test("say the body is cut for the body shown", () => {
    const cut = parsed({
      html: {
        truncated: false,
        remoteImages: false,
        embeddedImagesLeftOut: false,
      },
      text: { truncated: true },
    });
    const body = openBody(17, cut);
    expect(notices(body, cut).truncated).toBe(false);
    expect(notices(showBody(body, "text"), cut).truncated).toBe(true);
  });
});

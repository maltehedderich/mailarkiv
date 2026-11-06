import { describe, expect, test } from "vitest";

import { getSession, listMessages, signIn, signOut, type Fetch } from "./api";

// FR-045 and the contract: every call ends in success, session_ended for a
// marked 401, or unavailable for anything else.

const HASH = "ea9449bf2f2e4ba3d3c4f3d61a1f6b8e0c3e5f8b0a9d2c7e6f5a4b3c2d1e0f9a";

interface Call {
  url: string;
  init: RequestInit | undefined;
}

function answer(
  status: number,
  body: unknown,
  { marked = true } = {},
): { fetch: Fetch; calls: Call[] } {
  const calls: Call[] = [];
  const fetch: Fetch = async (input, init) => {
    calls.push({ url: String(input), init });
    const headers = new Headers({ "Content-Type": "application/json" });
    if (marked) headers.set("MailArkiv-Response", "1");
    const text =
      body === undefined
        ? null
        : typeof body === "string"
          ? body
          : JSON.stringify(body);
    return new Response(text, { status, headers });
  };
  return { fetch, calls };
}

function failing(error: unknown): Fetch {
  return async () => {
    throw error;
  };
}

const networkError = new TypeError("Failed to fetch");
const timeout = new DOMException("The operation timed out.", "TimeoutError");

describe("getSession", () => {
  test("reads the problem count", async () => {
    const { fetch, calls } = answer(200, { problem_count: 2 });
    expect(await getSession(fetch)).toEqual({
      kind: "ok",
      value: { problemCount: 2 },
    });
    expect(calls[0].url).toBe("/api/session");
  });

  test("gives every call a timeout signal", async () => {
    const { fetch, calls } = answer(200, { problem_count: 0 });
    await getSession(fetch);
    expect(calls[0].init?.signal).toBeInstanceOf(AbortSignal);
  });

  test("a marked 401 means the session ended", async () => {
    const { fetch } = answer(401, { error: "no_session" });
    expect(await getSession(fetch)).toEqual({ kind: "session_ended" });
  });

  test.each([
    [
      "an unmarked 401",
      answer(401, { error: "no_session" }, { marked: false }).fetch,
    ],
    ["a marked 500", answer(500, { error: "internal_error" }).fetch],
    [
      "an unmarked 200",
      answer(200, { problem_count: 0 }, { marked: false }).fetch,
    ],
    [
      "an unmarked 502",
      answer(502, "<html>Bad Gateway</html>", { marked: false }).fetch,
    ],
    ["a body that doesn't parse", answer(200, "{").fetch],
    ["a count as text", answer(200, { problem_count: "0" }).fetch],
    ["no count", answer(200, {}).fetch],
    ["a network error", failing(networkError)],
    ["a timeout", failing(timeout)],
  ])("%s is unavailable", async (_name, fetch) => {
    expect(await getSession(fetch)).toEqual({ kind: "unavailable" });
  });
});

describe("listMessages", () => {
  const item = {
    id: 17,
    content_hash: HASH,
    size: 31,
    file_name: null,
    imported_at: "2026-11-06T15:50:52.123Z",
    import_count: 3,
  };

  test("reads a page", async () => {
    const { fetch, calls } = answer(200, {
      items: [item],
      page: 2,
      page_size: 50,
      total: 51,
    });
    expect(await listMessages("2", fetch)).toEqual({
      kind: "ok",
      value: {
        items: [
          {
            id: 17,
            contentHash: HASH,
            size: 31,
            fileName: null,
            importedAt: "2026-11-06T15:50:52.123Z",
            importCount: 3,
          },
        ],
        page: 2,
        pageSize: 50,
        total: 51,
      },
    });
    expect(calls[0].url).toBe("/api/messages?page=2");
  });

  test("asks for no page when none is given", async () => {
    const { fetch, calls } = answer(200, {
      items: [],
      page: 1,
      page_size: 50,
      total: 0,
    });
    await listMessages(null, fetch);
    expect(calls[0].url).toBe("/api/messages");
  });

  // A hand-edited ?page= goes to the server as typed, which answers 400.
  test("passes a page as typed", async () => {
    const { fetch, calls } = answer(400, { error: "bad_page" });
    expect(await listMessages("two&x=1", fetch)).toEqual({
      kind: "unavailable",
    });
    expect(calls[0].url).toBe("/api/messages?page=two%26x%3D1");
  });

  test("a marked 401 means the session ended", async () => {
    const { fetch } = answer(401, { error: "no_session" });
    expect(await listMessages(null, fetch)).toEqual({ kind: "session_ended" });
  });

  test.each([
    ["a bad page", answer(400, { error: "bad_page" }).fetch],
    ["no items", answer(200, { page: 1, page_size: 50, total: 0 }).fetch],
    [
      "a broken item",
      answer(200, {
        items: [{ ...item, id: "17" }],
        page: 1,
        page_size: 50,
        total: 1,
      }).fetch,
    ],
    ["no total", answer(200, { items: [], page: 1, page_size: 50 }).fetch],
    ["a network error", failing(networkError)],
    ["a timeout", failing(timeout)],
  ])("%s is unavailable", async (_name, fetch) => {
    expect(await listMessages(null, fetch)).toEqual({ kind: "unavailable" });
  });
});

describe("signIn", () => {
  test("sends the credentials as JSON and succeeds on a marked 204", async () => {
    const { fetch, calls } = answer(204, undefined);
    expect(
      await signIn("admin@example.test", "correct horse battery", fetch),
    ).toEqual({ kind: "ok" });
    expect(calls[0].url).toBe("/api/session");
    expect(calls[0].init?.method).toBe("POST");
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      email: "admin@example.test",
      password: "correct horse battery",
    });
    expect(calls[0].init?.signal).toBeInstanceOf(AbortSignal);
  });

  test("a marked 401 means wrong credentials", async () => {
    const { fetch } = answer(401, { error: "invalid_credentials" });
    expect(await signIn("a@example.test", "x", fetch)).toEqual({
      kind: "invalid",
    });
  });

  test("a marked 429 means blocked", async () => {
    const { fetch } = answer(429, { error: "sign_in_blocked" });
    expect(await signIn("a@example.test", "x", fetch)).toEqual({
      kind: "blocked",
    });
  });

  // FR-045: a failure must never show the wrong-password message.
  test.each([
    ["an unmarked 401", answer(401, "", { marked: false }).fetch],
    ["an unmarked 204", answer(204, undefined, { marked: false }).fetch],
    ["an unmarked 429", answer(429, "", { marked: false }).fetch],
    ["a marked 500", answer(500, { error: "internal_error" }).fetch],
    ["an unmarked 502", answer(502, "", { marked: false }).fetch],
    ["a network error", failing(networkError)],
    ["a timeout", failing(timeout)],
  ])("%s is unavailable", async (_name, fetch) => {
    expect(await signIn("a@example.test", "x", fetch)).toEqual({
      kind: "unavailable",
    });
  });
});

describe("signOut", () => {
  test("sends DELETE and succeeds on a marked 204", async () => {
    const { fetch, calls } = answer(204, undefined);
    expect(await signOut(fetch)).toEqual({ kind: "ok" });
    expect(calls[0].url).toBe("/api/session");
    expect(calls[0].init?.method).toBe("DELETE");
    expect(calls[0].init?.signal).toBeInstanceOf(AbortSignal);
  });

  // The session had already ended, so the browser is signed out either way.
  test("a marked 401 means the session ended", async () => {
    const { fetch } = answer(401, { error: "no_session" });
    expect(await signOut(fetch)).toEqual({ kind: "session_ended" });
  });

  // FR-045: the administrator is told they're still signed in.
  test.each([
    ["an unmarked 204", answer(204, undefined, { marked: false }).fetch],
    ["an unmarked 401", answer(401, "", { marked: false }).fetch],
    ["a marked 403", answer(403, { error: "cross_origin" }).fetch],
    ["a marked 500", answer(500, { error: "internal_error" }).fetch],
    ["an unmarked 502", answer(502, "", { marked: false }).fetch],
    ["a network error", failing(networkError)],
    ["a timeout", failing(timeout)],
  ])("%s is unavailable", async (_name, fetch) => {
    expect(await signOut(fetch)).toEqual({ kind: "unavailable" });
  });
});

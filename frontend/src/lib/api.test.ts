import { describe, expect, test } from "vitest";

import {
  getMessage,
  getSession,
  getStatus,
  listMessages,
  signIn,
  signOut,
  type Fetch,
} from "./api";

// API results: every call ends in success, session_ended for a marked 401, or
// unavailable for anything else.

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
    observation_count: 3,
    latest: {
      source_type: "manual_import",
      observed_at: "2026-11-06T15:50:52.123Z",
      account_email: "admin@example.test",
      file_name: null,
    },
  };
  const collected = {
    id: 18,
    content_hash: HASH,
    size: 42,
    observation_count: 1,
    latest: {
      source_type: "imap_collection",
      observed_at: "2026-11-08T09:10:04.123Z",
      source: {
        id: 3,
        name: "Anna",
        host: "imap.example.test",
        user_name: "anna@example.test",
        removed: false,
      },
      folder: "Entwürfe",
      received_at: "2026-11-08T09:07:55.000Z",
    },
  };

  test("reads a page with both kinds of latest entry", async () => {
    const { fetch, calls } = answer(200, {
      items: [collected, item],
      page: 2,
      page_size: 50,
      total: 52,
    });
    expect(await listMessages("2", fetch)).toEqual({
      kind: "ok",
      value: {
        items: [
          {
            id: 18,
            contentHash: HASH,
            size: 42,
            observedAt: "2026-11-08T09:10:04.123Z",
            observationCount: 1,
            latest: {
              sourceType: "imap_collection",
              observedAt: "2026-11-08T09:10:04.123Z",
              source: {
                id: 3,
                name: "Anna",
                host: "imap.example.test",
                userName: "anna@example.test",
                removed: false,
              },
              folder: "Entwürfe",
              receivedAt: "2026-11-08T09:07:55.000Z",
            },
          },
          {
            id: 17,
            contentHash: HASH,
            size: 31,
            observedAt: "2026-11-06T15:50:52.123Z",
            observationCount: 3,
            latest: {
              sourceType: "manual_import",
              observedAt: "2026-11-06T15:50:52.123Z",
              accountEmail: "admin@example.test",
              fileName: null,
            },
          },
        ],
        page: 2,
        pageSize: 50,
        total: 52,
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
    [
      "an item with the old import fields",
      answer(200, {
        items: [
          {
            id: 17,
            content_hash: HASH,
            size: 31,
            file_name: null,
            imported_at: "2026-11-06T15:50:52.123Z",
            import_count: 3,
          },
        ],
        page: 1,
        page_size: 50,
        total: 1,
      }).fetch,
    ],
    [
      "an item with no latest entry",
      answer(200, {
        items: [{ ...item, latest: null }],
        page: 1,
        page_size: 50,
        total: 1,
      }).fetch,
    ],
    [
      "an item with no count",
      answer(200, {
        items: [{ ...item, observation_count: undefined }],
        page: 1,
        page_size: 50,
        total: 1,
      }).fetch,
    ],
    [
      "a latest entry of an unknown source type",
      answer(200, {
        items: [{ ...item, latest: { ...item.latest, source_type: "pop3" } }],
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

  // A failure must never show the wrong-password message.
  test.each([
    ["an unmarked 401", answer(401, "", { marked: false }).fetch],
    ["an unmarked 204", answer(204, undefined, { marked: false }).fetch],
    ["an unmarked 429", answer(429, "", { marked: false }).fetch],
    ["a marked 500", answer(500, { error: "internal_error" }).fetch],
    ["sign-in overload", answer(503, { error: "sign_in_unavailable" }).fetch],
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

  // The administrator is told they're still signed in.
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

describe("getMessage", () => {
  const OBSERVATION = {
    source_type: "manual_import",
    observed_at: "2026-11-06T15:50:52.123Z",
    account_email: "admin@example.test",
    file_name: "invoice.eml",
  };
  const COLLECTION = {
    source_type: "imap_collection",
    observed_at: "2026-11-08T09:10:04.123Z",
    source: {
      id: 3,
      name: "Anna",
      host: "imap.example.test",
      user_name: "anna@example.test",
      removed: false,
    },
    folder: "INBOX",
    received_at: "2026-11-08T09:07:55.000Z",
  };
  const HISTORY = {
    id: 17,
    content_hash: HASH,
    size: 31,
    observations: [
      COLLECTION,
      // A removed source keeps its labels, and a server may report no time.
      {
        ...COLLECTION,
        source: { ...COLLECTION.source, removed: true },
        folder: "Entwürfe",
        received_at: null,
      },
      OBSERVATION,
      { ...OBSERVATION, file_name: null },
    ],
  };

  test("reads the contract's history, keeping its order", async () => {
    const { fetch, calls } = answer(200, HISTORY);
    expect(await getMessage("17", fetch)).toEqual({
      kind: "ok",
      value: {
        id: 17,
        contentHash: HASH,
        size: 31,
        observations: [
          {
            sourceType: "imap_collection",
            observedAt: "2026-11-08T09:10:04.123Z",
            source: {
              id: 3,
              name: "Anna",
              host: "imap.example.test",
              userName: "anna@example.test",
              removed: false,
            },
            folder: "INBOX",
            receivedAt: "2026-11-08T09:07:55.000Z",
          },
          {
            sourceType: "imap_collection",
            observedAt: "2026-11-08T09:10:04.123Z",
            source: {
              id: 3,
              name: "Anna",
              host: "imap.example.test",
              userName: "anna@example.test",
              removed: true,
            },
            folder: "Entwürfe",
            receivedAt: null,
          },
          {
            sourceType: "manual_import",
            observedAt: "2026-11-06T15:50:52.123Z",
            accountEmail: "admin@example.test",
            fileName: "invoice.eml",
          },
          {
            sourceType: "manual_import",
            observedAt: "2026-11-06T15:50:52.123Z",
            accountEmail: "admin@example.test",
            fileName: null,
          },
        ],
      },
    });
    expect(calls[0].url).toBe("/api/messages/17");
    expect(calls[0].init?.signal).toBeInstanceOf(AbortSignal);
  });

  test("escapes an ID typed into the address", async () => {
    const { fetch, calls } = answer(404, { error: "not_found" });
    await getMessage("1/original?x=1", fetch);
    expect(calls[0].url).toBe("/api/messages/1%2Foriginal%3Fx%3D1");
  });

  test("reads a marked 401 as session_ended", async () => {
    const { fetch } = answer(401, { error: "no_session" });
    expect(await getMessage("17", fetch)).toEqual({ kind: "session_ended" });
  });

  test.each([
    ["an unknown ID", 404, { error: "not_found" }],
    ["a server error", 500, { error: "internal_error" }],
    ["a body that isn't JSON", 200, "<!doctype html>"],
    ["no observations field", 200, { ...HISTORY, observations: undefined }],
    ["a short hash", 200, { ...HISTORY, content_hash: "ea94" }],
    [
      "a source type the interface doesn't know",
      200,
      {
        ...HISTORY,
        observations: [{ ...OBSERVATION, source_type: "imap" }],
      },
    ],
    [
      "a time that can't be read",
      200,
      { ...HISTORY, observations: [{ ...OBSERVATION, observed_at: "soon" }] },
    ],
    [
      "an observation with no account",
      200,
      {
        ...HISTORY,
        observations: [{ ...OBSERVATION, account_email: undefined }],
      },
    ],
    [
      "a collection with no folder",
      200,
      { ...HISTORY, observations: [{ ...COLLECTION, folder: undefined }] },
    ],
    [
      "a collection with no source",
      200,
      { ...HISTORY, observations: [{ ...COLLECTION, source: null }] },
    ],
    [
      "a collection source with no removed flag",
      200,
      {
        ...HISTORY,
        observations: [
          {
            ...COLLECTION,
            source: { ...COLLECTION.source, removed: undefined },
          },
        ],
      },
    ],
    [
      "a collection source with a text ID",
      200,
      {
        ...HISTORY,
        observations: [
          { ...COLLECTION, source: { ...COLLECTION.source, id: "3" } },
        ],
      },
    ],
    [
      "a received time that can't be read",
      200,
      { ...HISTORY, observations: [{ ...COLLECTION, received_at: "soon" }] },
    ],
    [
      "a collection with no received time field",
      200,
      { ...HISTORY, observations: [{ ...COLLECTION, received_at: undefined }] },
    ],
  ])("reads %s as unavailable", async (_name, status, body) => {
    const { fetch } = answer(status, body);
    expect(await getMessage("17", fetch)).toEqual({ kind: "unavailable" });
  });

  test("reads an unmarked answer as unavailable", async () => {
    const { fetch } = answer(200, HISTORY, { marked: false });
    expect(await getMessage("17", fetch)).toEqual({ kind: "unavailable" });
  });

  test("reads a network error as unavailable", async () => {
    expect(await getMessage("17", failing(networkError))).toEqual({
      kind: "unavailable",
    });
  });
});

describe("getStatus", () => {
  const STATUS = {
    started_at: "2026-11-06T15:00:00.000Z",
    recovery: {
      finished_at: "2026-11-06T15:00:00.410Z",
      temp_files_removed: 1,
      temp_files_not_removed: 0,
      unreferenced_found: 1,
    },
    unreferenced: [{ content_hash: HASH, size: 2048 }],
    failures: [
      {
        at: "2026-11-06T15:20:00.000Z",
        category: "stored_original_damaged",
        message_id: 17,
        content_hash: HASH,
        restored: false,
      },
      {
        at: "2026-11-06T15:21:00.000Z",
        category: "temp_file_not_removed",
        restored: false,
      },
    ],
  };

  test("reads the contract's status, keeping its order", async () => {
    const { fetch, calls } = answer(200, STATUS);
    expect(await getStatus(fetch)).toEqual({
      kind: "ok",
      value: {
        startedAt: "2026-11-06T15:00:00.000Z",
        recovery: {
          finishedAt: "2026-11-06T15:00:00.410Z",
          tempFilesRemoved: 1,
          tempFilesNotRemoved: 0,
          unreferencedFound: 1,
        },
        unreferenced: [{ contentHash: HASH, size: 2048 }],
        failures: [
          {
            at: "2026-11-06T15:20:00.000Z",
            category: "stored_original_damaged",
            messageId: 17,
            contentHash: HASH,
            restored: false,
          },
          {
            at: "2026-11-06T15:21:00.000Z",
            category: "temp_file_not_removed",
            messageId: null,
            contentHash: null,
            restored: false,
          },
        ],
      },
    });
    expect(calls[0].url).toBe("/api/status");
    expect(calls[0].init?.signal).toBeInstanceOf(AbortSignal);
  });

  test("reads every failure category", async () => {
    const categories = [
      "storage_full",
      "storage_write_failed",
      "catalog_write_failed",
      "stored_original_missing",
      "stored_original_damaged",
      "temp_file_not_removed",
    ];
    const failures = categories.map((category) => ({
      at: "2026-11-06T15:20:00.000Z",
      category,
      restored: false,
    }));
    const { fetch } = answer(200, { ...STATUS, failures });
    const status = await getStatus(fetch);
    expect(status.kind).toBe("ok");
    if (status.kind !== "ok") return;
    expect(status.value.failures.map((failure) => failure.category)).toEqual(
      categories,
    );
  });

  test("reads a marked 401 as session_ended", async () => {
    const { fetch } = answer(401, { error: "no_session" });
    expect(await getStatus(fetch)).toEqual({ kind: "session_ended" });
  });

  const FAILURE = STATUS.failures[0];
  test.each([
    ["a server error", 500, { error: "internal_error" }],
    ["a body that isn't JSON", 200, "<!doctype html>"],
    ["no recovery result", 200, { ...STATUS, recovery: null }],
    ["no failures field", 200, { ...STATUS, failures: undefined }],
    ["a start time that can't be read", 200, { ...STATUS, started_at: "soon" }],
    [
      "an unreferenced original with a short hash",
      200,
      { ...STATUS, unreferenced: [{ content_hash: "ea94", size: 1 }] },
    ],
    [
      "a category the interface doesn't know",
      200,
      { ...STATUS, failures: [{ ...FAILURE, category: "disk_on_fire" }] },
    ],
    [
      "a failure with no restored flag",
      200,
      { ...STATUS, failures: [{ ...FAILURE, restored: undefined }] },
    ],
    [
      "a failure with a negative message ID",
      200,
      { ...STATUS, failures: [{ ...FAILURE, message_id: -1 }] },
    ],
  ])("reads %s as unavailable", async (_name, status, body) => {
    const { fetch } = answer(status, body);
    expect(await getStatus(fetch)).toEqual({ kind: "unavailable" });
  });

  test("reads an unmarked answer as unavailable", async () => {
    const { fetch } = answer(200, STATUS, { marked: false });
    expect(await getStatus(fetch)).toEqual({ kind: "unavailable" });
  });

  test("reads a network error as unavailable", async () => {
    expect(await getStatus(failing(networkError))).toEqual({
      kind: "unavailable",
    });
  });
});

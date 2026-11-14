import { describe, expect, test } from "vitest";

import {
  addSource,
  checkNewSource,
  checkSource,
  editSource,
  getMessage,
  getSession,
  getStatus,
  listMessages,
  listSources,
  pauseSource,
  readSource,
  removeSource,
  resumeSource,
  SIGN_IN_TIMEOUT_MS,
  signIn,
  signOut,
  syncSource,
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
  test("reads the problem count and the time left", async () => {
    const { fetch, calls } = answer(200, {
      problem_count: 2,
      expires_in: 39599,
    });
    expect(await getSession(fetch)).toEqual({
      kind: "ok",
      value: { problemCount: 2, expiresIn: 39599 },
    });
    expect(calls[0].url).toBe("/api/session");
  });

  test("gives every call a timeout signal", async () => {
    const { fetch, calls } = answer(200, { problem_count: 0, expires_in: 60 });
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
      answer(200, { problem_count: 0, expires_in: 60 }, { marked: false })
        .fetch,
    ],
    [
      "an unmarked 502",
      answer(502, "<html>Bad Gateway</html>", { marked: false }).fetch,
    ],
    ["a body that doesn't parse", answer(200, "{").fetch],
    [
      "a count as text",
      answer(200, { problem_count: "0", expires_in: 60 }).fetch,
    ],
    ["no count", answer(200, { expires_in: 60 }).fetch],
    ["no time left", answer(200, { problem_count: 0 }).fetch],
    [
      "a negative time left",
      answer(200, { problem_count: 0, expires_in: -1 }).fetch,
    ],
    [
      "a fractional time left",
      answer(200, { problem_count: 0, expires_in: 1.5 }).fetch,
    ],
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

// The contract's example problem sources.
const PROBLEM_SOURCES = [
  {
    id: 3,
    name: "Anna",
    category: "timed_out",
    since: "2026-11-08T09:15:00.000Z",
    failed_attempts: 3,
  },
  {
    id: 5,
    name: "Office",
    category: "folder_unavailable",
    since: "2026-11-08T08:00:00.000Z",
    failed_attempts: null,
  },
];

describe("getStatus", () => {
  const STATUS = {
    started_at: "2026-11-06T15:00:00.000Z",
    recovery: {
      finished_at: "2026-11-06T15:00:00.410Z",
      temp_files_removed: 1,
      temp_files_not_removed: 0,
      unreferenced_found: 1,
    },
    sources: [],
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
        sources: [],
        unreferenced: [{ contentHash: HASH, size: 2048 }],
        failures: [
          {
            at: "2026-11-06T15:20:00.000Z",
            category: "stored_original_damaged",
            messageId: 17,
            contentHash: HASH,
            restored: false,
            source: null,
          },
          {
            at: "2026-11-06T15:21:00.000Z",
            category: "temp_file_not_removed",
            messageId: null,
            contentHash: null,
            restored: false,
            source: null,
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
      "message_too_large",
      "message_removed",
    ];
    const failures = categories.map((category) => ({
      at: "2026-11-06T15:20:00.000Z",
      category,
      ...(category.startsWith("message_")
        ? { source_id: 3, source_name: "Anna" }
        : {}),
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

  test("reads message events with the source they name", async () => {
    const { fetch } = answer(200, {
      ...STATUS,
      failures: [
        {
          at: "2026-11-08T09:00:01.000Z",
          category: "message_too_large",
          source_id: 3,
          source_name: "Anna",
          restored: false,
        },
        {
          at: "2026-11-08T09:05:00.000Z",
          category: "message_removed",
          source_id: 5,
          source_name: "<b>Büro</b>",
          restored: false,
        },
      ],
    });
    const status = await getStatus(fetch);
    expect(status.kind).toBe("ok");
    if (status.kind !== "ok") return;
    expect(status.value.failures).toEqual([
      {
        at: "2026-11-08T09:00:01.000Z",
        category: "message_too_large",
        messageId: null,
        contentHash: null,
        source: { id: 3, name: "Anna" },
        restored: false,
      },
      {
        at: "2026-11-08T09:05:00.000Z",
        category: "message_removed",
        messageId: null,
        contentHash: null,
        source: { id: 5, name: "<b>Büro</b>" },
        restored: false,
      },
    ]);
  });

  test("reads problem sources in the server's order", async () => {
    const { fetch } = answer(200, { ...STATUS, sources: PROBLEM_SOURCES });
    const status = await getStatus(fetch);
    expect(status.kind).toBe("ok");
    if (status.kind !== "ok") return;
    expect(status.value.sources).toEqual([
      {
        id: 3,
        name: "Anna",
        category: "timed_out",
        since: "2026-11-08T09:15:00.000Z",
        failedAttempts: 3,
      },
      {
        id: 5,
        name: "Office",
        category: "folder_unavailable",
        since: "2026-11-08T08:00:00.000Z",
        failedAttempts: null,
      },
    ]);
  });

  test("reads a marked 401 as session_ended", async () => {
    const { fetch } = answer(401, { error: "no_session" });
    expect(await getStatus(fetch)).toEqual({ kind: "session_ended" });
  });

  const PROBLEM_SOURCE = PROBLEM_SOURCES[0];
  const FAILURE = STATUS.failures[0];
  const MESSAGE_EVENT = {
    at: "2026-11-08T09:00:01.000Z",
    category: "message_removed",
    source_id: 3,
    source_name: "Anna",
    restored: false,
  };
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
    [
      "a message event with no source",
      200,
      {
        ...STATUS,
        failures: [
          { ...MESSAGE_EVENT, source_id: undefined, source_name: undefined },
        ],
      },
    ],
    [
      "a message event whose source has no name",
      200,
      { ...STATUS, failures: [{ ...MESSAGE_EVENT, source_name: null }] },
    ],
    [
      "a message event with a negative source ID",
      200,
      { ...STATUS, failures: [{ ...MESSAGE_EVENT, source_id: -3 }] },
    ],
    ["no sources field", 200, { ...STATUS, sources: undefined }],
    [
      "a problem source with a category the interface doesn't know",
      200,
      { ...STATUS, sources: [{ ...PROBLEM_SOURCE, category: "disk_on_fire" }] },
    ],
    [
      "a problem source with an import category",
      200,
      {
        ...STATUS,
        sources: [{ ...PROBLEM_SOURCE, category: "stored_original_missing" }],
      },
    ],
    [
      "a problem source with no start time",
      200,
      { ...STATUS, sources: [{ ...PROBLEM_SOURCE, since: null }] },
    ],
    [
      "a problem source with negative failed attempts",
      200,
      { ...STATUS, sources: [{ ...PROBLEM_SOURCE, failed_attempts: -1 }] },
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

// The contract's list entry, retrying after three timeouts.
const RETRYING = {
  id: 3,
  name: "Anna",
  host: "imap.example.test",
  port: 993,
  security: "tls",
  user_name: "anna@example.test",
  state: "retrying",
  folders: ["INBOX", "Gesendete Objekte"],
  progress: null,
  last_success: { at: "2026-11-08T09:10:00.000Z", archived: 12 },
  collected: 18044,
  next_sync_at: "2026-11-08T09:24:00.000Z",
  paused_at: null,
  failure: {
    category: "timed_out",
    since: "2026-11-08T09:15:00.000Z",
    failed_attempts: 3,
    last_at: "2026-11-08T09:20:00.000Z",
    reply: null,
  },
  problems: { unavailable_folders: 0, waiting_for_retry: 2, not_archived: 1 },
};

const RETRYING_PARSED = {
  id: 3,
  name: "Anna",
  host: "imap.example.test",
  port: 993,
  security: "tls",
  userName: "anna@example.test",
  state: "retrying",
  folders: ["INBOX", "Gesendete Objekte"],
  progress: null,
  lastSuccess: { at: "2026-11-08T09:10:00.000Z", archived: 12 },
  collected: 18044,
  nextSyncAt: "2026-11-08T09:24:00.000Z",
  pausedAt: null,
  failure: {
    category: "timed_out",
    since: "2026-11-08T09:15:00.000Z",
    failedAttempts: 3,
    lastAt: "2026-11-08T09:20:00.000Z",
    reply: null,
  },
  problems: { unavailableFolders: 0, waitingForRetry: 2, notArchived: 1 },
};

// A first sync that is running: everything a sync sets later is null.
const SYNCING = {
  ...RETRYING,
  id: 4,
  name: "Büro",
  security: "starttls",
  port: 143,
  state: "syncing",
  progress: { archived: 120, found: 4000 },
  last_success: null,
  collected: 120,
  next_sync_at: null,
  failure: null,
  problems: { unavailable_folders: 0, waiting_for_retry: 0, not_archived: 0 },
};

describe("listSources", () => {
  test("reads the contract's list, keeping its order", async () => {
    const { fetch, calls } = answer(200, {
      collection: "available",
      sources: [RETRYING, SYNCING],
    });
    expect(await listSources(fetch)).toEqual({
      kind: "ok",
      value: {
        collection: "available",
        sources: [
          RETRYING_PARSED,
          {
            ...RETRYING_PARSED,
            id: 4,
            name: "Büro",
            security: "starttls",
            port: 143,
            state: "syncing",
            progress: { archived: 120, found: 4000 },
            lastSuccess: null,
            collected: 120,
            nextSyncAt: null,
            failure: null,
            problems: {
              unavailableFolders: 0,
              waitingForRetry: 0,
              notArchived: 0,
            },
          },
        ],
      },
    });
    expect(calls[0].url).toBe("/api/sources");
    expect(calls[0].init?.signal).toBeInstanceOf(AbortSignal);
  });

  test("reads every state", async () => {
    const states = [
      "waiting",
      "syncing",
      "retrying",
      "needs_attention",
      "paused",
    ];
    const sources = states.map((state, i) => ({
      ...RETRYING,
      id: i + 1,
      state,
    }));
    const { fetch } = answer(200, { collection: "available", sources });
    const list = await listSources(fetch);
    expect(list.kind).toBe("ok");
    if (list.kind !== "ok") return;
    expect(list.value.sources.map((source) => source.state)).toEqual(states);
  });

  test("reads every failure category a source can have", async () => {
    const categories = [
      "server_not_reachable",
      "secure_connection_failed",
      "sign_in_rejected",
      "password_unreadable",
      "timed_out",
      "server_error",
      "storage_full",
      "storage_write_failed",
      "catalog_write_failed",
    ];
    const sources = categories.map((category, i) => ({
      ...RETRYING,
      id: i + 1,
      failure: { ...RETRYING.failure, category },
    }));
    const { fetch } = answer(200, { collection: "available", sources });
    const list = await listSources(fetch);
    expect(list.kind).toBe("ok");
    if (list.kind !== "ok") return;
    expect(
      list.value.sources.map((source) => source.failure?.category),
    ).toEqual(categories);
  });

  test("reads an unreadable password, which no sign-in tried", async () => {
    const failure = {
      category: "password_unreadable",
      since: "2026-11-08T09:00:00.000Z",
      failed_attempts: null,
      last_at: null,
      reply: null,
    };
    const { fetch } = answer(200, {
      collection: "no_key",
      sources: [
        { ...RETRYING, state: "needs_attention", next_sync_at: null, failure },
      ],
    });
    const list = await listSources(fetch);
    expect(list.kind).toBe("ok");
    if (list.kind !== "ok") return;
    expect(list.value.collection).toBe("no_key");
    expect(list.value.sources[0].failure).toEqual({
      category: "password_unreadable",
      since: "2026-11-08T09:00:00.000Z",
      failedAttempts: null,
      lastAt: null,
      reply: null,
    });
  });

  test("keeps the server's reply as it came", async () => {
    const reply = "[UNAVAILABLE] ‮Backend down";
    const { fetch } = answer(200, {
      collection: "available",
      sources: [{ ...RETRYING, failure: { ...RETRYING.failure, reply } }],
    });
    const list = await listSources(fetch);
    expect(list.kind === "ok" && list.value.sources[0].failure?.reply).toBe(
      reply,
    );
  });

  test("reads an empty list", async () => {
    const { fetch } = answer(200, { collection: "available", sources: [] });
    expect(await listSources(fetch)).toEqual({
      kind: "ok",
      value: { collection: "available", sources: [] },
    });
  });

  test("reads a marked 401 as session_ended", async () => {
    const { fetch } = answer(401, { error: "no_session" });
    expect(await listSources(fetch)).toEqual({ kind: "session_ended" });
  });

  const list = (source: unknown) => ({
    collection: "available",
    sources: [source],
  });
  test.each([
    ["a server error", 500, { error: "internal_error" }],
    [
      "an unknown path, as without the preview flag",
      404,
      { error: "not_found" },
    ],
    ["a body that isn't JSON", 200, "<!doctype html>"],
    ["no sources field", 200, { collection: "available" }],
    ["an unknown collection", 200, { collection: "maybe", sources: [] }],
    [
      "a state the interface doesn't know",
      200,
      list({ ...RETRYING, state: "napping" }),
    ],
    [
      "a failure category the interface doesn't know",
      200,
      list({
        ...RETRYING,
        failure: { ...RETRYING.failure, category: "disk_on_fire" },
      }),
    ],
    ["an unknown security", 200, list({ ...RETRYING, security: "ssl" })],
    ["a port of 0", 200, list({ ...RETRYING, port: 0 })],
    ["a port over 65535", 200, list({ ...RETRYING, port: 65536 })],
    [
      "folders that aren't names",
      200,
      list({ ...RETRYING, folders: [{ name: "INBOX" }] }),
    ],
    [
      "progress with no found count",
      200,
      list({ ...SYNCING, progress: { archived: 1 } }),
    ],
    [
      "a last success with no time",
      200,
      list({ ...RETRYING, last_success: { archived: 1 } }),
    ],
    ["a negative collected count", 200, list({ ...RETRYING, collected: -1 })],
    [
      "a next sync time that can't be read",
      200,
      list({ ...RETRYING, next_sync_at: "soon" }),
    ],
    [
      "a failure with no start time",
      200,
      list({ ...RETRYING, failure: { ...RETRYING.failure, since: null } }),
    ],
    [
      "a reply that isn't text",
      200,
      list({ ...RETRYING, failure: { ...RETRYING.failure, reply: 7 } }),
    ],
    ["no problem counts", 200, list({ ...RETRYING, problems: null })],
  ])("reads %s as unavailable", async (_name, status, body) => {
    const { fetch } = answer(status, body);
    expect(await listSources(fetch)).toEqual({ kind: "unavailable" });
  });

  test("reads an unmarked answer as unavailable", async () => {
    const { fetch } = answer(
      200,
      { collection: "available", sources: [] },
      { marked: false },
    );
    expect(await listSources(fetch)).toEqual({ kind: "unavailable" });
  });

  test("reads a timeout as unavailable", async () => {
    expect(await listSources(failing(timeout))).toEqual({
      kind: "unavailable",
    });
  });
});

describe("readSource", () => {
  const WAITING_ITEM = {
    folder: "INBOX",
    size: 48211,
    received_at: "2026-11-07T17:02:11.000Z",
    recorded_at: "2026-11-08T09:00:03.000Z",
    category: "message_unavailable",
  };
  const DETAIL = {
    ...RETRYING,
    folders: [
      {
        name: "INBOX",
        unavailable_since: null,
        renumbered_at: "2026-11-08T08:00:00.000Z",
      },
      {
        name: "Projekte",
        unavailable_since: "2026-11-08T09:00:00.000Z",
        renumbered_at: null,
      },
    ],
    messages: {
      waiting_for_retry: { total: 2, items: [WAITING_ITEM] },
      not_archived: {
        total: 1,
        items: [
          {
            folder: "INBOX",
            size: 160000000,
            received_at: null,
            recorded_at: "2026-11-08T09:00:01.000Z",
            category: "message_too_large",
          },
        ],
      },
    },
  };

  test("reads the contract's page", async () => {
    const { fetch, calls } = answer(200, DETAIL);
    expect(await readSource("3", fetch)).toEqual({
      kind: "ok",
      value: {
        ...RETRYING_PARSED,
        folders: [
          {
            name: "INBOX",
            unavailableSince: null,
            renumberedAt: "2026-11-08T08:00:00.000Z",
          },
          {
            name: "Projekte",
            unavailableSince: "2026-11-08T09:00:00.000Z",
            renumberedAt: null,
          },
        ],
        messages: {
          waitingForRetry: {
            total: 2,
            items: [
              {
                folder: "INBOX",
                size: 48211,
                receivedAt: "2026-11-07T17:02:11.000Z",
                recordedAt: "2026-11-08T09:00:03.000Z",
                category: "message_unavailable",
              },
            ],
          },
          notArchived: {
            total: 1,
            items: [
              {
                folder: "INBOX",
                size: 160000000,
                receivedAt: null,
                recordedAt: "2026-11-08T09:00:01.000Z",
                category: "message_too_large",
              },
            ],
          },
        },
      },
    });
    expect(calls[0].url).toBe("/api/sources/3");
    expect(calls[0].init?.signal).toBeInstanceOf(AbortSignal);
  });

  test("sends the ID as typed, encoded", async () => {
    const { fetch, calls } = answer(404, { error: "not_found" });
    expect(await readSource("a/b?c", fetch)).toEqual({ kind: "unavailable" });
    expect(calls[0].url).toBe("/api/sources/a%2Fb%3Fc");
  });

  test("reads a message with no size", async () => {
    const { fetch } = answer(200, {
      ...DETAIL,
      messages: {
        ...DETAIL.messages,
        waiting_for_retry: {
          total: 1,
          items: [{ ...WAITING_ITEM, size: null }],
        },
      },
    });
    const page = await readSource("3", fetch);
    expect(
      page.kind === "ok" && page.value.messages.waitingForRetry.items[0].size,
    ).toBeNull();
  });

  test("reads a removed message", async () => {
    const removed = { ...WAITING_ITEM, category: "message_removed" };
    const { fetch } = answer(200, {
      ...DETAIL,
      messages: {
        ...DETAIL.messages,
        not_archived: { total: 1, items: [removed] },
      },
    });
    const page = await readSource("3", fetch);
    expect(
      page.kind === "ok" && page.value.messages.notArchived.items[0].category,
    ).toBe("message_removed");
  });

  test("reads a marked 401 as session_ended", async () => {
    const { fetch } = answer(401, { error: "no_session" });
    expect(await readSource("3", fetch)).toEqual({ kind: "session_ended" });
  });

  const withWaiting = (item: unknown) => ({
    ...DETAIL,
    messages: {
      ...DETAIL.messages,
      waiting_for_retry: { total: 1, items: [item] },
    },
  });
  test.each([
    ["an unknown source", 404, { error: "not_found" }],
    ["the list's folder names", 200, { ...DETAIL, folders: RETRYING.folders }],
    ["no messages", 200, { ...DETAIL, messages: undefined }],
    [
      "a list with no total",
      200,
      {
        ...DETAIL,
        messages: { ...DETAIL.messages, not_archived: { items: [] } },
      },
    ],
    [
      "a state the interface doesn't know",
      200,
      { ...DETAIL, state: "napping" },
    ],
    [
      "a message category the interface doesn't know",
      200,
      withWaiting({ ...WAITING_ITEM, category: "message_lost" }),
    ],
    [
      "a sync category on a message",
      200,
      withWaiting({ ...WAITING_ITEM, category: "timed_out" }),
    ],
    [
      "a message with no folder",
      200,
      withWaiting({ ...WAITING_ITEM, folder: null }),
    ],
    [
      "a message with no recorded time",
      200,
      withWaiting({ ...WAITING_ITEM, recorded_at: null }),
    ],
    [
      "a folder whose unavailable time can't be read",
      200,
      {
        ...DETAIL,
        folders: [{ ...DETAIL.folders[0], unavailable_since: "soon" }],
      },
    ],
  ])("reads %s as unavailable", async (_name, status, body) => {
    const { fetch } = answer(status, body);
    expect(await readSource("3", fetch)).toEqual({ kind: "unavailable" });
  });
});

// Calls that change a source. Each ends in one of a closed set of results,
// so a form can't mistake a refusal for success. The answers come from the
// HTTP contract.

const DETAILS = {
  host: "imap.example.test",
  port: 993,
  security: "tls",
  userName: "anna@example.test",
  password: "MARKER-PW-4f1c",
} as const;

const DETAILS_JSON = {
  host: "imap.example.test",
  port: 993,
  security: "tls",
  user_name: "anna@example.test",
  password: "MARKER-PW-4f1c",
};

const EMPTY_DETAIL = {
  ...RETRYING,
  folders: [{ name: "INBOX", unavailable_since: null, renumbered_at: null }],
  messages: {
    waiting_for_retry: { total: 0, items: [] },
    not_archived: { total: 0, items: [] },
  },
};

describe("SIGN_IN_TIMEOUT_MS", () => {
  // The server has 120 seconds and raises its write deadline to 130.
  test("is 150 seconds", () => {
    expect(SIGN_IN_TIMEOUT_MS).toBe(150_000);
  });
});

describe("checkNewSource", () => {
  test("posts the details and reads the folders in the server's order", async () => {
    const { fetch, calls } = answer(200, {
      folders: [
        { name: "INBOX", messages: 1204 },
        { name: "Gesendete Objekte", messages: 311 },
        { name: "Archiv/2025", messages: null },
      ],
    });
    expect(await checkNewSource(DETAILS, fetch)).toEqual({
      kind: "ok",
      value: [
        { name: "INBOX", messages: 1204 },
        { name: "Gesendete Objekte", messages: 311 },
        { name: "Archiv/2025", messages: null },
      ],
    });
    expect(calls[0].url).toBe("/api/sources/check");
    expect(calls[0].init?.method).toBe("POST");
    expect(new Headers(calls[0].init?.headers).get("Content-Type")).toBe(
      "application/json",
    );
    expect(JSON.parse(String(calls[0].init?.body))).toEqual(DETAILS_JSON);
    expect(calls[0].init?.signal).toBeInstanceOf(AbortSignal);
  });

  test("sends a port that isn't a number as null, for the server to refuse", async () => {
    const { fetch, calls } = answer(200, { folders: [] });
    await checkNewSource({ ...DETAILS, port: null }, fetch);
    expect(JSON.parse(String(calls[0].init?.body)).port).toBeNull();
  });

  test("reads a failed sign-in with its category and the server's reply", async () => {
    const { fetch } = answer(422, {
      error: "check_failed",
      category: "sign_in_rejected",
      reply: "[AUTHENTICATIONFAILED] Invalid credentials",
    });
    expect(await checkNewSource(DETAILS, fetch)).toEqual({
      kind: "check_failed",
      category: "sign_in_rejected",
      reply: "[AUTHENTICATIONFAILED] Invalid credentials",
    });
  });

  test.each([
    "server_not_reachable",
    "secure_connection_failed",
    "sign_in_rejected",
    "timed_out",
    "server_error",
  ])("reads the check category %s, with no reply", async (category) => {
    const { fetch } = answer(422, { error: "check_failed", category });
    expect(await checkNewSource(DETAILS, fetch)).toEqual({
      kind: "check_failed",
      category,
      reply: null,
    });
  });

  test("reads an existing source with its ID", async () => {
    const { fetch } = answer(409, { error: "source_exists", source_id: 3 });
    expect(await checkNewSource(DETAILS, fetch)).toEqual({
      kind: "source_exists",
      sourceId: 3,
    });
  });

  test("reads a server without its key", async () => {
    const { fetch } = answer(409, { error: "no_key" });
    expect(await checkNewSource(DETAILS, fetch)).toEqual({ kind: "no_key" });
  });

  test("reads the fields that broke a rule", async () => {
    const { fetch } = answer(400, {
      error: "invalid_fields",
      fields: ["port", "password"],
    });
    expect(await checkNewSource(DETAILS, fetch)).toEqual({
      kind: "invalid_fields",
      fields: ["port", "password"],
    });
  });

  // A body that isn't JSON or has unknown fields names no field.
  test("reads a refusal that names no field", async () => {
    const { fetch } = answer(400, { error: "invalid_fields", fields: [] });
    expect(await checkNewSource(DETAILS, fetch)).toEqual({
      kind: "invalid_fields",
      fields: [],
    });
  });

  test("reads the client's timeout as timed out", async () => {
    expect(await checkNewSource(DETAILS, failing(timeout))).toEqual({
      kind: "timed_out",
    });
  });

  test("reads a marked 401 as session_ended", async () => {
    const { fetch } = answer(401, { error: "no_session" });
    expect(await checkNewSource(DETAILS, fetch)).toEqual({
      kind: "session_ended",
    });
  });

  test.each([
    ["a network error", failing(networkError)],
    [
      "an unmarked answer",
      answer(200, { folders: [] }, { marked: false }).fetch,
    ],
    ["an internal error", answer(500, { error: "internal_error" }).fetch],
    ["a body that isn't JSON", answer(200, "<html>").fetch],
    [
      "a folder with no name",
      answer(200, { folders: [{ name: null, messages: 1 }] }).fetch,
    ],
    [
      "a count that isn't a count",
      answer(200, { folders: [{ name: "INBOX", messages: -1 }] }).fetch,
    ],
    [
      "a check category the interface doesn't know",
      answer(422, { error: "check_failed", category: "storage_full" }).fetch,
    ],
    [
      "a reply that isn't text",
      answer(422, {
        error: "check_failed",
        category: "server_error",
        reply: 5,
      }).fetch,
    ],
    [
      "a field the interface doesn't know",
      answer(400, { error: "invalid_fields", fields: ["colour"] }).fetch,
    ],
    [
      "an existing source with no ID",
      answer(409, { error: "source_exists" }).fetch,
    ],
    // Sync now's refusals don't answer a check.
    ["a sync refusal", answer(409, { error: "already_syncing" }).fetch],
  ])("reads %s as unavailable", async (_name, fetch) => {
    expect(await checkNewSource(DETAILS, fetch)).toEqual({
      kind: "unavailable",
    });
  });
});

describe("addSource", () => {
  test("posts the details, name, and folders, and reads the new source", async () => {
    const { fetch, calls } = answer(201, RETRYING);
    expect(
      await addSource(
        { ...DETAILS, name: "Anna", folders: ["INBOX", "Gesendete Objekte"] },
        fetch,
      ),
    ).toEqual({ kind: "ok", value: RETRYING_PARSED });
    expect(calls[0].url).toBe("/api/sources");
    expect(calls[0].init?.method).toBe("POST");
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      ...DETAILS_JSON,
      name: "Anna",
      folders: ["INBOX", "Gesendete Objekte"],
    });
  });

  test.each([
    [
      422,
      { error: "check_failed", category: "timed_out" },
      { kind: "check_failed", category: "timed_out", reply: null },
    ],
    [
      409,
      { error: "source_exists", source_id: 3 },
      { kind: "source_exists", sourceId: 3 },
    ],
    [409, { error: "no_key" }, { kind: "no_key" }],
    [
      400,
      { error: "invalid_fields", fields: ["folders"] },
      { kind: "invalid_fields", fields: ["folders"] },
    ],
  ])("reads a %d %j", async (status, body, want) => {
    const { fetch } = answer(status, body);
    expect(
      await addSource({ ...DETAILS, name: "Anna", folders: [] }, fetch),
    ).toEqual(want);
  });

  test("reads the client's timeout as timed out", async () => {
    expect(
      await addSource(
        { ...DETAILS, name: "Anna", folders: ["INBOX"] },
        failing(timeout),
      ),
    ).toEqual({ kind: "timed_out" });
  });

  test("reads a 201 that isn't a source as unavailable", async () => {
    const { fetch } = answer(201, { ...RETRYING, state: "napping" });
    expect(
      await addSource({ ...DETAILS, name: "Anna", folders: ["INBOX"] }, fetch),
    ).toEqual({ kind: "unavailable" });
  });
});

describe("editSource", () => {
  test("sends only the given fields and reads the source's page", async () => {
    const { fetch, calls } = answer(200, EMPTY_DETAIL);
    const result = await editSource(
      "3",
      { name: "Anna B", folders: ["INBOX"] },
      fetch,
    );
    expect(result.kind).toBe("ok");
    expect(calls[0].url).toBe("/api/sources/3");
    expect(calls[0].init?.method).toBe("PATCH");
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      name: "Anna B",
      folders: ["INBOX"],
    });
  });

  test("sends a port, security, and password when given", async () => {
    const { fetch, calls } = answer(200, EMPTY_DETAIL);
    await editSource(
      "3",
      { port: 143, security: "starttls", password: "new password" },
      fetch,
    );
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      port: 143,
      security: "starttls",
      password: "new password",
    });
  });

  test.each([
    [
      422,
      { error: "check_failed", category: "sign_in_rejected", reply: "No" },
      { kind: "check_failed", category: "sign_in_rejected", reply: "No" },
    ],
    [409, { error: "no_key" }, { kind: "no_key" }],
    [
      400,
      { error: "invalid_fields", fields: ["host", "user_name"] },
      { kind: "invalid_fields", fields: ["host", "user_name"] },
    ],
    // A removed source, or one another administrator just removed.
    [404, { error: "not_found" }, { kind: "unavailable" }],
    // Only a new source can exist already.
    [409, { error: "source_exists", source_id: 3 }, { kind: "unavailable" }],
  ])("reads a %d %j", async (status, body, want) => {
    const { fetch } = answer(status, body);
    expect(await editSource("3", { name: "Anna" }, fetch)).toEqual(want);
  });

  test("reads the client's timeout as timed out", async () => {
    expect(
      await editSource("3", { password: "new" }, failing(timeout)),
    ).toEqual({ kind: "timed_out" });
  });
});

describe("checkSource", () => {
  test("posts the changed details and reads the folders", async () => {
    const { fetch, calls } = answer(200, {
      folders: [{ name: "INBOX", messages: 3 }],
    });
    expect(await checkSource("3", { port: 1993 }, fetch)).toEqual({
      kind: "ok",
      value: [{ name: "INBOX", messages: 3 }],
    });
    expect(calls[0].url).toBe("/api/sources/3/check");
    expect(calls[0].init?.method).toBe("POST");
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({ port: 1993 });
  });

  test("posts an empty object when nothing changed", async () => {
    const { fetch, calls } = answer(200, { folders: [] });
    await checkSource("3", {}, fetch);
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({});
  });

  test.each([
    [
      422,
      { error: "check_failed", category: "server_error", reply: "BAD" },
      { kind: "check_failed", category: "server_error", reply: "BAD" },
    ],
    [409, { error: "no_key" }, { kind: "no_key" }],
    [
      400,
      { error: "invalid_fields", fields: ["password"] },
      { kind: "invalid_fields", fields: ["password"] },
    ],
    [404, { error: "not_found" }, { kind: "unavailable" }],
  ])("reads a %d %j", async (status, body, want) => {
    const { fetch } = answer(status, body);
    expect(await checkSource("3", {}, fetch)).toEqual(want);
  });

  test("reads the client's timeout as timed out", async () => {
    expect(await checkSource("3", {}, failing(timeout))).toEqual({
      kind: "timed_out",
    });
  });
});

describe("pauseSource, resumeSource, and syncSource", () => {
  test.each([
    ["pause", pauseSource],
    ["resume", resumeSource],
    ["sync", syncSource],
  ] as const)(
    "%s posts with no body and reads the source",
    async (path, act) => {
      const { fetch, calls } = answer(200, SYNCING);
      const result = await act("4", fetch);
      expect(result.kind).toBe("ok");
      expect(result.kind === "ok" && result.value.state).toBe("syncing");
      expect(calls[0].url).toBe(`/api/sources/4/${path}`);
      expect(calls[0].init?.method).toBe("POST");
      expect(calls[0].init?.body).toBeUndefined();
      expect(calls[0].init?.signal).toBeInstanceOf(AbortSignal);
    },
  );

  test.each([
    [{ error: "already_syncing" }, { kind: "already_syncing" }],
    [{ error: "paused" }, { kind: "paused" }],
    [{ error: "no_key" }, { kind: "no_key" }],
  ])("sync reads a 409 %j", async (body, want) => {
    const { fetch } = answer(409, body);
    expect(await syncSource("4", fetch)).toEqual(want);
  });

  test.each([
    ["pause", pauseSource],
    ["resume", resumeSource],
  ] as const)("%s reads a sync refusal as unavailable", async (_path, act) => {
    const { fetch } = answer(409, { error: "already_syncing" });
    expect(await act("4", fetch)).toEqual({ kind: "unavailable" });
  });

  test.each([
    [
      "a marked 401",
      answer(401, { error: "no_session" }).fetch,
      "session_ended",
    ],
    ["a 404", answer(404, { error: "not_found" }).fetch, "unavailable"],
    // Only a sign-in waits long enough to call a timeout timed out.
    ["a timeout", failing(timeout), "unavailable"],
    ["a network error", failing(networkError), "unavailable"],
  ] as const)("sync reads %s as %s", async (_name, fetch, kind) => {
    expect(await syncSource("4", fetch)).toEqual({ kind });
  });
});

describe("removeSource", () => {
  test("sends DELETE and succeeds on a marked 204", async () => {
    const { fetch, calls } = answer(204, undefined);
    expect(await removeSource("3", fetch)).toEqual({ kind: "ok", value: null });
    expect(calls[0].url).toBe("/api/sources/3");
    expect(calls[0].init?.method).toBe("DELETE");
  });

  test.each([
    [
      "a marked 401",
      answer(401, { error: "no_session" }).fetch,
      "session_ended",
    ],
    ["a 404", answer(404, { error: "not_found" }).fetch, "unavailable"],
    [
      "an unmarked 204",
      answer(204, undefined, { marked: false }).fetch,
      "unavailable",
    ],
    ["a timeout", failing(timeout), "unavailable"],
  ] as const)("reads %s as %s", async (_name, fetch, kind) => {
    expect(await removeSource("3", fetch)).toEqual({ kind });
  });
});

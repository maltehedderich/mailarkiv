import { describe, expect, test } from "vitest";

import { classify, type ImportResponse } from "./classify";

// Every row of "How the client classifies a result" in contracts/http-api.md
// (FR-032, FR-033).

const HASH = "ea9449bf2f2e4ba3d3c4f3d61a1f6b8e0c3e5f8b0a9d2c7e6f5a4b3c2d1e0f9a";

const messageJSON = {
  id: 17,
  content_hash: HASH,
  size: 31,
  file_name: "invoice.eml",
  imported_at: "2026-11-06T15:50:52.123Z",
  import_count: 1,
};

const message = {
  id: 17,
  contentHash: HASH,
  size: 31,
  fileName: "invoice.eml",
  importedAt: "2026-11-06T15:50:52.123Z",
  importCount: 1,
};

type Answered = Extract<ImportResponse, { kind: "response" }>;

function marked(status: number, body: unknown): Answered {
  return {
    kind: "response",
    status,
    marked: true,
    body: typeof body === "string" ? body : JSON.stringify(body),
  };
}

function unmarked(status: number, body: unknown): Answered {
  return { ...marked(status, body), marked: false };
}

describe("a marked response with a listed outcome gives that outcome", () => {
  test.each([
    [201, "archived"],
    [200, "already_archived"],
    [200, "restored"],
  ] as const)("%i %s", (status, outcome) => {
    expect(classify(marked(status, { outcome, message: messageJSON }))).toEqual(
      { outcome, message },
    );
  });

  test("a missing file name stays missing", () => {
    const body = {
      outcome: "archived",
      message: { ...messageJSON, file_name: null },
    };
    expect(classify(marked(201, body))).toEqual({
      outcome: "archived",
      message: { ...message, fileName: null },
    });
  });

  test.each([
    [422, "empty"],
    [422, "no_mail_header"],
    [413, "too_large"],
  ] as const)("%i refused, %s", (status, reason) => {
    expect(classify(marked(status, { outcome: "refused", reason }))).toEqual({
      outcome: "refused",
      reason,
    });
  });

  test.each([
    [507, "storage_full"],
    [500, "storage_write_failed"],
    [500, "catalog_write_failed"],
  ] as const)("%i failed, %s", (status, reason) => {
    expect(classify(marked(status, { outcome: "failed", reason }))).toEqual({
      outcome: "failed",
      reason,
    });
  });
});

describe("a marked response with anything else is Unconfirmed", () => {
  test.each([
    ["an unknown outcome", 200, { outcome: "stored", message: messageJSON }],
    ["no outcome", 201, { message: messageJSON }],
    [
      "an outcome with another status",
      200,
      { outcome: "archived", message: messageJSON },
    ],
    [
      "a refusal with an unknown reason",
      422,
      { outcome: "refused", reason: "too_old" },
    ],
    [
      "a refusal with another status",
      400,
      { outcome: "refused", reason: "empty" },
    ],
    [
      "a failure with an unknown reason",
      500,
      { outcome: "failed", reason: "unknown" },
    ],
    [
      "storage full with another status",
      500,
      { outcome: "failed", reason: "storage_full" },
    ],
    ["an archived outcome with no message", 201, { outcome: "archived" }],
    [
      "a message missing its hash",
      201,
      {
        outcome: "archived",
        message: { ...messageJSON, content_hash: undefined },
      },
    ],
    [
      "a message with a size as text",
      201,
      { outcome: "archived", message: { ...messageJSON, size: "31" } },
    ],
    ["an error body", 400, { error: "bad_file_name" }],
    ["internal_error", 500, { error: "internal_error" }],
    ["an array", 201, [{ outcome: "archived", message: messageJSON }]],
    ["null", 201, null],
  ])("%s", (_name, status, body) => {
    expect(classify(marked(status, body))).toEqual({ outcome: "unconfirmed" });
  });

  test.each([
    ["an empty body", ""],
    ["HTML", "<html><body>Bad Gateway</body></html>"],
    ["cut-off JSON", '{"outcome":"archived","message":{"id":17'],
  ])("a body that doesn't parse: %s", (_name, body) => {
    expect(classify(marked(201, body))).toEqual({ outcome: "unconfirmed" });
  });
});

// A marked 401 means the session ended during the upload, so the page opens
// sign-in with the session-ended message (FR-040).
describe("a marked 401 opens sign-in", () => {
  test.each([
    ["no_session", { error: "no_session" }],
    ["an empty body", ""],
    ["a body with an outcome", { outcome: "archived", message: messageJSON }],
  ])("with %s", (_name, body) => {
    expect(classify(marked(401, body))).toEqual({ outcome: "session_ended" });
  });
});

// MailArkiv refused the request, so nothing was stored.
describe("a marked 403 is Failed", () => {
  test.each([
    ["cross_origin", { error: "cross_origin" }],
    ["no_access_rule", { error: "no_access_rule" }],
    ["an empty body", ""],
  ])("with %s", (_name, body) => {
    expect(classify(marked(403, body))).toEqual({
      outcome: "failed",
      reason: "refused_by_server",
    });
  });
});

// FR-033: a proxy refused the file as too large, so it never reached MailArkiv.
describe("an unmarked 413 is Failed, naming the proxy's limit", () => {
  test.each([
    ["nginx's page", "<html><body>413 Request Entity Too Large</body></html>"],
    ["an empty body", ""],
    [
      "a body that looks like MailArkiv's",
      { outcome: "refused", reason: "too_large" },
    ],
  ])("with %s", (_name, body) => {
    expect(classify(unmarked(413, body))).toEqual({
      outcome: "failed",
      reason: "proxy_limit",
    });
  });
});

describe("an unmarked response is Unconfirmed", () => {
  // FR-033: a proxy's timeout or bad gateway, and a 200 MailArkiv didn't send.
  test.each([
    [200, ""],
    [502, "<html><body>502 Bad Gateway</body></html>"],
    [504, "<html><body>504 Gateway Time-out</body></html>"],
  ])("%i from a proxy", (status, body) => {
    expect(classify(unmarked(status, body))).toEqual({
      outcome: "unconfirmed",
    });
  });

  test("even a 201 with a valid-looking body", () => {
    expect(
      classify(unmarked(201, { outcome: "archived", message: messageJSON })),
    ).toEqual({ outcome: "unconfirmed" });
  });

  test("even a 200 with a valid-looking body", () => {
    expect(
      classify(
        unmarked(200, { outcome: "already_archived", message: messageJSON }),
      ),
    ).toEqual({ outcome: "unconfirmed" });
  });

  test.each([500, 422, 507])("%i with a valid-looking body", (status) => {
    expect(
      classify(unmarked(status, { outcome: "failed", reason: "storage_full" })),
    ).toEqual({ outcome: "unconfirmed" });
  });
});

describe("no answer is Unconfirmed", () => {
  test.each(["network_error", "aborted", "timeout"] as const)("%s", (kind) => {
    expect(classify({ kind })).toEqual({ outcome: "unconfirmed" });
  });
});

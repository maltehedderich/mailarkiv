import { describe, expect, test } from "vitest";

import { classify, type ImportResponse } from "./classify";

// Rows from "How the client classifies a result" in contracts/http-api.md, for
// this phase. T104 adds marked 401 and 403 and unmarked 413.

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

describe("an unmarked response is Unconfirmed", () => {
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

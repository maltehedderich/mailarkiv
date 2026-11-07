import { describe, expect, it, vi } from "vitest";

import { MARKER } from "#lib/api.js";

import { classifyRefusal, fetchOriginal } from "./original";

// Expected results come from the download table in contracts/http-api.md.

const MARKED = { [MARKER]: "1" };

function answer(
  status: number,
  body: string,
  headers: Record<string, string> = MARKED,
): Response {
  return new Response(body, { status, headers });
}

describe("classifyRefusal", () => {
  it.each([
    [404, { error: "stored_original_missing" }, "stored_original_missing"],
    [404, { error: "not_found" }, "stored_original_missing"],
    [500, { error: "stored_original_damaged" }, "stored_original_damaged"],
    [500, { error: "internal_error" }, "unavailable"],
    [404, { error: "stored_original_damaged" }, "unavailable"],
    [500, null, "unavailable"],
  ])("reads a marked %i with %j as %s", (status, body, reason) => {
    expect(classifyRefusal(status, true, body)).toEqual({
      kind: "failed",
      reason,
    });
  });

  it("reads a marked 401 as an ended session", () => {
    expect(classifyRefusal(401, true, { error: "no_session" })).toEqual({
      kind: "session_ended",
    });
  });

  it("reads an unmarked answer, such as a proxy's, as unavailable", () => {
    expect(
      classifyRefusal(404, false, { error: "stored_original_missing" }),
    ).toEqual({ kind: "failed", reason: "unavailable" });
  });
});

describe("fetchOriginal", () => {
  it("returns the exact bytes and reports progress", async () => {
    const bytes = new TextEncoder().encode("Subject: hello\r\n\r\nBody.\r\n");
    const seen: number[] = [];
    const result = await fetchOriginal(
      7,
      (fraction) => seen.push(fraction),
      async (input) => {
        expect(input).toBe("/api/messages/7/original");
        return answer(200, new TextDecoder().decode(bytes), {
          ...MARKED,
          "Content-Length": String(bytes.byteLength),
        });
      },
    );

    expect(result.kind).toBe("ok");
    if (result.kind !== "ok") return;
    expect(new Uint8Array(await result.file.arrayBuffer())).toEqual(bytes);
    expect(seen.at(-1)).toBe(1);
  });

  it("reads a failure from its JSON body", async () => {
    const result = await fetchOriginal(
      7,
      () => {},
      async () => answer(500, '{"error":"stored_original_damaged"}'),
    );
    expect(result).toEqual({
      kind: "failed",
      reason: "stored_original_damaged",
    });
  });

  it("reads a network error as unavailable", async () => {
    const result = await fetchOriginal(
      7,
      () => {},
      async () => {
        throw new TypeError("Failed to fetch");
      },
    );
    expect(result).toEqual({ kind: "failed", reason: "unavailable" });
  });

  it("saves nothing when the body breaks after it starts", async () => {
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new TextEncoder().encode("Subject: hel"));
        controller.error(new TypeError("network error"));
      },
    });
    const result = await fetchOriginal(
      7,
      () => {},
      async () =>
        new Response(body, {
          status: 200,
          headers: { ...MARKED, "Content-Length": "40" },
        }),
    );
    expect(result).toEqual({ kind: "failed", reason: "interrupted" });
  });

  it("saves nothing when fewer bytes arrive than were promised", async () => {
    const result = await fetchOriginal(
      7,
      () => {},
      async () =>
        answer(200, "Subject: short", { ...MARKED, "Content-Length": "40" }),
    );
    expect(result).toEqual({ kind: "failed", reason: "interrupted" });
  });
});

describe("buffered transfer limits", () => {
  it("cancels a pending read and releases the stream without returning a file", async () => {
    const canceled = vi.fn();
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(new Uint8Array([1, 2, 3]));
      },
      cancel: canceled,
    });
    const controller = new AbortController();
    let received!: () => void;
    const progress = new Promise<void>((resolve) => {
      received = resolve;
    });
    const result = fetchOriginal(
      7,
      received,
      async () =>
        new Response(body, {
          headers: { ...MARKED, "Content-Length": "10" },
        }),
      controller.signal,
    );
    await progress;
    controller.abort();
    expect(await result).toEqual({ kind: "canceled" });
    expect(canceled).toHaveBeenCalledOnce();
    expect(body.locked).toBe(false);
  });

  it("accepts the maximum 150 MB original", async () => {
    const size = 150_000_000;
    let remaining = size;
    const body = new ReadableStream<Uint8Array>({
      pull(controller) {
        if (remaining === 0) {
          controller.close();
          return;
        }
        const length = Math.min(1_000_000, remaining);
        controller.enqueue(new Uint8Array(length).fill(37));
        remaining -= length;
      },
    });
    const result = await fetchOriginal(
      7,
      () => {},
      async () =>
        new Response(body, {
          headers: { ...MARKED, "Content-Length": String(size) },
        }),
    );
    expect(result.kind).toBe("ok");
    if (result.kind !== "ok") return;
    expect(result.file.size).toBe(size);
    expect(new Uint8Array(await result.file.slice(-1).arrayBuffer())).toEqual(
      new Uint8Array([37]),
    );
    expect(body.locked).toBe(false);
  });
});

import { afterEach, beforeEach, expect, it, vi } from "vitest";

import { MARKER } from "#lib/api.js";
import { dismissToast, toastsIn } from "#lib/toasts/store.svelte.js";

import { startDownload } from "./start";

vi.mock("$app/navigation", () => ({ goto: vi.fn() }));
vi.mock("$app/state", () => ({
  page: { url: new URL("https://example.test/") },
}));

const message = (id: number) => ({
  id,
  contentHash: String(id),
  fileName: `${id}.eml`,
});
const clicked = vi.fn();

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("document", { createElement: () => ({ click: clicked }) });
  vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:download");
  vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
});

afterEach(() => {
  for (const { key } of toastsIn("page")) dismissToast(key);
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  clicked.mockClear();
});

function cancel(id: number) {
  const toast = toastsIn("page").find(
    ({ toast }) => toast.kind !== "import" && toast.messageId === id,
  )?.toast;
  if (toast?.kind !== "downloading" && toast?.kind !== "download_waiting") {
    throw new Error(`No cancellable download for ${id}`);
  }
  toast.cancel();
}

it("buffers one distinct download, keeps one waiter, and rejects more work", async () => {
  const aborted = vi.fn();
  const requested: string[] = [];
  vi.stubGlobal("fetch", async (input: string) => {
    requested.push(input);
    return new Response(new ReadableStream<Uint8Array>({ cancel: aborted }), {
      headers: { [MARKER]: "1", "Content-Length": "150000000" },
    });
  });
  const first = startDownload(message(1));
  const second = startDownload(message(2));
  await startDownload(message(1));
  await startDownload(message(3));
  await vi.advanceTimersByTimeAsync(400);
  expect(requested).toEqual(["/api/messages/1/original"]);
  expect(toastsIn("page").map(({ toast }) => toast.kind)).toEqual([
    "download_waiting",
    "download_busy",
    "downloading",
  ]);

  cancel(1);
  await first;
  await vi.advanceTimersByTimeAsync(400);
  expect(requested).toEqual([
    "/api/messages/1/original",
    "/api/messages/2/original",
  ]);
  expect(aborted).toHaveBeenCalledOnce();
  cancel(2);
  await second;
  expect(aborted).toHaveBeenCalledTimes(2);
  expect(clicked).not.toHaveBeenCalled();
});

it("cancels a waiting request without fetching it and frees its place", async () => {
  const fetch = vi.fn(
    async () =>
      new Response(new ReadableStream<Uint8Array>(), {
        headers: { [MARKER]: "1" },
      }),
  );
  vi.stubGlobal("fetch", fetch);
  const first = startDownload(message(1));
  const second = startDownload(message(2));
  cancel(2);
  await second;
  const third = startDownload(message(3));
  await vi.advanceTimersByTimeAsync(400);
  cancel(1);
  await first;
  await vi.advanceTimersByTimeAsync(400);
  expect(fetch.mock.calls).toHaveLength(2);
  cancel(3);
  await third;
});

it("keeps at most one completed object URL and eventually releases the last", async () => {
  vi.stubGlobal(
    "fetch",
    async () =>
      new Response("mail", {
        headers: { [MARKER]: "1", "Content-Length": "4" },
      }),
  );
  vi.mocked(URL.createObjectURL)
    .mockReturnValueOnce("blob:first")
    .mockReturnValueOnce("blob:second");
  await startDownload(message(1));
  await startDownload(message(2));
  expect(clicked).toHaveBeenCalledTimes(2);
  expect(URL.revokeObjectURL).toHaveBeenCalledExactlyOnceWith("blob:first");
  await vi.advanceTimersByTimeAsync(40_000);
  expect(URL.revokeObjectURL).toHaveBeenLastCalledWith("blob:second");
  expect(URL.revokeObjectURL).toHaveBeenCalledTimes(2);
});

import { afterEach, describe, expect, it } from "vitest";

import {
  dismissToast,
  downloadKey,
  IMPORT_KEY,
  setDrawerOpen,
  showToast,
  toastsIn,
  type Toast,
} from "./store.svelte";

const failed = (messageId: number): Toast => ({
  kind: "download_failed",
  messageId,
  fileName: "a.eml",
  reason: "stored_original_damaged",
});

const unconfirmed: Toast = {
  kind: "import",
  fileName: "a.eml",
  result: { outcome: "unconfirmed" },
};

afterEach(() => {
  setDrawerOpen(false);
  for (const { key } of toastsIn("page")) dismissToast(key);
});

describe("toasts", () => {
  it("keeps one toast per key, in place, oldest first", () => {
    showToast(failed(1));
    showToast(unconfirmed);
    showToast({
      kind: "downloading",
      messageId: 1,
      fileName: "a.eml",
      fraction: 0.5,
    });

    expect(toastsIn("page").map(({ key }) => key)).toEqual([
      downloadKey(1),
      IMPORT_KEY,
    ]);
    expect(toastsIn("page")[0].toast.kind).toBe("downloading");
  });

  it("removes only the dismissed toast", () => {
    showToast(failed(1));
    showToast(failed(2));
    dismissToast(downloadKey(1));

    expect(toastsIn("page").map(({ key }) => key)).toEqual([downloadKey(2)]);
  });

  // The drawer is a modal dialog, so the page's toasts are inert while it's
  // open. A toast raised then shows in the drawer, and moves out when it closes.
  it("shows a toast raised while the drawer is open in the drawer", () => {
    showToast(unconfirmed);
    setDrawerOpen(true);
    showToast(failed(1));

    expect(toastsIn("page").map(({ key }) => key)).toEqual([IMPORT_KEY]);
    expect(toastsIn("drawer").map(({ key }) => key)).toEqual([downloadKey(1)]);

    setDrawerOpen(false);
    expect(toastsIn("drawer")).toEqual([]);
    expect(toastsIn("page").map(({ key }) => key)).toEqual([
      IMPORT_KEY,
      downloadKey(1),
    ]);
  });
});

import { createHash } from "node:crypto";
import { readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { expect, test, type Download, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// The server checks the stored file before it sends a header, so a bad copy
// is never saved as the message, and the page says why in a toast.
// journey.spec.ts covers the download that works.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const HASH = createHash("sha256").update(CONTENT).digest("hex");
const NOT_DOWNLOADED = "This message wasn't downloaded.";
// Missing and damaged originals share the same recovery step.
const NEXT_STEP =
  "Import the same file again, if you still have it, to restore the original.";
const DAMAGED =
  "The stored file can't be read or does not match its content hash.";

let server: TestServer;

test.beforeEach(async () => {
  server = await startServer();
});

test.afterEach(async () => {
  await server.stop();
});

async function archiveOneFile(page: Page) {
  await server.createAccount(EMAIL, PASSWORD);
  await page.goto(server.origin);
  await page.getByLabel("Email address").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.getByLabel("Choose a file").setInputFiles({
    name: "synthetic.eml",
    mimeType: "message/rfc822",
    buffer: CONTENT,
  });
  await page.getByRole("button", { name: "Import" }).click();
  await expect(toasts(page)).toContainText("Archived");
  // Only the download's toast is under test.
  await toasts(page).getByRole("button", { name: "Dismiss" }).click();
  await expect(toasts(page).getByRole("listitem")).toHaveCount(0);
}

function toasts(page: Page) {
  return page.getByRole("region", { name: "Notifications" });
}

function downloadButton(page: Page) {
  return page
    .getByRole("list", { name: "Messages" })
    .getByRole("button", { name: /download/i });
}

function blobPath(): string {
  return join(
    server.dataDir,
    "blobs",
    HASH.slice(0, 2),
    HASH.slice(2, 4),
    HASH,
  );
}

async function damage() {
  const damaged = Buffer.from(CONTENT);
  damaged[damaged.length - 1] ^= 0xff;
  await writeFile(blobPath(), damaged);
}

const CASES = [
  { name: "a damaged file", change: damage, status: 500, reason: DAMAGED },
  {
    name: "a deleted file",
    change: () => rm(blobPath()),
    status: 404,
    reason: "A download or repeat import found no stored file.",
  },
];

for (const { name, change, status, reason } of CASES) {
  test(`${name} shows a toast and saves nothing`, async ({ page }) => {
    await archiveOneFile(page);
    await change();

    const downloads: Download[] = [];
    page.on("download", (download) => downloads.push(download));
    const answered = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith("/original"),
    );
    const before = page.url();
    await downloadButton(page).click();

    expect((await answered).status()).toBe(status);
    const toast = toasts(page).getByRole("listitem");
    await expect(toast).toHaveCount(1);
    await expect(toast).toContainText("Download failed");
    await expect(toast).toContainText("synthetic.eml");
    await expect(toast).toContainText(`${NOT_DOWNLOADED} ${reason}`);
    await expect(toast).toContainText(NEXT_STEP);
    expect(downloads).toEqual([]);
    // The list stays as it was.
    expect(page.url()).toBe(before);
    await expect(downloadButton(page)).toBeVisible();
  });
}

// A failure doesn't leave on a timer, and trying again replaces it.
test("a failure stays until it's dismissed, and a retry replaces it", async ({
  page,
}) => {
  await page.clock.install();
  await archiveOneFile(page);
  await damage();

  await downloadButton(page).click();
  const toast = toasts(page).getByRole("listitem");
  await expect(toast).toContainText(DAMAGED);
  await page.clock.fastForward(60_000);
  await expect(toast).toHaveCount(1);

  let release!: () => void;
  let requested!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const retryRequested = new Promise<void>((resolve) => {
    requested = resolve;
  });
  await page.route("**/original", async (route) => {
    requested();
    await held;
    await route.fulfill({
      status: 404,
      headers: {
        "MailArkiv-Response": "1",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ error: "stored_original_missing" }),
    });
  });
  await downloadButton(page).click();
  await retryRequested;
  await page.clock.fastForward(400);
  await expect(toast).toContainText("Downloading");
  release();
  // A distinct result proves the retry's UI update completed before Dismiss.
  await expect(toast).toContainText(
    "A download or repeat import found no stored file.",
  );
  await expect(toast).not.toContainText(DAMAGED);
  await expect(toast).toHaveCount(1);

  // Dismissing by keyboard moves focus on purpose, not to the page.
  await toast.getByRole("button", { name: "Dismiss" }).focus();
  await page.keyboard.press("Enter");
  await expect(toast).toHaveCount(0);
  expect(
    await page.evaluate(() => document.activeElement === document.body),
  ).toBe(false);
});

// The drawer is a modal dialog, so the page's toasts are inert while it's
// open. Its own failure shows inside it, and stays after it closes.
test("a failure in the history shows in the drawer and outlasts it", async ({
  page,
}) => {
  await archiveOneFile(page);
  await damage();

  await page.getByRole("link", { name: /history of/i }).click();
  const drawer = page.getByRole("dialog", { name: "History" });
  await drawer.getByRole("button", { name: "Download" }).click();

  const inDrawer = drawer.getByRole("region", { name: "Notifications" });
  await expect(inDrawer).toContainText(DAMAGED);
  await inDrawer.getByRole("button", { name: "Dismiss" }).click();
  await expect(inDrawer.getByRole("listitem")).toHaveCount(0);

  await drawer.getByRole("button", { name: "Download" }).click();
  await expect(inDrawer).toContainText(DAMAGED);
  await drawer.getByRole("button", { name: "Close" }).click();
  await expect(drawer).toBeHidden();
  await expect(toasts(page)).toContainText(DAMAGED);
});

// An answer from something other than MailArkiv, such as a proxy's 502, says
// only that the download failed, and offers no reason it can't know.
test("an unmarked answer shows a failure with no reason", async ({ page }) => {
  await archiveOneFile(page);
  await page.route("**/original", (route) =>
    route.fulfill({ status: 502, body: "Bad Gateway" }),
  );

  await downloadButton(page).click();

  const toast = toasts(page).getByRole("listitem");
  await expect(toast).toContainText(NOT_DOWNLOADED);
  await expect(toast).toContainText(
    "Try again. If this repeats, check the server log.",
  );
  await expect(toast).not.toContainText(NEXT_STEP);
});

test("a second download waits and Cancel releases the active transfer", async ({
  page,
}) => {
  await archiveOneFile(page);
  await page.getByLabel("Choose a file").setInputFiles({
    name: "second.eml",
    mimeType: "message/rfc822",
    buffer: Buffer.from("Subject: Second\r\n\r\nSecond.\r\n"),
  });
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await expect(toasts(page)).toContainText("Archived");
  await toasts(page).getByRole("button", { name: "Dismiss" }).click();

  const requests: string[] = [];
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/original", async (route) => {
    requests.push(route.request().url());
    await held;
    await route.abort();
  });
  const buttons = downloadButton(page);
  await buttons.first().click();
  const active = toasts(page)
    .getByRole("listitem")
    .filter({ hasText: "Downloading" });
  await expect(active).toBeVisible();
  await buttons.last().click();
  const waiting = toasts(page)
    .getByRole("listitem")
    .filter({ hasText: "Download waiting" });
  await expect(waiting).toContainText(
    "Waiting for the previous download to finish.",
  );
  expect(requests).toHaveLength(1);
  await waiting.getByRole("button", { name: "Cancel", exact: true }).focus();
  const saved: Download[] = [];
  page.on("download", (download) => saved.push(download));

  await active.getByRole("button", { name: "Cancel", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(toasts(page)).toContainText("Download canceled");
  await expect(
    toasts(page)
      .getByRole("listitem")
      .filter({ hasText: "Download canceled" })
      .getByRole("button", { name: "Dismiss" }),
  ).toBeFocused();
  await expect.poll(() => requests.length).toBe(2);
  await expect(waiting).toHaveCount(0);
  await expect(active).toBeVisible();
  await active.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(
    toasts(page).getByRole("listitem").filter({ hasText: "Download canceled" }),
  ).toHaveCount(2);
  release();
  expect(saved).toEqual([]);
});

test("successive saves retain the exact bytes after the previous URL is released", async ({
  page,
}) => {
  await archiveOneFile(page);
  const completed: Download[] = [];
  for (let attempt = 0; attempt < 2; attempt++) {
    const saved = page.waitForEvent("download");
    await downloadButton(page).click();
    completed.push(await saved);
  }
  for (const download of completed) {
    expect(download.suggestedFilename()).toBe(`${HASH}.eml`);
    const path = await download.path();
    expect(await readFile(path)).toEqual(CONTENT);
  }
});

test("a focused waiting Cancel survives admission and completion moves focus to notifications", async ({
  page,
}) => {
  await archiveOneFile(page);
  await page.getByLabel("Choose a file").setInputFiles({
    name: "second.eml",
    mimeType: "message/rfc822",
    buffer: Buffer.from("Subject: Second\r\n\r\nSecond.\r\n"),
  });
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await expect(toasts(page)).toContainText("Archived");
  await toasts(page).getByRole("button", { name: "Dismiss" }).click();
  let releaseFirst!: () => void;
  let releaseSecond!: () => void;
  const first = new Promise<void>((resolve) => {
    releaseFirst = resolve;
  });
  const second = new Promise<void>((resolve) => {
    releaseSecond = resolve;
  });
  let count = 0;
  await page.route("**/original", async (route) => {
    const body = await route.fetch();
    await (++count === 1 ? first : second);
    await route.fulfill({ response: body });
  });
  await downloadButton(page).first().click();
  await downloadButton(page).last().click();
  // The original synthetic message is last in the list.
  const waiting = toasts(page)
    .getByRole("listitem")
    .filter({ hasText: "synthetic.eml" });
  await expect(waiting).toContainText("Download waiting");
  await waiting.getByRole("button", { name: "Cancel" }).focus();
  releaseFirst();
  await expect(waiting).toContainText("Downloading");
  await expect(waiting.getByRole("button", { name: "Cancel" })).toBeFocused();
  releaseSecond();
  await expect(waiting).toHaveCount(0);
  await expect(toasts(page)).toBeFocused();
});

// Toasts have no timer, so they can pile up. The stack keeps to half the
// screen and scrolls, newest first, so the page stays usable and no toast is
// pushed off the top, where it couldn't be read or dismissed.
test("a tall stack keeps to half the screen, newest first, and every toast stays reachable", async ({
  page,
}) => {
  await archiveOneFile(page);
  for (const name of ["second.eml", "third.eml", "fourth.eml"]) {
    await page.getByLabel("Choose a file").setInputFiles({
      name,
      mimeType: "message/rfc822",
      buffer: Buffer.from(`Subject: ${name}\r\n\r\n${name}\r\n`),
    });
    await page.getByRole("button", { name: "Import", exact: true }).click();
    await expect(toasts(page)).toContainText(name);
  }
  await toasts(page).getByRole("button", { name: "Dismiss" }).click();
  await page.route("**/original", (route) =>
    route.fulfill({
      status: 500,
      headers: {
        "MailArkiv-Response": "1",
        "Content-Type": "application/json",
      },
      body: JSON.stringify({ error: "stored_original_damaged" }),
    }),
  );
  // Newest row first, so synthetic.eml, the first import, fails last.
  const items = toasts(page).getByRole("listitem");
  for (let index = 0; index < 4; index++) {
    await downloadButton(page).nth(index).click();
    await expect(items).toHaveCount(index + 1);
  }

  const height = 560;
  await page.setViewportSize({ width: 360, height });
  await expect(items.first()).toContainText("synthetic.eml");
  await expect(items.last()).toContainText("fourth.eml");
  const stack = toasts(page).getByRole("list");
  const box = (await stack.boundingBox())!;
  expect(box.y).toBeGreaterThan(0);
  expect(box.height).toBeLessThanOrEqual(height / 2 + 1);

  // The oldest starts below the stack's fold. Tab reaches it and scrolls it
  // into view, and its Dismiss says which toast it closes.
  await items.nth(2).getByRole("button", { name: "Dismiss" }).focus();
  await page.keyboard.press("Tab");
  const oldest = items.last().getByRole("button", { name: "Dismiss" });
  await expect(oldest).toBeFocused();
  await expect(oldest).toHaveAccessibleDescription(/^Download failed/);
  const button = (await oldest.boundingBox())!;
  expect(button.y).toBeGreaterThanOrEqual(box.y);
  expect(button.y + button.height).toBeLessThanOrEqual(box.y + box.height);
});

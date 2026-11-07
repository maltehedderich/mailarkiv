import { createHash } from "node:crypto";
import { rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { expect, test, type Download, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// FR-023, risk 10: the server checks the stored file before it sends a header,
// so a bad copy is never saved as the message, and the page says why in a
// toast. journey.spec.ts covers the download that works.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const HASH = createHash("sha256").update(CONTENT).digest("hex");
const NOT_DOWNLOADED = "This message wasn't downloaded.";
// FR-038's next step, which both stored-original rows share.
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

// FR-023: a failure doesn't leave on a timer, and trying again replaces it.
test("a failure stays until it's dismissed, and a retry replaces it", async ({
  page,
}) => {
  await archiveOneFile(page);
  await damage();

  await downloadButton(page).click();
  const toast = toasts(page).getByRole("listitem");
  await expect(toast).toContainText(DAMAGED);
  await page.waitForTimeout(1_000);
  await expect(toast).toHaveCount(1);

  await downloadButton(page).click();
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
  const drawer = page.getByRole("dialog", { name: "Import history" });
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

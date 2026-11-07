import { createHash } from "node:crypto";
import { rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { expect, test, type Download, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// FR-023, risk 10: the server checks the stored file before it sends a header,
// so a bad copy is never saved as the message, and the browser shows a page in
// its place. journey.spec.ts covers the download that works.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const HASH = createHash("sha256").update(CONTENT).digest("hex");
const HEADING = "This message wasn't downloaded";
// FR-038's next step, which both stored-original rows share.
const NEXT_STEP =
  "Import the same file again, if you still have it, to restore the original.";

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
  await expect(page.getByRole("link", { name: /download/i })).toBeVisible();
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

const CASES = [
  {
    name: "a damaged file",
    change: async () => {
      const damaged = Buffer.from(CONTENT);
      damaged[damaged.length - 1] ^= 0xff;
      await writeFile(blobPath(), damaged);
    },
    status: 500,
    reason: "The stored file can't be read or does not match its content hash.",
  },
  {
    name: "a deleted file",
    change: () => rm(blobPath()),
    status: 404,
    reason: "A download or repeat import found no stored file.",
  },
];

for (const { name, change, status, reason } of CASES) {
  test(`${name} shows a page and saves nothing`, async ({ page }) => {
    await archiveOneFile(page);
    await change();

    const downloads: Download[] = [];
    page.on("download", (download) => downloads.push(download));
    const answered = page.waitForResponse((response) =>
      new URL(response.url()).pathname.endsWith("/original"),
    );
    await page.getByRole("link", { name: /download/i }).click();

    expect((await answered).status()).toBe(status);
    await expect(page.getByRole("heading", { name: HEADING })).toBeVisible();
    await expect(page.getByText(reason)).toBeVisible();
    await expect(page.getByText(NEXT_STEP)).toBeVisible();
    await expect(page.locator("script")).toHaveCount(0);
    expect(downloads).toEqual([]);

    // The page sits at a path under /api/, so only an absolute stylesheet link
    // loads. A link has an underline until the global styles remove it.
    const back = page.getByRole("link", { name: "Back to the list" });
    await expect(back).toHaveCSS("text-decoration-line", "none");

    await back.click();
    await expect(page).toHaveURL(`${server.origin}/`);
    await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
    await expect(page.getByRole("link", { name: /download/i })).toBeVisible();
  });
}

// The router sends a path it has no client code for back to the server, so a
// page that only the server can show must not end up reloading itself.
test("opening a failed-download page by its own address loads it once", async ({
  page,
}) => {
  let loads = 0;
  page.on("load", () => (loads += 1));

  await page.goto(`${server.origin}/download-failed/damaged`);

  await expect(page.getByRole("heading", { name: HEADING })).toBeVisible();
  await page.waitForLoadState("networkidle");
  expect(loads).toBe(1);
});

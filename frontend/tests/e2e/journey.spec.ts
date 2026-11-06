import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";

import { expect, test, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// User Story 1: sign in, import one synthetic file, see it listed, and download
// the same bytes. Scenarios 1 to 5, 7, 12, and 13.

const PASSWORD = "correct horse battery";
const MISMATCH = "That email address and password don't match.";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const CONTENT_HASH = createHash("sha256").update(CONTENT).digest("hex");

let server: TestServer;

test.beforeEach(async () => {
  server = await startServer();
});

test.afterEach(async () => {
  await server.stop();
});

async function submitSignIn(page: Page, email: string, password: string) {
  await page.getByLabel("Email address").fill(email);
  await page.getByLabel("Password").fill(password);
  const answered = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === "/api/session" &&
      response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Sign in" }).click();
  await answered;
}

test("an administrator imports one file and downloads the same bytes", async ({
  page,
}) => {
  await server.createAccount("Admin@example.test", PASSWORD);

  // Scenario 1: with no session, the app opens sign-in.
  await page.goto(server.origin);
  await expect(page).toHaveURL(`${server.origin}/sign-in`);

  // Scenario 5, FR-003: a wrong password and an unknown address look the same.
  for (const [email, password] of [
    ["admin@example.test", "wrong horse battery"],
    ["nobody@example.test", PASSWORD],
  ]) {
    await submitSignIn(page, email, password);
    await expect(page.getByText(MISMATCH)).toBeVisible();
    await expect(page).toHaveURL(`${server.origin}/sign-in`);
    await expect(page.getByText(/session ended/i)).toHaveCount(0);
  }

  // Scenario 13: the lowercase address signs in to the account created as
  // Admin@example.test.
  await submitSignIn(page, "admin@example.test", PASSWORD);
  await expect(page).toHaveURL(`${server.origin}/`);
  await expect(page.getByText("No mail yet")).toBeVisible();
  await expect(page.getByLabel("Choose a file")).toBeVisible();

  await page.getByLabel("Choose a file").setInputFiles({
    name: "synthetic.eml",
    mimeType: "message/rfc822",
    buffer: CONTENT,
  });
  await page.getByRole("button", { name: "Import" }).click();
  await expect(page.getByRole("status")).toContainText("Archived");

  // The new message is first, with its name, size, short hash, and a time
  // that ends in its UTC offset (scenario 12, FR-043).
  const row = page
    .getByRole("list", { name: "Messages" })
    .getByRole("listitem")
    .first();
  await expect(row).toContainText("synthetic.eml");
  await expect(row).toContainText(`${CONTENT.length} B`);
  await expect(row).toContainText(CONTENT_HASH.slice(0, 8));
  await expect(row.locator("time")).toHaveText(/[+-]\d\d:\d\d$/);
  await expect(page.getByText("No mail yet")).toHaveCount(0);

  // A plain link with no download attribute. The server's Content-Disposition
  // makes it a download, so the page stays where it is.
  const link = row.getByRole("link", { name: /download/i });
  expect(await link.getAttribute("download")).toBeNull();
  const before = page.url();
  const downloading = page.waitForEvent("download");
  await link.click();
  const download = await downloading;
  expect(download.suggestedFilename()).toBe(`${CONTENT_HASH}.eml`);

  const saved = await readFile(await download.path());
  expect(createHash("sha256").update(saved).digest("hex")).toBe(CONTENT_HASH);
  expect(page.url()).toBe(before);
});

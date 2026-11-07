import { expect, test } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

const PASSWORD = "correct horse battery";
const FILE = {
  name: "synthetic.eml",
  mimeType: "message/rfc822",
  buffer: Buffer.from(
    "From: sender@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
  ),
};

let server: TestServer;

test.beforeEach(async () => {
  server = await startServer();
});

test.afterEach(async () => {
  await server.stop();
});

// FR-032, Quickstart scenario 3: the refresh after the import fails too, and
// the result has to survive that, or the administrator never learns that the
// file may be archived.
test("a server that stops during an import leaves Unconfirmed on screen", async ({
  page,
}) => {
  await server.createAccount("admin@example.test", PASSWORD);

  await page.goto(server.origin);
  await page.getByLabel("Email address").fill("admin@example.test");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();

  await page.getByLabel("Choose a file").setInputFiles(FILE);
  await server.stop();
  await page.getByRole("button", { name: "Import" }).click();

  const unconfirmed = page.getByText(
    "The file may be archived. Importing it again is safe and stores no second copy.",
  );
  const failedLoad = page.getByText("This page couldn't be loaded.");
  await expect(unconfirmed).toBeVisible();
  // FR-045: the list couldn't be read, so it says so, and shows no empty list.
  await expect(failedLoad).toBeVisible();
  await expect(page.getByText("No mail yet")).toHaveCount(0);

  await page.getByRole("button", { name: "Try again" }).click();
  await expect(failedLoad).toBeVisible();
  await expect(unconfirmed).toBeVisible();
});

import { readFile } from "node:fs/promises";

import { expect, test } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// A message is found by a word from its body. The words name people and
// topics, so they never reach an address the browser, a proxy, or the
// server's log records.

const PASSWORD = "correct horse battery";
// Its body says "Der <b>Quartals</b>bericht", so the word is split by markup.
const FIXTURE = new URL(
  "../../../internal/mailparse/testdata/plain/html-only.eml",
  import.meta.url,
);
const WORD = "quartals";

let server: TestServer;

test.beforeEach(async () => {
  server = await startServer();
});

test.afterEach(async () => {
  await server.stop();
});

test("a body word finds its message, and no address holds the word", async ({
  page,
}) => {
  const requested: string[] = [];
  page.on("request", (request) => requested.push(request.url()));

  await server.createAccount("admin@example.test", PASSWORD);
  await page.goto(server.origin);
  await page.getByLabel("Email address").fill("admin@example.test");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();

  await page.getByLabel("Choose a file").setInputFiles({
    name: "report.eml",
    mimeType: "message/rfc822",
    buffer: await readFile(FIXTURE),
  });
  await page.getByRole("button", { name: "Import" }).click();
  await expect(
    page.getByRole("list", { name: "Messages" }).getByRole("listitem"),
  ).toHaveCount(1);

  // A new message is searchable within 60 s. Until then it isn't included.
  const results = page.getByRole("list", { name: "Search results" });
  await page.getByLabel("Words").fill(WORD);
  await expect(async () => {
    await page.getByRole("button", { name: "Search" }).click();
    await expect(page.getByText("1 message matches.")).toBeVisible({
      timeout: 1_000,
    });
  }).toPass({ timeout: 60_000 });

  const row = results.getByRole("listitem");
  await expect(row).toHaveCount(1);
  await expect(row).toContainText("Bernd Weber");
  await expect(row).toContainText("bernd@example.test");
  await expect(
    row.getByRole("link", { name: "History of Bericht" }),
  ).toBeVisible();
  // The file name shows only in the history.
  await expect(row).not.toContainText("report.eml");

  // The history opens over the results and closes back to them.
  await row.getByRole("link", { name: "History of Bericht" }).click();
  const drawer = page.getByRole("dialog", { name: "History" });
  await expect(drawer).toContainText("report.eml");
  await drawer.getByRole("button", { name: "Close" }).click();
  await expect(drawer).toBeHidden();
  await expect(results.getByRole("listitem")).toHaveCount(1);

  await page.getByRole("button", { name: "Clear" }).click();
  await expect(page.getByLabel("Words")).toHaveValue("");
  await expect(
    page.getByRole("list", { name: "Messages" }).getByRole("listitem"),
  ).toHaveCount(1);

  expect(page.url().toLowerCase()).not.toContain(WORD);
  expect(requested.length).toBeGreaterThan(0);
  for (const url of requested) {
    expect(url.toLowerCase()).not.toContain(WORD);
  }
});

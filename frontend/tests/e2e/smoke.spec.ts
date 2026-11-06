import { expect, test } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

let server: TestServer;

test.beforeEach(async () => {
  server = await startServer();
});

test.afterEach(async () => {
  await server.stop();
});

// ADR 0004: nothing loads from a third party at runtime.
test("the page renders and no request leaves its origin", async ({ page }) => {
  const requested: string[] = [];
  page.on("request", (request) => requested.push(request.url()));

  await page.goto(server.origin, { waitUntil: "networkidle" });

  await expect(page.getByRole("heading", { name: "MailArkiv" })).toBeVisible();
  // The page and its scripts loaded, so an empty list can't pass this test.
  expect(requested.length).toBeGreaterThan(1);
  expect(
    requested.filter((url) => new URL(url).origin !== server.origin),
  ).toEqual([]);
});

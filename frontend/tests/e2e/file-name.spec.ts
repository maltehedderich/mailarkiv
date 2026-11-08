import { expect, test, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// Risk 7, FR-021: a hostile file name, which only a direct request can send,
// shows as plain text with its bidi override as a code. No script runs, and
// the page's policy reports no violation, so nothing even tried to.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const MARKUP = `<img src=x onerror="window.__ran=1"><script>window.__ran=2</script>`;
const NAME = `${MARKUP}\u202Elmth.exe`;

declare global {
  interface Window {
    __ran?: number;
    __violations?: string[];
  }
}

let server: TestServer;

test.beforeEach(async () => {
  server = await startServer();
  await server.createAccount(EMAIL, PASSWORD);
});

test.afterEach(async () => {
  await server.stop();
});

async function signIn(page: Page) {
  await page.goto(server.origin);
  await page.getByLabel("Email address").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
}

test("a hostile name shows as text with its override as a code", async ({
  page,
}) => {
  await page.addInitScript(() => {
    window.__violations = [];
    document.addEventListener("securitypolicyviolation", (event) => {
      window.__violations?.push(event.violatedDirective);
    });
  });
  const consoleErrors: string[] = [];
  page.on("console", (message) => {
    if (message.type() === "error") consoleErrors.push(message.text());
  });
  await signIn(page);

  // The browser's session cookie goes with this request. The name goes as
  // the contract says: percent-encoded UTF-8.
  const imported = await page.request.post(`${server.origin}/api/imports`, {
    headers: { "MailArkiv-File-Name": encodeURIComponent(NAME) },
    data: CONTENT,
  });
  expect(imported.status()).toBe(201);
  await page.reload();

  const row = page
    .getByRole("list", { name: "Messages" })
    .getByRole("listitem");
  await expect(row).toHaveCount(1);
  await expect(row).toContainText(MARKUP);
  await expect(row).toContainText("U+202E");
  await expect(row).toContainText("lmth.exe");
  expect(await row.textContent()).not.toContain("\u202E");
  await expect(row.locator("img, script")).toHaveCount(0);

  // The history shows the whole name the same way.
  await row.getByRole("link", { name: /history of/i }).click();
  const drawer = page.getByRole("dialog", { name: "Import history" });
  await expect(drawer.getByRole("listitem")).toContainText(
    `${MARKUP}U+202Elmth.exe`,
  );
  await expect(drawer.locator("img, script")).toHaveCount(0);

  expect(await page.evaluate(() => window.__ran)).toBeUndefined();
  expect(await page.evaluate(() => window.__violations)).toEqual([]);
  expect(
    consoleErrors.filter((text) => /content security policy/i.test(text)),
  ).toEqual([]);
});

// A long name is cut 12 characters from its end, here just after a space. The
// part before the cut is a flex item, which dropped the space it ended with,
// so the row read "zum 1.Dezember.eml".
test("a space where a long name is cut still shows", async ({ page }) => {
  const name = "Vertrag zum 1. Dezember.eml";
  await signIn(page);
  const imported = await page.request.post(`${server.origin}/api/imports`, {
    headers: { "MailArkiv-File-Name": encodeURIComponent(name) },
    data: CONTENT,
  });
  expect(imported.status()).toBe(201);
  await page.reload();

  const width = await page
    .getByRole("link", { name: `History of ${name}` })
    .evaluate((link) => {
      const walker = document.createTreeWalker(link, NodeFilter.SHOW_TEXT);
      for (let node = walker.nextNode(); node; node = walker.nextNode()) {
        const text = node.textContent ?? "";
        if (!text.endsWith(" ")) continue;
        const space = document.createRange();
        space.setStart(node, text.length - 1);
        space.setEnd(node, text.length);
        return space.getBoundingClientRect().width;
      }
      return null;
    });
  expect(width).toBeGreaterThan(0);
});

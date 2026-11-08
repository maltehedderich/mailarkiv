import { expect, test, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// Paging: every message can be reached a page at a time, and a page past the
// end moves to the last page.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";

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

/** Imports `count` distinct messages through the API, with the page's cookie. */
async function importMany(page: Page, count: number) {
  for (let index = 1; index <= count; index++) {
    const name = `message-${String(index).padStart(2, "0")}.eml`;
    const response = await page.request.post(`${server.origin}/api/imports`, {
      headers: { "MailArkiv-File-Name": name },
      data: Buffer.from(`Subject: Message ${index}\r\n\r\nBody ${index}.\r\n`),
    });
    expect(response.status()).toBe(201);
  }
}

function pages(page: Page) {
  return page.getByRole("navigation", { name: "Pages" });
}

function rows(page: Page) {
  return page.getByRole("list", { name: "Messages" }).getByRole("listitem");
}

test("51 messages take two pages, newest first", async ({ page }) => {
  await signIn(page);
  await importMany(page, 51);
  await page.reload();

  await expect(pages(page)).toContainText("1–50 of 51");
  await expect(rows(page)).toHaveCount(50);
  await expect(rows(page).first()).toContainText("message-51.eml");
  // Screen readers may skip the dash, so they get the range in words.
  await expect(pages(page)).toContainText("Messages 1 to 50 of 51");
  // The first page has nothing before it.
  await expect(
    pages(page).getByRole("link", { name: "Previous page" }),
  ).toHaveAttribute("aria-disabled", "true");

  // SvelteKit announces the title after a navigation only when it changes,
  // so the page number in it is what screen readers hear.
  await expect(page).toHaveTitle("Messages · MailArkiv");
  await pages(page).getByRole("link", { name: "Next page" }).click();
  await expect(page).toHaveURL(`${server.origin}/?page=2`);
  await expect(page).toHaveTitle("Messages, page 2 · MailArkiv");
  await expect(pages(page)).toContainText("51–51 of 51");
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page)).toContainText("message-01.eml");
  await expect(
    pages(page).getByRole("link", { name: "Next page" }),
  ).toHaveAttribute("aria-disabled", "true");

  await pages(page).getByRole("link", { name: "Previous page" }).click();
  await expect(page).toHaveURL(`${server.origin}/`);
  await expect(page).toHaveTitle("Messages · MailArkiv");
  await expect(rows(page)).toHaveCount(50);
});

// DESIGN.md: the pager sits in the sticky header. A turn goes to the top of
// the page, where the next one is.
for (const width of [1280, 360]) {
  test(`at ${width} px the pager stays in view, scrolled and after a turn`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 720 });
    await signIn(page);
    await importMany(page, 51);
    await page.reload();
    await expect(rows(page)).toHaveCount(50);

    await page.evaluate(() =>
      window.scrollTo(0, document.documentElement.scrollHeight),
    );
    const next = pages(page).getByRole("link", { name: "Next page" });
    await expect(next).toBeInViewport();
    await next.click();
    await expect(page).toHaveURL(`${server.origin}/?page=2`);
    await expect(
      pages(page).getByRole("link", { name: "Previous page" }),
    ).toBeInViewport();
  });
}

// WCAG 2.4.11: focus moving up the list scrolls each control clear of the
// sticky header.
test("focus moving up the list stays clear of the header", async ({ page }) => {
  await signIn(page);
  await importMany(page, 51);
  await page.reload();
  await expect(rows(page)).toHaveCount(50);

  await page
    .getByRole("link", { name: /history of/i })
    .last()
    .focus();
  for (let presses = 0; presses < 40; presses++) {
    await page.keyboard.press("Shift+Tab");
    const { top, headerBottom } = await page.evaluate(() => ({
      top: document.activeElement!.getBoundingClientRect().top,
      headerBottom: document
        .querySelector("main > header")!
        .getBoundingClientRect().bottom,
    }));
    expect(top).toBeGreaterThanOrEqual(headerBottom);
  }
});

// The pager shows the page being loaded at once, so a turn shows where it
// went, and the arrows stay for the next one while the rows load.
test("a turn shows its range before the rows arrive", async ({ page }) => {
  await signIn(page);
  await importMany(page, 51);
  await page.reload();
  await expect(rows(page)).toHaveCount(50);

  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route(
    (url) =>
      url.pathname === "/api/messages" && url.searchParams.get("page") === "2",
    async (route) => {
      await held;
      await route.fallback();
    },
  );
  await pages(page).getByRole("link", { name: "Next page" }).click();

  await expect(pages(page)).toContainText("51–51 of 51");
  await expect(page.getByRole("list", { name: "Messages" })).toHaveCount(0);
  await expect(
    pages(page).getByRole("link", { name: "Previous page" }),
  ).not.toHaveAttribute("aria-disabled");
  release();
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page)).toContainText("message-01.eml");
});

// Page 1 has no parameter, as in the pager's links.
test("a page past the end of one page moves to the list", async ({ page }) => {
  await signIn(page);
  await importMany(page, 1);

  await page.goto(`${server.origin}/?page=3`);

  await expect(page).toHaveURL(`${server.origin}/`);
  await expect(rows(page)).toHaveCount(1);
  await expect(pages(page)).toContainText("1–1 of 1");
});

test("a page past the end moves to the last page", async ({ page }) => {
  await signIn(page);
  await importMany(page, 51);

  await page.goto(`${server.origin}/?page=9`);

  await expect(page).toHaveURL(`${server.origin}/?page=2`);
  await expect(rows(page)).toHaveCount(1);
  await expect(pages(page)).toContainText("51–51 of 51");
});

test("an import from a later page shows the newest message on page 1", async ({
  page,
}) => {
  await signIn(page);
  await importMany(page, 51);
  await page.goto(`${server.origin}/?page=2`);
  await expect(rows(page)).toHaveCount(1);

  await page.getByLabel("Choose a file").setInputFiles({
    name: "newest.eml",
    mimeType: "message/rfc822",
    buffer: Buffer.from("Subject: Newest\r\n\r\nLast in.\r\n"),
  });
  await page.getByRole("button", { name: "Import", exact: true }).click();

  await expect(
    page.getByRole("region", { name: "Notifications" }),
  ).toContainText("Archived");
  await expect(page).toHaveURL(`${server.origin}/`);
  await expect(rows(page).first()).toContainText("newest.eml");
  await expect(pages(page)).toContainText("1–50 of 52");
});

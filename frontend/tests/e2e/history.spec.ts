import { createHash } from "node:crypto";
import { readdir } from "node:fs/promises";
import { join } from "node:path";

import { expect, test, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// User Story 4 scenario 2, FR-020, FR-041, and T098: a message's import history
// opens from its row, by pointer or keyboard, from an address that survives Back
// and sign-in.

const PASSWORD = "correct horse battery";
const NEW_PASSWORD = "a different horse battery";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const HASH = createHash("sha256").update(CONTENT).digest("hex");
const FAILED_LOAD = "This page couldn't be loaded.";

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
  await page.getByRole("button", { name: "Sign in" }).click();
}

async function signIn(page: Page, createdAs = "admin@example.test") {
  await server.createAccount(createdAs, PASSWORD);
  await page.goto(server.origin);
  await submitSignIn(page, createdAs, PASSWORD);
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
}

async function importFile(page: Page, name: string) {
  await page.getByLabel("Choose a file").setInputFiles({
    name,
    mimeType: "message/rfc822",
    buffer: CONTENT,
  });
  await page.getByRole("button", { name: "Import" }).click();
}

/** Signs in and imports one file, so one row is on the list. */
async function signInWithOneMessage(page: Page) {
  await signIn(page);
  await importFile(page, "synthetic.eml");
  await expect(rowLink(page)).toBeVisible();
}

function rowLink(page: Page) {
  return page.getByRole("link", { name: /history of/i });
}

function drawer(page: Page) {
  return page.getByRole("dialog", { name: "Import history" });
}

/** Waits for the drawer's slide-in to end, so measurements see where it settles. */
async function settled(page: Page) {
  await drawer(page).evaluate((dialog) =>
    Promise.all(dialog.getAnimations().map(({ finished }) => finished)).then(
      () => undefined,
    ),
  );
}

test("a repeat import adds history and no copy", async ({ page }) => {
  await signIn(page, "Admin@example.test");
  const messages = page.getByRole("list", { name: "Messages" });

  await importFile(page, "first.eml");
  await expect(rowLink(page)).toBeVisible();
  await importFile(page, "second.eml");
  await expect(messages).toContainText("2×");
  await importFile(page, "second.eml");
  await expect(messages).toContainText("3×");

  await expect(messages.getByRole("listitem")).toHaveCount(1);
  const stored = await readdir(join(server.dataDir, "blobs"), {
    recursive: true,
    withFileTypes: true,
  });
  expect(stored.filter((entry) => entry.isFile())).toHaveLength(1);

  await rowLink(page).click();
  await expect(drawer(page)).toBeVisible();

  // The full hash, as 16 groups of 4 in 4 columns.
  const groups = drawer(page).locator("code > span");
  await expect(groups).toHaveCount(16);
  await settled(page);
  expect((await groups.allTextContents()).join("")).toBe(HASH);
  const [first, fifth] = await Promise.all([
    groups.nth(0).boundingBox(),
    groups.nth(4).boundingBox(),
  ]);
  expect(fifth?.x).toBe(first?.x);
  expect(fifth?.y).toBeGreaterThan(first?.y ?? Infinity);

  await expect(drawer(page)).toContainText(`${CONTENT.length} bytes`);

  // Newest first, each with its source, time, account, and file name.
  const imports = drawer(page).getByRole("listitem");
  await expect(imports).toHaveCount(3);
  for (const [index, name] of [
    "second.eml",
    "second.eml",
    "first.eml",
  ].entries()) {
    const entry = imports.nth(index);
    await expect(entry).toContainText(name);
    await expect(entry).toContainText("Manual import");
    await expect(entry).toContainText("admin@example.test");
    await expect(entry.locator("time")).toHaveAttribute(
      "datetime",
      /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}/,
    );
  }
});

test("Copy puts the 64 characters on the clipboard", async ({
  page,
  context,
}) => {
  await signInWithOneMessage(page);
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await rowLink(page).click();

  await drawer(page).getByRole("button", { name: /copy/i }).click();

  await expect(drawer(page).getByText("Hash copied.")).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(HASH);
});

test("Enter opens the history, and Escape returns to the row", async ({
  page,
}) => {
  await signInWithOneMessage(page);
  await rowLink(page).focus();

  // The key is held until the drawer is open, so the press can't reach Close.
  await page.keyboard.down("Enter");
  await expect(drawer(page)).toBeVisible();
  await page.keyboard.up("Enter");
  await expect(drawer(page)).toBeVisible();
  await expect(page).toHaveURL(/\?message=\d+$/);
  await expect(
    drawer(page).getByRole("button", { name: "Close" }),
  ).toBeFocused();

  await page.keyboard.press("Escape");
  await expect(drawer(page)).toBeHidden();
  await expect(page).toHaveURL(`${server.origin}/`);
  await expect(rowLink(page)).toBeFocused();
});

test("a click opens the history, and Close returns to the row", async ({
  page,
}) => {
  await signInWithOneMessage(page);

  await rowLink(page).click();
  await expect(drawer(page)).toBeVisible();

  await drawer(page).getByRole("button", { name: "Close" }).click();
  await expect(drawer(page)).toBeHidden();
  await expect(page).toHaveURL(`${server.origin}/`);
  await expect(rowLink(page)).toBeFocused();
});

// The drawer opens once its history has arrived. Until then the row shows the
// click, and it stays marked while the drawer is open.
test("the row stays marked from the click until the drawer closes", async ({
  page,
}) => {
  await signInWithOneMessage(page);
  const row = page
    .getByRole("list", { name: "Messages" })
    .getByRole("listitem");
  const fill = () =>
    row.evaluate((item) => getComputedStyle(item).backgroundColor);
  await page.mouse.move(0, 0);
  const rest = await fill();

  let release = () => {};
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route(/\/api\/messages\/\d+$/, async (route) => {
    await held;
    await route.fallback();
  });
  await rowLink(page).click();
  await page.mouse.move(0, 0);
  await expect.poll(fill).not.toBe(rest);
  await expect(drawer(page)).toBeHidden();
  const marked = await fill();

  release();
  await expect(drawer(page)).toBeVisible();
  expect(await fill()).toBe(marked);

  await drawer(page).getByRole("button", { name: "Close" }).click();
  await expect(drawer(page)).toBeHidden();
  await expect.poll(fill).not.toBe(marked);
});

test("the row's download button saves the file and opens nothing", async ({
  page,
}) => {
  await signInWithOneMessage(page);

  const saved = page.waitForEvent("download");
  await page.getByRole("button", { name: /download/i }).click();
  await saved;

  await expect(drawer(page)).toBeHidden();
  await expect(page).toHaveURL(`${server.origin}/`);
});

test("the drawer's Download saves the file and stays open", async ({
  page,
}) => {
  await signInWithOneMessage(page);
  await rowLink(page).click();

  const saved = page.waitForEvent("download");
  await drawer(page).getByRole("button", { name: "Download" }).click();

  expect((await saved).suggestedFilename()).toBe(`${HASH}.eml`);
  await expect(drawer(page)).toBeVisible();
});

test("Back closes the history, and Forward opens it again", async ({
  page,
}) => {
  await signInWithOneMessage(page);
  await rowLink(page).click();
  await expect(drawer(page)).toBeVisible();

  await page.goBack();
  await expect(drawer(page)).toBeHidden();
  await expect(page).toHaveURL(`${server.origin}/`);

  await page.goForward();
  await expect(drawer(page)).toBeVisible();
  await expect(page).toHaveURL(/\?message=\d+$/);
});

test("an address with the message opens its history, and closing keeps the page", async ({
  page,
}) => {
  await signInWithOneMessage(page);
  await rowLink(page).click();
  await expect(drawer(page)).toBeVisible();
  const id = new URL(page.url()).searchParams.get("message");

  await page.goto(`${server.origin}/?page=1&message=${id}`);
  await expect(drawer(page)).toBeVisible();

  await page.keyboard.press("Escape");
  await expect(drawer(page)).toBeHidden();
  await expect(page).toHaveURL(`${server.origin}/?page=1`);
  await expect(rowLink(page)).toBeFocused();
});

test("the history reopens after a session ends and sign-in", async ({
  page,
}) => {
  await signInWithOneMessage(page);
  await rowLink(page).click();
  await expect(drawer(page)).toBeVisible();
  const address = page.url();

  await server.setPassword("admin@example.test", NEW_PASSWORD);
  await page.reload();
  await expect(page).toHaveURL(/\/sign-in\?/);

  await submitSignIn(page, "admin@example.test", NEW_PASSWORD);
  await expect(page).toHaveURL(address);
  await expect(drawer(page)).toBeVisible();
});

// FR-045: the drawer's data didn't arrive, so it says so, and Close still works.
for (const id of ["999", "abc"]) {
  test(`message ${id} shows the failed-load state in the drawer`, async ({
    page,
  }) => {
    await signInWithOneMessage(page);

    await page.goto(`${server.origin}/?message=${id}`);

    await expect(drawer(page).getByText(FAILED_LOAD)).toBeVisible();
    await expect(
      drawer(page).getByRole("button", { name: "Try again" }),
    ).toBeVisible();
    await expect(drawer(page).getByRole("link")).toHaveCount(0);

    await drawer(page).getByRole("button", { name: "Close" }).click();
    await expect(drawer(page)).toBeHidden();
    await expect(page).toHaveURL(`${server.origin}/`);
  });
}

// DESIGN.md: Try again's button goes when the data arrives, and focus stays.
test("Try again loads the history and keeps focus in the drawer", async ({
  page,
}) => {
  await signInWithOneMessage(page);
  await rowLink(page).click();
  await expect(drawer(page)).toBeVisible();
  const pathname = `/api/messages/${new URL(page.url()).searchParams.get("message")}`;

  await page.route(`**${pathname}`, (route) => route.abort());
  await page.reload();
  await expect(drawer(page).getByText(FAILED_LOAD)).toBeVisible();

  await page.unroute(`**${pathname}`);
  await drawer(page).getByRole("button", { name: "Try again" }).click();

  await expect(drawer(page).getByRole("listitem")).toHaveCount(1);
  await expect(
    drawer(page).getByRole("button", { name: "Close" }),
  ).toBeFocused();
});

// The spec's edge case: the history shows a long name in full. SC-008: at 360 px
// nothing scrolls sideways. DESIGN: Download stays in view on a long history.
test("at 360 px a long name fits, and Download stays in view", async ({
  page,
}) => {
  const longName = `${"quarterly-statement-".repeat(8)}final.eml`;
  await signIn(page);
  await importFile(page, longName);
  await expect(rowLink(page)).toBeVisible();
  for (const count of [2, 3, 4, 5, 6]) {
    await importFile(page, longName);
    await expect(page.getByRole("list", { name: "Messages" })).toContainText(
      `${count}×`,
    );
  }

  await rowLink(page).click();
  await expect(drawer(page)).toBeVisible();
  await page.setViewportSize({ width: 360, height: 480 });

  await expect(drawer(page).getByRole("listitem").first()).toContainText(
    longName,
  );
  await settled(page);
  const widths = await drawer(page).evaluate((dialog) => ({
    dialog: dialog.getBoundingClientRect().width,
    content: Math.max(
      ...[dialog, ...dialog.querySelectorAll("*")].map(
        (element) => element.getBoundingClientRect().right,
      ),
    ),
  }));
  expect(widths.dialog).toBe(360);
  expect(widths.content).toBeLessThanOrEqual(360);

  await drawer(page).getByRole("listitem").last().scrollIntoViewIfNeeded();
  await expect(drawer(page).getByRole("listitem").last()).toBeInViewport();
  await expect(
    drawer(page).getByRole("button", { name: "Download" }),
  ).toBeInViewport();
});

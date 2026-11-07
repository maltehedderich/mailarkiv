import { createHash } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";

import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Locator, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// FR-041, FR-042, FR-045, SC-008, and risk 17: every screen works by keyboard,
// at 360 px with no sideways scroll, and meets WCAG 2.2 AA in both themes. Data
// that didn't arrive says so and offers Try again, and never looks empty.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const HASH = createHash("sha256").update(CONTENT).digest("hex");
const LONG_NAME = `${"quarterly-statement-".repeat(8)}final.eml`;
const FAILED_LOAD = "This page couldn't be loaded.";
const WCAG_22_AA = [
  "wcag2a",
  "wcag2aa",
  "wcag21a",
  "wcag21aa",
  "wcag22a",
  "wcag22aa",
];

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

async function importFile(page: Page, name: string, buffer = CONTENT) {
  await page
    .getByLabel("Choose a file")
    .setInputFiles({ name, mimeType: "message/rfc822", buffer });
  await page.getByRole("button", { name: "Import", exact: true }).click();
  await expect(
    page.getByRole("region", { name: "Notifications" }),
  ).toContainText(/Archived|Already archived/);
}

function rowLink(page: Page) {
  return page.getByRole("link", { name: /history of/i }).first();
}

function drawer(page: Page) {
  return page.getByRole("dialog", { name: "Import history" });
}

/** Presses Tab until the target has focus, as a keyboard user would. */
async function tabTo(page: Page, target: Locator) {
  for (let presses = 0; presses < 40; presses++) {
    if (
      await target.evaluate((element) => element === document.activeElement)
    ) {
      return;
    }
    await page.keyboard.press("Tab");
  }
  throw new Error(`Tab never reached ${target}`);
}

// SC-008: sign-in to download by keyboard alone.
test("the whole flow works by keyboard", async ({ page }) => {
  await page.goto(server.origin);
  await tabTo(page, page.getByLabel("Email address"));
  await page.keyboard.type(EMAIL);
  await tabTo(page, page.getByLabel("Password"));
  await page.keyboard.type(PASSWORD);
  await page.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();

  await tabTo(page, page.getByLabel("Choose a file"));
  const chooser = page.waitForEvent("filechooser");
  await page.keyboard.press("Space");
  await (
    await chooser
  ).setFiles({
    name: "keyboard.eml",
    mimeType: "message/rfc822",
    buffer: CONTENT,
  });
  const importButton = page.getByRole("button", {
    name: "Import",
    exact: true,
  });
  await tabTo(page, importButton);
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("region", { name: "Notifications" }),
  ).toContainText("Archived");
  // Focus moved on purpose when the Import button went away.
  expect(
    await page.evaluate(() => document.activeElement === document.body),
  ).toBe(false);

  await tabTo(page, rowLink(page));
  await page.keyboard.press("Enter");
  await expect(drawer(page)).toBeVisible();
  await expect(
    drawer(page).getByRole("button", { name: "Close" }),
  ).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(drawer(page)).toBeHidden();
  await expect(rowLink(page)).toBeFocused();

  // The row's second stop is its download.
  await page.keyboard.press("Tab");
  await expect(
    page.getByRole("button", { name: "Download keyboard.eml" }),
  ).toBeFocused();
  const download = page.waitForEvent("download");
  await page.keyboard.press("Enter");
  expect((await download).suggestedFilename()).toBe(`${HASH}.eml`);
});

/**
 * Opens every screen in turn and runs check on each: sign-in and its error,
 * the list with an import result, the import box with a choice and with a
 * refusal, the history, status with a failure and an unreferenced original,
 * the failed-download toast, and the failed-load state.
 */
async function visitEveryScreen(
  page: Page,
  check: (screen: string) => Promise<void>,
) {
  // What a crash leaves, so status has an unreferenced original to list.
  const other = Buffer.from("Subject: Left over\r\n\r\nNo catalog entry.\r\n");
  const otherHash = createHash("sha256").update(other).digest("hex");
  const blob = join(
    server.dataDir,
    "blobs",
    otherHash.slice(0, 2),
    otherHash.slice(2, 4),
    otherHash,
  );
  await mkdir(dirname(blob), { recursive: true });
  await writeFile(blob, other);
  await server.restart();

  await page.goto(server.origin);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await check("sign-in");
  await page.getByLabel("Email address").fill(EMAIL);
  await page.getByLabel("Password").fill("not the password");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(
    page.getByText("That email address and password don't match."),
  ).toBeVisible();
  await check("sign-in with an error");

  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
  await importFile(page, "short.eml");
  await importFile(
    page,
    LONG_NAME,
    Buffer.from("Subject: Long\r\n\r\nName.\r\n"),
  );
  await check("the list with an import result");

  await page.getByLabel("Choose a file").setInputFiles({
    name: LONG_NAME,
    mimeType: "message/rfc822",
    buffer: CONTENT,
  });
  await check("the import box with a chosen file");
  await page.getByLabel("Choose a file").setInputFiles({
    name: "empty.eml",
    mimeType: "message/rfc822",
    buffer: Buffer.alloc(0),
  });
  await expect(
    page.getByText("This file is empty. Nothing was uploaded."),
  ).toBeVisible();
  await check("the import box with a refusal");

  await rowLink(page).click();
  await expect(drawer(page).getByRole("listitem")).toHaveCount(1);
  await check("the history");
  await drawer(page).getByRole("button", { name: "Close" }).click();

  // Damage the short file's stored copy, then download it.
  await writeFile(
    join(server.dataDir, "blobs", HASH.slice(0, 2), HASH.slice(2, 4), HASH),
    "Subject: damaged\r\n\r\n",
  );
  await page.getByRole("button", { name: "Download short.eml" }).click();
  await expect(
    page
      .getByRole("region", { name: "Notifications" })
      .getByText(/This message wasn't downloaded/),
  ).toBeVisible();
  await check("the failed-download toast");

  await page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: /^Status/ })
    .click();
  await expect(page.getByText("Stored original damaged")).toBeVisible();
  await expect(
    page
      .getByRole("region", { name: "Unreferenced originals" })
      .getByRole("listitem"),
  ).toHaveCount(1);
  await check("status");

  await page.route("**/api/messages*", (route) => route.abort());
  await page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: "Messages" })
    .click();
  await expect(page.getByText(FAILED_LOAD)).toBeVisible();
  await check("the failed-load state");
  await page.unroute("**/api/messages*");
}

async function expectNoAxeViolations(page: Page, screen: string) {
  // A toast fades in and the drawer slides in. Mid-way, colours are lighter
  // than they rest at, so measure once they've arrived. A cancelled animation
  // belongs to an element that has gone, so it has nothing to wait for.
  await page.evaluate(() =>
    Promise.allSettled(
      document.getAnimations().map((animation) => animation.finished),
    ),
  );
  const { violations } = await new AxeBuilder({ page })
    .withTags(WCAG_22_AA)
    .analyze();
  expect(
    violations.map(({ id, nodes }) => ({
      id,
      targets: nodes.map((node) => node.target.join(" ")),
    })),
    screen,
  ).toEqual([]);
}

async function expectNoSidewaysScroll(page: Page, screen: string) {
  const widths = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }));
  expect(widths.scroll, screen).toBeLessThanOrEqual(widths.viewport);
}

for (const colorScheme of ["light", "dark"] as const) {
  for (const width of [1280, 360]) {
    test(`every screen meets WCAG 2.2 AA in the ${colorScheme} theme at ${width} px`, async ({
      page,
    }) => {
      await page.emulateMedia({ colorScheme });
      await page.setViewportSize({ width, height: 800 });
      await visitEveryScreen(page, async (screen) => {
        await expectNoSidewaysScroll(page, screen);
        await expectNoAxeViolations(page, screen);
      });
    });
  }
}

// FR-045: data that didn't arrive shows the failed-load state, never an empty
// list or screen, and Try again loads it once the failure is gone.
const FAILURES = {
  "a server error": {
    status: 500,
    headers: { "MailArkiv-Response": "1" },
    contentType: "application/json",
    body: '{"error":"internal_error"}',
  },
  "a network error": null,
} as const;

for (const [failure, answer] of Object.entries(FAILURES)) {
  test.describe(`after ${failure}`, () => {
    async function fail(page: Page, pattern: string) {
      await page.route(pattern, (route) =>
        answer === null ? route.abort() : route.fulfill(answer),
      );
    }

    test("the list shows the failed-load state and loads again", async ({
      page,
    }) => {
      await signIn(page);
      await fail(page, "**/api/messages*");

      await page.reload();
      await expect(page.getByText(FAILED_LOAD)).toBeVisible();
      await expect(page.getByText("No mail yet")).toHaveCount(0);
      await expect(page.getByRole("list", { name: "Messages" })).toHaveCount(0);

      await page.unroute("**/api/messages*");
      await page.getByRole("button", { name: "Try again" }).click();
      await expect(page.getByText("No mail yet")).toBeVisible();
      await expect(page.getByText(FAILED_LOAD)).toHaveCount(0);
    });

    test("status shows the failed-load state and loads again", async ({
      page,
    }) => {
      await signIn(page);
      await fail(page, "**/api/status");

      await page.goto(`${server.origin}/status`);
      await expect(page.getByText(FAILED_LOAD)).toBeVisible();
      await expect(page.getByRole("main").getByRole("region")).toHaveCount(0);

      await page.unroute("**/api/status");
      await page.getByRole("button", { name: "Try again" }).click();
      await expect(
        page.getByRole("region", { name: "Failures" }),
      ).toBeVisible();
      await expect(page.getByText(FAILED_LOAD)).toHaveCount(0);
    });

    test("the history shows the failed-load state and loads again", async ({
      page,
    }) => {
      await signIn(page);
      await importFile(page, "synthetic.eml");
      await rowLink(page).click();
      await expect(drawer(page)).toBeVisible();
      const id = new URL(page.url()).searchParams.get("message");
      await fail(page, `**/api/messages/${id}`);

      await page.reload();
      await expect(drawer(page).getByText(FAILED_LOAD)).toBeVisible();
      await expect(drawer(page).getByRole("listitem")).toHaveCount(0);

      await page.unroute(`**/api/messages/${id}`);
      await drawer(page).getByRole("button", { name: "Try again" }).click();
      await expect(drawer(page).getByRole("listitem")).toHaveCount(1);
      await expect(drawer(page).getByText(FAILED_LOAD)).toHaveCount(0);
    });

    test("sign-in says it didn't finish, and not that the password is wrong", async ({
      page,
    }) => {
      await page.goto(server.origin);
      await fail(page, "**/api/session");

      await page.getByLabel("Email address").fill(EMAIL);
      await page.getByLabel("Password").fill(PASSWORD);
      await page.getByRole("button", { name: "Sign in" }).click();

      await expect(
        page.getByText(
          "Sign-in didn't finish. Try again. If this repeats, check the server log.",
        ),
      ).toBeVisible();
      await expect(
        page.getByText("That email address and password don't match."),
      ).toHaveCount(0);
      await expect(page).toHaveURL(`${server.origin}/sign-in`);
    });

    test("sign-out says it didn't finish, and stays signed in", async ({
      page,
    }) => {
      await signIn(page);
      await page.route("**/api/session", (route) =>
        route.request().method() === "DELETE"
          ? answer === null
            ? route.abort()
            : route.fulfill(answer)
          : route.fallback(),
      );

      await page.getByRole("button", { name: "Sign out" }).click();

      await expect(
        page.getByText(
          "Sign-out didn't finish, so you're still signed in. Try again.",
        ),
      ).toBeVisible();
      await expect(page).toHaveURL(`${server.origin}/`);
      await expect(
        page.getByRole("heading", { name: "Messages" }),
      ).toBeVisible();
    });
  });
}

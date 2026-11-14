import { createHash } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";

import AxeBuilder from "@axe-core/playwright";
import { expect, test, type Locator, type Page } from "@playwright/test";

import { startImap, type ImapServer } from "./support/imap";
import { startServer, type TestServer } from "./support/server";
import {
  connect,
  expectState,
  folderChoice,
  MAILBOX,
  REMOVAL_WARNING,
} from "./support/sources";

// Every screen works by keyboard, at 360 px with no sideways scroll, and
// meets WCAG 2.2 AA in both themes. Data that didn't arrive says so and
// offers Try again, and never looks empty.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const HASH = createHash("sha256").update(CONTENT).digest("hex");
const LONG_NAME = `${"quarterly-statement-".repeat(8)}final.eml`;
const FAILED_LOAD = "This page couldn't be loaded.";
/** The error page's heading when a page's code didn't arrive. */
const PAGE_FAILED = "This page couldn't be loaded";
/** The code SvelteKit fetches for a page the tab hasn't opened yet. */
const PAGE_CODE = "**/_app/immutable/nodes/**";
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
  return page.getByRole("dialog", { name: "History" });
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

// Sign-in to download by keyboard alone.
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
  // Focus moved on purpose when the Import button turned off.
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

  await page.goto(`${server.origin}/no/such/page`);
  await expect(
    page.getByRole("heading", { name: "Page not found" }),
  ).toBeVisible();
  await check("the unknown-address page");

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

  // Status's code hasn't loaded yet, so this navigation fails. The toast
  // stays on the error page that replaces the signed-in layout.
  await page.route(PAGE_CODE, (route) => route.abort());
  await page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: /^Status/ })
    .click();
  await expect(page.getByRole("heading", { name: PAGE_FAILED })).toBeVisible();
  await expect(
    page
      .getByRole("region", { name: "Notifications" })
      .getByText(/This message wasn't downloaded/),
  ).toBeVisible();
  await check("the page that didn't load");
  await page.unroute(PAGE_CODE);
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(page.getByText("Stored original damaged")).toBeVisible();
  await expect(
    page
      .getByRole("region", { name: "Problems" })
      .getByRole("listitem")
      .filter({
        has: page.getByRole("heading", {
          name: "Unreferenced original",
          exact: true,
        }),
      })
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

test.describe("the sources screens", () => {
  let imap: ImapServer;

  // Collection needs a key and a mail server, so this server replaces the
  // one every test starts. The top-level afterEach stops it.
  test.beforeEach(async () => {
    await server.stop();
    imap = await startImap({
      ...MAILBOX,
      folders: [
        { name: "INBOX", messages: 2 },
        { name: "Gesendete Objekte", messages: 1 },
      ],
    });
    server = await startServer({ collection: { caFile: imap.caFile } });
    await server.createAccount(EMAIL, PASSWORD);
  });

  test.afterEach(async () => {
    await imap.stop();
  });

  /**
   * Opens each sources screen in turn and runs check on each: the empty
   * list, the add form plain, with field problems, and with a failed check,
   * the folder choice, a source, its edit form, its remove confirmation, and
   * the list with the source.
   */
  async function visitSourceScreens(
    page: Page,
    check: (screen: string) => Promise<void>,
  ) {
    await signIn(page);
    await page.goto(`${server.origin}/sources`);
    await expect(
      page.getByRole("heading", { name: "No sources yet" }),
    ).toBeVisible();
    await check("the empty sources list");

    await page.getByRole("link", { name: "Add a source" }).click();
    await expect(
      page.getByRole("heading", { name: "Connect to the mailbox" }),
    ).toBeVisible();
    await check("the add form");

    await page.getByLabel("Name", { exact: true }).fill("Anna");
    await page.getByRole("button", { name: "Connect" }).click();
    await expect(page.getByLabel("Server", { exact: true })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    await check("the add form with field problems");

    await connect(page, imap, {
      name: "Anna",
      password: "not the mailbox password",
    });
    await expect(
      page.getByText("Sign-in rejected", { exact: true }),
    ).toBeVisible();
    await check("the add form with a failed check");

    await connect(page, imap, { name: "Anna" });
    await expect(folderChoice(page).getByRole("checkbox")).toHaveCount(2);
    await check("the folder choice");

    await page.getByRole("button", { name: "Save" }).click();
    await expectState(page, "Waiting", 20_000);
    await check("a source");

    await page.getByRole("button", { name: "Edit" }).click();
    await page.getByRole("button", { name: "Add folders" }).click();
    await expect(page.getByText(REMOVAL_WARNING)).toBeVisible();
    await check("the edit form");
    await page.getByRole("button", { name: "Cancel" }).click();

    await page.getByRole("button", { name: "Remove", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Remove Anna?" });
    await expect(dialog).toBeVisible();
    await check("the remove confirmation");
    await dialog.getByRole("button", { name: "Cancel" }).click();

    await page.goto(`${server.origin}/sources`);
    await expect(page.getByRole("link", { name: "Anna" })).toBeVisible();
    await check("the sources list");
  }

  for (const colorScheme of ["light", "dark"] as const) {
    for (const width of [1280, 360]) {
      test(`meet WCAG 2.2 AA in the ${colorScheme} theme at ${width} px`, async ({
        page,
      }) => {
        await page.emulateMedia({ colorScheme });
        await page.setViewportSize({ width, height: 800 });
        await visitSourceScreens(page, async (screen) => {
          await expectNoSidewaysScroll(page, screen);
          await expectNoAxeViolations(page, screen);
        });
      });
    }
  }
});

// DESIGN.md: beside the sidebar, rows take two lines up to 1040 px, so the
// name has room. At 721 px one line used to leave the name no width at all.
for (const width of [721, 1041]) {
  test(`at ${width} px beside the sidebar, a row shows its whole name`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 800 });
    await signIn(page);
    await importFile(page, "statement-29.eml");

    const row = page
      .getByRole("list", { name: "Messages" })
      .getByRole("listitem")
      .first();
    const boxes = await row.evaluate((item) => {
      const link = item.querySelector("a") as HTMLElement;
      const text = document.createRange();
      text.selectNodeContents(link);
      const box = (rect: DOMRect) => ({
        left: rect.left,
        right: rect.right,
        top: rect.top,
        bottom: rect.bottom,
      });
      return {
        name: box(text.getBoundingClientRect()),
        link: box(link.getBoundingClientRect()),
        time: box(item.querySelector("time")!.getBoundingClientRect()),
      };
    });
    // The whole name is inside its link, and the time is clear of it.
    expect(boxes.name.right - boxes.name.left).toBeGreaterThan(0);
    expect(boxes.name.left).toBeGreaterThanOrEqual(boxes.link.left);
    expect(boxes.name.right).toBeLessThanOrEqual(boxes.link.right);
    const overlap =
      boxes.link.left < boxes.time.right &&
      boxes.time.left < boxes.link.right &&
      boxes.link.top < boxes.time.bottom &&
      boxes.time.top < boxes.link.bottom;
    expect(overlap).toBe(false);
    await expectNoSidewaysScroll(page, `the list at ${width} px`);
  });
}

// DESIGN.md: at 720 px and below a row stacks. The name keeps the whole width,
// and the download sits beside the two lines under it. On a line of its own,
// the download made each row 97 px tall at 360 px.
test("at 360 px a stacked row gives the name the whole width", async ({
  page,
}) => {
  await page.setViewportSize({ width: 360, height: 800 });
  await signIn(page);
  await importFile(page, "statement-29.eml");

  const row = page
    .getByRole("list", { name: "Messages" })
    .getByRole("listitem")
    .first();
  const boxes = await row.evaluate((item) => {
    const box = (element: Element) => {
      const { left, right, top, bottom } = element.getBoundingClientRect();
      return { left, right, top, bottom };
    };
    return {
      row: item.getBoundingClientRect().height,
      link: box(item.querySelector("a")!),
      time: box(item.querySelector("time")!),
      download: box(item.querySelector("button")!),
    };
  });
  // The name's line runs past the download's left edge.
  expect(boxes.link.right).toBeGreaterThan(boxes.download.left);
  // The download starts below the name, level with the time, and after it.
  expect(boxes.download.top).toBeGreaterThanOrEqual(boxes.link.bottom);
  expect(boxes.download.top).toBeLessThanOrEqual(boxes.time.top);
  expect(boxes.download.left).toBeGreaterThan(boxes.time.right);
  expect(boxes.row).toBeLessThan(80);
  await expectNoSidewaysScroll(page, "the stacked list at 360 px");
});

// A page whose code didn't arrive gets the error page, which offers Try again
// like the failed-load state, and focus moves to the heading when it works.
test("a page that didn't load shows the error page and loads again", async ({
  page,
}) => {
  await signIn(page);
  await page.route(PAGE_CODE, (route) => route.abort());

  await page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: /^Status/ })
    .click();
  await expect(page.getByRole("heading", { name: PAGE_FAILED })).toBeVisible();
  await expect(page).toHaveTitle(`${PAGE_FAILED} · MailArkiv`);

  await page.unroute(PAGE_CODE);
  await page.getByRole("button", { name: "Try again" }).press("Enter");
  await expect(page.getByRole("region", { name: "Problems" })).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Status", level: 1 }),
  ).toBeFocused();
});

// DESIGN.md: a slow Try again says so and keeps focus, a second press sends
// nothing, and a failed one says when it tried. The list streams, so it
// arrives after the load settles, and the press waits for the list.
test("Try again sends one request, then says when it tried", async ({
  page,
}) => {
  await signIn(page);
  await page.route("**/api/messages*", (route) => route.abort());
  await page.reload();
  const tried = page.getByRole("status").filter({ hasText: "Last tried" });
  await expect(tried).toBeVisible();
  const before = await tried.textContent();

  await page.unroute("**/api/messages*");
  let requests = 0;
  await page.route("**/api/messages*", async (route) => {
    requests += 1;
    await new Promise((resolve) => setTimeout(resolve, 2_000));
    await route.abort();
  });
  await page.getByRole("button", { name: "Try again" }).press("Enter");
  const trying = page.getByRole("button", { name: "Trying again…" });
  await expect(trying).toBeFocused();
  await trying.press("Enter");

  await expect(page.getByRole("button", { name: "Try again" })).toBeFocused();
  expect(requests).toBe(1);
  // At least 2 seconds later, so the time has moved.
  await expect(tried).not.toHaveText(before ?? "");
  await expect(page.getByText(FAILED_LOAD)).toBeVisible();
});

// Data that didn't arrive shows the failed-load state, never an empty list or
// screen, and Try again loads it once the failure is gone.
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
        page.getByRole("region", { name: "Problems" }),
      ).toBeVisible();
      await expect(page.getByText(FAILED_LOAD)).toHaveCount(0);
      // Try again went away, so focus moved to the heading on purpose.
      await expect(
        page.getByRole("heading", { name: "Status", level: 1 }),
      ).toBeFocused();
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

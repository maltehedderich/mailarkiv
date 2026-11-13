import { expect, type Locator, type Page } from "@playwright/test";

import type { ImapServer } from "./imap";
import type { TestServer } from "./server";

// What the sources specs share: the administrator, the mailbox the test IMAP
// server holds, and the steps that aren't what a spec is about.

export const EMAIL = "admin@example.test";
export const PASSWORD = "correct horse battery";

/** The mailbox on the test IMAP server. Synthetic, under example.test. */
export const MAILBOX = {
  user: "anna@example.test",
  password: "mailbox password 7c2e",
} as const;

/** FR-007's line, shown before Save and on adding a folder. */
export const REMOVAL_WARNING =
  "Archived mail can't be removed in this version.";

export async function signIn(page: Page, server: TestServer): Promise<void> {
  await page.goto(server.origin);
  await page.getByLabel("Email address").fill(EMAIL);
  await page.getByLabel("Password", { exact: true }).fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
}

/** Presses Tab until the target has focus, as a keyboard user would. */
export async function tabTo(page: Page, target: Locator): Promise<void> {
  for (let presses = 0; presses < 60; presses++) {
    if (
      await target.evaluate((element) => element === document.activeElement)
    ) {
      return;
    }
    await page.keyboard.press("Tab");
  }
  throw new Error(`Tab never reached ${target}`);
}

export async function expectNoSidewaysScroll(
  page: Page,
  screen: string,
): Promise<void> {
  const widths = await page.evaluate(() => ({
    scroll: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }));
  expect(widths.scroll, screen).toBeLessThanOrEqual(widths.viewport);
}

export interface Connection {
  name: string;
  user?: string;
  password?: string;
}

/** Fills the add form's first step and presses Connect, by pointer. */
export async function connect(
  page: Page,
  imap: ImapServer,
  { name, user = MAILBOX.user, password = MAILBOX.password }: Connection,
): Promise<void> {
  await page.getByLabel("Name", { exact: true }).fill(name);
  await page.getByLabel("Server", { exact: true }).fill(imap.host);
  await page.getByLabel("Port", { exact: true }).fill(String(imap.port));
  await page.getByLabel("User name", { exact: true }).fill(user);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Connect" }).click();
}

/** The add form's folder checklist. */
export function folderChoice(page: Page): Locator {
  return page.getByRole("group", { name: "Folders" });
}

/**
 * Adds a source through the form with the given folders checked, besides
 * INBOX, and returns once its page is open.
 */
export async function addSource(
  page: Page,
  server: TestServer,
  imap: ImapServer,
  connection: Connection,
  folders: string[] = [],
): Promise<void> {
  await page.goto(`${server.origin}/sources/new`);
  await connect(page, imap, connection);
  for (const folder of folders) {
    await folderChoice(page)
      .getByRole("checkbox", { name: folder, exact: true })
      .check();
  }
  await page.getByRole("button", { name: "Save" }).click();
  await expect(page).toHaveURL(/\/sources\/\d+$/);
  await expect(
    page.getByRole("heading", { name: connection.name, level: 1 }),
  ).toBeVisible();
}

/** The source page's State section. */
export function stateSection(page: Page): Locator {
  return page.getByRole("region", { name: "State" });
}

/** Waits for the state word, which reloads every 3 seconds while syncing. */
export async function expectState(
  page: Page,
  word: string,
  timeout = 5_000,
): Promise<void> {
  await expect(stateSection(page).getByText(word, { exact: true })).toBeVisible(
    { timeout },
  );
}

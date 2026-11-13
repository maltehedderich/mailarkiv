import { expect, test, type Locator, type Page } from "@playwright/test";

import { startImap, type ImapServer } from "./support/imap";
import { startServer, type TestServer } from "./support/server";
import {
  connect,
  EMAIL,
  expectNoSidewaysScroll,
  expectState,
  folderChoice,
  MAILBOX,
  PASSWORD,
  signIn,
} from "./support/sources";

// Folder names come from the mail server, so they're untrusted. Markup shows
// as text, a bidi override shows as its code, and a long name is cut in its
// middle, on every screen that names a folder. Nothing runs.

const MARKUP = "<img src=x onerror=alert(1)>";
const OVERRIDE = "Rechnungen‮fdp.exe";
const LONG =
  `Ablage ${"Projektunterlagen ".repeat(15)}`.padEnd(290, "x") + " Ende 2026";
const FOLDERS = [MARKUP, OVERRIDE, LONG];

let imap: ImapServer;
let server: TestServer;
let dialogs: string[];

test.beforeEach(async ({ page }) => {
  expect(LONG).toHaveLength(300);
  imap = await startImap({
    ...MAILBOX,
    folders: FOLDERS.map((name) => ({ name, messages: 1 })),
  });
  server = await startServer({ collection: { caFile: imap.caFile } });
  await server.createAccount(EMAIL, PASSWORD);
  dialogs = [];
  page.on("dialog", (dialog) => {
    dialogs.push(dialog.message());
    void dialog.dismiss();
  });
});

test.afterEach(async () => {
  await server.stop();
  await imap.stop();
});

/** Markup as text and the override as a code, never as themselves. */
async function expectPlainText(scope: Locator) {
  await expect(scope).toContainText(MARKUP);
  await expect(scope).toContainText("RechnungenU+202Efdp.exe");
  expect(await scope.textContent()).not.toContain("‮");
  await expect(scope.locator("img, script")).toHaveCount(0);
}

/** The long name has all its text, with its start cut short on screen. */
async function expectCut(scope: Locator) {
  const name = scope.locator("bdi", { hasText: LONG.slice(0, 40) }).first();
  await expect(name).toContainText(" Ende 2026");
  const cut = await name.evaluate((bdi) => {
    const start = bdi.querySelector(".start");
    return start !== null && start.scrollWidth > start.clientWidth;
  });
  expect(cut, "the long folder name is cut").toBe(true);
}

async function expectNothingRan(page: Page) {
  expect(dialogs).toEqual([]);
  await expect(page.locator("img")).toHaveCount(0);
}

test("hostile and long folder names show as text on every screen", async ({
  page,
}) => {
  await signIn(page, server);
  await page.goto(`${server.origin}/sources/new`);
  await connect(page, imap, { name: "Anna" });

  const choice = folderChoice(page);
  await expect(choice.getByRole("checkbox")).toHaveCount(4);
  await expectPlainText(choice);
  await expectCut(choice);
  await expectNoSidewaysScroll(page, "the folder choice");
  for (const name of [MARKUP, LONG]) {
    await choice.getByRole("checkbox", { name, exact: true }).check();
  }
  await choice
    .getByRole("checkbox", { name: "RechnungenU+202Efdp.exe", exact: true })
    .check();
  await page.getByRole("button", { name: "Save" }).click();

  // The source's page.
  await expect(page).toHaveURL(/\/sources\/\d+$/);
  await expectState(page, "Waiting", 20_000);
  const folders = page.getByRole("region", { name: "Folders" });
  await expectPlainText(folders);
  await expectCut(folders);
  await expectNoSidewaysScroll(page, "the source");

  // The sources list.
  await page.goto(`${server.origin}/sources`);
  const source = page.getByRole("listitem").filter({ hasText: "Anna" });
  await expectPlainText(source);
  await expectCut(source);
  await expectNoSidewaysScroll(page, "the sources list");

  // The message rows, then the history, which shows the whole name.
  await page.goto(server.origin);
  const list = page.getByRole("list", { name: "Messages" });
  await expect(list.getByRole("listitem")).toHaveCount(3);
  await expectPlainText(list);
  await expectCut(list);
  await expectNoSidewaysScroll(page, "the message list");

  for (const name of FOLDERS) {
    const row = list
      .getByRole("listitem")
      .filter({ hasText: name.slice(0, 10) });
    await row.getByRole("link", { name: /^History of/ }).click();
    const drawer = page.getByRole("dialog", { name: /^History/ });
    const entry = drawer.getByRole("listitem");
    if (name === OVERRIDE) {
      await expect(entry).toContainText("RechnungenU+202Efdp.exe");
    } else {
      await expect(entry).toContainText(name);
    }
    expect(await drawer.textContent()).not.toContain("‮");
    await expect(drawer.locator("img, script")).toHaveCount(0);
    await drawer.getByRole("button", { name: "Close" }).click();
    await expect(drawer).toBeHidden();
  }

  await expectNothingRan(page);
});

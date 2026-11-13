import { expect, test, type Page } from "@playwright/test";

import { startImap, type ImapServer } from "./support/imap";
import { startServer, type TestServer } from "./support/server";
import {
  addSource,
  connect,
  EMAIL,
  expectState,
  MAILBOX,
  PASSWORD,
  REMOVAL_WARNING,
  signIn,
  stateSection,
} from "./support/sources";

// An administrator pauses, resumes, syncs, edits, and removes a source.

let imap: ImapServer;
let server: TestServer;

test.beforeEach(async ({ page }) => {
  imap = await startImap({
    ...MAILBOX,
    folders: [
      { name: "INBOX", messages: 3 },
      { name: "Projekte", messages: 2 },
    ],
    // Each message's first fetch waits, so a sync lasts a few seconds.
    fetchDelayMs: 1_500,
  });
  server = await startServer({ collection: { caFile: imap.caFile } });
  await server.createAccount(EMAIL, PASSWORD);
  await signIn(page, server);
});

test.afterEach(async () => {
  await server.stop();
  await imap.stop();
});

function sourceSection(page: Page) {
  return page.getByRole("region", { name: "Source" });
}

function foldersSection(page: Page) {
  return page.getByRole("region", { name: "Folders" });
}

/** Adds Anna with INBOX and waits for the first sync to end. */
async function addAnna(page: Page) {
  await addSource(page, server, imap, { name: "Anna" });
  await expectState(page, "Waiting", 20_000);
}

test("Sync now, Pause, and Resume change the state, and a second sync is refused", async ({
  page,
}) => {
  await addSource(page, server, imap, { name: "Anna" });
  const state = stateSection(page);
  const syncNow = state.getByRole("button", { name: "Sync now" });
  await expectState(page, "Syncing");

  // One sync at a time, said in words.
  await syncNow.click();
  await expect(state.getByText("A sync is already running.")).toBeVisible();

  // Pausing stops the sync, and the state shows it at once, while focus
  // stays on the button.
  await state.getByRole("button", { name: "Pause" }).click();
  await expectState(page, "Paused");
  await expect(state.getByText(/^Since /)).toBeVisible();
  const resume = state.getByRole("button", { name: "Resume" });
  await expect(resume).toBeFocused();
  await expect(syncNow).toBeDisabled();
  await expect(
    page.getByRole("status").filter({ hasText: "State: Paused" }),
  ).toHaveCount(1);

  // Resuming starts a sync at once, which finishes what pausing stopped.
  await resume.click();
  await expectState(page, "Syncing");
  await expectState(page, "Waiting", 20_000);
  await expect(sourceSection(page)).toContainText("archived");
  await expect(sourceSection(page)).toContainText(/Collected\s*3/);

  // With nothing new, the sync may end before the page shows it, so its
  // finish time, to the millisecond, is what shows it ran.
  const lastSync = sourceSection(page).locator("time");
  const before = await lastSync.getAttribute("datetime");
  await syncNow.click();
  await expect(lastSync).not.toHaveAttribute("datetime", before ?? "", {
    timeout: 10_000,
  });
  await expectState(page, "Waiting");
  await expect(state.getByText("A sync is already running.")).toHaveCount(0);
});

test("an edit with the password left empty keeps collecting, and adding a folder warns first", async ({
  page,
}) => {
  await addAnna(page);
  await sourceSection(page).getByRole("button", { name: "Edit" }).click();

  const password = page.getByLabel("Password", { exact: true });
  await expect(password).toHaveValue("");
  await page.getByLabel("Name", { exact: true }).fill("Anna Büro");

  // Only the source's folders until the server lists the rest.
  const folders = page.getByRole("group", { name: "Folders" });
  await expect(folders.getByRole("checkbox")).toHaveCount(1);
  await expect(page.getByText(REMOVAL_WARNING)).toHaveCount(0);
  await page.getByRole("button", { name: "Add folders" }).click();
  const projekte = folders.getByRole("checkbox", {
    name: "Projekte",
    exact: true,
  });
  await expect(projekte).not.toBeChecked();
  await expect(
    folders.getByRole("checkbox", { name: "INBOX", exact: true }),
  ).toBeChecked();
  await expect(page.getByText(REMOVAL_WARNING)).toBeVisible();
  await projekte.check();
  await page.getByRole("button", { name: "Save" }).click();

  // The stored password signs in for the new folder's sync.
  await expect(
    page.getByRole("heading", { name: "Anna Büro", level: 1 }),
  ).toBeVisible();
  await expect(
    sourceSection(page).getByRole("button", { name: "Edit" }),
  ).toBeFocused();
  await expect(foldersSection(page)).toContainText("Projekte");
  await expectState(page, "Waiting", 20_000);
  await expect(sourceSection(page)).toContainText(/Collected\s*5/);
});

test("a wrong password while editing shows why and changes nothing", async ({
  page,
}) => {
  await addAnna(page);
  await sourceSection(page).getByRole("button", { name: "Edit" }).click();
  await page.getByLabel("Name", { exact: true }).fill("Anna Büro");
  await page
    .getByLabel("Password", { exact: true })
    .fill("not the mailbox password");
  await page.getByRole("button", { name: "Save" }).click();

  await expect(
    page.getByText("Sign-in rejected", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("[AUTHENTICATIONFAILED] Authentication failed"),
  ).toBeVisible();
  await expect(page.getByText("Nothing was changed.")).toBeVisible();

  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(
    page.getByRole("heading", { name: "Anna", level: 1 }),
  ).toBeVisible();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Anna", level: 1 }),
  ).toBeVisible();
  await expectState(page, "Waiting");
});

test("removing asks first, naming the source, and its history stays", async ({
  page,
}) => {
  await addAnna(page);
  const remove = sourceSection(page).getByRole("button", { name: "Remove" });

  await remove.click();
  const dialog = page.getByRole("dialog", { name: "Remove Anna?" });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("stay in the archive");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
  await expect(remove).toBeFocused();

  await remove.click();
  await dialog.getByRole("button", { name: "Remove source" }).click();
  await expect(page).toHaveURL(/\/sources$/);
  await expect(page.getByText("No sources.")).toBeVisible();

  await page.goto(server.origin);
  const rows = page
    .getByRole("list", { name: "Messages" })
    .getByRole("listitem");
  await expect(rows).toHaveCount(3);
  await rows
    .first()
    .getByRole("link", { name: /^History of/ })
    .click();
  const drawer = page.getByRole("dialog", { name: /^History/ });
  await expect(drawer.locator("dl.collection dd").first()).toHaveText("Anna");
});

test("adding the same server and user name again links to the existing source", async ({
  page,
}) => {
  await addAnna(page);
  const existing = page.url();

  await page.goto(`${server.origin}/sources/new`);
  // The user name matches ignoring case.
  await connect(page, imap, {
    name: "Anna again",
    user: MAILBOX.user.toUpperCase(),
  });
  await expect(
    page.getByText("A source for this server and user name already exists."),
  ).toBeVisible();
  await page.getByRole("link", { name: "Open the existing source" }).click();
  await expect(page).toHaveURL(existing);
  await expect(
    page.getByRole("heading", { name: "Anna", level: 1 }),
  ).toBeVisible();
});

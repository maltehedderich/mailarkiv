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

// Edit and Remove sit in the page header, beside the source's name.
function sourceAction(page: Page, name: "Edit" | "Remove") {
  return page.getByRole("main").getByRole("button", { name, exact: true });
}

function foldersSection(page: Page) {
  return page.getByRole("region", { name: "Folders" });
}

test("the scrim closes an empty drawer and confirms before discarding edits", async ({
  page,
}) => {
  await page.goto(`${server.origin}/sources`);
  const add = page.getByRole("link", { name: "Add a source" });
  const drawer = page.getByRole("dialog", { name: /^Add a source/ });
  await add.click();
  await expect(page.getByLabel("Server", { exact: true })).toBeFocused();
  await page.mouse.click(10, 100);
  await expect(drawer).toBeHidden();
  await expect(add).toBeFocused();

  await add.click();
  const host = page.getByLabel("Server", { exact: true });
  await host.fill("imap.example.test");
  // A drag that starts inside the drawer must not count as an outside click.
  await host.hover();
  await page.mouse.down();
  await page.mouse.move(10, 100);
  await page.mouse.up();
  const confirmation = page.getByRole("dialog", {
    name: "Discard this source?",
  });
  await expect(confirmation).toBeHidden();
  await page.mouse.click(10, 100);
  await expect(confirmation).toBeVisible();
  await expect(
    confirmation.getByRole("button", { name: "Keep editing" }),
  ).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(confirmation).toBeHidden();
  await expect(host).toHaveValue("imap.example.test");
  await page.mouse.click(10, 100);
  await confirmation.getByRole("button", { name: "Discard source" }).click();
  await expect(drawer).toBeHidden();
  await expect(page).toHaveURL(`${server.origin}/sources`);
  await expect(add).toBeFocused();
});

test("Back preserves edits until confirmed and Forward opens a fresh drawer", async ({
  page,
}) => {
  await page.goto(`${server.origin}/sources`);
  await page.getByRole("link", { name: "Add a source" }).click();
  await connect(page, imap);
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();
  await page.goBack();
  const confirmation = page.getByRole("dialog", {
    name: "Discard this source?",
  });
  await expect(confirmation).toBeVisible();
  await expect(page).toHaveURL(`${server.origin}/sources?add`);
  await confirmation.getByRole("button", { name: "Keep editing" }).click();
  await expect(page.getByLabel("Name", { exact: true })).toHaveValue(
    MAILBOX.user,
  );
  await page.goBack();
  await confirmation.getByRole("button", { name: "Discard source" }).click();
  await expect(page).toHaveURL(`${server.origin}/sources`);
  await page.goForward();
  const host = page.getByLabel("Server", { exact: true });
  await expect(host).toBeFocused();
  await expect(host).toHaveValue("");
});

test("Cancel from the folder step confirms and reopening focuses Server", async ({
  page,
}) => {
  await page.goto(`${server.origin}/sources?add`);
  await connect(page, imap);
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.getByRole("button", { name: "Discard source" }).click();
  await expect(page).toHaveURL(`${server.origin}/sources`);
  await page.getByRole("link", { name: "Add a source" }).click();
  await expect(page.getByLabel("Server", { exact: true })).toBeFocused();
});

test("a pending save blocks Back, Escape, Close, Cancel, and the scrim", async ({
  page,
}) => {
  await page.goto(`${server.origin}/sources`);
  await page.getByRole("link", { name: "Add a source" }).click();
  await connect(page, imap);
  await expect(page.getByLabel("Name", { exact: true })).toBeVisible();
  let release = () => {};
  const held = new Promise<void>((resolve) => (release = resolve));
  let saves = 0;
  await page.route("**/api/sources", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    saves += 1;
    await held;
    await route.continue();
  });
  try {
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect.poll(() => saves).toBe(1);
    await page.goBack();
    await page.keyboard.press("Escape");
    await page
      .getByRole("button", { name: "Close", exact: true })
      .click({ force: true });
    await page
      .getByRole("button", { name: "Cancel", exact: true })
      .click({ force: true });
    await page.mouse.click(10, 100);
    await expect(page).toHaveURL(`${server.origin}/sources?add`);
    await expect(page.getByRole("button", { name: "Saving…" })).toBeVisible();
    await expect(
      page.getByRole("dialog", { name: "Discard this source?" }),
    ).toBeHidden();
  } finally {
    release();
  }
  await expect(page).toHaveURL(/\/sources\/\d+$/);
  await expectState(page, "Waiting", 20_000);
  expect(saves).toBe(1);
});

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

test("an edit lists the server's folders as it opens, keeps the empty password, and warns first", async ({
  page,
}) => {
  await addAnna(page);
  await sourceAction(page, "Edit").click();
  // Listing doesn't take focus from the heading.
  await expect(
    page
      .getByRole("status")
      .filter({ hasText: "1 more folder listed, unchecked." }),
  ).toHaveCount(1);
  await expect(
    page.getByRole("heading", { name: "Edit source" }),
  ).toBeFocused();

  const password = page.getByLabel("Password", { exact: true });
  await expect(password).toHaveValue("");
  await page
    .getByLabel("Name", { exact: true })
    .fill(" \u200BAnna Büro\uFEFF ");

  // The stored details sign in as the form opens, so no button is needed.
  const folders = page.getByRole("group", { name: "Folders" });
  await expect(page.getByRole("button", { name: "Add folders" })).toHaveCount(
    0,
  );
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
  await expect(sourceAction(page, "Edit")).toBeFocused();
  await expect(foldersSection(page)).toContainText("Projekte");
  await expectState(page, "Waiting", 20_000);
  await expect(sourceSection(page)).toContainText(/Collected\s*5/);
});

test("after a rejected sign-in, opening the edit form doesn't sign in", async ({
  page,
}) => {
  await addAnna(page);
  // The test server can't reject a stored password, so the page is told
  // that the last sync's sign-in was rejected.
  await page.route(/\/api\/sources\/\d+$/, async (route) => {
    if (route.request().method() !== "GET") return route.fallback();
    const response = await route.fetch();
    const source = await response.json();
    await route.fulfill({
      response,
      json: {
        ...source,
        state: "needs_attention",
        next_sync_at: null,
        failure: {
          category: "sign_in_rejected",
          since: new Date().toISOString(),
          failed_attempts: 1,
          last_at: new Date().toISOString(),
          reply: null,
        },
      },
    });
  });
  const checks: string[] = [];
  page.on("request", (request) => {
    if (request.url().endsWith("/check")) checks.push(request.url());
  });
  await page.reload();
  await expectState(page, "Needs attention");

  await sourceAction(page, "Edit").click();
  const addFolders = page.getByRole("button", { name: "Add folders" });
  await expect(addFolders).toBeVisible();
  await expect(
    page.getByText("Enter the mailbox's current password first."),
  ).toBeVisible();
  const folders = page.getByRole("group", { name: "Folders" });
  await expect(folders.getByRole("checkbox")).toHaveCount(1);
  expect(checks).toEqual([]);

  // Pressed, it signs in with the form's details and stays for focus.
  await page.getByLabel("Password", { exact: true }).fill(MAILBOX.password);
  await addFolders.click();
  await expect(
    folders.getByRole("checkbox", { name: "Projekte", exact: true }),
  ).not.toBeChecked();
  await expect(addFolders).toBeFocused();
  await expect(page.getByText(REMOVAL_WARNING)).toBeVisible();
  expect(checks).toHaveLength(1);
});

test("a wrong password while editing shows why and changes nothing", async ({
  page,
}) => {
  await addAnna(page);
  await sourceAction(page, "Edit").click();
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

test("password managers fill only the mailbox sign-in", async ({ page }) => {
  // Proton Pass, Bitwarden, LastPass, and 1Password each skip a field only
  // for their own attribute, and some ignore autocomplete="off".
  async function expectIgnored(label: string) {
    const input = page.getByLabel(label, { exact: true });
    await expect(input).toHaveAttribute("autocomplete", "off");
    await expect(input).toHaveAttribute("data-protonpass-ignore", "true");
    await expect(input).toHaveAttribute("data-bwignore", "");
    await expect(input).toHaveAttribute("data-lpignore", "true");
    await expect(input).toHaveAttribute("data-1p-ignore", "");
  }

  await page.goto(`${server.origin}/sources?add`);
  for (const label of ["Server", "Port"]) {
    await expectIgnored(label);
  }
  const userName = page.getByLabel("User name", { exact: true });
  await expect(userName).toHaveAttribute("autocomplete", "username");
  await expect(userName).not.toHaveAttribute("data-protonpass-ignore");
  const password = page.getByLabel("Password", { exact: true });
  await expect(password).toHaveAttribute("autocomplete", "current-password");
  await expect(password).not.toHaveAttribute("data-protonpass-ignore");

  // The edit form's password has no user name beside it, so a manager would
  // fill another login and replace the stored password with it.

  await addAnna(page);
  await sourceAction(page, "Edit").click();
  for (const label of ["Name", "Port", "Password"]) {
    await expectIgnored(label);
  }
});

test("removing asks first, naming the source, and its history stays", async ({
  page,
}) => {
  await addAnna(page);
  const remove = sourceAction(page, "Remove");

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
  await expect(
    page.getByRole("heading", { name: "No sources yet" }),
  ).toBeVisible();

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

  await page.goto(`${server.origin}/sources?add`);
  // The user name matches ignoring case.
  await connect(page, imap, { user: MAILBOX.user.toUpperCase() });
  await expect(
    page.getByText("A source for this server and user name already exists."),
  ).toBeVisible();
  await page.getByRole("link", { name: "Open the existing source" }).click();
  await page.getByRole("button", { name: "Discard source" }).click();
  await expect(page).toHaveURL(existing);
  await expect(
    page.getByRole("heading", { name: "Anna", level: 1 }),
  ).toBeVisible();
});

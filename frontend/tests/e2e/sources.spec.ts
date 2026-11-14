import { expect, test, type Locator, type Page } from "@playwright/test";

import { startImap, type ImapServer } from "./support/imap";
import { startServer, type TestServer } from "./support/server";
import {
  EMAIL,
  expectNoSidewaysScroll,
  expectState,
  folderChoice,
  MAILBOX,
  PASSWORD,
  REMOVAL_WARNING,
  signIn,
  stateSection,
  tabTo,
} from "./support/sources";

// An administrator adds a source and follows its first sync with the keyboard
// alone, at 360 px wide, and the page never scrolls sideways.

let imap: ImapServer;
let server: TestServer;

test.beforeEach(async ({ page }) => {
  imap = await startImap({
    ...MAILBOX,
    folders: [
      { name: "INBOX", messages: 3 },
      { name: "Gesendete Objekte", messages: 2 },
      { name: "Entwürfe", messages: 1 },
    ],
    // Slow enough that the page reloads at least once while it syncs.
    fetchDelayMs: 1_500,
  });
  server = await startServer({ collection: { caFile: imap.caFile } });
  await server.createAccount(EMAIL, PASSWORD);
  await page.setViewportSize({ width: 360, height: 800 });
});

test.afterEach(async () => {
  await server.stop();
  await imap.stop();
});

/** Exact, so Name doesn't also find User name. */
function field(page: Page, label: string): Locator {
  return page.getByLabel(label, { exact: true });
}

/** Tabs forward to the field, then replaces its text by keyboard. */
async function typeInto(page: Page, target: Locator, text: string) {
  await tabTo(page, target);
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type(text);
}

/** Fills the first step by keyboard, in the order Tab reaches the fields. */
async function typeDetails(page: Page, password: string) {
  await typeInto(page, field(page, "Server"), imap.host);
  await typeInto(page, field(page, "Port"), String(imap.port));
  await typeInto(page, field(page, "User name"), MAILBOX.user);
  await typeInto(page, field(page, "Password"), password);
}

test("an administrator adds a source and follows its first sync", async ({
  page,
}) => {
  await signIn(page, server);
  const sourcesLink = page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: "Sources", exact: true });
  await tabTo(page, sourcesLink);
  await page.keyboard.press("Enter");
  await expect(sourcesLink).toHaveAttribute("aria-current", "page");
  await expect(
    page.getByRole("heading", { name: "No sources yet" }),
  ).toBeVisible();
  await expectNoSidewaysScroll(page, "the empty list");

  await tabTo(page, page.getByRole("link", { name: "Add a source" }));
  await page.keyboard.press("Enter");
  // A drawer over the list, at its own address, starting at its first field.
  const adding = page.getByRole("dialog", { name: /^Add a source/ });
  await expect(
    adding.getByRole("heading", { name: "Connect to the mailbox" }),
  ).toBeVisible();
  await expect(page).toHaveURL(/\/sources\?add$/);
  await expect(field(page, "Server")).toBeFocused();
  await expectNoSidewaysScroll(page, "the add form");
  await expect(sourcesLink).toHaveAttribute("aria-current", "page");

  // Escape closes it, gives focus back to the link, and leaves the address.
  await page.keyboard.press("Escape");
  await expect(adding).toBeHidden();
  await expect(page).toHaveURL(/\/sources$/);
  const addLink = page.getByRole("link", { name: "Add a source" });
  await expect(addLink).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(field(page, "Server")).toBeFocused();

  // The fields in the order Tab reaches them.
  await typeInto(page, field(page, "Server"), imap.host);

  // The port follows the connection security until one is typed.
  const port = field(page, "Port");
  const tls = page.getByRole("radio", { name: "TLS", exact: true });
  const starttls = page.getByRole("radio", { name: "STARTTLS" });
  await expect(tls).toBeChecked();
  await expect(port).toHaveValue("993");
  await tabTo(page, tls);
  await page.keyboard.press("ArrowDown");
  await expect(starttls).toBeChecked();
  await expect(port).toHaveValue("143");
  await page.keyboard.press("ArrowUp");
  await expect(tls).toBeChecked();
  await expect(port).toHaveValue("993");

  await typeInto(page, port, String(imap.port));
  await typeInto(page, field(page, "User name"), MAILBOX.user);
  await typeInto(page, field(page, "Password"), MAILBOX.password);
  const connect = page.getByRole("button", { name: "Connect" });
  await tabTo(page, connect);
  await page.keyboard.press("Enter");

  // Each folder with its count, and the inbox checked.
  const folders = folderChoice(page);
  await expect(folders.getByRole("checkbox")).toHaveCount(3);
  const folder = (name: string) =>
    folders.getByRole("checkbox", { name, exact: true });
  // A filter's locator is searched inside each item, so it starts afresh.
  const item = (name: string) =>
    folders.getByRole("listitem").filter({
      has: page.getByRole("checkbox", { name, exact: true }),
    });
  await expect(folder("INBOX")).toBeChecked();
  await expect(folder("Gesendete Objekte")).not.toBeChecked();
  await expect(folder("Entwürfe")).not.toBeChecked();
  await expect(item("INBOX")).toContainText("3 messages");
  await expect(item("Gesendete Objekte")).toContainText("2 messages");
  await expect(item("Entwürfe")).toContainText("1 message");
  await expectNoSidewaysScroll(page, "the folder choice");

  // The name comes after the sign-in, and starts as the user name.
  const name = field(page, "Name");
  await expect(name).toHaveValue(MAILBOX.user);
  await typeInto(page, name, "Anna");

  await tabTo(page, folder("Gesendete Objekte"));
  await page.keyboard.press("Space");
  await expect(folder("Gesendete Objekte")).toBeChecked();

  // The warning comes before Save, on screen and in the tab order.
  const warning = page.getByText(REMOVAL_WARNING);
  const save = page.getByRole("button", { name: "Save" });
  await expect(warning).toBeVisible();
  const warningBox = await warning.boundingBox();
  const saveBox = await save.boundingBox();
  expect(warningBox!.y + warningBox!.height).toBeLessThanOrEqual(saveBox!.y);

  await tabTo(page, save);
  await page.keyboard.press("Enter");

  // The first sync starts at once and counts what it has archived.
  await expect(page).toHaveURL(/\/sources\/\d+$/);
  await expect(sourcesLink).toHaveAttribute("aria-current", "page");
  await expect(
    page.getByRole("heading", { name: "Anna", level: 1 }),
  ).toBeVisible();
  await expectState(page, "Syncing");
  await expect(stateSection(page)).toContainText(
    /Archived\s+[1-9]\d*\s+of\s+[1-9]\d*\s+found/,
    { timeout: 10_000 },
  );
  await expectNoSidewaysScroll(page, "the source while it syncs");
  await expectState(page, "Waiting", 30_000);
  await expect(stateSection(page)).toContainText("Next sync");
  const facts = page.getByRole("region", { name: "Source" });
  await expect(facts).toContainText("archived 5");

  // The list names each message by its source and folder.
  await tabTo(
    page,
    page
      .getByRole("navigation", { name: "Main" })
      .getByRole("link", { name: "Messages" }),
  );
  await page.keyboard.press("Enter");
  const rows = page
    .getByRole("list", { name: "Messages" })
    .getByRole("listitem");
  await expect(rows).toHaveCount(5);
  await expect(
    rows.filter({
      has: page.getByRole("link", { name: "History of Anna, INBOX" }),
    }),
  ).toHaveCount(3);
  const sent = rows.filter({
    has: page.getByRole("link", { name: "History of Anna, Gesendete Objekte" }),
  });
  await expect(sent).toHaveCount(2);
  await expectNoSidewaysScroll(page, "the list of collected messages");

  // The history shows the collection entry.
  await tabTo(page, sent.first().getByRole("link", { name: /^History of/ }));
  await page.keyboard.press("Enter");
  const drawer = page.getByRole("dialog", { name: /^History/ });
  await expect(drawer).toBeVisible();
  const entry = drawer.getByRole("listitem");
  await expect(entry).toHaveCount(1);
  await expect(entry).toContainText("Gesendete Objekte");
  // Collected from, Server, User name, and Received.
  await expect(entry.locator("dl dd")).toHaveText([
    "Anna",
    imap.host,
    MAILBOX.user,
    /./,
  ]);
  await expectNoSidewaysScroll(page, "the history");
});

test("a wrong password names the step that failed and saves nothing", async ({
  page,
}) => {
  await signIn(page, server);
  await page.goto(`${server.origin}/sources?add`);
  await typeDetails(page, "not the mailbox password");
  const connect = page.getByRole("button", { name: "Connect" });
  await tabTo(page, connect);
  await page.keyboard.press("Enter");

  await expect(
    page.getByText("Sign-in rejected", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("[AUTHENTICATIONFAILED] Authentication failed"),
  ).toBeVisible();
  await expect(page.getByText("Nothing was saved.")).toBeVisible();
  await expect(folderChoice(page)).toHaveCount(0);
  // Focus stays where the keyboard left it, for the next try.
  await expect(connect).toBeFocused();
  await expectNoSidewaysScroll(page, "the failed check");

  await page.goto(`${server.origin}/sources`);
  await expect(
    page.getByRole("heading", { name: "No sources yet" }),
  ).toBeVisible();
});

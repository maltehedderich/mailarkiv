import { expect, test, type Page } from "@playwright/test";

import { startImap, type ImapServer } from "./support/imap";
import { startServer, type TestServer } from "./support/server";
import {
  addSource,
  EMAIL,
  expectState,
  MAILBOX,
  PASSWORD,
  signIn,
} from "./support/sources";

// The states a first sync doesn't reach: no key, more than one source, a
// source that fails, a reload that fails, and a source's problem lists.

const FAILED_LOAD = "This page couldn't be loaded.";

let server: TestServer | undefined;
let imap: ImapServer | undefined;

test.afterEach(async () => {
  await server?.stop();
  await imap?.stop();
  server = undefined;
  imap = undefined;
});

/** Starts a mail server, then a server with the key that trusts it, and signs in. */
async function start(
  page: Page,
  folders: { name: string; messages: number }[],
  fetchDelayMs?: number,
): Promise<{ server: TestServer; imap: ImapServer }> {
  imap = await startImap({ ...MAILBOX, folders, fetchDelayMs });
  server = await startServer({ collection: { caFile: imap.caFile } });
  await server.createAccount(EMAIL, PASSWORD);
  await signIn(page, server);
  return { server, imap };
}

test("without the key, Sources says why and offers no way to add one", async ({
  page,
}) => {
  server = await startServer();
  await server.createAccount(EMAIL, PASSWORD);
  await signIn(page, server);
  await page.goto(`${server.origin}/sources`);

  await expect(
    page.getByText("Collection needs the server's key"),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "section on the key" }),
  ).toHaveAttribute("href", /\/docs\/OPERATIONS\.md#the-key$/);
  // The notice already says why the list is empty, so the empty state
  // keeps only its heading.
  await expect(
    page.getByRole("heading", { name: "No sources yet" }),
  ).toBeVisible();
  await expect(page.getByText(/^A source is an IMAP mailbox/)).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Add a source" })).toHaveCount(0);

  // The add form's own address opens nothing either.
  await page.goto(`${server.origin}/sources?add`);
  await expect(
    page.getByRole("heading", { name: "No sources yet" }),
  ).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("the list counts its sources from the second one on", async ({ page }) => {
  const { server, imap } = await start(page, [{ name: "INBOX", messages: 1 }]);
  const count = page.getByText(/^\d+ sources$/);

  await addSource(page, server, imap, { name: "Anna" });
  await page.goto(`${server.origin}/sources`);
  await expect(page.getByRole("link", { name: "Anna" })).toBeVisible();
  await expect(count).toHaveCount(0);

  // Sources are told apart by host as typed, so the same mailbox under the
  // certificate's other name is a second source.
  await addSource(
    page,
    server,
    { ...imap, host: "localhost" },
    { name: "Office" },
  );
  await page.goto(`${server.origin}/sources`);
  await expect(page.getByRole("link", { name: "Office" })).toBeVisible();
  await expect(count).toHaveText("2 sources");
});

test("Status lists a source that can't reach its server, and leads to it", async ({
  page,
}) => {
  const { server, imap } = await start(page, [{ name: "INBOX", messages: 1 }]);
  await addSource(page, server, imap, { name: "Anna" });
  await expectState(page, "Waiting", 20_000);
  const sourcePage = page.url();

  const problems = page.getByRole("region", { name: "Problems" });
  const lasts = page.getByText(
    "A source stays listed until a sync clears its problem",
  );
  await page.goto(`${server.origin}/status`);
  await expect(
    problems.getByText(
      "No failures and no unreferenced originals since the latest start.",
    ),
  ).toBeVisible();
  await expect(lasts).toHaveCount(0);

  await imap.stop();
  await page.goto(sourcePage);
  await page.getByRole("button", { name: "Sync now" }).click();
  await expectState(page, "Retrying", 10_000);

  // The next retry is a minute away, so the attempt count holds still.
  await page.goto(`${server.origin}/status`);
  const anna = problems
    .getByRole("listitem")
    .filter({ has: page.getByRole("link", { name: "Anna" }) });
  await expect(
    anna.getByText("Server not reachable", { exact: true }),
  ).toBeVisible();
  await expect(anna.getByText(/^Since /)).toBeVisible();
  await expect(
    anna.getByText("1 failed attempt", { exact: true }),
  ).toBeVisible();
  await expect(lasts).toBeVisible();

  await anna.getByRole("link", { name: "Anna" }).click();
  await expect(page).toHaveURL(sourcePage);
});

for (const screen of ["list", "source"] as const) {
  test(`a reload that fails during a sync shows the failed-load block on the ${screen} page`, async ({
    page,
  }) => {
    // Each message's first fetch waits, so the sync outlasts the first reload.
    const { server, imap } = await start(
      page,
      [{ name: "INBOX", messages: 4 }],
      2_000,
    );
    await addSource(page, server, imap, { name: "Anna" });
    if (screen === "list") await page.goto(`${server.origin}/sources`);
    const syncing = page.getByRole("main").getByText("Syncing", {
      exact: true,
    });
    await expect(syncing).toBeVisible();

    await page.route(
      screen === "list" ? /\/api\/sources$/ : /\/api\/sources\/\d+$/,
      (route) => route.abort(),
    );
    // Reloads come every 3 seconds while a source syncs.
    await expect(page.getByText(FAILED_LOAD)).toBeVisible({ timeout: 6_000 });
    await expect(syncing).toHaveCount(0);
  });
}

test("a source's page notes changed folders and shows the newest 100 messages of a list", async ({
  page,
}) => {
  const { server, imap } = await start(page, [
    { name: "INBOX", messages: 1 },
    { name: "Projekte", messages: 1 },
  ]);
  await addSource(page, server, imap, { name: "Anna" }, ["Projekte"]);
  await expectState(page, "Waiting", 20_000);

  // The test server can't drop or renumber a folder while it runs, or fail
  // 101 messages, so the page is given those states. The server's own tests
  // check that it reports them.
  const unavailableSince = "2026-11-20T08:15:00.000Z";
  const renumberedAt = "2026-11-21T09:30:00.000Z";
  const messages = (count: number, category: string) =>
    Array.from({ length: count }, (_, index) => {
      const at = new Date(Date.UTC(2026, 10, 22, 12, 0) - index * 60_000);
      return {
        folder: "INBOX",
        size: 48_211,
        received_at: at.toISOString(),
        recorded_at: at.toISOString(),
        category,
      };
    });
  await page.route(/\/api\/sources\/\d+$/, async (route) => {
    if (route.request().method() !== "GET") return route.fallback();
    const response = await route.fetch();
    const source = await response.json();
    await route.fulfill({
      response,
      json: {
        ...source,
        folders: [
          {
            name: "INBOX",
            unavailable_since: null,
            renumbered_at: renumberedAt,
          },
          {
            name: "Projekte",
            unavailable_since: unavailableSince,
            renumbered_at: null,
          },
        ],
        problems: {
          unavailable_folders: 1,
          waiting_for_retry: 101,
          not_archived: 100,
        },
        messages: {
          waiting_for_retry: {
            total: 101,
            items: messages(100, "message_unavailable"),
          },
          not_archived: {
            total: 100,
            items: messages(100, "message_too_large"),
          },
        },
      },
    });
  });
  await page.reload();

  const folders = page.getByRole("region", { name: "Folders" });
  const projekte = folders
    .getByRole("listitem")
    .filter({ hasText: "Projekte" });
  await expect(projekte.getByText(/^Unavailable since /)).toBeVisible();
  await expect(projekte.locator("time")).toHaveAttribute(
    "datetime",
    unavailableSince,
  );
  const inbox = folders.getByRole("listitem").filter({ hasText: "INBOX" });
  await expect(
    inbox.getByText(
      /^Renumbered by the server .+, so MailArkiv collected it again$/,
    ),
  ).toBeVisible();
  await expect(inbox.locator("time")).toHaveAttribute("datetime", renumberedAt);
  await expect(
    folders.getByRole("heading", { name: "Folder unavailable" }),
  ).toHaveCount(1);

  // Only a list with more messages than it shows says so.
  const waiting = page.getByRole("region", { name: /^Waiting for retry/ });
  await expect(waiting.getByRole("listitem")).toHaveCount(100);
  await expect(
    waiting.getByText("Showing the newest 100 of 101.", { exact: true }),
  ).toBeVisible();
  const notArchived = page.getByRole("region", { name: /^Not archived/ });
  await expect(notArchived.getByRole("listitem")).toHaveCount(100);
  await expect(notArchived.getByText(/^Showing the newest/)).toHaveCount(0);
});

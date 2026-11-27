import { createHash } from "node:crypto";
import { mkdir, readdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";

import { expect, test, type Page } from "@playwright/test";

import {
  collectMalformedSet,
  SOURCE,
  startMalformedSet,
  type MalformedSet,
} from "./support/malformed";
import { startServer, type TestServer } from "./support/server";
import { expectNoSidewaysScroll, tabTo } from "./support/sources";

// The status screen lists what the server knows since its latest start, the
// Status item counts the problems on every signed-in page, and a restart
// clears both. It shows the search and indexing states and the processing
// counts, and starts a rebuild after asking.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const HASH = createHash("sha256").update(CONTENT).digest("hex");

// What the screen covers, with the start time, and where older failures are.
const COVERS = "This screen covers only the time since the server started at";
const OLDER = "Older failures are in the server log.";
const NOTHING_FOUND =
  "Found no leftover temporary files and no unreferenced originals.";
const NO_PROBLEMS =
  "No failures and no unreferenced originals since the latest start.";
// Unreadable originals and hash mismatches share this explanation.
const DAMAGED = "Stored original damaged";
const DAMAGED_MEANING =
  "The stored file can't be read or does not match its content hash.";
const DAMAGED_NEXT_STEP =
  "Import the same file again, if you still have it, to restore the original.";
// An unreferenced original can be listed again by importing the same bytes.
const UNREFERENCED_NEXT_STEP =
  "Importing the same file again puts it back in the list, and nothing is deleted.";

let server: TestServer | null = null;
let set: MalformedSet | null = null;

/** Starts a server with an account. afterEach stops it. */
async function start(
  options: Parameters<typeof startServer>[0] = {},
): Promise<TestServer> {
  server = await startServer(options);
  await server.createAccount(EMAIL, PASSWORD);
  return server;
}

test.afterEach(async () => {
  await server?.stop();
  server = null;
  await set?.stop();
  set = null;
});

async function signIn(page: Page) {
  await page.goto(running().origin);
  await page.getByLabel("Email address").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
}

/**
 * The Status item in the sidebar. Its name carries the problem count, so a
 * string must match all of it.
 */
function statusItem(page: Page, name: string | RegExp = /^Status/) {
  return page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name, exact: typeof name === "string" });
}

/** The server the test started, alone or for the malformed set. */
function running(): TestServer {
  const current = server ?? set?.server;
  if (current === undefined) throw new Error("no server runs");
  return current;
}

async function openStatus(page: Page) {
  await statusItem(page).click();
  await expect(page).toHaveURL(`${running().origin}/status`);
  await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();
}

function section(page: Page, name: string) {
  return page.getByRole("region", { name });
}

/** One group in Problems: a failure category, or the unreferenced originals. */
function problem(page: Page, name: string) {
  return section(page, "Problems")
    .getByRole("listitem")
    .filter({ has: page.getByRole("heading", { name, exact: true }) });
}

/** The status the server answers, read with the browser's session cookie. */
async function readStatus(page: Page) {
  const response = await page.request.get(`${running().origin}/api/status`);
  expect(response.status()).toBe(200);
  return (await response.json()) as {
    started_at: string;
    failures: { at: string; message_id?: number }[];
    search: {
      needs_rebuild: { cause: string; since: string } | null;
      rebuild: { id: number } | null;
    };
  };
}

test("a clean start shows the start time and that recovery found nothing", async ({
  page,
}) => {
  await start();
  await signIn(page);
  await expect(statusItem(page, "Status")).toBeVisible();
  await openStatus(page);

  const { started_at } = await readStatus(page);
  const scope = page.getByText(COVERS);
  await expect(scope).toContainText(OLDER);
  await expect(scope.locator(`time[datetime="${started_at}"]`)).toBeVisible();
  await expect(section(page, "Problems")).toContainText(NO_PROBLEMS);
  await expect(section(page, "Recovery")).toContainText(NOTHING_FOUND);
  await expect(page.locator("body")).not.toContainText(/healthy/i);
  await expect(statusItem(page, "Status")).toHaveAttribute(
    "aria-current",
    "page",
  );
});

test("a damaged original is counted on every page, and a restart clears it", async ({
  page,
}) => {
  const started = await start();
  await signIn(page);
  await page.getByLabel("Choose a file").setInputFiles({
    name: "synthetic.eml",
    mimeType: "message/rfc822",
    buffer: CONTENT,
  });
  await page.getByRole("button", { name: "Import" }).click();
  const row = page
    .getByRole("list", { name: "Messages" })
    .getByRole("link", { name: /^Open / });
  await expect(row).toBeVisible();
  const id = /^\/messages\/(\d+)$/.exec(
    (await row.getAttribute("href")) ?? "",
  )?.[1];
  expect(id).toBeDefined();
  await expect(statusItem(page, "Status")).toBeVisible();

  // Damage the stored copy and download it through the browser's session, so
  // the list page stays open and only an in-app navigation can update the
  // count.
  const blob = join(
    started.dataDir,
    "blobs",
    HASH.slice(0, 2),
    HASH.slice(2, 4),
    HASH,
  );
  await writeFile(blob, "Subject: not the original\r\n\r\n");
  const failed = await page.request.get(
    `${started.origin}/api/messages/${id}/original`,
  );
  expect(failed.status()).toBe(500);

  await openStatus(page);
  await expect(statusItem(page, "Status 1 problem")).toBeVisible();
  const failure = problem(page, DAMAGED);
  await expect(failure).toHaveCount(1);
  await expect(failure).toContainText(DAMAGED_MEANING);
  await expect(failure).toContainText(DAMAGED_NEXT_STEP);
  const [{ at }] = (await readStatus(page)).failures;
  const occurrence = failure.getByRole("listitem");
  await expect(occurrence).toHaveCount(1);
  await expect(occurrence.locator(`time[datetime="${at}"]`)).toBeVisible();
  await expect(occurrence).toContainText(`Hash ${HASH.slice(0, 8)}`);
  await expect(page.locator("body")).not.toContainText(/healthy/i);

  // The message ID opens its history, which names the file to import again.
  await occurrence.getByRole("link", { name: `Message ${id}` }).click();
  await expect(page).toHaveURL(`${started.origin}/?message=${id}`);
  const drawer = page.getByRole("dialog", { name: "History" });
  await expect(drawer).toContainText("synthetic.eml");
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
  await expect(statusItem(page, "Status 1 problem")).toBeVisible();

  await started.restart();
  await page.goto(`${started.origin}/status`);

  await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();
  await expect(page.getByText(COVERS)).toContainText(OLDER);
  await expect(section(page, "Problems")).toContainText(NO_PROBLEMS);
  await expect(page.getByText(DAMAGED)).toHaveCount(0);
  await expect(statusItem(page, "Status")).toBeVisible();
});

test("a restart after a crash lists the temp files removed and the unreferenced original", async ({
  page,
}) => {
  const started = await start();
  // What a crash leaves: a partial upload in tmp/, and a whole original whose
  // catalog entry was never written.
  await writeFile(join(started.dataDir, "tmp", "up-leftover"), "Subject: part");
  const blob = join(
    started.dataDir,
    "blobs",
    HASH.slice(0, 2),
    HASH.slice(2, 4),
    HASH,
  );
  await mkdir(dirname(blob), { recursive: true });
  await writeFile(blob, CONTENT);
  await started.restart();

  await signIn(page);
  await expect(statusItem(page, "Status 1 problem")).toBeVisible();
  await openStatus(page);

  const recovery = section(page, "Recovery");
  await expect(recovery).toContainText(/Temporary files removed\s*1(?!\d)/);
  await expect(recovery).toContainText(/Temporary files not removed\s*0(?!\d)/);
  await expect(recovery).toContainText(
    /Unreferenced originals found\s*1(?!\d)/,
  );

  const unreferenced = problem(page, "Unreferenced original");
  await expect(unreferenced).toContainText(UNREFERENCED_NEXT_STEP);
  const original = unreferenced.getByRole("listitem");
  await expect(original).toHaveCount(1);
  const groups = original.locator("code span");
  await expect(groups).toHaveCount(16);
  expect((await groups.allTextContents()).join("")).toBe(HASH);
  await expect(original).toContainText(`${CONTENT.length} bytes`);

  // Recovery never deletes a stored original.
  expect(await readdir(dirname(blob))).toEqual([HASH]);
  expect(await readdir(join(started.dataDir, "tmp"))).toEqual([]);
});

/** Imports a small message with its own subject through the API. */
async function importMessage(page: Page, n: number) {
  const response = await page.request.post(`${running().origin}/api/imports`, {
    headers: { "MailArkiv-File-Name": `message-${n}.eml` },
    data: Buffer.from(`Subject: Message ${n}\r\n\r\nBody ${n}.\r\n`),
  });
  expect(response.status()).toBe(201);
}

/**
 * Opens Status and reloads it until check passes. The screen shows what the
 * server knew when it loaded, so a change shows on the next load.
 */
async function statusUntil(page: Page, check: () => Promise<void>) {
  await page.goto(`${running().origin}/status`);
  await expect(async () => {
    await page.reload();
    await check();
  }).toPass({ timeout: 20_000 });
}

/** A label and its value in the Search or Processing facts. */
function fact(label: string, value: string): RegExp {
  return new RegExp(`${label}\\s*${value}(?![\\d,])`);
}

/** Opens Status once every message is indexed. */
async function statusWhenIndexed(page: Page) {
  const search = section(page, "Search");
  await statusUntil(page, async () => {
    await expect(search).toContainText(fact("Indexing", "Up to date"), {
      timeout: 1_000,
    });
    await expect(search).toContainText(fact("Waiting to be indexed", "0"), {
      timeout: 1_000,
    });
  });
}

test("the search section shows the states, the counts, and the restarts", async ({
  page,
}) => {
  set = await startMalformedSet();
  await collectMalformedSet(page, set);
  await statusWhenIndexed(page);

  const search = section(page, "Search");
  await expect(search).toContainText(fact("Search", "Available"));
  await expect(search).toContainText(fact("Search process restarts", "0"));
  const processing = section(page, "Processing");
  await expect(processing).toContainText(fact("Not processed yet", "0"));
  await expect(processing).toContainText(fact("Processed with problems", "4"));
  await expect(processing).toContainText(fact("Couldn't be read", "1"));
  // Messages with processing problems have their own list and don't count.
  await expect(statusItem(page, "Status")).toBeVisible();
  await expect(page.locator("body")).not.toContainText(/healthy/i);

  await processing.getByRole("link", { name: "Processing problems" }).click();
  await expect(page).toHaveURL(`${running().origin}/problems`);
  const list = page.getByRole("list", { name: "Processing problems" });
  await expect(list.getByRole("link", { name: /^Open / })).toHaveText(
    [
      `${SOURCE}INBOX`,
      "Abgeschnitten",
      "Ohne Grenze",
      "Unbekannter Zeichensatz",
      "Bericht mit kaputtem Anhang",
    ].map((text) => new RegExp(text)),
  );
});

test("a killed search process is listed with its time and counted", async ({
  page,
}) => {
  await start();
  await signIn(page);
  await expect(statusItem(page, "Status")).toBeVisible();

  await running().killSearch();
  const stopped = problem(page, "Search process stopped");
  const search = section(page, "Search");
  await statusUntil(page, async () => {
    await expect(stopped).toHaveCount(1, { timeout: 1_000 });
    await expect(search).toContainText(fact("Search", "Available"), {
      timeout: 1_000,
    });
  });

  const [failure] = (await readStatus(page)).failures;
  await expect(stopped.locator(`time[datetime="${failure.at}"]`)).toBeVisible();
  await expect(stopped).toContainText("The search process exited or crashed.");
  await expect(stopped).toContainText("Signal 9");
  await expect(search).toContainText(fact("Search process restarts", "1"));
  await expect(statusItem(page, "Status 1 problem")).toBeVisible();
});

test("Rebuild asks first, then shows Rebuilding with its progress", async ({
  page,
}) => {
  await start({ search: { faults: "" } });
  await signIn(page);
  for (let n = 1; n <= 3; n++) await importMessage(page, n);
  await statusWhenIndexed(page);
  // The rebuild's first batch never gets its answer, which holds the rebuild
  // at its start.
  await running().restart({ searchFaults: "after-commit=hang" });
  await page.goto(`${running().origin}/status`);

  const search = section(page, "Search");
  const rebuild = search.getByRole("button", { name: "Rebuild index" });
  await tabTo(page, rebuild);
  await page.keyboard.press("Enter");
  const dialog = page.getByRole("dialog", {
    name: "Rebuild the search index?",
  });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Cancel" })).toBeFocused();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(rebuild).toBeFocused();
  expect((await readStatus(page)).search.rebuild).toBeNull();

  await rebuild.click();
  await dialog.getByRole("button", { name: "Rebuild index" }).click();
  await expect(dialog).toBeHidden();
  await expect(search).toContainText(fact("Indexing", "Rebuilding"));
  await expect(search).toContainText(/(?<![\d,])0 of 3 messages/);
  await expect(search).toContainText(`Started by ${EMAIL}`);
  await expect(
    search.getByRole("button", { name: "Rebuild index" }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("heading", { name: "Search", level: 2 }),
  ).toBeFocused();

  await page.setViewportSize({ width: 360, height: 780 });
  await expectNoSidewaysScroll(page, "Status while rebuilding");
});

test("Needs rebuild shows its cause, since when, and Rebuild", async ({
  page,
}) => {
  await start({ search: { faults: "" } });
  await signIn(page);
  await importMessage(page, 1);
  await statusWhenIndexed(page);
  // The active index then fails to open, which marks it damaged.
  await running().restart({ searchFaults: "on-open=fail" });
  await page.goto(`${running().origin}/status`);

  const search = section(page, "Search");
  await expect(search).toContainText(fact("Search", "Needs rebuild"));
  await expect(search).toContainText(fact("Indexing", "Stopped"));
  const needsRebuild = (await readStatus(page)).search.needs_rebuild;
  expect(needsRebuild?.cause).toBe("index_damaged");
  const since = search.locator(`time[datetime="${needsRebuild?.since}"]`);
  await expect(since).toBeVisible();
  await expect(search).toContainText("Index damaged");
  await expect(
    search.getByRole("button", { name: "Rebuild index" }),
  ).toBeVisible();
  await expect(problem(page, "Index needs rebuild")).toHaveCount(1);
  // The event, and search while it needs a rebuild.
  await expect(statusItem(page, "Status 2 problems")).toBeVisible();

  await page.setViewportSize({ width: 360, height: 780 });
  await expectNoSidewaysScroll(page, "Status while search needs a rebuild");
});

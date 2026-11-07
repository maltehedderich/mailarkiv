import { createHash } from "node:crypto";
import { mkdir, readdir, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";

import { expect, test, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// User Story 5 scenarios 1 to 4, 7, and 8, FR-027, FR-038, FR-039: the status
// screen lists what the server knows since its latest start, the Status item
// counts the problems on every signed-in page, and a restart clears both.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const HASH = createHash("sha256").update(CONTENT).digest("hex");

// FR-027: what the screen covers, with the start time, and where older
// failures are.
const COVERS = "This screen covers only the time since the server started at";
const OLDER = "Older failures are in the server log.";
const NOTHING_FOUND =
  "Found no leftover temporary files and no unreferenced originals.";
const NO_PROBLEMS =
  "No failures and no unreferenced originals since the latest start.";
// FR-038's Stored original damaged row.
const DAMAGED = "Stored original damaged";
const DAMAGED_MEANING =
  "The stored file can't be read or does not match its content hash.";
const DAMAGED_NEXT_STEP =
  "Import the same file again, if you still have it, to restore the original.";
// FR-038's next step for an unreferenced original.
const UNREFERENCED_NEXT_STEP =
  "Importing the same file again puts it back in the list, and nothing is deleted.";

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

/**
 * The Status item in the sidebar. Its name carries the problem count, so a
 * string must match all of it.
 */
function statusItem(page: Page, name: string | RegExp = /^Status/) {
  return page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name, exact: typeof name === "string" });
}

async function openStatus(page: Page) {
  await statusItem(page).click();
  await expect(page).toHaveURL(`${server.origin}/status`);
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
  const response = await page.request.get(`${server.origin}/api/status`);
  expect(response.status()).toBe(200);
  return (await response.json()) as {
    started_at: string;
    failures: { at: string; message_id?: number }[];
  };
}

test("a clean start shows the start time and that recovery found nothing", async ({
  page,
}) => {
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
  await signIn(page);
  await page.getByLabel("Choose a file").setInputFiles({
    name: "synthetic.eml",
    mimeType: "message/rfc822",
    buffer: CONTENT,
  });
  await page.getByRole("button", { name: "Import" }).click();
  const history = page.getByRole("link", { name: /history of/i });
  await expect(history).toBeVisible();
  const id = /[?&]message=(\d+)$/.exec(
    (await history.getAttribute("href")) ?? "",
  )?.[1];
  expect(id).toBeDefined();
  await expect(statusItem(page, "Status")).toBeVisible();

  // Damage the stored copy and download it through the browser's session, so
  // the list page stays open and only an in-app navigation can update the
  // count.
  const blob = join(
    server.dataDir,
    "blobs",
    HASH.slice(0, 2),
    HASH.slice(2, 4),
    HASH,
  );
  await writeFile(blob, "Subject: not the original\r\n\r\n");
  const failed = await page.request.get(
    `${server.origin}/api/messages/${id}/original`,
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
  await expect(page).toHaveURL(`${server.origin}/?message=${id}`);
  const drawer = page.getByRole("dialog", { name: "Import history" });
  await expect(drawer).toContainText("synthetic.eml");
  await page.keyboard.press("Escape");
  await expect(drawer).toBeHidden();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
  await expect(statusItem(page, "Status 1 problem")).toBeVisible();

  await server.restart();
  await page.goto(`${server.origin}/status`);

  await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();
  await expect(page.getByText(COVERS)).toContainText(OLDER);
  await expect(section(page, "Problems")).toContainText(NO_PROBLEMS);
  await expect(page.getByText(DAMAGED)).toHaveCount(0);
  await expect(statusItem(page, "Status")).toBeVisible();
});

test("a restart after a crash lists the temp files removed and the unreferenced original", async ({
  page,
}) => {
  // What a crash leaves: a partial upload in tmp/, and a whole original whose
  // catalog entry was never written.
  await writeFile(join(server.dataDir, "tmp", "up-leftover"), "Subject: part");
  const blob = join(
    server.dataDir,
    "blobs",
    HASH.slice(0, 2),
    HASH.slice(2, 4),
    HASH,
  );
  await mkdir(dirname(blob), { recursive: true });
  await writeFile(blob, CONTENT);
  await server.restart();

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

  // Recovery never deletes a stored original (FR-024).
  expect(await readdir(dirname(blob))).toEqual([HASH]);
  expect(await readdir(join(server.dataDir, "tmp"))).toEqual([]);
});

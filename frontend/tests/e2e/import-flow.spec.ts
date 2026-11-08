import { expect, test, type Locator, type Page } from "@playwright/test";

import { startImportGate, type ImportGate } from "./support/proxy";
import { startServer, type TestServer } from "./support/server";

// Import takes two steps, shows progress and then a separate storing state,
// allows one submission, keeps its result until the next import, and warns
// before the page is left. The browser refuses an empty file and several
// dropped files without uploading, and its file dialog takes one file.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);
const FILE = {
  name: "synthetic.eml",
  mimeType: "message/rfc822",
  buffer: CONTENT,
};
const WARNING = "Imported mail can't be removed in this version.";

/**
 * Larger than the socket buffers between the browser and the proxy, so a held
 * body stops the upload partway.
 */
function largeFile(name = "large.eml") {
  const header = Buffer.from("Subject: Large\r\n\r\n");
  const body = Buffer.alloc(48_000_000 - header.length, "a");
  return {
    name,
    mimeType: "message/rfc822",
    buffer: Buffer.concat([header, body]),
  };
}

let server: TestServer;
let gate: ImportGate;

test.beforeEach(async () => {
  server = await startServer();
  await server.createAccount(EMAIL, PASSWORD);
  gate = await startImportGate(server.origin);
});

test.afterEach(async () => {
  await gate.close();
  await server.stop();
});

async function signIn(page: Page) {
  await page.goto(gate.origin);
  await page.getByLabel("Email address").fill(EMAIL);
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
}

/** Counts the uploads the page starts. */
function countImports(page: Page): () => number {
  let count = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/imports") count += 1;
  });
  return () => count;
}

function importBox(page: Page) {
  return page.getByRole("region", { name: "Import" });
}

function importButton(page: Page) {
  return page.getByRole("button", { name: "Import", exact: true });
}

/** The result is a toast, and these tests raise no other. */
function result(page: Page) {
  return page.getByRole("region", { name: "Notifications" });
}

test("choosing a file shows its name, exact size, and the warning, and stores nothing", async ({
  page,
}) => {
  await signIn(page);
  const imports = countImports(page);
  await expect(importBox(page)).toContainText("Drop one .eml file here");
  await expect(importBox(page)).toContainText(WARNING);
  await expect(importButton(page)).toBeDisabled();

  await page.getByLabel("Choose a file").setInputFiles(FILE);

  await expect(importBox(page)).toContainText("synthetic.eml");
  await expect(importBox(page)).toContainText(`${CONTENT.length} bytes`);
  await expect(importBox(page)).toContainText(WARNING);
  await expect(importButton(page)).toBeEnabled();
  // The choice alone sends nothing, and the list stays empty after a reload.
  await page.waitForTimeout(500);
  expect(imports()).toBe(0);
  await page.reload();
  await expect(page.getByText("No mail yet")).toBeVisible();
});

test("progress moves to a separate storing state, and a second submission isn't possible", async ({
  page,
}) => {
  await signIn(page);
  const imports = countImports(page);
  gate.hold();
  await page.getByLabel("Choose a file").setInputFiles(largeFile());

  await importButton(page).click();

  const progress = importBox(page).getByRole("progressbar");
  await expect(progress).toBeVisible();
  await expect(importBox(page)).toContainText(/Uploading\s+\d+%/);
  expect(Number(await progress.getAttribute("aria-valuenow"))).toBeLessThan(
    100,
  );
  await expect(importButton(page)).toHaveAttribute("aria-disabled", "true");

  gate.sendBody();

  await expect(importBox(page)).toContainText("Storing");
  await expect(progress).toHaveCount(0);
  await expect(importBox(page)).not.toContainText("%");
  // Neither a click nor Enter starts a second upload.
  await importButton(page).click({ force: true });
  await importButton(page).press("Enter");
  await expect(importButton(page)).toHaveAttribute("aria-disabled", "true");

  gate.answer();

  await expect(result(page)).toContainText("Archived");
  expect(imports()).toBe(1);
  expect(gate.imports).toBe(1);
});

test("the result stays until the next import starts", async ({ page }) => {
  await signIn(page);
  await page.getByLabel("Choose a file").setInputFiles(FILE);
  await importButton(page).click();
  await expect(result(page)).toContainText("Archived");

  // Opening and closing the history, and choosing the next file, start nothing.
  await result(page).getByRole("link", { name: "Open import history" }).click();
  const drawer = page.getByRole("dialog", { name: "Import history" });
  await expect(drawer).toBeVisible();
  await drawer.getByRole("button", { name: "Close" }).click();
  await expect(drawer).toBeHidden();
  await page.getByLabel("Choose a file").setInputFiles({
    ...FILE,
    name: "second.eml",
    buffer: Buffer.from("Subject: Second\r\n\r\nAgain.\r\n"),
  });
  await expect(result(page)).toContainText("Archived");
  await expect(result(page)).toContainText("synthetic.eml");

  gate.hold();
  await importButton(page).click();
  await expect(result(page)).toHaveText("");

  gate.sendBody();
  gate.answer();
  await expect(result(page)).toContainText("second.eml");
  await expect(result(page)).not.toContainText("synthetic.eml");
});

test("leaving during an import asks first", async ({ page }) => {
  await signIn(page);
  gate.hold();
  await page.getByLabel("Choose a file").setInputFiles(FILE);
  await importButton(page).click();
  await expect(importButton(page)).toHaveAttribute("aria-disabled", "true");

  // A link inside the app asks, and staying keeps the import.
  const asked = page.waitForEvent("dialog");
  void page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: /^Status/ })
    .click();
  const confirm = await asked;
  expect(confirm.type()).toBe("confirm");
  await confirm.dismiss();
  await expect(page).toHaveURL(`${gate.origin}/`);
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();

  // Closing the tab asks too.
  const unloading = page.waitForEvent("dialog");
  await page.close({ runBeforeUnload: true });
  const beforeUnload = await unloading;
  expect(beforeUnload.type()).toBe("beforeunload");
  await beforeUnload.dismiss();
  expect(page.isClosed()).toBe(false);

  gate.sendBody();
  gate.answer();
  await expect(result(page)).toContainText("Archived");

  // With nothing running, leaving asks nothing.
  page.on("dialog", (dialog) => {
    throw new Error(`Unexpected ${dialog.type()} dialog`);
  });
  await page
    .getByRole("navigation", { name: "Main" })
    .getByRole("link", { name: /^Status/ })
    .click();
  await expect(page).toHaveURL(`${gate.origin}/status`);
});

test("an import finishes after leaving without changing the destination page", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await signIn(page);
  gate.hold();
  await page.getByLabel("Choose a file").setInputFiles(FILE);
  await importButton(page).click();
  gate.sendBody();
  await expect(importBox(page)).toContainText("Storing");

  page.once("dialog", (dialog) => dialog.accept());
  const navigation = page.getByRole("navigation", { name: "Main" });
  await navigation.getByRole("link", { name: /^Status/ }).click();
  await expect(page).toHaveURL(`${gate.origin}/status`);
  await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();
  const messagesLink = navigation.getByRole("link", { name: "Messages" });
  await messagesLink.focus();
  let refreshes = 0;
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/status") refreshes += 1;
  });

  gate.answer();

  await expect(result(page)).toContainText("Archived");
  await expect(result(page)).toContainText(FILE.name);
  await expect(messagesLink).toBeFocused();
  await expect(page).toHaveURL(`${gate.origin}/status`);
  expect(errors).toEqual([]);
  expect(refreshes).toBe(0);
  await messagesLink.click();
  await expect(
    page.getByRole("link", { name: /history of synthetic.eml/i }),
  ).toBeVisible();
});

test.describe("the browser refuses before uploading", () => {
  // The dialog prevents several, so only a drop can bring them.
  test("the file dialog takes one file", async ({ page }) => {
    await signIn(page);

    await expect(page.getByLabel("Choose a file")).toHaveJSProperty(
      "multiple",
      false,
    );
  });

  test("an empty file", async ({ page }) => {
    await signIn(page);
    const imports = countImports(page);

    await page.getByLabel("Choose a file").setInputFiles({
      ...FILE,
      buffer: Buffer.alloc(0),
    });

    await expect(importBox(page)).toContainText(
      "This file is empty. Nothing was uploaded.",
    );
    await expect(importButton(page)).toBeDisabled();
    expect(imports()).toBe(0);
  });

  // Dropping adds to the button. One file is chosen, two are refused.
  test("several dropped files, while one dropped file is chosen", async ({
    page,
  }) => {
    await signIn(page);
    const imports = countImports(page);

    await drop(page, ["dropped.eml"]);
    await expect(importBox(page)).toContainText("dropped.eml");
    await expect(importButton(page)).toBeEnabled();

    await drop(page, ["one.eml", "two.eml"]);
    await expect(importBox(page)).toContainText(
      "Choose one file at a time. Nothing was uploaded.",
    );
    await expect(importBox(page)).not.toContainText("dropped.eml");
    await expect(importButton(page)).toBeDisabled();
    expect(imports()).toBe(0);
  });

  test("a file dragged beside the box is neither taken nor opened", async ({
    page,
  }) => {
    await signIn(page);
    const heading = page.getByRole("heading", { name: "Messages" });

    // Cancelled everywhere, and only the box offers to copy the file.
    expect(await dragOver(importBox(page))).toEqual({
      cancelled: true,
      effect: "copy",
    });
    expect(await dragOver(heading)).toEqual({
      cancelled: true,
      effect: "none",
    });

    await drop(page, ["beside.eml"], heading);
    await expect(importBox(page)).not.toContainText("beside.eml");
    await expect(importButton(page)).toBeDisabled();
  });
});

// The two steps exist so a wrong file isn't imported. When Import appeared
// only after a choice, it took the place of Choose a file, so a second click
// meant to change the file imported it instead.
test("choosing a file moves neither button, and the box keeps its height through an import", async ({
  page,
}) => {
  await signIn(page);
  const choose = page.locator("label", { hasText: "Choose a file" });
  const ready = {
    choose: await choose.boundingBox(),
    import: await importButton(page).boundingBox(),
    box: await importBox(page).boundingBox(),
  };

  gate.hold();
  await page.getByLabel("Choose a file").setInputFiles(largeFile());
  expect(await choose.boundingBox()).toEqual(ready.choose);
  expect(await importButton(page).boundingBox()).toEqual(ready.import);
  expect(await importBox(page).boundingBox()).toEqual(ready.box);

  await importButton(page).click();
  await expect(importBox(page).getByRole("progressbar")).toBeVisible();
  expect(await importBox(page).boundingBox()).toEqual(ready.box);

  gate.sendBody();
  await expect(importBox(page)).toContainText("Storing");
  expect(await importBox(page).boundingBox()).toEqual(ready.box);

  gate.answer();
  await expect(result(page)).toContainText("Archived");
  await page.getByLabel("Choose a file").setInputFiles({
    ...FILE,
    buffer: Buffer.alloc(0),
  });
  await expect(importBox(page)).toContainText("This file is empty.");
  expect(await importBox(page).boundingBox()).toEqual(ready.box);
});

// WCAG 4.1.3: each step is heard without moving focus. The percentage isn't
// announced, because the bar holds it.
test("a screen reader hears the choice, each slow step, and each refusal", async ({
  page,
}) => {
  await signIn(page);
  const said = importBox(page).getByRole("status");

  await page.getByLabel("Choose a file").setInputFiles(FILE);
  await expect(said).toHaveText(
    `synthetic.eml, ${CONTENT.length} bytes. ${WARNING}`,
  );

  // Large, so the held upload stops partway.
  gate.hold();
  await page.getByLabel("Choose a file").setInputFiles(largeFile());
  await importButton(page).click();
  await expect(said).toHaveText("Uploading");
  gate.sendBody();
  await expect(said).toHaveText("Storing the file");
  gate.answer();
  await expect(result(page)).toContainText("Archived");
  // The result toast speaks for a finished import.
  await expect(said).toHaveText("");

  // The same refusal twice is heard twice: the line is cleared between them.
  await said.evaluate((line) => {
    const heard: string[] = [];
    (window as unknown as { heard: string[] }).heard = heard;
    new MutationObserver(() => heard.push(line.textContent ?? "")).observe(
      line,
      { childList: true, characterData: true, subtree: true },
    );
  });
  const empty = { ...FILE, buffer: Buffer.alloc(0) };
  const refusal = "This file is empty. Nothing was uploaded.";
  await page.getByLabel("Choose a file").setInputFiles(empty);
  await expect(said).toHaveText(refusal);
  await page.getByLabel("Choose a file").setInputFiles(empty);
  await expect
    .poll(() =>
      page.evaluate(() =>
        (window as unknown as { heard: string[] }).heard.filter(
          (text) => text !== "",
        ),
      ),
    )
    .toEqual([refusal, refusal]);
});

test("an import ending moves focus only if Import had it", async ({ page }) => {
  await signIn(page);
  await page.getByLabel("Choose a file").setInputFiles(FILE);
  await importButton(page).click();
  await expect(result(page)).toContainText("Archived");

  // Focus left for a row during the upload, and stays there.
  gate.hold();
  await page.getByLabel("Choose a file").setInputFiles(largeFile());
  await importButton(page).click();
  const row = page.getByRole("link", { name: /history of synthetic.eml/i });
  await row.focus();
  gate.sendBody();
  gate.answer();
  await expect(result(page)).toContainText("large.eml");
  await expect(row).toBeFocused();

  // Focus on Import moves to Choose a file when Import turns off, after an
  // import and after a refusal replaces the choice.
  await page.getByLabel("Choose a file").setInputFiles(FILE);
  await importButton(page).press("Enter");
  await expect(result(page)).toContainText("Already archived");
  await expect(page.getByLabel("Choose a file")).toBeFocused();

  await page.getByLabel("Choose a file").setInputFiles(FILE);
  await importButton(page).focus();
  await drop(page, ["one.eml", "two.eml"]);
  await expect(importBox(page)).toContainText("Choose one file at a time.");
  await expect(page.getByLabel("Choose a file")).toBeFocused();
});

/** Files with mail content, as a drag from the desktop carries them. */
function transfer(page: Page, names: string[]) {
  return page.evaluateHandle((names) => {
    const data = new DataTransfer();
    for (const name of names) {
      data.items.add(
        new File([`Subject: ${name}\r\n\r\nDropped.\r\n`], name, {
          type: "message/rfc822",
        }),
      );
    }
    return data;
  }, names);
}

/** Drops files on the target, the import box unless given. */
async function drop(page: Page, names: string[], target = importBox(page)) {
  const dataTransfer = await transfer(page, names);
  await target.dispatchEvent("dragenter", { dataTransfer });
  await target.dispatchEvent("dragover", { dataTransfer });
  await target.dispatchEvent("drop", { dataTransfer });
}

/**
 * Drags a file over the target. Returns whether the page cancelled the event,
 * which stops the browser opening a dropped file, and the drop effect it set,
 * which the cursor shows.
 */
function dragOver(target: Locator) {
  return target.evaluate((element) => {
    // A constructed DataTransfer ignores dropEffect, so this stands in for
    // the one a real drag carries.
    const data = { types: ["Files"], dropEffect: "none" };
    const event = new DragEvent("dragover", {
      bubbles: true,
      cancelable: true,
    });
    Object.defineProperty(event, "dataTransfer", { value: data });
    element.dispatchEvent(event);
    return { cancelled: event.defaultPrevented, effect: data.dropEffect };
  });
}

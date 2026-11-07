import { expect, test, type Page } from "@playwright/test";

import { startImportGate, type ImportGate } from "./support/proxy";
import { startServer, type TestServer } from "./support/server";

// User Story 1 scenarios 8 to 11, FR-034 to FR-037: import takes two steps,
// shows progress and then a separate storing state, allows one submission,
// keeps its result until the next import, and warns before the page is left.
// The browser refuses an empty file and several files without uploading.

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

// Scenario 8, FR-034.
test("choosing a file shows its name, exact size, and the warning, and stores nothing", async ({
  page,
}) => {
  await signIn(page);
  const imports = countImports(page);
  await expect(importBox(page)).toContainText("Drop one .eml file here");
  await expect(importBox(page)).toContainText(WARNING);
  await expect(importButton(page)).toHaveCount(0);

  await page.getByLabel("Choose a file").setInputFiles(FILE);

  await expect(importBox(page)).toContainText("synthetic.eml");
  await expect(importBox(page)).toContainText(`${CONTENT.length} bytes`);
  await expect(importBox(page)).toContainText(WARNING);
  await expect(importButton(page)).toBeVisible();
  // The choice alone sends nothing, and the list stays empty after a reload.
  await page.waitForTimeout(500);
  expect(imports()).toBe(0);
  await page.reload();
  await expect(page.getByText("No mail yet")).toBeVisible();
});

// Scenario 9, FR-035.
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
  await expect(importBox(page)).toContainText(/Uploading, \d+%/);
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

// Scenario 10, FR-032.
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

// Scenario 11, FR-037.
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

// FR-036.
test.describe("the browser refuses before uploading", () => {
  test("several files", async ({ page }) => {
    await signIn(page);
    const imports = countImports(page);

    await page
      .getByLabel("Choose a file")
      .setInputFiles([FILE, { ...FILE, name: "second.eml" }]);

    await expect(importBox(page)).toContainText(
      "Choose one file at a time. Nothing was uploaded.",
    );
    await expect(importBox(page)).not.toContainText("synthetic.eml");
    await expect(importButton(page)).toHaveCount(0);
    expect(imports()).toBe(0);
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
    await expect(importButton(page)).toHaveCount(0);
    expect(imports()).toBe(0);
  });

  // FR-041: dropping adds to the button. One file is chosen, two are refused.
  test("several dropped files, while one dropped file is chosen", async ({
    page,
  }) => {
    await signIn(page);
    const imports = countImports(page);

    await drop(page, ["dropped.eml"]);
    await expect(importBox(page)).toContainText("dropped.eml");
    await expect(importButton(page)).toBeVisible();

    await drop(page, ["one.eml", "two.eml"]);
    await expect(importBox(page)).toContainText(
      "Choose one file at a time. Nothing was uploaded.",
    );
    await expect(importButton(page)).toHaveCount(0);
    expect(imports()).toBe(0);
  });
});

/** Drops files with mail content on the import box. */
async function drop(page: Page, names: string[]) {
  const transfer = await page.evaluateHandle((names) => {
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
  const box = importBox(page);
  await box.dispatchEvent("dragenter", { dataTransfer: transfer });
  await box.dispatchEvent("dragover", { dataTransfer: transfer });
  await box.dispatchEvent("drop", { dataTransfer: transfer });
}

import { readFile } from "node:fs/promises";

import { expect, test, type Download, type Page } from "@playwright/test";

import { importRead, readMail, type MailFile } from "./support/archive";
import { startServer, type TestServer } from "./support/server";
import { EMAIL, PASSWORD, signIn } from "./support/sources";

// Attachments are listed with their names as plain text, and saved with their
// decoded bytes under the cleaned name. Their addresses download, so an HTML
// or SVG attachment opened in a tab shows nothing and runs nothing.

const FIXTURE = "attachments.eml";
const BOUNDARY = "hostile-attachments";

interface Expected {
  position: string;
  /** What the row shows, with a control character as its code. */
  shown: string;
  type: string;
  /** The name after the download name rules. */
  saved: string;
  /** Text that opens as a page, and so must only download. */
  page?: boolean;
}

const ATTACHMENTS: Expected[] = [
  {
    position: "2",
    shown: "../../etc/passwd",
    type: "text/plain",
    // Slashes go, then the dots at the start.
    saved: "etcpasswd",
  },
  {
    position: "3",
    shown: "<b>x</b>.html",
    type: "text/html",
    saved: "<b>x<b>.html",
    page: true,
  },
  {
    position: "4",
    shown: "RechnungU+202Efdp.exe",
    type: "application/pdf",
    saved: "Rechnungfdp.exe",
  },
  {
    position: "5",
    shown: "report.html",
    type: "text/html",
    saved: "report.html",
    page: true,
  },
  {
    position: "6",
    shown: "drawing.svg",
    type: "image/svg+xml",
    saved: "drawing.svg",
    page: true,
  },
];

let server: TestServer;

test.beforeEach(async ({ page }) => {
  server = await startServer();
  await server.createAccount(EMAIL, PASSWORD);
  await signIn(page, server);
});

test.afterEach(async () => {
  await server.stop();
});

/**
 * The decoded bytes of each part, by part number. None of these parts has a
 * transfer encoding, so a part's bytes are what lies between its blank line
 * and the newline before the next boundary (RFC 2046, section 5.1.1).
 */
function partBytes(mail: MailFile): Map<string, Buffer> {
  const parts = mail.content
    .toString("latin1")
    .split(`\n--${BOUNDARY}`)
    .slice(1, -1);
  return new Map(
    parts.map((part, index) => [
      String(index + 1),
      Buffer.from(part.slice(part.indexOf("\n\n") + 2), "latin1"),
    ]),
  );
}

async function openFixture(page: Page) {
  const mail = await readMail(
    new URL("./fixtures/hostile/", import.meta.url),
    FIXTURE,
  );
  const ids = await importRead(page, server, [mail]);
  const id = ids.get(FIXTURE)!;
  await page.goto(`${server.origin}/messages/${id}`);
  await expect(
    page.getByRole("heading", { name: "Hostile attachments" }),
  ).toBeVisible();
  return { id, bytes: partBytes(mail) };
}

function attachments(page: Page) {
  return page.getByRole("region", { name: /^Attachments/ });
}

function row(page: Page, attachment: Expected) {
  return attachments(page)
    .getByRole("listitem")
    .filter({
      has: page.getByRole("button", {
        name: `Save ${attachment.saved}`,
        exact: true,
      }),
    });
}

/**
 * The name Chromium saves under. It replaces characters that some file
 * systems refuse, such as < and >, with _ after MailArkiv's rules.
 */
function savedAs(name: string): string {
  return name.replace(/[<>]/g, "_");
}

async function expectSaved(download: Download, name: string, bytes: Buffer) {
  expect(download.suggestedFilename()).toBe(savedAs(name));
  expect(await readFile((await download.path())!)).toEqual(bytes);
}

test("each attachment shows its name, type, and size as plain text", async ({
  page,
}) => {
  const { bytes } = await openFixture(page);

  await expect(
    attachments(page).getByRole("heading", { name: "Attachments 5" }),
  ).toBeVisible();
  await expect(attachments(page).getByRole("listitem")).toHaveCount(5);
  for (const attachment of ATTACHMENTS) {
    const item = row(page, attachment);
    await expect(item, attachment.saved).toContainText(attachment.shown);
    await expect(item, attachment.saved).toContainText(attachment.type);
    await expect(item, attachment.saved).toContainText(
      `${bytes.get(attachment.position)!.length} B`,
    );
  }
  // The name's markup shows as text, and its bidi control as a code.
  await expect(attachments(page).locator("b")).toHaveCount(0);
  await expect(
    row(page, ATTACHMENTS[2]).getByText("U+202E", { exact: true }),
  ).toBeVisible();
  expect(await attachments(page).textContent()).not.toContain("‮");
});

test("Save keeps the decoded bytes under the cleaned name", async ({
  page,
}) => {
  const { bytes } = await openFixture(page);

  for (const attachment of ATTACHMENTS) {
    const saved = page.waitForEvent("download");
    await row(page, attachment)
      .getByRole("button", { name: /^Save / })
      .click();
    await expectSaved(
      await saved,
      attachment.saved,
      bytes.get(attachment.position)!,
    );
  }
  expect(await page.title()).not.toContain("MARKER");
});

test("an HTML or SVG attachment opened in a tab only downloads", async ({
  page,
}) => {
  const { id, bytes } = await openFixture(page);

  for (const attachment of ATTACHMENTS.filter(({ page }) => page)) {
    const tab = await page.context().newPage();
    const dialogs: string[] = [];
    tab.on("dialog", (dialog) => {
      dialogs.push(dialog.message());
      void dialog.dismiss();
    });
    const saved = tab.waitForEvent("download");
    const opened = await tab
      .goto(
        `${server.origin}/api/messages/${id}/attachments/${attachment.position}`,
      )
      .catch((error: Error) => error);
    // A navigation that turns into a download has no page to answer with.
    expect(opened, attachment.saved).toBeInstanceOf(Error);
    await expectSaved(
      await saved,
      attachment.saved,
      bytes.get(attachment.position)!,
    );

    expect(tab.url(), attachment.saved).toBe("about:blank");
    expect(await tab.title(), attachment.saved).toBe("");
    expect(
      await tab.evaluate(() => document.body?.innerHTML ?? ""),
      attachment.saved,
    ).toBe("");
    expect(dialogs, attachment.saved).toEqual([]);
    await tab.close();
  }
});

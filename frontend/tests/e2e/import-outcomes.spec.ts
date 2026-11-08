import { rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { expect, test, type Page, type Route } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// Every import ends in exactly one of six outcomes. Stubbed answers force
// each outcome and each kind of proxy answer. The real server then shows that
// Archived means listed and Failed means not listed.

const PASSWORD = "correct horse battery";
const FILE = {
  name: "synthetic.eml",
  mimeType: "message/rfc822",
  buffer: Buffer.from(
    "From: sender@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
  ),
};
const HASH = "ea9449bf2f2e4ba3d3c4f3d61a1f6b8e0c3e5f8b0a9d2c7e6f5a4b3c2d1e0f9a";
const STUB_MESSAGE = {
  id: 17,
  content_hash: HASH,
  size: 31,
  file_name: "stub.eml",
  imported_at: "2026-11-06T15:50:52.123Z",
  import_count: 1,
};

const WORDS = [
  "Archived",
  "Already archived",
  "Restored",
  "Refused",
  "Failed",
  "Unconfirmed",
] as const;
type Word = (typeof WORDS)[number];

const NOT_STORED = "Nothing was stored.";
const NOT_ARCHIVED = "The file is not archived.";
const UNCONFIRMED =
  "The file may be archived, and importing it again is safe and stores no second copy.";
const PROXY_LIMIT = "The reverse proxy's upload limit refused it.";

let server: TestServer;

test.beforeEach(async () => {
  server = await startServer();
  await server.createAccount("admin@example.test", PASSWORD);
});

test.afterEach(async () => {
  await server.stop();
});

async function signIn(page: Page) {
  await page.goto(server.origin);
  await page.getByLabel("Email address").fill("admin@example.test");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
}

async function importFile(page: Page, file = FILE) {
  await page.getByLabel("Choose a file").setInputFiles(file);
  await page.getByRole("button", { name: "Import" }).click();
}

/** The result is a toast, and these tests raise no other. */
function result(page: Page) {
  return page.getByRole("region", { name: "Notifications" });
}

/** The outcome shows its own word and no other outcome's word. */
async function expectOnly(page: Page, word: Word) {
  await expect(result(page).getByText(word, { exact: true })).toBeVisible();
  for (const other of WORDS.filter((each) => each !== word)) {
    await expect(result(page).getByText(other, { exact: true })).toHaveCount(0);
  }
}

type Stub = {
  name: string;
  answer: (route: Route) => Promise<void>;
  word: Word;
  lines: string[];
};

/** An answer from MailArkiv, which carries its marker. */
function marked(status: number, body: unknown) {
  return (route: Route) =>
    route.fulfill({
      status,
      headers: { "MailArkiv-Response": "1" },
      contentType: "application/json",
      body: JSON.stringify(body),
    });
}

/** An answer from something in between, such as a proxy. */
function unmarked(status: number, body: string) {
  return (route: Route) =>
    route.fulfill({ status, contentType: "text/html", body });
}

const STUBS: Stub[] = [
  {
    name: "Archived",
    answer: marked(201, { outcome: "archived", message: STUB_MESSAGE }),
    word: "Archived",
    lines: ["stub.eml", "31 B"],
  },
  {
    name: "Already archived",
    answer: marked(200, { outcome: "already_archived", message: STUB_MESSAGE }),
    word: "Already archived",
    lines: [
      "This import was added to the history, and no second copy was stored.",
    ],
  },
  {
    name: "Restored",
    answer: marked(200, { outcome: "restored", message: STUB_MESSAGE }),
    word: "Restored",
    lines: ["The stored copy was restored from this file."],
  },
  {
    name: "Refused as empty",
    answer: marked(422, { outcome: "refused", reason: "empty" }),
    word: "Refused",
    lines: ["This file is empty.", NOT_STORED],
  },
  {
    name: "Refused with no mail header line",
    answer: marked(422, { outcome: "refused", reason: "no_mail_header" }),
    word: "Refused",
    lines: ["This file doesn't start with a mail header line.", NOT_STORED],
  },
  {
    name: "Refused as too large",
    answer: marked(413, { outcome: "refused", reason: "too_large" }),
    word: "Refused",
    lines: ["This file is over the 150 MB limit.", NOT_STORED],
  },
  {
    name: "Failed with storage full",
    answer: marked(507, { outcome: "failed", reason: "storage_full" }),
    word: "Failed",
    lines: [
      NOT_ARCHIVED,
      "The disk holding the archive ran out of space during an import.",
      "Free disk space, then import the file again.",
    ],
  },
  {
    name: "Failed with a storage write",
    answer: marked(500, { outcome: "failed", reason: "storage_write_failed" }),
    word: "Failed",
    lines: [
      NOT_ARCHIVED,
      "The server could not write to its storage location, such as through denied permissions.",
      "Fix the storage location, then import the file again.",
    ],
  },
  {
    name: "Failed with a catalog write",
    answer: marked(500, { outcome: "failed", reason: "catalog_write_failed" }),
    word: "Failed",
    lines: [
      NOT_ARCHIVED,
      "The original was stored but its catalog entry was not written.",
      "Import the same file again. If this repeats, check the server log.",
    ],
  },
  {
    name: "a marked 403",
    answer: marked(403, { error: "cross_origin" }),
    word: "Failed",
    lines: [NOT_ARCHIVED],
  },
  {
    name: "a marked 500 with no outcome",
    answer: marked(500, { error: "internal_error" }),
    word: "Unconfirmed",
    lines: [UNCONFIRMED],
  },
  {
    name: "an unmarked 413",
    answer: unmarked(
      413,
      "<html><body>413 Request Entity Too Large</body></html>",
    ),
    word: "Failed",
    lines: [NOT_ARCHIVED, PROXY_LIMIT],
  },
  {
    name: "an unmarked 502",
    answer: unmarked(502, "<html><body>502 Bad Gateway</body></html>"),
    word: "Unconfirmed",
    lines: [UNCONFIRMED],
  },
  {
    name: "an unmarked 504",
    answer: unmarked(504, "<html><body>504 Gateway Time-out</body></html>"),
    word: "Unconfirmed",
    lines: [UNCONFIRMED],
  },
  {
    name: "an unmarked 200 that looks like MailArkiv's",
    answer: (route) =>
      route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ outcome: "archived", message: STUB_MESSAGE }),
      }),
    word: "Unconfirmed",
    lines: [UNCONFIRMED],
  },
  {
    name: "an aborted request",
    answer: (route) => route.abort("aborted"),
    word: "Unconfirmed",
    lines: [UNCONFIRMED],
  },
];

for (const { name, answer, word, lines } of STUBS) {
  test(`${name} shows ${word}`, async ({ page }) => {
    await signIn(page);
    await page.route("**/api/imports", answer);

    await importFile(page);

    await expectOnly(page, word);
    for (const line of lines) {
      await expect(result(page)).toContainText(line);
    }
    if (word !== "Failed") {
      await expect(result(page)).not.toContainText(PROXY_LIMIT);
    }
  });
}

test("Archived links to the message's history", async ({ page }) => {
  await signIn(page);
  await page.route(
    "**/api/imports",
    marked(201, { outcome: "archived", message: STUB_MESSAGE }),
  );

  await importFile(page);

  await expect(
    result(page).getByRole("link", { name: "Open import history" }),
  ).toHaveAttribute("href", "/?message=17");
});

// No Archived for a message that isn't listed, and no Failed for one that is.
test("on the real server, Archived is listed and Failed isn't", async ({
  page,
}) => {
  await signIn(page);
  const list = page.getByRole("list", { name: "Messages" });

  await importFile(page, { ...FILE, name: "archived.eml" });
  await expectOnly(page, "Archived");
  await expect(list).toContainText("archived.eml");

  await result(page).getByRole("link", { name: "Open import history" }).click();
  const drawer = page.getByRole("dialog", { name: "Import history" });
  await expect(drawer).toContainText("archived.eml");
  await drawer.getByRole("button", { name: "Close" }).click();

  // A file where the temp directory was makes every temp file fail to open,
  // whichever user runs the server.
  const tmp = join(server.dataDir, "tmp");
  await rm(tmp, { recursive: true });
  await writeFile(tmp, "");

  await importFile(page, {
    ...FILE,
    name: "failed.eml",
    buffer: Buffer.from("Subject: Another\r\n\r\nNot stored.\r\n"),
  });
  await expectOnly(page, "Failed");
  await expect(result(page)).toContainText(NOT_ARCHIVED);

  await page.reload();
  await expect(list.getByRole("listitem")).toHaveCount(1);
  await expect(list).toContainText("archived.eml");
  await expect(list).not.toContainText("failed.eml");
});

// The refresh after the import fails too, and the result has to survive that,
// or the administrator never learns that the file may be archived.
test("a server that stops during an import leaves Unconfirmed on screen", async ({
  page,
}) => {
  await signIn(page);

  await page.getByLabel("Choose a file").setInputFiles(FILE);
  await server.stop();
  await page.getByRole("button", { name: "Import" }).click();

  const unconfirmed = page.getByText(UNCONFIRMED);
  const failedLoad = page.getByText("This page couldn't be loaded.");
  await expect(unconfirmed).toBeVisible();
  // The list couldn't be read, so it says so, and shows no empty list.
  await expect(failedLoad).toBeVisible();
  await expect(page.getByText("No mail yet")).toHaveCount(0);

  await page.getByRole("button", { name: "Try again" }).click();
  await expect(failedLoad).toBeVisible();
  await expect(unconfirmed).toBeVisible();
});

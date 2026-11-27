import { fileURLToPath } from "node:url";

import { expect, type Locator, type Page } from "@playwright/test";

import { startImap, type ImapServer } from "./imap";
import { startServer, type TestServer } from "./server";
import { addSource, EMAIL, MAILBOX, PASSWORD, signIn } from "./sources";

// The malformed messages, collected from a test mailbox, so the one an import
// refuses for not starting with a header arrives as mail from a server would.

const FIXTURES = [
  "broken-base64.eml",
  "unknown-charset.eml",
  "missing-boundary.eml",
  "cut-off.eml",
  "unreadable-header.eml",
  "no-headers.eml",
  "spoofed-sender.eml",
  "long-name.eml",
  "control-characters.eml",
  "html-subject.eml",
];

/** The source's name, which names the rows processing couldn't read. */
export const SOURCE = "Postfach";

export interface MalformedSet {
  server: TestServer;
  imap: ImapServer;
  stop(): Promise<void>;
}

export async function startMalformedSet(): Promise<MalformedSet> {
  const imap = await startImap({
    ...MAILBOX,
    folders: [],
    messages: FIXTURES.map((name) => ({
      folder: "INBOX",
      path: fileURLToPath(
        new URL(
          `../../../../internal/mailparse/testdata/malformed/${name}`,
          import.meta.url,
        ),
      ),
    })),
  });
  let server: TestServer;
  try {
    server = await startServer({ collection: { caFile: imap.caFile } });
  } catch (error) {
    await imap.stop();
    throw error;
  }
  await server.createAccount(EMAIL, PASSWORD);
  return {
    server,
    imap,
    stop: async () => {
      await server.stop();
      await imap.stop();
    },
  };
}

/**
 * Signs in, collects the set, and returns once the list holds every message
 * and processing has reached each.
 */
export async function collectMalformedSet(
  page: Page,
  set: MalformedSet,
): Promise<Locator> {
  await signIn(page, set.server);
  await addSource(page, set.server, set.imap, { name: SOURCE });
  await page.goto(set.server.origin);
  const list = page.getByRole("list", { name: "Messages" });
  await expect(async () => {
    await page.reload();
    await expect(list.getByRole("listitem")).toHaveCount(FIXTURES.length, {
      timeout: 1_000,
    });
    await expect(list.getByText("Not processed yet")).toHaveCount(0, {
      timeout: 1_000,
    });
  }).toPass({ timeout: 60_000 });
  return list;
}

/** The row whose link is named by label. */
export function rowOf(list: Locator, label: string): Locator {
  return list.getByRole("listitem").filter({
    has: list.page().getByRole("link", { name: `Open ${label}`, exact: true }),
  });
}

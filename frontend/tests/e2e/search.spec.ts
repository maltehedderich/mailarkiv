import { readFile } from "node:fs/promises";

import { expect, test, type Page, type Request } from "@playwright/test";

import {
  startServer,
  type ServerOptions,
  type TestServer,
} from "./support/server";

// Search words and filters name people and topics, so they never reach an
// address the browser, a proxy, or the server's log records. They travel
// only in the body of POST /api/search.

// Day ranges are in the browser's time zone. Berlin changes to summer time
// on 2026-03-29 and back on 2026-10-25.
test.use({ timezoneId: "Europe/Berlin" });

const PASSWORD = "correct horse battery";
// Its body says "Der <b>Quartals</b>bericht", so the word is split by markup.
const FIXTURE = new URL(
  "../../../internal/mailparse/testdata/plain/html-only.eml",
  import.meta.url,
);
const WORD = "quartals";
const SEARCH_SET = new URL(
  "../../../internal/web/testdata/searchset/",
  import.meta.url,
);
// Every search below waits for this long at most to see a new message.
const SEARCHABLE_WITHIN_MS = 60_000;

let server: TestServer | null = null;

async function start(options: ServerOptions = {}): Promise<TestServer> {
  server = await startServer(options);
  return server;
}

test.afterEach(async () => {
  await server?.stop();
  server = null;
});

async function signIn(page: Page, running: TestServer) {
  await running.createAccount("admin@example.test", PASSWORD);
  await page.goto(running.origin);
  await page.getByLabel("Email address").fill("admin@example.test");
  await page.getByLabel("Password").fill(PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("heading", { name: "Messages" })).toBeVisible();
}

async function importFile(
  page: Page,
  running: TestServer,
  name: string,
  data: Buffer,
) {
  const response = await page.request.post(`${running.origin}/api/imports`, {
    headers: { "MailArkiv-File-Name": name },
    data,
  });
  expect(response.status()).toBe(201);
}

// The imports below go past the page, so each reloads it to list them.

async function importSearchSet(
  page: Page,
  running: TestServer,
  names: string[],
) {
  for (const name of names) {
    const file = await readFile(new URL(`${name}.eml`, SEARCH_SET));
    await importFile(page, running, `${name}.eml`, file);
  }
  await page.reload();
}

/** Imports messages with subject `Message N` and body `Body N.`. */
async function importNumbered(page: Page, running: TestServer, count: number) {
  for (let index = 1; index <= count; index++) {
    await importFile(
      page,
      running,
      `message-${String(index).padStart(2, "0")}.eml`,
      Buffer.from(`Subject: Message ${index}\r\n\r\nBody ${index}.\r\n`),
    );
  }
  await page.reload();
}

/** Collects every POST /api/search the page sends. */
function searchRequests(page: Page): Request[] {
  const sent: Request[] = [];
  page.on("request", (request) => {
    if (request.method() === "POST" && request.url().endsWith("/api/search")) {
      sent.push(request);
    }
  });
  return sent;
}

function results(page: Page) {
  return page.getByRole("list", { name: "Search results" });
}

/** The subjects of the result rows, in order. */
function subjects(page: Page) {
  return results(page).getByRole("link", { name: /^History of / });
}

function field(page: Page, label: string) {
  return page.getByLabel(label, { exact: true });
}

async function openFilters(page: Page) {
  const toggle = page.getByRole("button", { name: /^Filters/ });
  if ((await toggle.getAttribute("aria-expanded")) !== "true") {
    await toggle.click();
  }
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
}

/**
 * Searches until the total reads `total` and nothing is left out, which
 * means processing and indexing have reached every message.
 */
async function searchUntilAll(page: Page, total: string) {
  await expect(async () => {
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(page.getByText(total, { exact: true })).toBeVisible({
      timeout: 1_000,
    });
    await expect(page.getByText(/included yet/)).toHaveCount(0);
  }).toPass({ timeout: SEARCHABLE_WITHIN_MS });
}

test("a body word finds its message, and no address holds the word", async ({
  page,
}) => {
  const requested: string[] = [];
  page.on("request", (request) => requested.push(request.url()));

  const running = await start();
  await signIn(page, running);

  await page.getByLabel("Choose a file").setInputFiles({
    name: "report.eml",
    mimeType: "message/rfc822",
    buffer: await readFile(FIXTURE),
  });
  await page.getByRole("button", { name: "Import" }).click();
  await expect(
    page.getByRole("list", { name: "Messages" }).getByRole("listitem"),
  ).toHaveCount(1);

  // A new message is searchable within 60 s. Until then it isn't included.
  await field(page, "Words").fill(WORD);
  await searchUntilAll(page, "1 message matches.");

  const row = results(page).getByRole("listitem");
  await expect(row).toHaveCount(1);
  await expect(row).toContainText("Bernd Weber");
  await expect(row).toContainText("bernd@example.test");
  await expect(
    row.getByRole("link", { name: "History of Bericht" }),
  ).toBeVisible();
  // The file name shows only in the history.
  await expect(row).not.toContainText("report.eml");

  // The history opens over the results and closes back to them.
  await row.getByRole("link", { name: "History of Bericht" }).click();
  const drawer = page.getByRole("dialog", { name: "History" });
  await expect(drawer).toContainText("report.eml");
  await drawer.getByRole("button", { name: "Close" }).click();
  await expect(drawer).toBeHidden();
  await expect(results(page).getByRole("listitem")).toHaveCount(1);

  await page.getByRole("button", { name: "Clear" }).click();
  await expect(field(page, "Words")).toHaveValue("");
  await expect(
    page.getByRole("list", { name: "Messages" }).getByRole("listitem"),
  ).toHaveCount(1);

  expect(page.url().toLowerCase()).not.toContain(WORD);
  expect(requested.length).toBeGreaterThan(0);
  for (const url of requested) {
    expect(url.toLowerCase()).not.toContain(WORD);
  }
});

test("each filter finds exactly its messages, newest first", async ({
  page,
}) => {
  const running = await start();
  await signIn(page, running);
  await importSearchSet(page, running, [
    "anna-com-bericht",
    "anna-com-vertrag",
    "anna-com-rueckfrage",
    "anna-org-apfelernte",
    "bob-com-termin",
    "carol-rechnung",
    "attacker-zahlung",
    "entwurf",
  ]);

  await openFilters(page);
  await field(page, "First day").fill("2000-01-01");
  await searchUntilAll(page, "8 messages match.");

  const cases: {
    name: string;
    fill: Record<string, string>;
    check?: string;
    expected: string[];
  }[] = [
    {
      name: "a complete address in From",
      fill: { From: "anna@example.com" },
      expected: ["Rueckfrage", "Vertrag", "Planung"],
    },
    {
      // Not mail.example.com, and not attacker-example.com.
      name: "a domain in From",
      fill: { From: "example.com" },
      expected: ["Rueckfrage", "Termin", "Vertrag", "Planung"],
    },
    {
      name: "a word in From",
      fill: { From: "anna" },
      expected: ["Rueckfrage", "Apfelernte", "Vertrag", "Planung"],
    },
    {
      name: "an address in To",
      fill: { To: "anna@example.com" },
      expected: ["Zahlung", "Termin"],
    },
    {
      name: "Subject",
      fill: { Subject: "termin" },
      expected: ["Termin"],
    },
    {
      // Entwurf's body says Vertragsentwurf, but its attachment is Notizen.csv.
      name: "Attachment name",
      fill: { "Attachment name": "vertrag" },
      expected: ["Vertrag"],
    },
    {
      // A 23-hour day: from 00:00 +01:00 to 00:00 +02:00.
      name: "the day summer time starts",
      fill: { "First day": "2026-03-29", "Last day": "2026-03-29" },
      expected: ["Termin", "Vertrag"],
    },
    {
      // A 25-hour day: from 00:00 +02:00 to 00:00 +01:00.
      name: "the day summer time ends",
      fill: { "First day": "2026-10-25", "Last day": "2026-10-25" },
      expected: ["Rueckfrage", "Rechnung"],
    },
    {
      name: "Has attachments",
      fill: {},
      check: "Has attachments",
      expected: ["Entwurf", "Vertrag"],
    },
    {
      name: "words and a filter together",
      fill: { Words: "vertrag", From: "anna" },
      expected: ["Vertrag"],
    },
  ];

  for (const { name, fill, check, expected } of cases) {
    await test.step(name, async () => {
      await page.getByRole("button", { name: "Clear" }).click();
      await expect(field(page, "Words")).toHaveValue("");
      await expect(field(page, "First day")).toHaveValue("");
      for (const [label, value] of Object.entries(fill)) {
        await field(page, label).fill(value);
      }
      if (check !== undefined) await field(page, check).check();
      await page.getByRole("button", { name: "Search", exact: true }).click();
      await expect(subjects(page)).toHaveText(expected);
      const count = expected.length;
      await expect(
        page.getByText(
          `${count} ${count === 1 ? "message matches" : "messages match"}.`,
          { exact: true },
        ),
      ).toBeVisible();
    });
  }

  await test.step("a last day before the first day sends nothing", async () => {
    const sent = searchRequests(page);
    await page.getByRole("button", { name: "Clear" }).click();
    await field(page, "First day").fill("2026-03-30");
    await field(page, "Last day").fill("2026-03-29");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(field(page, "Last day")).toHaveAccessibleDescription(
      "The last day is before the first day.",
    );
    await expect(field(page, "Last day")).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    expect(sent).toHaveLength(0);
  });

  await test.step("a form with nothing to search for shows the list", async () => {
    const sent = searchRequests(page);
    await page.getByRole("button", { name: "Clear" }).click();
    await field(page, "Words").fill("?! --");
    await field(page, "From").fill("@");
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(
      page.getByRole("list", { name: "Messages" }).getByRole("listitem"),
    ).toHaveCount(8);
    await expect(results(page)).toHaveCount(0);
    expect(sent).toHaveLength(0);
  });
});

test("Back from a result shows the same search, page, and results", async ({
  page,
}) => {
  const running = await start();
  await signIn(page, running);
  await importNumbered(page, running, 51);

  await field(page, "Words").fill("body");
  await searchUntilAll(page, "51 messages match.");
  await expect(results(page).getByRole("listitem")).toHaveCount(50);

  const pager = page.getByRole("navigation", { name: "Pages" });
  await expect(pager).toContainText("1–50 of 51");
  await pager.getByRole("button", { name: "Next page" }).click();
  await expect(pager).toContainText("51–51 of 51");
  await expect(subjects(page)).toHaveText(["Message 1"]);
  await expect(page).toHaveTitle("Search results, page 2 · MailArkiv");

  const sent = searchRequests(page);

  await test.step("a result's history, then Back", async () => {
    await subjects(page).first().click();
    const drawer = page.getByRole("dialog", { name: "History" });
    await expect(drawer).toContainText("message-01.eml");
    await page.goBack();
    await expect(drawer).toBeHidden();
    await expect(pager).toContainText("51–51 of 51");
    await expect(subjects(page)).toHaveText(["Message 1"]);
  });

  await test.step("another page, then Back", async () => {
    await page.getByRole("link", { name: "Status" }).first().click();
    await expect(page.getByRole("heading", { name: "Status" })).toBeVisible();
    await page.goBack();
    await expect(field(page, "Words")).toHaveValue("body");
    await expect(page.getByText("51 messages match.")).toBeVisible();
    await expect(pager).toContainText("51–51 of 51");
    await expect(subjects(page)).toHaveText(["Message 1"]);
  });

  expect(sent).toHaveLength(0);

  await pager.getByRole("button", { name: "Previous page" }).click();
  await expect(pager).toContainText("1–50 of 51");
  await expect(results(page).getByRole("listitem")).toHaveCount(50);
  expect(sent).toHaveLength(1);
  expect(sent[0].postDataJSON()).toMatchObject({ words: "body", page: 1 });
});

test("words and filters reach only the request body", async ({ page }) => {
  const requested: string[] = [];
  page.on("request", (request) => requested.push(request.url()));
  const sent = searchRequests(page);

  const running = await start();
  await signIn(page, running);

  const markers: Record<string, string> = {
    Words: "wortmarke",
    From: "absendermarke",
    To: "empfaengermarke",
    Subject: "betreffmarke",
    "Attachment name": "anhangmarke",
  };
  await openFilters(page);
  for (const [label, marker] of Object.entries(markers)) {
    await field(page, label).fill(marker);
  }
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(page.getByText("No messages match.")).toBeVisible();

  expect(sent).toHaveLength(1);
  expect(sent[0].postDataJSON()).toMatchObject({
    words: "wortmarke",
    from: "absendermarke",
    to: "empfaengermarke",
    subject: "betreffmarke",
    attachment: "anhangmarke",
  });
  const addresses = [...requested, page.url()];
  for (const marker of Object.values(markers)) {
    for (const address of addresses) {
      expect(address.toLowerCase()).not.toContain(marker);
    }
  }
});

test("the results header counts messages search can't include yet", async ({
  page,
}) => {
  // Each commit lands in the index, but the server never hears it did, so
  // it counts the messages as not indexed.
  const running = await start({ search: { faults: "after-commit=fail" } });
  await signIn(page, running);
  await importNumbered(page, running, 3);

  await field(page, "Words").fill("body");
  await expect(async () => {
    await page.getByRole("button", { name: "Search", exact: true }).click();
    await expect(
      page.getByText("3 messages match.", { exact: true }),
    ).toBeVisible({ timeout: 1_000 });
  }).toPass({ timeout: SEARCHABLE_WITHIN_MS });
  await expect(
    page.getByText(
      "3 messages aren't included yet, because processing or indexing hasn't reached them.",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(results(page).getByRole("listitem")).toHaveCount(3);
});

test("a search that runs out of time asks for more words or filters", async ({
  page,
}) => {
  const running = await start({ search: { faults: "during-search=fail" } });
  await signIn(page, running);

  await field(page, "Words").fill("body");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(
    page.getByText("This search took too long. Add words or filters.", {
      exact: true,
    }),
  ).toBeVisible();
});

test("without the search program, search says why and the list still works", async ({
  page,
}) => {
  const running = await start({ search: "missing" });
  await signIn(page, running);
  await importNumbered(page, running, 1);

  await field(page, "Words").fill("body");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(
    page.getByText(
      /^Search is unavailable: Search process didn't start\. (Next start \d{2}:\d{2}:\d{2} \+0[12]:00|A start is under way)\. The list and downloads still work\.$/,
    ),
  ).toBeVisible();

  await page.getByRole("button", { name: "Clear" }).click();
  await expect(
    page.getByRole("list", { name: "Messages" }).getByRole("listitem"),
  ).toHaveCount(1);
});

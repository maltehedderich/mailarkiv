import { createServer } from "node:http";
import type { AddressInfo } from "node:net";

import { expect, test, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// User Story 2 in the browser: scenarios 6, 8, and 9, FR-040, and US1
// scenario 6. Direct requests without a session are tested in Go, generated
// from the route table.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const NEW_PASSWORD = "a different horse battery";
const ENDED = "Your session ended. Sign in again to continue.";
const NOTE = "Accounts and passwords are managed on the server.";
const GUIDE =
  "https://github.com/maltehedderich/mailarkiv/blob/main/docs/OPERATIONS.md";
const CONTENT = Buffer.from(
  "From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Synthetic\r\n\r\nHello.\r\n",
);

let server: TestServer;

test.beforeEach(async () => {
  server = await startServer();
});

test.afterEach(async () => {
  await server.stop();
});

async function submitSignIn(page: Page, email: string, password: string) {
  await page.getByLabel("Email address").fill(email);
  await page.getByLabel("Password").fill(password);
  const answered = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === "/api/session" &&
      response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Sign in" }).click();
  await answered;
}

/** Opens the app with no session and signs in from the sign-in page. */
async function signInFromStart(page: Page) {
  await page.goto(server.origin);
  await expect(page).toHaveURL(`${server.origin}/sign-in`);
  await submitSignIn(page, EMAIL, PASSWORD);
  await expect(page).toHaveURL(`${server.origin}/`);
}

async function importFile(page: Page) {
  await page.getByLabel("Choose a file").setInputFiles({
    name: "synthetic.eml",
    mimeType: "message/rfc822",
    buffer: CONTENT,
  });
  await page.getByRole("button", { name: "Import" }).click();
  await expect(page.getByRole("main").getByRole("status")).toContainText(
    "Archived",
  );
}

/** Waits for sign-in and returns its query. */
async function signInQuery(page: Page): Promise<URLSearchParams> {
  await expect(page).toHaveURL(/\/sign-in(\?|$)/);
  return new URL(page.url()).searchParams;
}

// Scenario 6, FR-001.
test("a fresh install offers only sign-in, with no way to create an account", async ({
  page,
}) => {
  await page.goto(server.origin);

  await expect(page).toHaveURL(`${server.origin}/sign-in`);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page.getByText(NOTE)).toBeVisible();
  // FR-001: the note points to the operator guide. The path is the link's
  // text, so it still helps where GitHub can't be reached.
  await expect(
    page.getByRole("link", { name: "docs/OPERATIONS.md" }),
  ).toHaveAttribute("href", GUIDE);
  // The only action on the page is signing in.
  await expect(page.getByRole("button")).toHaveText(["Sign in"]);
  await expect(
    page.getByRole("link", { name: /create|sign up|register/i }),
  ).toHaveCount(0);
  await expect(page.getByText(/session ended/i)).toHaveCount(0);
});

// Scenario 8, FR-007. The server sends frame-ancestors 'none', and the
// browser must refuse to show the page. The other site is a real server on
// 127.0.0.1, another origin than localhost: Chromium's local network checks
// would block a public origin's frame before MailArkiv answered at all.
test("another site can't show MailArkiv in a frame", async ({ page }) => {
  const other = await serveFramingPage(`${server.origin}/sign-in`);
  try {
    const answered = page.waitForResponse(`${server.origin}/sign-in`);
    const refused = page.waitForEvent("requestfailed", (request) =>
      request.url().startsWith(server.origin),
    );

    await page.goto(other.origin);

    // MailArkiv answered, so only the browser's refusal keeps the page out.
    const response = await answered;
    expect(response.status()).toBe(200);
    expect(response.headers()["content-security-policy"]).toContain(
      "frame-ancestors 'none'",
    );
    expect((await refused).failure()?.errorText).toBe(
      "net::ERR_BLOCKED_BY_RESPONSE",
    );
    // Chromium shows its own error page in the frame.
    await expect
      .poll(() => page.frames().map((frame) => frame.url()))
      .toEqual([`${other.origin}/`, "chrome-error://chromewebdata/"]);
    await expect(
      page.frameLocator("iframe").getByRole("heading", { name: "Sign in" }),
    ).toHaveCount(0);
  } finally {
    await other.close();
  }
});

/** Serves a page on another origin that frames `url`. */
async function serveFramingPage(
  url: string,
): Promise<{ origin: string; close(): Promise<void> }> {
  const framer = createServer((_request, response) => {
    response.writeHead(200, { "Content-Type": "text/html" });
    response.end(
      `<!doctype html><title>Another site</title><iframe title="Framed" src="${url}"></iframe>`,
    );
  });
  await new Promise<void>((resolve) =>
    framer.listen(0, "127.0.0.1", () => resolve()),
  );
  // A server listening on a TCP port always reports an AddressInfo.
  const address = framer.address() as AddressInfo;
  return {
    origin: `http://127.0.0.1:${address.port}`,
    close: () =>
      new Promise<void>((resolve, reject) =>
        framer.close((error) => (error ? reject(error) : resolve())),
      ),
  };
}

// FR-040: only a tab that had a signed-in page open is told its session
// ended. Signing in still returns to the page asked for.
test("a first visit with no session shows no session-ended message", async ({
  page,
}) => {
  await server.createAccount(EMAIL, PASSWORD);

  // The server's download redirect always says ended=1. A new tab ignores it.
  await page.goto(`${server.origin}/sign-in?ended=1&next=%2F`);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page.getByText(/session ended/i)).toHaveCount(0);

  await page.goto(`${server.origin}/?page=1`);
  await expect(page).toHaveURL(`${server.origin}/sign-in?next=%2F%3Fpage%3D1`);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page.getByText(/session ended/i)).toHaveCount(0);

  await submitSignIn(page, EMAIL, PASSWORD);
  await expect(page).toHaveURL(`${server.origin}/?page=1`);
});

// Scenario 9, FR-005, FR-040: a new password ends the session, the next
// action opens sign-in with the message, and signing in returns to the page.
test("a session ended by a new password opens sign-in, then returns to the page", async ({
  page,
}) => {
  await server.createAccount(EMAIL, PASSWORD);
  await signInFromStart(page);
  await page.goto(`${server.origin}/?page=1`);
  await expect(page.getByText("No mail yet")).toBeVisible();

  await server.setPassword(EMAIL, NEW_PASSWORD);

  const imports: string[] = [];
  page.on("request", (request) => {
    if (new URL(request.url()).pathname === "/api/imports") {
      imports.push(request.method());
    }
  });
  await page.getByLabel("Choose a file").setInputFiles({
    name: "synthetic.eml",
    mimeType: "message/rfc822",
    buffer: CONTENT,
  });
  await page.getByRole("button", { name: "Import" }).click();

  await expect(page).toHaveURL(
    `${server.origin}/sign-in?ended=1&next=%2F%3Fpage%3D1`,
  );
  await expect(page.getByText(ENDED)).toBeVisible();
  // The session check runs before the upload, so no byte was sent.
  expect(imports).toEqual([]);

  await submitSignIn(page, EMAIL, NEW_PASSWORD);
  await expect(page).toHaveURL(`${server.origin}/?page=1`);
  await expect(page.getByText("No mail yet")).toBeVisible();
});

// FR-040 through the plain download link, which the router doesn't handle.
// The server answers 303 to sign-in, and the browser saves nothing.
test("a download link after the session ended opens sign-in", async ({
  page,
}) => {
  await server.createAccount(EMAIL, PASSWORD);
  await signInFromStart(page);
  await importFile(page);
  await page.goto(`${server.origin}/?page=1`);
  const link = page
    .getByRole("list", { name: "Messages" })
    .getByRole("link", { name: /download/i });
  await expect(link).toBeVisible();

  await server.setPassword(EMAIL, NEW_PASSWORD);

  let downloads = 0;
  page.on("download", () => downloads++);
  await link.click();

  const query = await signInQuery(page);
  expect(query.get("ended")).toBe("1");
  expect(query.get("next")).toBe("/?page=1");
  await expect(page.getByText(ENDED)).toBeVisible();
  expect(downloads).toBe(0);

  await submitSignIn(page, EMAIL, NEW_PASSWORD);
  await expect(page).toHaveURL(`${server.origin}/?page=1`);
});

// US1 scenario 6, FR-003, FR-005.
test("sign-out opens sign-in, and Back shows no list", async ({ page }) => {
  await server.createAccount(EMAIL, PASSWORD);
  await signInFromStart(page);
  await importFile(page);
  const list = page.getByRole("list", { name: "Messages" });
  await expect(list).toContainText("synthetic.eml");

  await page.getByRole("button", { name: "Sign out" }).click();

  await expect(page).toHaveURL(`${server.origin}/sign-in`);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page.getByText(/session ended/i)).toHaveCount(0);
  // The server ended the session, not only this page.
  const status = await page.evaluate(
    async () => (await fetch("/api/session")).status,
  );
  expect(status).toBe(401);

  // Back reaches the list's URL, whose load reads the session again.
  const reread = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === "/api/session" &&
      response.request().method() === "GET",
  );
  await page.goBack();
  expect((await reread).status()).toBe(401);

  // The router shows sign-in but keeps the history entry Back reached, so the
  // address bar can still read /. The screen is what the scenario specifies.
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(list).toHaveCount(0);
  await expect(page.getByText("synthetic.eml")).toHaveCount(0);
  await expect(page.getByText(/session ended/i)).toHaveCount(0);

  // Signing in from there still opens the list.
  await submitSignIn(page, EMAIL, PASSWORD);
  await expect(page).toHaveURL(`${server.origin}/`);
  await expect(list).toContainText("synthetic.eml");
});

import { createServer } from "node:http";
import type { AddressInfo } from "node:net";

import { expect, test, type Page } from "@playwright/test";

import { startServer, type TestServer } from "./support/server";

// Direct requests without a session are tested in Go using the route table.
// These tests cover browser session behavior.

const EMAIL = "admin@example.test";
const PASSWORD = "correct horse battery";
const NEW_PASSWORD = "a different horse battery";
const ENDED = "Your session ended. Sign in again to continue.";
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
  await expect(
    page.getByRole("region", { name: "Notifications" }),
  ).toContainText("Archived");
}

/** Waits for sign-in and returns its query. */
async function signInQuery(page: Page): Promise<URLSearchParams> {
  await expect(page).toHaveURL(/\/sign-in(\?|$)/);
  return new URL(page.url()).searchParams;
}

test("a fresh install offers only sign-in, with no way to create an account", async ({
  page,
}) => {
  await page.goto(server.origin);

  await expect(page).toHaveURL(`${server.origin}/sign-in`);
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  // The only action on the page is signing in.
  await expect(page.getByRole("button")).toHaveText(["Sign in"]);
  await expect(
    page.getByRole("link", { name: /create|sign up|register/i }),
  ).toHaveCount(0);
  await expect(page.getByText(/session ended/i)).toHaveCount(0);
});

// The server sends frame-ancestors 'none', and the browser must refuse to
// show the page. The other site is a real server on 127.0.0.1, another origin
// than localhost: Chromium's local network checks would block a public
// origin's frame before MailArkiv answered at all.
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

// DESIGN.md, Sign-in: while the request runs, the button says so and keeps
// focus, and a second press doesn't send the form again.
test("sign-in shows that it's working and sends the form once", async ({
  page,
}) => {
  await server.createAccount(EMAIL, PASSWORD);
  await page.goto(`${server.origin}/sign-in`);
  let release = () => {};
  const held = new Promise<void>((resolve) => (release = resolve));
  let posts = 0;
  await page.route("**/api/session", async (route) => {
    if (route.request().method() !== "POST") return route.continue();
    posts++;
    await held;
    await route.continue();
  });

  await page.getByLabel("Email address").fill(EMAIL);
  await page.getByLabel("Password").fill("not the password");
  await page.getByRole("button", { name: "Sign in" }).click();

  const busy = page.getByRole("button", { name: "Signing in…" });
  await expect(busy).toHaveAttribute("aria-disabled", "true");
  await expect(busy).toBeFocused();
  await page.keyboard.press("Enter");

  release();
  await expect(
    page.getByText("That email address and password don't match."),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeFocused();
  expect(posts).toBe(1);
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

// Only a tab that had a signed-in page open is told its session ended.
// Signing in still returns to the page asked for.
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

// A new password ends the session, the next action opens sign-in with the
// message, and signing in returns to the page.
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

// A page left open opens sign-in when the session's 12 hours run out, with no
// action, and the mail is gone from the screen.
test("a page left open opens sign-in when the session runs out", async ({
  page,
}) => {
  await server.createAccount(EMAIL, PASSWORD);
  await page.clock.install();
  await signInFromStart(page);
  await importFile(page);
  await expect(page.getByRole("list", { name: "Messages" })).toContainText(
    "synthetic.eml",
  );

  await page.clock.fastForward("12:00:00");

  await expect(page).toHaveURL(`${server.origin}/sign-in?ended=1`);
  await expect(page.getByText(ENDED)).toBeVisible();
  await expect(page.getByText("synthetic.eml")).toHaveCount(0);
});

test("a failed session read during navigation keeps the expiry deadline", async ({
  page,
}) => {
  await server.createAccount(EMAIL, PASSWORD);
  await page.clock.install();
  await signInFromStart(page);
  await page.route("**/api/session", (route) =>
    route.fulfill({
      status: 500,
      json: { error: "internal_error" },
      headers: { "MailArkiv-Response": "1" },
    }),
  );
  await page.getByRole("link", { name: "Sources", exact: true }).click();
  await expect(page).toHaveURL(`${server.origin}/sources`);
  await expect(page.getByText("This page couldn't be loaded.")).toBeVisible();

  await page.clock.fastForward("12:00:00");
  await expect(page).toHaveURL(
    `${server.origin}/sign-in?ended=1&next=%2Fsources`,
  );
  await expect(page.getByText(ENDED)).toBeVisible();
});

// Headless Chromium doesn't change visibility, so the test sends the event
// the browser would when showing the tab again.
test("a page shown again after a new password opens sign-in", async ({
  page,
}) => {
  await server.createAccount(EMAIL, PASSWORD);
  await signInFromStart(page);
  // A failed navigation read must also keep the visibility listeners.
  await page.route("**/api/session", (route) => route.abort());
  await page.getByRole("link", { name: "Sources", exact: true }).click();
  await expect(page).toHaveURL(`${server.origin}/sources`);
  await expect(page.getByText("This page couldn't be loaded.")).toBeVisible();
  await page.unroute("**/api/session");

  await server.setPassword(EMAIL, NEW_PASSWORD);
  await page.evaluate(() =>
    document.dispatchEvent(new Event("visibilitychange")),
  );

  await expect(page).toHaveURL(
    `${server.origin}/sign-in?ended=1&next=%2Fsources`,
  );
  await expect(page.getByText(ENDED)).toBeVisible();

  await submitSignIn(page, EMAIL, NEW_PASSWORD);
  await expect(page).toHaveURL(`${server.origin}/sources`);
});

// The page fetches the file, gets a marked 401, and opens sign-in. The
// browser saves nothing.
test("a download after the session ended opens sign-in", async ({ page }) => {
  await server.createAccount(EMAIL, PASSWORD);
  await signInFromStart(page);
  await importFile(page);
  await page.goto(`${server.origin}/?page=1`);
  const download = page
    .getByRole("list", { name: "Messages" })
    .getByRole("button", { name: /download/i });
  await expect(download).toBeVisible();

  await server.setPassword(EMAIL, NEW_PASSWORD);

  let downloads = 0;
  page.on("download", () => downloads++);
  await download.click();

  const query = await signInQuery(page);
  expect(query.get("ended")).toBe("1");
  expect(query.get("next")).toBe("/?page=1");
  await expect(page.getByText(ENDED)).toBeVisible();
  expect(downloads).toBe(0);

  await submitSignIn(page, EMAIL, NEW_PASSWORD);
  await expect(page).toHaveURL(`${server.origin}/?page=1`);
});

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
  // address bar can still read /, so assert against the rendered screen.
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(list).toHaveCount(0);
  await expect(page.getByText("synthetic.eml")).toHaveCount(0);
  await expect(page.getByText(/session ended/i)).toHaveCount(0);

  // Signing in from there still opens the list.
  await submitSignIn(page, EMAIL, PASSWORD);
  await expect(page).toHaveURL(`${server.origin}/`);
  await expect(list).toContainText("synthetic.eml");
});

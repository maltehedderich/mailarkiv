import { readdirSync } from "node:fs";

import {
  expect,
  test,
  type Frame,
  type Page,
  type Request,
} from "@playwright/test";

import { importRead, readMail, type MailFile } from "./support/archive";
import {
  IMAGE_HOST_LAUNCH,
  startImageHost,
  type ImageHost,
} from "./support/imagehost";
import { startServer, type TestServer } from "./support/server";
import { EMAIL, PASSWORD, signIn } from "./support/sources";

// Each hostile message shows in the preview, in the view's frame and opened
// as a page of its own. No script runs, no dialog or window opens, and
// nothing leaves for another host: a request the policy blocks fails with
// "csp", and one that went out fails to resolve leak.example.test. Links and
// forms send nothing. Images from the internet load only when asked for, and
// then only images go out, with no Referer and no cookie.

const HOSTILE = new URL("./fixtures/hostile/", import.meta.url);
const IMAGE_HOST_TEMPLATE = "image-host.eml";
// These refer to images on other hosts, so the policy has requests to block,
// which shows the watch sees them.
const BLOCKED_IMAGES = new Set(["remote-resources.eml", IMAGE_HOST_TEMPLATE]);
// Longer than the refresh tag's one second, so a refresh would have happened.
const SETTLE_MS = 1_500;

declare global {
  interface Window {
    __posts?: string[];
  }
}

test.use({ ignoreHTTPSErrors: true, launchOptions: IMAGE_HOST_LAUNCH });

let server: TestServer;
let host: ImageHost;

test.beforeEach(async ({ page }) => {
  server = await startServer();
  host = await startImageHost();
  await server.createAccount(EMAIL, PASSWORD);
  // A message posted to the page around the frame lands here.
  await page.addInitScript(() => {
    window.__posts = [];
    addEventListener("message", (event) =>
      window.__posts?.push(String(event.data)),
    );
  });
  await signIn(page, server);
});

test.afterEach(async () => {
  await server.stop();
  await host.stop();
});

/** What a page asked for and opened while it showed a preview. */
class Watch {
  readonly requests: Request[] = [];
  readonly failures = new Map<Request, string>();
  readonly dialogs: string[] = [];
  readonly popups: string[] = [];

  constructor(readonly page: Page) {
    page.on("request", (request) => this.requests.push(request));
    page.on("requestfailed", (request) =>
      this.failures.set(request, request.failure()?.errorText ?? ""),
    );
    page.on("dialog", (dialog) => {
      this.dialogs.push(dialog.message());
      void dialog.dismiss();
    });
    page.on("popup", (popup) => this.popups.push(popup.url()));
  }

  since(count: number): string[] {
    return this.requests.slice(count).map((request) => request.url());
  }
}

/**
 * Every request the preview made, other than for its own document, was
 * blocked by the policy. Requests the page around the frame makes to
 * MailArkiv are its own.
 */
async function expectIsolated(
  watch: Watch,
  preview: string,
  around: Frame | null,
) {
  const origin = new URL(server.origin).origin;
  for (const request of watch.requests) {
    const url = request.url();
    if (url === preview) continue;
    if (around !== null && request.frame() === around) {
      expect(new URL(url).origin, url).toBe(origin);
      continue;
    }
    await expect
      .poll(() => watch.failures.get(request), { message: url })
      .toBe("csp");
  }
  const resolved = [...watch.failures.values()].filter((text) =>
    text.includes("NAME_NOT_RESOLVED"),
  );
  expect(resolved).toEqual([]);
  expect(watch.dialogs).toEqual([]);
  expect(watch.popups).toEqual([]);
}

/** Neither the page's title nor the preview's shows a script's marker. */
async function expectNoMarker(page: Page, preview: Frame) {
  expect(await page.title()).not.toContain("MARKER");
  expect(await preview.title()).not.toContain("MARKER");
  // The rendered text, which leaves out the scripts' own source.
  expect(await preview.locator("body").innerText()).not.toContain(
    "MARKER-TEXT",
  );
}

/** Clicks every link, image map, and button, and checks nothing went out. */
async function clickEverything(watch: Watch, preview: Frame) {
  const before = watch.requests.length;
  const address = preview.url();
  for (const link of await preview.locator("a").all()) await link.click();
  for (const map of await preview.locator("img[usemap]").all())
    await map.click();
  for (const button of await preview.locator("button").all())
    await button.click();
  await watch.page.waitForTimeout(SETTLE_MS);
  expect(watch.since(before)).toEqual([]);
  expect(preview.url()).toBe(address);
}

function hostileFiles(): string[] {
  return readdirSync(HOSTILE).filter((name) => name.endsWith(".eml"));
}

async function mailFor(name: string): Promise<MailFile> {
  if (name === IMAGE_HOST_TEMPLATE) {
    return { name, content: await host.fill(new URL(name, HOSTILE)) };
  }
  return readMail(HOSTILE, name);
}

function previewFrame(page: Page, id: number): Frame {
  const frame = page.frame({ url: new RegExp(`/api/messages/${id}/html`) });
  if (frame === null) throw new Error(`no preview of message ${id}`);
  return frame;
}

for (const name of hostileFiles()) {
  test(`${name} stays isolated in the frame and in a tab`, async ({ page }) => {
    const ids = await importRead(page, server, [await mailFor(name)]);
    const id = ids.get(name)!;
    const view = `${server.origin}/messages/${id}`;
    const preview = `${server.origin}/api/messages/${id}/html`;

    const watch = new Watch(page);
    await page.goto(view);
    const frame = page.frameLocator('iframe[title="Message body"]');
    await expect(frame.locator("body")).not.toBeEmpty();
    await page.waitForTimeout(SETTLE_MS);

    await expectIsolated(watch, preview, page.mainFrame());
    if (BLOCKED_IMAGES.has(name)) {
      expect([...watch.failures.values()]).toContain("csp");
    }
    await expectNoMarker(page, previewFrame(page, id));
    expect(page.url()).toBe(view);
    expect(await page.evaluate(() => window.__posts)).toEqual([]);
    expect(await page.evaluate(() => localStorage.length)).toBe(0);
    await clickEverything(watch, previewFrame(page, id));
    expect(page.url()).toBe(view);
    expect(watch.dialogs).toEqual([]);
    expect(watch.popups).toEqual([]);

    // The preview's address opened in a tab has the same isolation.
    const tab = await page.context().newPage();
    const tabWatch = new Watch(tab);
    await tab.goto(preview);
    await expect(tab.locator("body")).not.toBeEmpty();
    await tab.waitForTimeout(SETTLE_MS);
    await expectIsolated(tabWatch, preview, null);
    await expectNoMarker(tab, tab.mainFrame());
    expect(tab.url()).toBe(preview);
    await clickEverything(tabWatch, tab.mainFrame());
    expect(tab.url()).toBe(preview);
    expect(tabWatch.dialogs).toEqual([]);
    expect(tabWatch.popups).toEqual([]);
    await tab.close();

    // Nothing reached the image host.
    expect(await host.requests()).toEqual([]);
  });
}

test("loaded images go out as images, with no Referer and no cookie", async ({
  page,
}) => {
  const ids = await importRead(page, server, [
    await mailFor(IMAGE_HOST_TEMPLATE),
  ]);
  const id = ids.get(IMAGE_HOST_TEMPLATE)!;
  const watch = new Watch(page);
  await page.goto(`${server.origin}/messages/${id}`);
  await expect(
    page.frameLocator('iframe[title="Message body"]').locator("body"),
  ).not.toBeEmpty();
  expect(await host.requests()).toEqual([]);

  await page.getByRole("button", { name: "Load images" }).click();
  const frame = page.frameLocator('iframe[title="Message body"]');
  await expect(frame.getByRole("img", { name: "HTTPS image" })).toBeVisible();
  await expect
    .poll(() =>
      frame
        .getByRole("img", { name: "HTTPS image" })
        .evaluate((image: HTMLImageElement) => image.naturalWidth),
    )
    .toBeGreaterThan(0);
  await page.waitForTimeout(SETTLE_MS);
  // Links and forms still send nothing.
  await clickEverything(
    watch,
    page.frame({ url: new RegExp(`/api/messages/${id}/html\\?images=1$`) })!,
  );
  expect(page.url()).toBe(`${server.origin}/messages/${id}`);
  expectOnlyImages(await host.requests());

  // The same holds for the preview's address with images, opened in a tab.
  const before = (await host.requests()).length;
  const tab = await page.context().newPage();
  await tab.goto(`${server.origin}/api/messages/${id}/html?images=1`);
  await expect(tab.getByRole("img", { name: "HTTPS image" })).toBeVisible();
  await tab.waitForTimeout(SETTLE_MS);
  const fromTab = (await host.requests()).slice(before);
  expect(fromTab.length).toBeGreaterThan(0);
  expectOnlyImages(fromTab);
  await tab.close();
});

function expectOnlyImages(
  requests: Awaited<ReturnType<ImageHost["requests"]>>,
) {
  expect(requests.length).toBeGreaterThan(0);
  for (const request of requests) {
    expect(request.path, request.path).toMatch(/^\/img\//);
    expect(request.secFetchDest, request.path).toBe("image");
    expect(request.referer, request.path).toBe("");
    expect(request.cookie, request.path).toBe(false);
  }
}

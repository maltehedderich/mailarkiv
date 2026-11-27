import { readFile } from "node:fs/promises";

import { expect, test, type Frame, type Page } from "@playwright/test";

import { importRead, readMail, type MailFile } from "./support/archive";
import {
  IMAGE_HOST_LAUNCH,
  startImageHost,
  type ImageHost,
} from "./support/imagehost";
import { startServer, type TestServer } from "./support/server";
import { EMAIL, PASSWORD, signIn } from "./support/sources";

// Embedded images show in their place from data: addresses, only in PNG,
// JPEG, GIF, and WebP, and always stay in the attachment list. Images from the
// internet wait for Load images, which lasts for one view of one message.
// Each fixture names its images by alt text: "Shown: …" must show and
// "Hidden: …" must not.

const IMAGES = new URL("./fixtures/images/", import.meta.url);
const EMBEDDED = [
  "screenshot.eml",
  "signature-logo.eml",
  "gif.eml",
  "webp.eml",
  "shown-twice.eml",
  "missing-part.eml",
  "svg-part.eml",
  "svg-as-png.eml",
  "tiff.eml",
  "damaged-png.eml",
];
const REMOTE = "remote.eml";
const SETTLE_MS = 1_500;
// Two of these fit in the 10,000,000 bytes the preview shows, and a third
// would pass it.
const LARGE_IMAGE_BYTES = 4_000_000;

test.use({ ignoreHTTPSErrors: true, launchOptions: IMAGE_HOST_LAUNCH });

let server: TestServer;
let host: ImageHost;

test.beforeEach(async ({ page }) => {
  server = await startServer();
  host = await startImageHost();
  await server.createAccount(EMAIL, PASSWORD);
  await signIn(page, server);
});

test.afterEach(async () => {
  await server.stop();
  await host.stop();
});

function frame(page: Page) {
  return page.frameLocator('iframe[title="Message body"]');
}

function previewFrame(page: Page, id: number): Frame {
  const found = page.frame({ url: new RegExp(`/api/messages/${id}/html`) });
  if (found === null) throw new Error(`no preview of message ${id}`);
  return found;
}

interface Shown {
  alt: string;
  naturalWidth: number;
  height: number;
  src: string;
}

async function imagesIn(preview: Frame): Promise<Shown[]> {
  return preview.locator("img").evaluateAll((images: HTMLImageElement[]) =>
    images.map((image) => ({
      alt: image.alt,
      naturalWidth: image.naturalWidth,
      height: image.getBoundingClientRect().height,
      src: image.getAttribute("src") ?? "",
    })),
  );
}

/** The images' alt texts, in the order the HTML has them. */
function altsIn(mail: MailFile): string[] {
  return [...mail.content.toString("utf8").matchAll(/alt="([^"]*)"/g)].map(
    (match) => match[1],
  );
}

/** The file names of the image parts, which the attachment list shows. */
function imageNamesIn(mail: MailFile): string[] {
  return [
    ...mail.content
      .toString("utf8")
      .matchAll(
        /Content-Type: image\/[^\n]*\n(?:[^\n]+\n)*?Content-Disposition: inline; filename="([^"]+)"/g,
      ),
  ].map((match) => match[1]);
}

function attachmentRow(page: Page, name: string) {
  return page
    .getByRole("region", { name: /^Attachments/ })
    .getByRole("listitem")
    .filter({ hasText: name });
}

test("each allowed embedded image shows in its place, and no other", async ({
  page,
}) => {
  // Each message waits SETTLE_MS for late requests, so the test takes about
  // 20 s alone and passes 30 s when the other suites run beside it.
  test.slow();
  const files = await Promise.all(
    EMBEDDED.map((name) => readMail(IMAGES, name)),
  );
  const ids = await importRead(page, server, files);
  const origin = new URL(server.origin).origin;

  for (const mail of files) {
    const id = ids.get(mail.name)!;
    const requested: string[] = [];
    const record = (request: { url(): string }) =>
      requested.push(request.url());
    page.on("request", record);
    await page.goto(`${server.origin}/messages/${id}`);
    await expect(frame(page).locator("body")).not.toBeEmpty();
    await page.waitForTimeout(SETTLE_MS);
    page.off("request", record);

    // In the HTML's order, so each sits where the message put it.
    const shown = await imagesIn(previewFrame(page, id));
    expect(
      shown.map(({ alt }) => alt),
      mail.name,
    ).toEqual(altsIn(mail));
    for (const image of shown) {
      if (image.alt.startsWith("Shown:")) {
        expect(
          image.naturalWidth,
          `${mail.name}: ${image.alt}`,
        ).toBeGreaterThan(0);
        expect(image.height, `${mail.name}: ${image.alt}`).toBeGreaterThan(0);
        expect(image.src, `${mail.name}: ${image.alt}`).toMatch(
          /^data:image\//,
        );
      } else {
        expect(image.naturalWidth, `${mail.name}: ${image.alt}`).toBe(0);
      }
    }

    for (const url of requested) {
      if (url.startsWith("data:")) continue;
      expect(new URL(url).origin, `${mail.name} requested ${url}`).toBe(origin);
    }

    // Each embedded image stays in the attachment list.
    for (const name of imageNamesIn(mail)) {
      await expect(
        attachmentRow(page, name),
        `${mail.name}: ${name}`,
      ).toContainText("Embedded image");
    }
  }
  expect(await host.requests()).toEqual([]);
});

test("images past 10,000,000 bytes are only in the attachment list", async ({
  page,
}) => {
  const ids = await importRead(page, server, [await largeImages()]);
  const id = ids.get("large-images.eml")!;
  await page.goto(`${server.origin}/messages/${id}`);
  await expect(frame(page).locator("body")).not.toBeEmpty();

  await expect(page.getByRole("main")).toContainText(
    "Some embedded images are only in the attachment list",
  );
  const shown = await imagesIn(previewFrame(page, id));
  expect(shown.map(({ alt, naturalWidth }) => [alt, naturalWidth > 0])).toEqual(
    [
      ["Shown: first large", true],
      ["Shown: second large", true],
      ["Hidden: third large", false],
    ],
  );
  for (const name of ["large-1.png", "large-2.png", "large-3.png"]) {
    await expect(attachmentRow(page, name)).toContainText("Embedded image");
    await expect(attachmentRow(page, name)).toContainText("4 MB");
  }
});

test("images from the internet load only when asked, for that view", async ({
  page,
}) => {
  const remote: MailFile = {
    name: REMOTE,
    content: await host.fill(new URL(REMOTE, IMAGES)),
  };
  const other = await readMail(IMAGES, "screenshot.eml");
  const ids = await importRead(page, server, [remote, other]);
  const id = ids.get(REMOTE)!;
  const rows = page.getByRole("list", { name: "Messages" });
  const notice = page
    .getByRole("main")
    .getByText("Images from the internet aren't loaded.");

  await page.goto(`${server.origin}/messages/${id}`);
  await expect(notice).toBeVisible();
  await expect(page.getByRole("main")).toContainText(
    /can tell the sender when and from which network address (this|the) message was opened/,
  );
  await expect(page.getByRole("main")).toContainText("They weren't archived");
  await expectRemoteImages(page, id, false);
  expect(await host.requests()).toEqual([]);

  await page.getByRole("button", { name: "Load images" }).click();
  await expect(notice).toBeHidden();
  await expect
    .poll(async () =>
      (await imagesIn(previewFrame(page, id))).map(({ alt, naturalWidth }) => [
        alt,
        naturalWidth > 0,
      ]),
    )
    .toEqual([
      ["Remote: http", true],
      ["Remote: https", true],
      ["Remote: srcset", true],
    ]);
  // The host speaks only HTTPS, so the http: image arriving there came over
  // HTTPS.
  const paths = (await host.requests()).map(({ path }) => path);
  expect(paths).toContain("/img/http.png");
  expect(paths).toContain("/img/https.png");

  await test.step("the same message opened again", async () => {
    const before = (await host.requests()).length;
    await page
      .getByRole("main")
      .getByRole("link", { name: "Messages" })
      .click();
    await rows
      .getByRole("link", { name: "Open Images from the internet" })
      .click();
    await expect(notice).toBeVisible();
    await expectRemoteImages(page, id, false);
    await page.waitForTimeout(SETTLE_MS);
    expect((await host.requests()).length).toBe(before);
  });

  await test.step("another message, then this one", async () => {
    await page.getByRole("button", { name: "Load images" }).click();
    await expect(notice).toBeHidden();
    await page
      .getByRole("main")
      .getByRole("link", { name: "Messages" })
      .click();
    await rows.getByRole("link", { name: "Open Pasted screenshot" }).click();
    await expect(page.getByRole("main")).toContainText("Pasted screenshot");
    await page
      .getByRole("main")
      .getByRole("link", { name: "Messages" })
      .click();
    const before = (await host.requests()).length;
    await rows
      .getByRole("link", { name: "Open Images from the internet" })
      .click();
    await expect(notice).toBeVisible();
    await expectRemoteImages(page, id, false);
    await page.waitForTimeout(SETTLE_MS);
    expect((await host.requests()).length).toBe(before);
  });
});

async function expectRemoteImages(page: Page, id: number, loaded: boolean) {
  await expect(
    frame(page).getByRole("img", { name: "Remote: https" }),
  ).toBeAttached();
  await expect(page.locator('iframe[title="Message body"]')).toHaveAttribute(
    "src",
    loaded ? `/api/messages/${id}/html?images=1` : `/api/messages/${id}/html`,
  );
  for (const image of await imagesIn(previewFrame(page, id))) {
    expect(image.naturalWidth, image.alt).toBe(0);
  }
}

/**
 * Three PNGs of 4,000,000 bytes each: a real image with zeros after its end,
 * which browsers ignore.
 */
async function largeImages(): Promise<MailFile> {
  const screenshot = (await readFile(new URL("screenshot.eml", IMAGES), "utf8"))
    .split("Content-Transfer-Encoding: base64\n\n")[1]
    .split("\n--images--")[0];
  const png = Buffer.from(screenshot.replace(/\s/g, ""), "base64");
  const large = Buffer.concat([
    png,
    Buffer.alloc(LARGE_IMAGE_BYTES - png.length),
  ])
    .toString("base64")
    .replace(/.{76}/g, "$&\n");
  const parts = [1, 2, 3].map(
    (n) =>
      `--large\nContent-Type: image/png\nContent-ID: <large${n}@example.test>\n` +
      `Content-Disposition: inline; filename="large-${n}.png"\n` +
      `Content-Transfer-Encoding: base64\n\n${large}\n`,
  );
  const content =
    "From: Anna Schmidt <anna@example.test>\nTo: admin@example.test\n" +
    "Subject: Large images\nDate: Tue, 10 Nov 2026 09:00:00 +0000\n" +
    'MIME-Version: 1.0\nContent-Type: multipart/related; boundary="large"\n\n' +
    "--large\nContent-Type: text/html; charset=utf-8\n\n" +
    '<p>Three large images:</p><img src="cid:large1@example.test" alt="Shown: first large">' +
    '<img src="cid:large2@example.test" alt="Shown: second large">' +
    '<img src="cid:large3@example.test" alt="Hidden: third large">\n' +
    parts.join("") +
    "--large--\n";
  return { name: "large-images.eml", content: Buffer.from(content) };
}

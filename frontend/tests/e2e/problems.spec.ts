import { expect, test } from "@playwright/test";

import {
  collectMalformedSet,
  SOURCE,
  startMalformedSet,
  type MalformedSet,
} from "./support/malformed";
import { expectNoSidewaysScroll } from "./support/sources";
import { openMessage, viewedId } from "./support/view";

// The problem list holds exactly the messages processing read in part or
// couldn't read, in the list's order, and opens their history in place.

let set: MalformedSet;

test.beforeEach(async () => {
  set = await startMalformedSet();
});

test.afterEach(async () => {
  await set.stop();
});

test("the problem list holds exactly the problem messages", async ({
  page,
}) => {
  await collectMalformedSet(page, set);
  await page.goto(`${set.server.origin}/problems`);
  await expect(
    page.getByRole("heading", { name: "Processing problems", level: 1 }),
  ).toBeVisible();

  // The unreadable message's list date is its archive time, after every
  // Date header in the set.
  const list = page.getByRole("list", { name: "Processing problems" });
  await expect(list.getByRole("link", { name: /^Open / })).toHaveText(
    [
      `${SOURCE}INBOX`,
      "Abgeschnitten",
      "Ohne Grenze",
      "Unbekannter Zeichensatz",
      "Bericht mit kaputtem Anhang",
    ].map((text) => new RegExp(text.replace(/[()]/g, "\\$&"))),
  );
  await expect(page.getByText("1–5 of 5")).toBeVisible();

  // A message opens from the problem list, and its way up goes back to it.
  await openMessage(page, list.getByRole("link", { name: "Open Ohne Grenze" }));
  const id = viewedId(page);
  await page
    .getByRole("main")
    .getByRole("link", { name: "Processing problems" })
    .click();
  await expect(page).toHaveURL(/\/problems$/);
  await expect(page.getByText("1–5 of 5")).toBeVisible();

  // A history opened over the problem list, as Status links to one, closes
  // back to it.
  await page.goto(`${set.server.origin}/problems?message=${id}`);
  const drawer = page.getByRole("dialog", { name: /^History of / });
  await expect(drawer).toBeVisible();
  await drawer.getByRole("button", { name: "Close" }).click();
  await expect(drawer).toBeHidden();
  await expect(page).toHaveURL(/\/problems$/);
  await expect(
    list.getByRole("link", { name: "Open Ohne Grenze" }),
  ).toBeFocused();

  await page.setViewportSize({ width: 360, height: 780 });
  await expectNoSidewaysScroll(page, "Processing problems");
});

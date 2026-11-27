import { expect, type Locator, type Page } from "@playwright/test";

// A row's link opens its message's view, and the view's History opens the
// history drawer over it.

/** Opens the message behind a row's link, and waits for its view. */
export async function openMessage(page: Page, link: Locator): Promise<void> {
  await link.click();
  await expect(page).toHaveURL(/\/messages\/\d+$/);
  await expect(
    page.getByRole("heading", { level: 1, name: "Message", exact: true }),
  ).toBeVisible();
}

/** Opens the message behind a row's link, then its history. */
export async function openHistory(page: Page, link: Locator): Promise<Locator> {
  await openMessage(page, link);
  await historyButton(page).click();
  const drawer = page.getByRole("dialog", { name: /^History/ });
  await expect(drawer).toBeVisible();
  return drawer;
}

export function historyButton(page: Page): Locator {
  return page.getByRole("button", { name: "History", exact: true });
}

/** The message's ID, from the view's address. */
export function viewedId(page: Page): string {
  const match = /\/messages\/(\d+)/.exec(new URL(page.url()).pathname);
  if (match === null) throw new Error(`not a message view: ${page.url()}`);
  return match[1];
}

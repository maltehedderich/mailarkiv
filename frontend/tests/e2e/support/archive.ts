import { readFile } from "node:fs/promises";

import { expect, type Page } from "@playwright/test";

import type { TestServer } from "./server";

// Fixtures go in through the import API, with the browser's session, and the
// message view needs processing to have read them.

export interface MailFile {
  name: string;
  content: Buffer;
}

export async function readMail(
  directory: URL,
  name: string,
): Promise<MailFile> {
  return { name, content: await readFile(new URL(name, directory)) };
}

/**
 * Imports each file and waits until processing has read it. Returns each
 * file's message ID by its name.
 */
export async function importRead(
  page: Page,
  server: TestServer,
  files: MailFile[],
): Promise<Map<string, number>> {
  const ids = new Map<string, number>();
  for (const { name, content } of files) {
    const answer = await page.request.post(`${server.origin}/api/imports`, {
      headers: { "MailArkiv-File-Name": encodeURIComponent(name) },
      data: content,
    });
    expect(answer.status(), name).toBe(201);
    const { message } = (await answer.json()) as { message: { id: number } };
    ids.set(name, message.id);
  }
  for (const [name, id] of ids) {
    await expect
      .poll(
        async () => {
          const answer = await page.request.get(
            `${server.origin}/api/messages/${id}/content`,
          );
          if (!answer.ok()) return answer.status();
          return ((await answer.json()) as { processing: string }).processing;
        },
        { message: `${name} is read`, timeout: 30_000 },
      )
      .toMatch(/^processed/);
  }
  return ids;
}

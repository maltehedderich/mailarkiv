import { describe, expect, test } from "vitest";

import type { Observation } from "#lib/api.js";

import { entryName } from "./entry";

// A row names its message by the latest entry: an import by its file name,
// and a collection by its source name and folder.

const source = {
  id: 3,
  name: "Anna",
  host: "imap.example.test",
  userName: "anna@example.test",
  removed: false,
};

describe("entryName", () => {
  test("an import is named by its file name", () => {
    const entry: Observation = {
      sourceType: "manual_import",
      observedAt: "2026-11-06T15:50:52.123Z",
      accountEmail: "admin@example.test",
      fileName: "statement.eml",
    };
    expect(entryName(entry)).toBe("statement.eml");
  });

  test("an import with no file name has no name", () => {
    const entry: Observation = {
      sourceType: "manual_import",
      observedAt: "2026-11-06T15:50:52.123Z",
      accountEmail: "admin@example.test",
      fileName: null,
    };
    expect(entryName(entry)).toBeNull();
  });

  test("a collection is named by its source and folder", () => {
    const entry: Observation = {
      sourceType: "imap_collection",
      observedAt: "2026-11-08T09:08:01.000Z",
      source,
      folder: "Entwürfe",
      receivedAt: "2026-11-08T09:07:55.000Z",
    };
    expect(entryName(entry)).toBe("Anna, Entwürfe");
  });

  test("a removed source keeps its name", () => {
    const entry: Observation = {
      sourceType: "imap_collection",
      observedAt: "2026-11-08T09:08:01.000Z",
      source: { ...source, removed: true },
      folder: "INBOX",
      receivedAt: null,
    };
    expect(entryName(entry)).toBe("Anna, INBOX");
  });
});

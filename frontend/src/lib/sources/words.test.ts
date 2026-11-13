import { describe, expect, test } from "vitest";

import { CATEGORY_WORDS, STATE_WORDS } from "./words";

// The expected words are the spec's state table and category table, copied
// as written there.

describe("STATE_WORDS", () => {
  test("names every state", () => {
    expect(STATE_WORDS).toEqual({
      waiting: "Waiting",
      syncing: "Syncing",
      retrying: "Retrying",
      needs_attention: "Needs attention",
      paused: "Paused",
    });
  });
});

describe("CATEGORY_WORDS", () => {
  const table: [string, string, string, string, string][] = [
    [
      "server_not_reachable",
      "Server not reachable",
      "The server name couldn't be resolved, or the connection was refused or got no route",
      "Retries",
      "Check the server address and the network",
    ],
    [
      "secure_connection_failed",
      "Secure connection failed",
      "The handshake failed, the certificate isn't trusted, has expired, or names another server, or STARTTLS isn't offered",
      "Retries",
      "Fix the server's certificate, or add its issuer to the host's trusted certificates",
    ],
    [
      "sign_in_rejected",
      "Sign-in rejected",
      "The server says the user name or password is wrong",
      "Stops until an administrator acts",
      "Save the mailbox's current password",
    ],
    [
      "password_unreadable",
      "Password can't be read",
      "The server has no key, or the stored password can't be decrypted with its key",
      "Stops until an administrator acts",
      "Start the server with the right key, or save the password again",
    ],
    [
      "timed_out",
      "Timed out",
      "The server sent no data within the time limit",
      "Retries",
      "If this repeats, check the network and the mail server",
    ],
    [
      "server_error",
      "Server error",
      "The server returned an error or an unexpected reply, or closed the connection",
      "Retries",
      "Read the server's reply on the source. If this repeats, check the mail server",
    ],
    [
      "folder_unavailable",
      "Folder unavailable",
      "A chosen folder doesn't exist or can't be opened",
      "Tries again each sync; other folders continue",
      "Add the folder's new name, or remove it",
    ],
    [
      "storage_full",
      "Storage full",
      "The disk holding the archive ran out of space",
      "Retries",
      "Free disk space",
    ],
    [
      "storage_write_failed",
      "Storage write failed",
      "The server could not write to its storage location",
      "Retries",
      "Fix the storage location, such as its permissions",
    ],
    [
      "catalog_write_failed",
      "Catalog write failed",
      "The message was stored but its catalog entry was not written",
      "Retries",
      "If this repeats, check the server log",
    ],
    [
      "message_unavailable",
      "Message unavailable",
      "The server didn't return one message",
      "Tries again each sync; later messages continue",
      "None if it passes. If it repeats, check the message on the mail server",
    ],
    [
      "message_too_large",
      "Message too large",
      "A message is over 150,000,000 bytes",
      "Doesn't retry",
      "Keep the message in the mailbox, or save it elsewhere",
    ],
    [
      "message_removed",
      "Removed before it was archived",
      "A message waiting for retry was removed from the mailbox",
      "Doesn't retry",
      "None. The archive doesn't have this message",
    ],
  ];

  test("covers exactly the categories in the table", () => {
    expect(Object.keys(CATEGORY_WORDS).sort()).toEqual(
      table.map(([category]) => category).sort(),
    );
  });

  test.each(table)(
    "%s has the table's words",
    (category, label, whatHappened, whatMailArkivDoes, nextStep) => {
      expect(CATEGORY_WORDS[category as keyof typeof CATEGORY_WORDS]).toEqual({
        label,
        whatHappened,
        whatMailArkivDoes,
        nextStep,
      });
    },
  );
});

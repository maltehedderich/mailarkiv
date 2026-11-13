import type { CollectionCategory, SourceState } from "#lib/api.js";

// Records over the closed unions make a new state or category fail type
// checking until it has words. The text has no closing period so screens can
// join or punctuate it.

export const STATE_WORDS: Record<SourceState, string> = {
  waiting: "Waiting",
  syncing: "Syncing",
  retrying: "Retrying",
  needs_attention: "Needs attention",
  paused: "Paused",
};

export interface CategoryWords {
  label: string;
  whatHappened: string;
  whatMailArkivDoes: string;
  nextStep: string;
}

export const CATEGORY_WORDS: Record<CollectionCategory, CategoryWords> = {
  server_not_reachable: {
    label: "Server not reachable",
    whatHappened:
      "The server name couldn't be resolved, or the connection was refused or got no route",
    whatMailArkivDoes: "Retries",
    nextStep: "Check the server address and the network",
  },
  secure_connection_failed: {
    label: "Secure connection failed",
    whatHappened:
      "The handshake failed, the certificate isn't trusted, has expired, or names another server, or STARTTLS isn't offered",
    whatMailArkivDoes: "Retries",
    nextStep:
      "Fix the server's certificate, or add its issuer to the host's trusted certificates",
  },
  sign_in_rejected: {
    label: "Sign-in rejected",
    whatHappened: "The server says the user name or password is wrong",
    whatMailArkivDoes: "Stops until an administrator acts",
    nextStep: "Save the mailbox's current password",
  },
  password_unreadable: {
    label: "Password can't be read",
    whatHappened:
      "The server has no key, or the stored password can't be decrypted with its key",
    whatMailArkivDoes: "Stops until an administrator acts",
    nextStep: "Start the server with the right key, or save the password again",
  },
  timed_out: {
    label: "Timed out",
    whatHappened: "The server sent no data within the time limit",
    whatMailArkivDoes: "Retries",
    nextStep: "If this repeats, check the network and the mail server",
  },
  server_error: {
    label: "Server error",
    whatHappened:
      "The server returned an error or an unexpected reply, or closed the connection",
    whatMailArkivDoes: "Retries",
    nextStep:
      "Read the server's reply on the source. If this repeats, check the mail server",
  },
  folder_unavailable: {
    label: "Folder unavailable",
    whatHappened: "A chosen folder doesn't exist or can't be opened",
    whatMailArkivDoes: "Tries again each sync; other folders continue",
    nextStep: "Add the folder's new name, or remove it",
  },
  storage_full: {
    label: "Storage full",
    whatHappened: "The disk holding the archive ran out of space",
    whatMailArkivDoes: "Retries",
    nextStep: "Free disk space",
  },
  storage_write_failed: {
    label: "Storage write failed",
    whatHappened: "The server could not write to its storage location",
    whatMailArkivDoes: "Retries",
    nextStep: "Fix the storage location, such as its permissions",
  },
  catalog_write_failed: {
    label: "Catalog write failed",
    whatHappened:
      "The message was stored but its catalog entry was not written",
    whatMailArkivDoes: "Retries",
    nextStep: "If this repeats, check the server log",
  },
  message_unavailable: {
    label: "Message unavailable",
    whatHappened: "The server didn't return one message",
    whatMailArkivDoes: "Tries again each sync; later messages continue",
    nextStep:
      "None if it passes. If it repeats, check the message on the mail server",
  },
  message_too_large: {
    label: "Message too large",
    whatHappened: "A message is over 150,000,000 bytes",
    whatMailArkivDoes: "Doesn't retry",
    nextStep: "Keep the message in the mailbox, or save it elsewhere",
  },
  message_removed: {
    label: "Removed before it was archived",
    whatHappened: "A message waiting for retry was removed from the mailbox",
    whatMailArkivDoes: "Doesn't retry",
    nextStep: "None. The archive doesn't have this message",
  },
};

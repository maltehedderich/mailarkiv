import type {
  NewSourceResult,
  Security,
  ServerFolder,
  SourceField,
} from "#lib/api.js";

// The add and edit forms' rules for the port and the folder choice.

export const DEFAULT_PORTS: Record<Security, number> = {
  tls: 993,
  starttls: 143,
};

/**
 * The port field's text after the connection security switches. An empty
 * field or the old mode's default follows the switch. A port the
 * administrator typed stays.
 */
export function portAfterSwitch(
  port: string,
  from: Security,
  to: Security,
): string {
  const typed = port.trim();
  if (typed === "" || typed === String(DEFAULT_PORTS[from])) {
    return String(DEFAULT_PORTS[to]);
  }
  return port;
}

/**
 * Reads the port field as digits only. Anything else is null, which the
 * server refuses with the port named, as it does a number out of range.
 */
export function parsePort(text: string): number | null {
  const typed = text.trim();
  if (!/^\d+$/.test(typed)) return null;
  const port = Number(typed);
  return Number.isSafeInteger(port) ? port : null;
}

/** One folder in a form's checklist. Its name is untrusted. */
export interface FolderOption {
  name: string;
  /** Null when the server didn't report a count, or wasn't asked yet. */
  messages: number | null;
  chosen: boolean;
}

/** A new source's choice: the server's folders in its order, INBOX checked. */
export function firstChoice(folders: ServerFolder[]): FolderOption[] {
  return folders.map(({ name, messages }) => ({
    name,
    messages,
    chosen: name === "INBOX",
  }));
}

/** An edit's choice before the server is asked: the source's folders. */
export function currentChoice(names: string[]): FolderOption[] {
  return names.map((name) => ({ name, messages: null, chosen: true }));
}

/**
 * Adds the server's folders to an edit's choice. Each folder keeps its
 * choice, so one gone from the server stays until it's unchecked, and the
 * server's others follow, unchecked.
 */
export function widenChoice(
  current: FolderOption[],
  server: ServerFolder[],
): FolderOption[] {
  const counts = new Map(server.map(({ name, messages }) => [name, messages]));
  const listed = new Set(current.map(({ name }) => name));
  return [
    ...current.map((option) => ({
      ...option,
      messages: counts.get(option.name) ?? option.messages,
    })),
    ...server
      .filter(({ name }) => !listed.has(name))
      .map(({ name, messages }) => ({ name, messages, chosen: false })),
  ];
}

export function chosenNames(options: FolderOption[]): string[] {
  return options.filter(({ chosen }) => chosen).map(({ name }) => name);
}

/** The line under a field the server refused, from its input rules. */
export const FIELD_PROBLEMS: Record<SourceField, string> = {
  name: "Enter a name of 1 to 100 characters.",
  host: "Enter the server's name or address.",
  port: "Enter a port from 1 to 65535.",
  security: "Choose TLS or STARTTLS.",
  user_name: "Enter the user name, with no control characters.",
  password: "Enter the mailbox's password, up to 1,024 bytes.",
  folders: "Choose at least one folder.",
};

/**
 * Whether a name keeps the server's rule: 1 to 100 code points after
 * trimming. Checked before Connect, because the check doesn't send the name.
 */
export function isValidName(name: string): boolean {
  const length = [...name.trim()].length;
  return length >= 1 && length <= 100;
}

/** What a form shows below its button after an attempt that didn't succeed. */
export type FormOutcome = Exclude<
  NewSourceResult<never>,
  { kind: "ok" | "session_ended" | "invalid_fields" }
>;

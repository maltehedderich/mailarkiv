// The one place the interface talks to the server's JSON API. Every answer is
// parsed here, once, into a closed union, so a screen can't mistake a failure
// for an empty list (FR-045).

/** Other API calls than an upload give up after 30 seconds. */
const TIMEOUT_MS = 30_000;

/** The server sets this on every answer, so a proxy's answer is told apart. */
export const MARKER = "MailArkiv-Response";

export type Fetch = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export interface Message {
  id: number;
  contentHash: string;
  size: number;
  /** Null when the import supplied no name. */
  fileName: string | null;
  /** RFC 3339 in UTC. */
  importedAt: string;
  importCount: number;
}

export interface MessagePage {
  items: Message[];
  page: number;
  pageSize: number;
  total: number;
}

export interface SessionInfo {
  problemCount: number;
}

export type ApiResult<T> =
  | { kind: "ok"; value: T }
  | { kind: "session_ended" }
  | { kind: "unavailable" };

export type SignInResult =
  | { kind: "ok" }
  | { kind: "invalid" }
  | { kind: "blocked" }
  | { kind: "unavailable" };

export async function getSession(
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<SessionInfo>> {
  return getJSON(fetch, "/api/session", (body) => {
    const count = field(body, "problem_count");
    return isCount(count) ? { problemCount: count } : null;
  });
}

/** Lists one page. `page` is passed as typed, or left out when null. */
export async function listMessages(
  page: string | null,
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<MessagePage>> {
  const query = page === null ? "" : `?${new URLSearchParams({ page })}`;
  return getJSON(fetch, `/api/messages${query}`, parseMessagePage);
}

export async function signIn(
  email: string,
  password: string,
  fetch: Fetch = globalThis.fetch,
): Promise<SignInResult> {
  let response: Response;
  try {
    response = await fetch("/api/session", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email, password }),
      signal: AbortSignal.timeout(TIMEOUT_MS),
    });
  } catch {
    return { kind: "unavailable" };
  }
  await discard(response);
  if (!isMarked(response)) return { kind: "unavailable" };
  switch (response.status) {
    case 204:
      return { kind: "ok" };
    case 401:
      return { kind: "invalid" };
    case 429:
      return { kind: "blocked" };
    default:
      return { kind: "unavailable" };
  }
}

export function isMarked(response: Response): boolean {
  return response.headers.get(MARKER) === "1";
}

async function getJSON<T>(
  fetch: Fetch,
  path: string,
  parse: (body: unknown) => T | null,
): Promise<ApiResult<T>> {
  try {
    const response = await fetch(path, {
      signal: AbortSignal.timeout(TIMEOUT_MS),
    });
    if (!isMarked(response) || response.status !== 200) {
      await discard(response);
      return isMarked(response) && response.status === 401
        ? { kind: "session_ended" }
        : { kind: "unavailable" };
    }
    const value = parse(await response.json());
    return value === null ? { kind: "unavailable" } : { kind: "ok", value };
  } catch {
    // A network error, the timeout, or a body that isn't JSON.
    return { kind: "unavailable" };
  }
}

/**
 * Reads a body the caller has no use for. A body left unread keeps the
 * request open, and the browser can't reuse its connection.
 */
async function discard(response: Response): Promise<void> {
  try {
    await response.arrayBuffer();
  } catch {
    // The answer is already decided by the status and the marker.
  }
}

function parseMessagePage(body: unknown): MessagePage | null {
  const items = field(body, "items");
  const page = field(body, "page");
  const pageSize = field(body, "page_size");
  const total = field(body, "total");
  if (!Array.isArray(items) || !isCount(page) || !isCount(pageSize)) {
    return null;
  }
  if (!isCount(total)) return null;
  const messages = items.map(parseMessage);
  if (messages.some((message) => message === null)) return null;
  return { items: messages as Message[], page, pageSize, total };
}

/** Reads one message as the contract writes it, or null if it isn't one. */
export function parseMessage(value: unknown): Message | null {
  const id = field(value, "id");
  const contentHash = field(value, "content_hash");
  const size = field(value, "size");
  const fileName = field(value, "file_name");
  const importedAt = field(value, "imported_at");
  const importCount = field(value, "import_count");
  if (
    !isCount(id) ||
    typeof contentHash !== "string" ||
    !/^[0-9a-f]{64}$/.test(contentHash) ||
    !isCount(size) ||
    (fileName !== null && typeof fileName !== "string") ||
    typeof importedAt !== "string" ||
    Number.isNaN(Date.parse(importedAt)) ||
    !isCount(importCount)
  ) {
    return null;
  }
  return { id, contentHash, size, fileName, importedAt, importCount };
}

/** Reads a field of a JSON object, or undefined when value isn't one. */
export function field(value: unknown, name: string): unknown {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return undefined;
  }
  return (value as Record<string, unknown>)[name];
}

function isCount(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

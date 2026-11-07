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

/** Slice 1 has one source. The column's CHECK closes the set on the server. */
export type SourceType = "manual_import";

export interface Observation {
  sourceType: SourceType;
  /** RFC 3339 in UTC. */
  observedAt: string;
  accountEmail: string;
  /** Null when the import supplied no name. */
  fileName: string | null;
}

export interface MessageHistory {
  id: number;
  contentHash: string;
  size: number;
  /** Every import, newest first, with no cap. */
  observations: Observation[];
}

export interface SessionInfo {
  problemCount: number;
}

/** FR-038's six failure categories, as the server names them. */
export const FAILURE_CATEGORIES = [
  "storage_full",
  "storage_write_failed",
  "catalog_write_failed",
  "stored_original_missing",
  "stored_original_damaged",
  "temp_file_not_removed",
] as const;

export type FailureCategory = (typeof FAILURE_CATEGORIES)[number];

export interface RecoveryResult {
  /** RFC 3339 in UTC. */
  finishedAt: string;
  tempFilesRemoved: number;
  tempFilesNotRemoved: number;
  unreferencedFound: number;
}

/** A stored original with no catalog entry. */
export interface UnreferencedOriginal {
  contentHash: string;
  size: number;
}

export interface FailureEvent {
  /** RFC 3339 in UTC. */
  at: string;
  category: FailureCategory;
  /** Null when the event names no message. */
  messageId: number | null;
  /** Null when the event names no stored original. */
  contentHash: string | null;
  /** An import has already replaced the stored original. */
  restored: boolean;
}

/** What the server knows since its latest start (FR-027). */
export interface ServerStatus {
  /** RFC 3339 in UTC. */
  startedAt: string;
  recovery: RecoveryResult;
  /** In the order recovery found them. */
  unreferenced: UnreferencedOriginal[];
  /** In the order they happened. */
  failures: FailureEvent[];
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

export type SignOutResult =
  { kind: "ok" } | { kind: "session_ended" } | { kind: "unavailable" };

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

/**
 * Reads one message's history. `id` goes to the server as typed, so one that
 * names no message comes back as unavailable.
 */
export async function getMessage(
  id: string,
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<MessageHistory>> {
  return getJSON(
    fetch,
    `/api/messages/${encodeURIComponent(id)}`,
    parseHistory,
  );
}

/** Reads the status screen's data. */
export async function getStatus(
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<ServerStatus>> {
  return getJSON(fetch, "/api/status", parseStatus);
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

/**
 * Ends the session. A marked 401 means it had already ended, so the browser is
 * signed out either way. Anything else that isn't a marked 204 leaves the
 * administrator signed in, and the screen says so (FR-045).
 */
export async function signOut(
  fetch: Fetch = globalThis.fetch,
): Promise<SignOutResult> {
  let response: Response;
  try {
    response = await fetch("/api/session", {
      method: "DELETE",
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
      return { kind: "session_ended" };
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

function parseHistory(body: unknown): MessageHistory | null {
  const id = field(body, "id");
  const contentHash = field(body, "content_hash");
  const size = field(body, "size");
  const observations = field(body, "observations");
  if (
    !isCount(id) ||
    !isContentHash(contentHash) ||
    !isCount(size) ||
    !Array.isArray(observations)
  ) {
    return null;
  }
  const parsed = observations.map(parseObservation);
  if (parsed.some((observation) => observation === null)) return null;
  return { id, contentHash, size, observations: parsed as Observation[] };
}

function parseObservation(value: unknown): Observation | null {
  const sourceType = field(value, "source_type");
  const observedAt = field(value, "observed_at");
  const accountEmail = field(value, "account_email");
  const fileName = field(value, "file_name");
  if (
    sourceType !== "manual_import" ||
    typeof observedAt !== "string" ||
    Number.isNaN(Date.parse(observedAt)) ||
    typeof accountEmail !== "string" ||
    (fileName !== null && typeof fileName !== "string")
  ) {
    return null;
  }
  return { sourceType, observedAt, accountEmail, fileName };
}

function parseStatus(body: unknown): ServerStatus | null {
  const startedAt = field(body, "started_at");
  const recovery = parseRecovery(field(body, "recovery"));
  const unreferenced = field(body, "unreferenced");
  const failures = field(body, "failures");
  if (
    !isTime(startedAt) ||
    recovery === null ||
    !Array.isArray(unreferenced) ||
    !Array.isArray(failures)
  ) {
    return null;
  }
  const originals = unreferenced.map(parseUnreferenced);
  const events = failures.map(parseFailure);
  if (originals.some((original) => original === null)) return null;
  if (events.some((event) => event === null)) return null;
  return {
    startedAt,
    recovery,
    unreferenced: originals as UnreferencedOriginal[],
    failures: events as FailureEvent[],
  };
}

function parseRecovery(value: unknown): RecoveryResult | null {
  const finishedAt = field(value, "finished_at");
  const tempFilesRemoved = field(value, "temp_files_removed");
  const tempFilesNotRemoved = field(value, "temp_files_not_removed");
  const unreferencedFound = field(value, "unreferenced_found");
  if (
    !isTime(finishedAt) ||
    !isCount(tempFilesRemoved) ||
    !isCount(tempFilesNotRemoved) ||
    !isCount(unreferencedFound)
  ) {
    return null;
  }
  return {
    finishedAt,
    tempFilesRemoved,
    tempFilesNotRemoved,
    unreferencedFound,
  };
}

function parseUnreferenced(value: unknown): UnreferencedOriginal | null {
  const contentHash = field(value, "content_hash");
  const size = field(value, "size");
  if (!isContentHash(contentHash) || !isCount(size)) return null;
  return { contentHash, size };
}

/** The contract leaves out `message_id` and `content_hash` where they don't apply. */
function parseFailure(value: unknown): FailureEvent | null {
  const at = field(value, "at");
  const category = field(value, "category");
  const messageId = field(value, "message_id") ?? null;
  const contentHash = field(value, "content_hash") ?? null;
  const restored = field(value, "restored");
  if (
    !isTime(at) ||
    !isFailureCategory(category) ||
    (messageId !== null && !isCount(messageId)) ||
    (contentHash !== null && !isContentHash(contentHash)) ||
    typeof restored !== "boolean"
  ) {
    return null;
  }
  return { at, category, messageId, contentHash, restored };
}

function isFailureCategory(value: unknown): value is FailureCategory {
  return FAILURE_CATEGORIES.some((category) => category === value);
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
    !isContentHash(contentHash) ||
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

function isTime(value: unknown): value is string {
  return typeof value === "string" && !Number.isNaN(Date.parse(value));
}

function isContentHash(value: unknown): value is string {
  return typeof value === "string" && /^[0-9a-f]{64}$/.test(value);
}

function isCount(value: unknown): value is number {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 0;
}

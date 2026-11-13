// The one place the interface talks to the server's JSON API. Every answer is
// parsed here, once, into a closed union, so a screen can't mistake a failure
// for an empty list.

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
  /** The latest entry's time, RFC 3339 in UTC. */
  observedAt: string;
  /** Counts every entry, imports and collections alike. */
  observationCount: number;
  latest: Observation;
}

export interface MessagePage {
  items: Message[];
  page: number;
  pageSize: number;
  total: number;
}

/** The source column's CHECK constraint enforces the same set on the server. */
export type SourceType = "manual_import" | "imap_collection";

/** One history entry: an import, or a collection from a mailbox folder. */
export type Observation = ImportObservation | CollectionObservation;

export interface ImportObservation {
  sourceType: "manual_import";
  /** RFC 3339 in UTC. */
  observedAt: string;
  accountEmail: string;
  /** Null when the import supplied no name. */
  fileName: string | null;
}

export interface CollectionObservation {
  sourceType: "imap_collection";
  /** When collection stored it, RFC 3339 in UTC. */
  observedAt: string;
  /** A removed source keeps its labels. */
  source: EntrySource;
  /** As mail clients show it. Untrusted. */
  folder: string;
  /** The server's INTERNALDATE. Null when it reported none. */
  receivedAt: string | null;
}

/** Names and server are untrusted text from the source's settings. */
export interface EntrySource {
  id: number;
  name: string;
  host: string;
  userName: string;
  removed: boolean;
}

export interface MessageHistory {
  id: number;
  contentHash: string;
  size: number;
  /** Every entry, newest first, with no cap. */
  observations: Observation[];
}

export interface SessionInfo {
  problemCount: number;
}

/** Failure categories use the server's names. */
export const FAILURE_CATEGORIES = [
  "storage_full",
  "storage_write_failed",
  "catalog_write_failed",
  "stored_original_missing",
  "stored_original_damaged",
  "temp_file_not_removed",
  "message_too_large",
  "message_removed",
] as const;

export type FailureCategory = (typeof FAILURE_CATEGORIES)[number];

export interface RecoveryResult {
  /** RFC 3339 in UTC. */
  finishedAt: string;
  tempFilesRemoved: number;
  tempFilesNotRemoved: number;
  unreferencedFound: number;
}

/** The source a message event names. A removed source keeps its name. */
export interface FailureSource {
  id: number;
  name: string;
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
  /** The source of a message event, and null for any other event. */
  source: FailureSource | null;
  /** An import has already replaced the stored original. */
  restored: boolean;
}

/** What the server knows since its latest start. */
export interface ServerStatus {
  /** RFC 3339 in UTC. */
  startedAt: string;
  recovery: RecoveryResult;
  /** In the order recovery found them. */
  unreferenced: UnreferencedOriginal[];
  /** In the order they happened. */
  failures: FailureEvent[];
  /** By name, then ID. Each source appears once. */
  sources: ProblemSource[];
}

/** A mail source the status screen lists. It names no user or folder. */
export interface ProblemSource {
  id: number;
  /** Untrusted. */
  name: string;
  category: SourceProblemCategory;
  /** When the problem began, RFC 3339 in UTC. */
  since: string;
  /** Set for a sync failure only. */
  failedAttempts: number | null;
}

/** Source states use the server's names. */
export const SOURCE_STATES = [
  "waiting",
  "syncing",
  "retrying",
  "needs_attention",
  "paused",
] as const;

export type SourceState = (typeof SOURCE_STATES)[number];

/** The categories a sync can fail with. */
export const SYNC_CATEGORIES = [
  "server_not_reachable",
  "secure_connection_failed",
  "sign_in_rejected",
  "password_unreadable",
  "timed_out",
  "server_error",
  "storage_full",
  "storage_write_failed",
  "catalog_write_failed",
] as const;

/** The categories of one message that wasn't archived. */
export const MESSAGE_PROBLEM_CATEGORIES = [
  "message_unavailable",
  "message_too_large",
  "message_removed",
] as const;

export type SyncCategory = (typeof SYNC_CATEGORIES)[number];
export type MessageProblemCategory =
  (typeof MESSAGE_PROBLEM_CATEGORIES)[number];

/** Every collection failure category, with the server's names. */
export type CollectionCategory =
  SyncCategory | "folder_unavailable" | MessageProblemCategory;

/** What the status screen can list a source for. */
export type SourceProblemCategory =
  SyncCategory | "folder_unavailable" | "message_unavailable";

export type Security = "tls" | "starttls";

/** `no_key` when the server started without its key file. */
export type Collection = "available" | "no_key";

export interface SyncProgress {
  /** Counts only stored messages. */
  archived: number;
  found: number;
}

export interface SyncSuccess {
  /** RFC 3339 in UTC. */
  at: string;
  archived: number;
}

/** The run of failed syncs a source is in, or its unreadable password. */
export interface SyncFailure {
  category: SyncCategory;
  /** The run's first failure, or when the password was found unreadable. */
  since: string;
  /** Null for an unreadable password, which no sign-in tried. */
  failedAttempts: number | null;
  lastAt: string | null;
  /** The server's NO or BAD text, if it sent one. Untrusted. */
  reply: string | null;
}

export interface ProblemCounts {
  unavailableFolders: number;
  waitingForRetry: number;
  notArchived: number;
}

/** A source's name, server, user name, and folders are untrusted. */
export interface Source {
  id: number;
  name: string;
  host: string;
  port: number;
  security: Security;
  userName: string;
  state: SourceState;
  /** Chosen folders in sync order, as mail clients show them. */
  folders: string[];
  /** Set while syncing. */
  progress: SyncProgress | null;
  /** Null before the first successful sync. */
  lastSuccess: SyncSuccess | null;
  /** Distinct messages with an entry from this source. */
  collected: number;
  /** Null while paused, syncing, or in Needs attention. */
  nextSyncAt: string | null;
  pausedAt: string | null;
  /** Set while retrying or in Needs attention. */
  failure: SyncFailure | null;
  problems: ProblemCounts;
}

export interface SourceList {
  collection: Collection;
  /** By name, then ID. */
  sources: Source[];
}

export interface SourceFolder {
  /** As mail clients show it. Untrusted. */
  name: string;
  unavailableSince: string | null;
  renumberedAt: string | null;
}

/** A message waiting for retry or not archived. It holds no mail content. */
export interface ProblemMessage {
  /** Untrusted. */
  folder: string;
  /** Null when the server didn't report it. */
  size: number | null;
  /** The server's INTERNALDATE, or null when it reported none. */
  receivedAt: string | null;
  recordedAt: string;
  category: MessageProblemCategory;
}

export interface ProblemList {
  /** Counts every message, though `items` holds at most the newest 100. */
  total: number;
  /** Newest first. */
  items: ProblemMessage[];
}

/** A source as its own page shows it. */
export interface SourceDetail extends Omit<Source, "folders"> {
  folders: SourceFolder[];
  messages: {
    waitingForRetry: ProblemList;
    notArchived: ProblemList;
  };
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

/** Lists the mail sources, unpaged. */
export async function listSources(
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<SourceList>> {
  return getJSON(fetch, "/api/sources", parseSourceList);
}

/**
 * Reads one source's page. `id` goes to the server as typed, so one that
 * names no source comes back as unavailable.
 */
export async function readSource(
  id: string,
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<SourceDetail>> {
  return getJSON(
    fetch,
    `/api/sources/${encodeURIComponent(id)}`,
    parseSourceDetail,
  );
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
 * administrator signed in, and the screen says so.
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
  if (!isTime(observedAt)) return null;
  if (sourceType === "manual_import") {
    const accountEmail = field(value, "account_email");
    const fileName = field(value, "file_name");
    if (
      typeof accountEmail !== "string" ||
      (fileName !== null && typeof fileName !== "string")
    ) {
      return null;
    }
    return { sourceType, observedAt, accountEmail, fileName };
  }
  if (sourceType === "imap_collection") {
    const source = parseEntrySource(field(value, "source"));
    const folder = field(value, "folder");
    const receivedAt = field(value, "received_at");
    if (
      source === null ||
      typeof folder !== "string" ||
      (receivedAt !== null && !isTime(receivedAt))
    ) {
      return null;
    }
    return { sourceType, observedAt, source, folder, receivedAt };
  }
  return null;
}

function parseEntrySource(value: unknown): EntrySource | null {
  const id = field(value, "id");
  const name = field(value, "name");
  const host = field(value, "host");
  const userName = field(value, "user_name");
  const removed = field(value, "removed");
  if (
    !isCount(id) ||
    typeof name !== "string" ||
    typeof host !== "string" ||
    typeof userName !== "string" ||
    typeof removed !== "boolean"
  ) {
    return null;
  }
  return { id, name, host, userName, removed };
}

function parseStatus(body: unknown): ServerStatus | null {
  const startedAt = field(body, "started_at");
  const recovery = parseRecovery(field(body, "recovery"));
  const unreferenced = field(body, "unreferenced");
  const failures = field(body, "failures");
  const sources = parseAll(field(body, "sources"), parseProblemSource);
  if (
    !isTime(startedAt) ||
    recovery === null ||
    !Array.isArray(unreferenced) ||
    !Array.isArray(failures) ||
    sources === null
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
    sources,
  };
}

function parseProblemSource(value: unknown): ProblemSource | null {
  const id = field(value, "id");
  const name = field(value, "name");
  const category = field(value, "category");
  const since = field(value, "since");
  const failedAttempts = field(value, "failed_attempts");
  if (
    !isCount(id) ||
    typeof name !== "string" ||
    !isOneOf(category, SOURCE_PROBLEM_CATEGORIES) ||
    !isTime(since) ||
    (failedAttempts !== null && !isCount(failedAttempts))
  ) {
    return null;
  }
  return { id, name, category, since, failedAttempts };
}

const SOURCE_PROBLEM_CATEGORIES: readonly SourceProblemCategory[] = [
  ...SYNC_CATEGORIES,
  "folder_unavailable",
  "message_unavailable",
];

function parseSourceList(body: unknown): SourceList | null {
  const collection = field(body, "collection");
  const sources = parseAll(field(body, "sources"), (value) => {
    const fields = parseSourceFields(value);
    const folders = field(value, "folders");
    if (fields === null || !isStringArray(folders)) return null;
    return { ...fields, folders };
  });
  if (collection !== "available" && collection !== "no_key") return null;
  return sources === null ? null : { collection, sources };
}

function parseSourceDetail(body: unknown): SourceDetail | null {
  const fields = parseSourceFields(body);
  const folders = parseAll(field(body, "folders"), parseSourceFolder);
  const messages = field(body, "messages");
  const waitingForRetry = parseProblemList(
    field(messages, "waiting_for_retry"),
  );
  const notArchived = parseProblemList(field(messages, "not_archived"));
  if (
    fields === null ||
    folders === null ||
    waitingForRetry === null ||
    notArchived === null
  ) {
    return null;
  }
  return { ...fields, folders, messages: { waitingForRetry, notArchived } };
}

/** Reads what the list entry and the page share: all but `folders`. */
function parseSourceFields(value: unknown): Omit<Source, "folders"> | null {
  const id = field(value, "id");
  const name = field(value, "name");
  const host = field(value, "host");
  const port = field(value, "port");
  const security = field(value, "security");
  const userName = field(value, "user_name");
  const state = field(value, "state");
  const progress = parseNullable(field(value, "progress"), parseProgress);
  const lastSuccess = parseNullable(field(value, "last_success"), parseSuccess);
  const collected = field(value, "collected");
  const nextSyncAt = field(value, "next_sync_at");
  const pausedAt = field(value, "paused_at");
  const failure = parseNullable(field(value, "failure"), parseSyncFailure);
  const problems = parseProblemCounts(field(value, "problems"));
  if (
    !isCount(id) ||
    typeof name !== "string" ||
    typeof host !== "string" ||
    !isCount(port) ||
    port < 1 ||
    port > 65535 ||
    (security !== "tls" && security !== "starttls") ||
    typeof userName !== "string" ||
    !isOneOf(state, SOURCE_STATES) ||
    progress === undefined ||
    lastSuccess === undefined ||
    !isCount(collected) ||
    !isTimeOrNull(nextSyncAt) ||
    !isTimeOrNull(pausedAt) ||
    failure === undefined ||
    problems === null
  ) {
    return null;
  }
  return {
    id,
    name,
    host,
    port,
    security,
    userName,
    state,
    progress,
    lastSuccess,
    collected,
    nextSyncAt,
    pausedAt,
    failure,
    problems,
  };
}

function parseProgress(value: unknown): SyncProgress | null {
  const archived = field(value, "archived");
  const found = field(value, "found");
  return isCount(archived) && isCount(found) ? { archived, found } : null;
}

function parseSuccess(value: unknown): SyncSuccess | null {
  const at = field(value, "at");
  const archived = field(value, "archived");
  return isTime(at) && isCount(archived) ? { at, archived } : null;
}

function parseSyncFailure(value: unknown): SyncFailure | null {
  const category = field(value, "category");
  const since = field(value, "since");
  const failedAttempts = field(value, "failed_attempts");
  const lastAt = field(value, "last_at");
  const reply = field(value, "reply");
  if (
    !isOneOf(category, SYNC_CATEGORIES) ||
    !isTime(since) ||
    (failedAttempts !== null && !isCount(failedAttempts)) ||
    !isTimeOrNull(lastAt) ||
    (reply !== null && typeof reply !== "string")
  ) {
    return null;
  }
  return { category, since, failedAttempts, lastAt, reply };
}

function parseProblemCounts(value: unknown): ProblemCounts | null {
  const unavailableFolders = field(value, "unavailable_folders");
  const waitingForRetry = field(value, "waiting_for_retry");
  const notArchived = field(value, "not_archived");
  if (
    !isCount(unavailableFolders) ||
    !isCount(waitingForRetry) ||
    !isCount(notArchived)
  ) {
    return null;
  }
  return { unavailableFolders, waitingForRetry, notArchived };
}

function parseSourceFolder(value: unknown): SourceFolder | null {
  const name = field(value, "name");
  const unavailableSince = field(value, "unavailable_since");
  const renumberedAt = field(value, "renumbered_at");
  if (
    typeof name !== "string" ||
    !isTimeOrNull(unavailableSince) ||
    !isTimeOrNull(renumberedAt)
  ) {
    return null;
  }
  return { name, unavailableSince, renumberedAt };
}

function parseProblemList(value: unknown): ProblemList | null {
  const total = field(value, "total");
  const items = parseAll(field(value, "items"), parseProblemMessage);
  return isCount(total) && items !== null ? { total, items } : null;
}

function parseProblemMessage(value: unknown): ProblemMessage | null {
  const folder = field(value, "folder");
  const size = field(value, "size");
  const receivedAt = field(value, "received_at");
  const recordedAt = field(value, "recorded_at");
  const category = field(value, "category");
  if (
    typeof folder !== "string" ||
    (size !== null && !isCount(size)) ||
    !isTimeOrNull(receivedAt) ||
    !isTime(recordedAt) ||
    !isOneOf(category, MESSAGE_PROBLEM_CATEGORIES)
  ) {
    return null;
  }
  return { folder, size, receivedAt, recordedAt, category };
}

/** Parses every element, or returns null if value isn't an array or one fails. */
function parseAll<T>(
  value: unknown,
  parse: (element: unknown) => T | null,
): T[] | null {
  if (!Array.isArray(value)) return null;
  const parsed = value.map(parse);
  return parsed.some((element) => element === null) ? null : (parsed as T[]);
}

/** Parses a value that may be null. Undefined means it was neither. */
function parseNullable<T>(
  value: unknown,
  parse: (value: unknown) => T | null,
): T | null | undefined {
  if (value === null) return null;
  return parse(value) ?? undefined;
}

function isOneOf<T extends string>(
  value: unknown,
  allowed: readonly T[],
): value is T {
  return allowed.some((member) => member === value);
}

function isStringArray(value: unknown): value is string[] {
  return (
    Array.isArray(value) &&
    value.every((element) => typeof element === "string")
  );
}

function isTimeOrNull(value: unknown): value is string | null {
  return value === null || isTime(value);
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

/**
 * The contract leaves out `message_id`, `content_hash`, `source_id`, and
 * `source_name` where they don't apply. A message event always names its
 * source.
 */
function parseFailure(value: unknown): FailureEvent | null {
  const at = field(value, "at");
  const category = field(value, "category");
  const messageId = field(value, "message_id") ?? null;
  const contentHash = field(value, "content_hash") ?? null;
  const sourceId = field(value, "source_id") ?? null;
  const sourceName = field(value, "source_name") ?? null;
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
  let source: FailureSource | null = null;
  if (sourceId !== null || sourceName !== null) {
    if (!isCount(sourceId) || typeof sourceName !== "string") return null;
    source = { id: sourceId, name: sourceName };
  }
  if (source === null && MESSAGE_CATEGORIES.has(category)) return null;
  return { at, category, messageId, contentHash, source, restored };
}

const MESSAGE_CATEGORIES: ReadonlySet<FailureCategory> = new Set([
  "message_too_large",
  "message_removed",
]);

function isFailureCategory(value: unknown): value is FailureCategory {
  return FAILURE_CATEGORIES.some((category) => category === value);
}

/** Reads one message as the contract writes it, or null if it isn't one. */
export function parseMessage(value: unknown): Message | null {
  const id = field(value, "id");
  const contentHash = field(value, "content_hash");
  const size = field(value, "size");
  const observationCount = field(value, "observation_count");
  const latest = parseObservation(field(value, "latest"));
  if (
    !isCount(id) ||
    !isContentHash(contentHash) ||
    !isCount(size) ||
    !isCount(observationCount) ||
    latest === null
  ) {
    return null;
  }
  return {
    id,
    contentHash,
    size,
    observedAt: latest.observedAt,
    observationCount,
    latest,
  };
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

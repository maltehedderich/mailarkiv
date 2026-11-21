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

/** How far processing has read a message, in the server's words. */
export const PROCESSING_STATES = [
  "not_processed",
  "processed",
  "processed_with_problems",
  "unreadable",
] as const;

export type Processing = (typeof PROCESSING_STATES)[number];

export interface Message {
  id: number;
  contentHash: string;
  size: number;
  processing: Processing;
  /** The date the list orders by, RFC 3339 in UTC. */
  listDate: string;
  /** The list date is the first archive time, not the Date header's. */
  listDateIsArchiveTime: boolean;
  /** Null when the message has no summary or no From header. */
  sender: Sender | null;
  /** Untrusted. Null when the message has no summary or no Subject header. */
  subject: string | null;
  /** False when the message has no summary. */
  hasAttachments: boolean;
  /** Counts every entry, imports and collections alike. */
  observationCount: number;
  latest: Observation;
}

/** The first From address. Both parts are untrusted text. */
export interface Sender {
  /** Null when the sender gave none, or it equals the address. */
  name: string | null;
  /** Null when From wasn't a list of addresses. The name then holds its text. */
  address: string | null;
  /** Further From addresses. */
  more: number;
}

export interface MessagePage {
  items: Message[];
  page: number;
  pageSize: number;
  total: number;
}

/** A search as the form holds it. Empty text is no filter. */
export interface SearchQuery {
  words: string;
  from: string;
  to: string;
  subject: string;
  attachment: string;
  /** RFC 3339 instants, or null for no bound. */
  listDateFrom: string | null;
  listDateBefore: string | null;
  /** True keeps only messages with attachments. */
  hasAttachments: boolean;
  page: number;
}

/** The last page a search can ask for. A later one answers page_too_deep. */
export const SEARCH_MAX_PAGE = 2_000;

export interface SearchPage extends MessagePage {
  /** Messages the search couldn't look at yet. */
  notIncluded: number;
  /** The words had nothing to search for, so this is the list's page. */
  fullList: boolean;
  /** Search needs a rebuild and answered anyway. */
  mayBeIncomplete: boolean;
}

/** The fields a search request names when it breaks a rule. */
export const SEARCH_FIELDS = [
  "words",
  "from",
  "to",
  "subject",
  "attachment",
  "list_date_from",
  "list_date_before",
  "has_attachments",
  "page",
] as const;

export type SearchField = (typeof SEARCH_FIELDS)[number];

/** Why search can't run: the category of its last failure. */
export const SEARCH_CAUSES = [
  "search_process_stopped",
  "search_process_did_not_start",
  "index_write_failed",
  "index_maintenance_failed",
  "index_needs_rebuild",
  "low_disk_space",
  "rebuild_failed",
] as const;

export type SearchCause = (typeof SEARCH_CAUSES)[number];

export type SearchResult =
  | ApiResult<SearchPage>
  | { kind: "invalid_fields"; fields: SearchField[] }
  | { kind: "page_too_deep" }
  | { kind: "search_took_too_long" }
  | {
      kind: "search_unavailable";
      cause: SearchCause;
      /** RFC 3339 in UTC, or null while a start is under way. */
      nextStartAt: string | null;
    }
  | { kind: "search_did_not_answer" };

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

/** What processing found wrong with a part, in the server's words. */
export const PROBLEM_KINDS = [
  "unknown_charset",
  "invalid_encoding",
  "malformed_header",
  "malformed_structure",
  "cut_short",
  "too_many_parts",
  "nested_too_deeply",
  "body_cut",
  "took_too_long",
] as const;

export type ProblemKind = (typeof PROBLEM_KINDS)[number];

/** Why the list date is the first archive time and not the Date header's. */
export const DATE_REASONS = ["missing", "unreadable", "ahead"] as const;

export type DateReason = (typeof DATE_REASONS)[number];

export const ATTACHMENT_KINDS = ["file", "message", "embedded_image"] as const;

export type AttachmentKind = (typeof ATTACHMENT_KINDS)[number];

export interface MessageProblem {
  /** The part number, or null for the whole message. */
  position: string | null;
  /** Untrusted. */
  declaredType: string | null;
  kind: ProblemKind;
}

/** One mailbox in full. Both parts are untrusted text. */
export interface Address {
  name: string | null;
  /** Null when the header wasn't a list of addresses. The name holds its text. */
  address: string | null;
}

export interface Attachment {
  /** The part number, which names it in its download address. */
  position: string;
  /** Untrusted. */
  name: string | null;
  /** Untrusted. */
  declaredType: string | null;
  /** Decoded bytes. */
  size: number;
  damaged: boolean;
  kind: AttachmentKind;
}

/** A message as the view shows it. */
export interface MessageContent {
  id: number;
  contentHash: string;
  size: number;
  processing: Processing;
  /** In part order, with the whole message last. */
  problems: MessageProblem[];
  listDate: string;
  dateReason: DateReason | null;
  /** Untrusted. The Date header's text. */
  dateHeader: string | null;
  /** Null when processing hasn't read the message, or couldn't. */
  parsed: ParsedMessage | null;
}

/** What a worker read from the original, in full. Text fields are untrusted. */
export interface ParsedMessage {
  subject: string | null;
  from: Address[];
  to: Address[];
  cc: Address[];
  bcc: Address[];
  encrypted: boolean;
  /** Null when the message has no HTML body. */
  html: {
    truncated: boolean;
    remoteImages: boolean;
    embeddedImagesLeftOut: boolean;
  } | null;
  /** Null when the message has no text body. */
  text: { truncated: boolean } | null;
  /** In part order. */
  attachments: Attachment[];
}

/** Why the view couldn't read a message, in the server's words. */
export const CONTENT_FAILURES = [
  "not_found",
  "stored_original_missing",
  "stored_original_damaged",
  "busy",
  "could_not_read",
] as const;

export type ContentFailure = (typeof CONTENT_FAILURES)[number];

export type ContentResult<T> =
  ApiResult<T> | { kind: "failed"; reason: ContentFailure };

export interface SessionInfo {
  problemCount: number;
  /** Whole seconds the session had left when the server answered. */
  expiresIn: number;
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
  "search_process_stopped",
  "search_process_did_not_start",
  "search_process_did_not_answer",
  "index_write_failed",
  "index_maintenance_failed",
  "index_needs_rebuild",
  "low_disk_space",
  "rebuild_failed",
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
  /**
   * How many failures the event stands for, at least 1. The server merges
   * a run of one search failure into one event.
   */
  repeats: number;
  /** The newest failure's time, RFC 3339 in UTC. Equals at for one. */
  lastAt: string;
  /** Set on search failures only. */
  detail: FailureDetail | null;
}

/** The closed reasons a search failure's detail can give. */
export const FAILURE_CAUSES = [
  "missing",
  "not_executable",
  "bad_hello",
  "no_space",
  "permission_denied",
  "other",
  "search_process_stopped",
  "index_maintenance_failed",
  "index_damaged",
] as const;

export type FailureCause = (typeof FAILURE_CAUSES)[number];

/** The calls to the search process, as a failure names them. */
export const SEARCH_OPERATIONS = [
  "open",
  "index",
  "search",
  "maintain",
  "close",
  "size",
] as const;

export type SearchOperation = (typeof SEARCH_OPERATIONS)[number];

/**
 * A search failure's IDs and closed words. Which keys are set depends on
 * the category; the others are null. None carries mail or search text.
 */
export interface FailureDetail {
  exitStatus: number | null;
  signal: number | null;
  cause: FailureCause | null;
  operation: SearchOperation | null;
  generationId: number | null;
  rebuildId: number | null;
  firstMessageId: number | null;
  lastMessageId: number | null;
  freeBytes: number | null;
}

/** Messages by how processing went. */
export interface ProcessingCounts {
  notProcessed: number;
  processedWithProblems: number;
  unreadable: number;
}

export const SEARCH_STATES = [
  "available",
  "unavailable",
  "needs_rebuild",
] as const;

export type SearchState = (typeof SEARCH_STATES)[number];

export const INDEXING_STATES = [
  "up_to_date",
  "catching_up",
  "building",
  "rebuilding",
  "paused",
  "stopped",
] as const;

export type IndexingState = (typeof INDEXING_STATES)[number];

/** What marked the index Needs rebuild. */
export const NEEDS_REBUILD_CAUSES = [
  "search_process_stopped",
  "index_maintenance_failed",
  "index_damaged",
] as const;

export type NeedsRebuildCause = (typeof NEEDS_REBUILD_CAUSES)[number];

/** The rebuild an administrator started, while it runs. */
export interface RunningRebuild {
  id: number;
  /** RFC 3339 in UTC. */
  startedAt: string;
  /** The account that started it. */
  startedBy: string;
}

export interface SearchStatus {
  state: SearchState;
  /** Set while Unavailable and waiting to start again. RFC 3339 in UTC. */
  nextStartAt: string | null;
  needsRebuild: { cause: NeedsRebuildCause; since: string } | null;
  /** Starts after the first since the server started. */
  restarts: number;
  indexing: {
    state: IndexingState;
    /** Processed messages the searched index hasn't saved. */
    waiting: number;
    /** Set while a first build or a rebuild runs, even when paused. */
    done: number | null;
    toDo: number | null;
    /** The latest reading where the index lives, null before the first. */
    freeBytes: number | null;
    pauseBelowBytes: number;
    resumeAtBytes: number;
  };
  rebuild: RunningRebuild | null;
}

/** What the server knows since its latest start. */
export interface ServerStatus {
  /** RFC 3339 in UTC. */
  startedAt: string;
  processing: ProcessingCounts;
  search: SearchStatus;
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

/**
 * A call that signs in to the mail server waits this long. The server has 120
 * seconds and raises its write deadline to 130.
 */
export const SIGN_IN_TIMEOUT_MS = 150_000;

/** The categories a sign-in check can fail with. */
export const CHECK_CATEGORIES = [
  "server_not_reachable",
  "secure_connection_failed",
  "sign_in_rejected",
  "timed_out",
  "server_error",
] as const;

export type CheckCategory = (typeof CHECK_CATEGORIES)[number];

/** The fields a source's requests have, with the server's names. */
export const SOURCE_FIELDS = [
  "name",
  "host",
  "port",
  "security",
  "user_name",
  "password",
  "folders",
] as const;

export type SourceField = (typeof SOURCE_FIELDS)[number];

/** A folder the mail server lists. Its name is untrusted. */
export interface ServerFolder {
  name: string;
  /** Null when the server didn't report a count. */
  messages: number | null;
}

/** How to reach and sign in to a mailbox. */
export interface Connection {
  host: string;
  /** Null when the field holds no number, which the server refuses. */
  port: number | null;
  security: Security;
  userName: string;
  password: string;
}

export interface NewSource extends Connection {
  name: string;
  /** In sync order. */
  folders: string[];
}

/** What a check while editing changes. Missing fields use the stored ones. */
export interface ConnectionChange {
  port?: number | null;
  security?: Security;
  password?: string;
}

/** The fields an edit sends. A missing password keeps the stored one. */
export interface SourceChange extends ConnectionChange {
  name?: string;
  folders?: string[];
}

export type ApiResult<T> =
  | { kind: "ok"; value: T }
  | { kind: "session_ended" }
  | { kind: "unavailable" };

/** Why the server refused to change a source, as it answered. */
export type Refusal =
  | { kind: "check_failed"; category: CheckCategory; reply: string | null }
  | { kind: "source_exists"; sourceId: number }
  | { kind: "no_key" }
  | { kind: "invalid_fields"; fields: SourceField[] }
  | { kind: "already_syncing" }
  | { kind: "paused" };

type RefusalOf<K extends Refusal["kind"]> = Extract<Refusal, { kind: K }>;

/** The client gave up on a call that signs in. */
export type TimedOut = { kind: "timed_out" };

/** What a new source's check or save ends in. */
export type NewSourceResult<T> =
  | ApiResult<T>
  | RefusalOf<"check_failed" | "source_exists" | "no_key" | "invalid_fields">
  | TimedOut;

/** What an edit, or a check while editing, ends in. */
export type ChangeResult<T> =
  | ApiResult<T>
  | RefusalOf<"check_failed" | "no_key" | "invalid_fields">
  | TimedOut;

export type SyncResult =
  ApiResult<Source> | RefusalOf<"already_syncing" | "paused" | "no_key">;

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
    const expiresIn = field(body, "expires_in");
    return isCount(count) && isCount(expiresIn)
      ? { problemCount: count, expiresIn }
      : null;
  });
}

/** Lists one page. `page` is passed as typed, or left out when null. */
export async function listMessages(
  page: string | null,
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<MessagePage>> {
  return getJSON(fetch, `/api/messages${pageQuery(page)}`, parseMessagePage);
}

/**
 * Lists one page of the messages processed with problems or that couldn't be
 * read, as listMessages lists every message.
 */
export async function listProcessingProblems(
  page: string | null,
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<MessagePage>> {
  return getJSON(
    fetch,
    `/api/processing-problems${pageQuery(page)}`,
    parseMessagePage,
  );
}

function pageQuery(page: string | null): string {
  return page === null ? "" : `?${new URLSearchParams({ page })}`;
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

/**
 * Searches. The query goes in the body, so search words stay out of
 * addresses, which proxies and browser history record.
 */
/**
 * The view's calls wait up to 30 s for a worker and 60 s for it to read, so
 * they give up later than the rest.
 */
export const VIEW_TIMEOUT_MS = 100_000;

/**
 * Reads one message for the view. `id` goes to the server as typed, so one
 * that names no message comes back as not_found.
 */
export async function getMessageContent(
  id: string,
  fetch: Fetch = globalThis.fetch,
): Promise<ContentResult<MessageContent>> {
  return getView(
    fetch,
    `/api/messages/${encodeURIComponent(id)}/content`,
    async (response) => parseContent(await response.json()),
  );
}

/** Reads a message's plain text body, as text. */
export async function getMessageText(
  id: number,
  fetch: Fetch = globalThis.fetch,
): Promise<ContentResult<string>> {
  return getView(fetch, `/api/messages/${id}/text`, (response) =>
    response.text(),
  );
}

/** The preview frame's address. Only `images` asks for images from the internet. */
export function previewAddress(id: number, images: boolean): string {
  return `/api/messages/${id}/html${images ? "?images=1" : ""}`;
}

/** The address that sends one attachment's decoded bytes as a file. */
export function attachmentAddress(id: number, position: string): string {
  return `/api/messages/${id}/attachments/${position}`;
}

async function getView<T>(
  fetch: Fetch,
  path: string,
  read: (response: Response) => Promise<T | null>,
): Promise<ContentResult<T>> {
  try {
    const response = await fetch(path, {
      signal: AbortSignal.timeout(VIEW_TIMEOUT_MS),
    });
    if (!isMarked(response)) {
      await discard(response);
      return { kind: "unavailable" };
    }
    if (response.status === 401) {
      await discard(response);
      return { kind: "session_ended" };
    }
    if (response.status !== 200) {
      return parseContentFailure(response.status, await response.json());
    }
    const value = await read(response);
    return value === null ? { kind: "unavailable" } : { kind: "ok", value };
  } catch {
    // A network error, the timeout, or a body that isn't what it says.
    return { kind: "unavailable" };
  }
}

const CONTENT_FAILURE_STATUS: Record<ContentFailure, number> = {
  not_found: 404,
  stored_original_missing: 404,
  stored_original_damaged: 500,
  busy: 503,
  could_not_read: 500,
};

function parseContentFailure(
  status: number,
  body: unknown,
): ContentResult<never> {
  const error = field(body, "error");
  return isOneOf(error, CONTENT_FAILURES) &&
    CONTENT_FAILURE_STATUS[error] === status
    ? { kind: "failed", reason: error }
    : { kind: "unavailable" };
}

export async function searchMessages(
  query: SearchQuery,
  fetch: Fetch = globalThis.fetch,
): Promise<SearchResult> {
  const reply = await send(fetch, "POST", "/api/search", {
    body: {
      words: query.words,
      from: query.from,
      to: query.to,
      subject: query.subject,
      attachment: query.attachment,
      list_date_from: query.listDateFrom,
      list_date_before: query.listDateBefore,
      has_attachments: query.hasAttachments,
      page: query.page,
    },
  });
  if (reply.kind === "timed_out") return { kind: "unavailable" };
  if (reply.kind !== "answer") return reply;
  if (reply.status === 200) {
    const value = parseSearchPage(reply.body);
    return value === null ? { kind: "unavailable" } : { kind: "ok", value };
  }
  return (
    parseSearchRefusal(reply.status, reply.body) ?? { kind: "unavailable" }
  );
}

/** Why a rebuild didn't start. */
export type RebuildResult =
  | ApiResult<RunningRebuild>
  | { kind: "rebuild_running" }
  | { kind: "not_enough_space"; neededBytes: number; freeBytes: number }
  | { kind: "no_index" }
  | { kind: "search_unavailable" };

/** Starts a rebuild of the search index. The request has no body. */
export async function startRebuild(
  fetch: Fetch = globalThis.fetch,
): Promise<RebuildResult> {
  const reply = await send(fetch, "POST", "/api/search/rebuild");
  if (reply.kind !== "answer") {
    return reply.kind === "timed_out" ? { kind: "unavailable" } : reply;
  }
  const error = field(reply.body, "error");
  if (reply.status === 202) {
    const rebuild = parseRunningRebuild(field(reply.body, "rebuild"));
    return rebuild === null
      ? { kind: "unavailable" }
      : { kind: "ok", value: rebuild };
  }
  if (reply.status === 409 && error === "not_enough_space") {
    const neededBytes = field(reply.body, "needed_bytes");
    const freeBytes = field(reply.body, "free_bytes");
    return isCount(neededBytes) && isCount(freeBytes)
      ? { kind: "not_enough_space", neededBytes, freeBytes }
      : { kind: "unavailable" };
  }
  if (
    reply.status === 409 &&
    (error === "rebuild_running" || error === "no_index")
  ) {
    return { kind: error };
  }
  if (reply.status === 503 && error === "search_unavailable") {
    return { kind: error };
  }
  return { kind: "unavailable" };
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

/** Signs in to the mailbox and lists its folders, saving nothing. */
export async function checkNewSource(
  connection: Connection,
  fetch: Fetch = globalThis.fetch,
): Promise<NewSourceResult<ServerFolder[]>> {
  const reply = await send(fetch, "POST", "/api/sources/check", {
    body: connectionJSON(connection),
    signsIn: true,
  });
  return read(reply, 200, parseServerFolders, [
    "check_failed",
    "source_exists",
    "no_key",
    "invalid_fields",
  ]);
}

/** Saves a source, which signs in again first. Its first sync starts. */
export async function addSource(
  source: NewSource,
  fetch: Fetch = globalThis.fetch,
): Promise<NewSourceResult<Source>> {
  const reply = await send(fetch, "POST", "/api/sources", {
    body: {
      ...connectionJSON(source),
      name: source.name,
      folders: source.folders,
    },
    signsIn: true,
  });
  return read(reply, 201, parseListedSource, [
    "check_failed",
    "source_exists",
    "no_key",
    "invalid_fields",
  ]);
}

/**
 * Changes a source. A new port, security, or password signs in first, so
 * the call waits as long as a sign-in.
 */
export async function editSource(
  id: string,
  change: SourceChange,
  fetch: Fetch = globalThis.fetch,
): Promise<ChangeResult<SourceDetail>> {
  const reply = await send(fetch, "PATCH", sourcePath(id), {
    body: changeJSON(change),
    signsIn: true,
  });
  return read(reply, 200, parseSourceDetail, [
    "check_failed",
    "no_key",
    "invalid_fields",
  ]);
}

/** Lists a saved source's folders, signing in with the changed details. */
export async function checkSource(
  id: string,
  change: ConnectionChange,
  fetch: Fetch = globalThis.fetch,
): Promise<ChangeResult<ServerFolder[]>> {
  const reply = await send(fetch, "POST", `${sourcePath(id)}/check`, {
    body: changeJSON(change),
    signsIn: true,
  });
  return read(reply, 200, parseServerFolders, [
    "check_failed",
    "no_key",
    "invalid_fields",
  ]);
}

/** Stops a running sync, and any sync until the source is resumed. */
export async function pauseSource(
  id: string,
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<Source>> {
  const reply = await send(fetch, "POST", `${sourcePath(id)}/pause`);
  return withoutTimedOut(read(reply, 200, parseListedSource, []));
}

/** Resuming a paused source starts a sync at once. */
export async function resumeSource(
  id: string,
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<Source>> {
  const reply = await send(fetch, "POST", `${sourcePath(id)}/resume`);
  return withoutTimedOut(read(reply, 200, parseListedSource, []));
}

export async function syncSource(
  id: string,
  fetch: Fetch = globalThis.fetch,
): Promise<SyncResult> {
  const reply = await send(fetch, "POST", `${sourcePath(id)}/sync`);
  return withoutTimedOut(
    read(reply, 200, parseListedSource, [
      "already_syncing",
      "paused",
      "no_key",
    ]),
  );
}

/** Removes a source and its password. Its messages and history stay. */
export async function removeSource(
  id: string,
  fetch: Fetch = globalThis.fetch,
): Promise<ApiResult<null>> {
  const reply = await send(fetch, "DELETE", sourcePath(id));
  if (reply.kind !== "answer") {
    return reply.kind === "timed_out" ? { kind: "unavailable" } : reply;
  }
  return reply.status === 204
    ? { kind: "ok", value: null }
    : { kind: "unavailable" };
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

/** A marked answer with its JSON body, or why there was none to read. */
type Reply =
  | { kind: "answer"; status: number; body: unknown }
  | { kind: "session_ended" }
  | { kind: "unavailable" }
  | TimedOut;

/**
 * Sends a request that changes something. A timeout is told apart, because
 * after a sign-in's 150 seconds the screen says so. A 204's body is null.
 */
async function send(
  fetch: Fetch,
  method: "POST" | "PATCH" | "DELETE",
  path: string,
  { body, signsIn = false }: { body?: object; signsIn?: boolean } = {},
): Promise<Reply> {
  let response: Response;
  try {
    response = await fetch(path, {
      method,
      ...(body === undefined
        ? {}
        : {
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          }),
      signal: AbortSignal.timeout(signsIn ? SIGN_IN_TIMEOUT_MS : TIMEOUT_MS),
    });
  } catch (error) {
    return isTimeout(error) ? { kind: "timed_out" } : { kind: "unavailable" };
  }
  if (
    !isMarked(response) ||
    response.status === 401 ||
    response.status === 204
  ) {
    await discard(response);
    if (!isMarked(response)) return { kind: "unavailable" };
    if (response.status === 401) return { kind: "session_ended" };
    return { kind: "answer", status: 204, body: null };
  }
  try {
    return {
      kind: "answer",
      status: response.status,
      body: await response.json(),
    };
  } catch (error) {
    // The timeout while the body arrived, or a body that isn't JSON.
    return isTimeout(error) ? { kind: "timed_out" } : { kind: "unavailable" };
  }
}

/**
 * Reads a reply as the expected status with a value, or as one of the
 * refusals the call can meet. Anything else is unavailable.
 */
function read<T, K extends Refusal["kind"]>(
  reply: Reply,
  status: number,
  parse: (body: unknown) => T | null,
  refusals: readonly K[],
): ApiResult<T> | RefusalOf<K> | TimedOut {
  if (reply.kind !== "answer") return reply;
  if (reply.status === status) {
    const value = parse(reply.body);
    return value === null ? { kind: "unavailable" } : { kind: "ok", value };
  }
  const refusal = parseRefusal(reply.status, reply.body);
  if (refusal === null || !isOneOf(refusal.kind, refusals)) {
    return { kind: "unavailable" };
  }
  return refusal as RefusalOf<K>;
}

/** Only a call that signs in waits long enough to say it timed out. */
function withoutTimedOut<T>(result: T | TimedOut): T | { kind: "unavailable" } {
  return isTimedOut(result) ? { kind: "unavailable" } : result;
}

function isTimedOut(result: unknown): result is TimedOut {
  return field(result, "kind") === "timed_out";
}

function isTimeout(error: unknown): boolean {
  return error instanceof DOMException && error.name === "TimeoutError";
}

function parseRefusal(status: number, body: unknown): Refusal | null {
  const error = field(body, "error");
  if (status === 422 && error === "check_failed") {
    const category = field(body, "category");
    const reply = field(body, "reply") ?? null;
    if (!isOneOf(category, CHECK_CATEGORIES)) return null;
    if (reply !== null && typeof reply !== "string") return null;
    return { kind: "check_failed", category, reply };
  }
  if (status === 400 && error === "invalid_fields") {
    const fields = field(body, "fields");
    if (
      !Array.isArray(fields) ||
      !fields.every((name) => isOneOf(name, SOURCE_FIELDS))
    ) {
      return null;
    }
    return { kind: "invalid_fields", fields };
  }
  if (status === 409 && error === "source_exists") {
    const sourceId = field(body, "source_id");
    return isCount(sourceId) ? { kind: "source_exists", sourceId } : null;
  }
  if (status === 409) {
    switch (error) {
      case "no_key":
      case "already_syncing":
      case "paused":
        return { kind: error };
    }
  }
  return null;
}

function sourcePath(id: string): string {
  return `/api/sources/${encodeURIComponent(id)}`;
}

function connectionJSON(connection: Connection) {
  return {
    host: connection.host,
    port: connection.port,
    security: connection.security,
    user_name: connection.userName,
    password: connection.password,
  };
}

/** Only the fields given, so the server keeps the rest. */
function changeJSON(change: SourceChange) {
  const { name, port, security, password, folders } = change;
  return Object.fromEntries(
    Object.entries({ name, port, security, password, folders }).filter(
      ([, value]) => value !== undefined,
    ),
  );
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

function parseSearchPage(body: unknown): SearchPage | null {
  const page = parseMessagePage(body);
  const notIncluded = field(body, "not_included");
  const fullList = field(body, "full_list");
  const mayBeIncomplete = field(body, "may_be_incomplete");
  if (
    page === null ||
    !isCount(notIncluded) ||
    typeof fullList !== "boolean" ||
    typeof mayBeIncomplete !== "boolean"
  ) {
    return null;
  }
  return { ...page, notIncluded, fullList, mayBeIncomplete };
}

function parseSearchRefusal(
  status: number,
  body: unknown,
): Exclude<
  SearchResult,
  { kind: "ok" | "session_ended" | "unavailable" }
> | null {
  const error = field(body, "error");
  if (status === 400 && error === "invalid_fields") {
    const fields = field(body, "fields");
    if (
      !Array.isArray(fields) ||
      !fields.every((name) => isOneOf(name, SEARCH_FIELDS))
    ) {
      return null;
    }
    return { kind: "invalid_fields", fields };
  }
  if (status === 400 && error === "page_too_deep") return { kind: error };
  if (status === 422 && error === "search_took_too_long")
    return { kind: error };
  if (status === 503 && error === "search_did_not_answer")
    return { kind: error };
  if (status === 503 && error === "search_unavailable") {
    const cause = field(body, "cause");
    const nextStartAt = field(body, "next_start_at");
    if (!isOneOf(cause, SEARCH_CAUSES) || !isTimeOrNull(nextStartAt)) {
      return null;
    }
    return { kind: error, cause, nextStartAt };
  }
  return null;
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

const READ_STATES: readonly Processing[] = [
  "processed",
  "processed_with_problems",
];

function parseContent(body: unknown): MessageContent | null {
  const id = field(body, "id");
  const contentHash = field(body, "content_hash");
  const size = field(body, "size");
  const processing = field(body, "processing");
  const problems = parseAll(field(body, "problems"), parseProblem);
  const listDate = field(body, "list_date");
  const dateReason = field(body, "date_reason");
  const dateHeader = field(body, "date_header");
  if (
    !isCount(id) ||
    !isContentHash(contentHash) ||
    !isCount(size) ||
    !isOneOf(processing, PROCESSING_STATES) ||
    problems === null ||
    !isTime(listDate) ||
    (dateReason !== null && !isOneOf(dateReason, DATE_REASONS)) ||
    (dateHeader !== null && typeof dateHeader !== "string")
  ) {
    return null;
  }
  let parsed: ParsedMessage | null = null;
  if (READ_STATES.includes(processing)) {
    parsed = parseParsed(body);
    if (parsed === null) return null;
  }
  return {
    id,
    contentHash,
    size,
    processing,
    problems,
    listDate,
    dateReason,
    dateHeader,
    parsed,
  };
}

function parseParsed(body: unknown): ParsedMessage | null {
  const subject = field(body, "subject");
  const from = parseAll(field(body, "from"), parseAddress);
  const to = parseAll(field(body, "to"), parseAddress);
  const cc = parseAll(field(body, "cc"), parseAddress);
  const bcc = parseAll(field(body, "bcc"), parseAddress);
  const encrypted = field(body, "encrypted");
  const html = parseNullable(field(body, "html"), parseHTMLBody);
  const text = parseNullable(field(body, "text"), (value) => {
    const truncated = field(value, "truncated");
    return typeof truncated === "boolean" ? { truncated } : null;
  });
  const attachments = parseAll(field(body, "attachments"), parseAttachment);
  if (
    (subject !== null && typeof subject !== "string") ||
    from === null ||
    to === null ||
    cc === null ||
    bcc === null ||
    typeof encrypted !== "boolean" ||
    html === undefined ||
    text === undefined ||
    attachments === null
  ) {
    return null;
  }
  return { subject, from, to, cc, bcc, encrypted, html, text, attachments };
}

function parseHTMLBody(value: unknown): ParsedMessage["html"] {
  const truncated = field(value, "truncated");
  const remoteImages = field(value, "remote_images");
  const embeddedImagesLeftOut = field(value, "embedded_images_left_out");
  if (
    typeof truncated !== "boolean" ||
    typeof remoteImages !== "boolean" ||
    typeof embeddedImagesLeftOut !== "boolean"
  ) {
    return null;
  }
  return { truncated, remoteImages, embeddedImagesLeftOut };
}

function parseProblem(value: unknown): MessageProblem | null {
  const position = field(value, "position");
  const declaredType = field(value, "declared_type");
  const kind = field(value, "kind");
  if (
    (position !== null && typeof position !== "string") ||
    (declaredType !== null && typeof declaredType !== "string") ||
    !isOneOf(kind, PROBLEM_KINDS)
  ) {
    return null;
  }
  return { position, declaredType, kind };
}

function parseAddress(value: unknown): Address | null {
  const name = field(value, "name");
  const address = field(value, "address");
  if (
    (name !== null && typeof name !== "string") ||
    (address !== null && typeof address !== "string")
  ) {
    return null;
  }
  return { name, address };
}

function parseAttachment(value: unknown): Attachment | null {
  const position = field(value, "position");
  const name = field(value, "name");
  const declaredType = field(value, "declared_type");
  const size = field(value, "size");
  const damaged = field(value, "damaged");
  const kind = field(value, "kind");
  if (
    typeof position !== "string" ||
    !/^[1-9][0-9]*(\.[1-9][0-9]*)*$/.test(position) ||
    (name !== null && typeof name !== "string") ||
    (declaredType !== null && typeof declaredType !== "string") ||
    !isCount(size) ||
    typeof damaged !== "boolean" ||
    !isOneOf(kind, ATTACHMENT_KINDS)
  ) {
    return null;
  }
  return { position, name, declaredType, size, damaged, kind };
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
  const processing = parseProcessingCounts(field(body, "processing"));
  const search = parseSearchStatus(field(body, "search"));
  const recovery = parseRecovery(field(body, "recovery"));
  const unreferenced = field(body, "unreferenced");
  const failures = field(body, "failures");
  const sources = parseAll(field(body, "sources"), parseProblemSource);
  if (
    !isTime(startedAt) ||
    processing === null ||
    search === null ||
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
    processing,
    search,
    recovery,
    unreferenced: originals as UnreferencedOriginal[],
    failures: events as FailureEvent[],
    sources,
  };
}

function parseProcessingCounts(value: unknown): ProcessingCounts | null {
  const notProcessed = field(value, "not_processed");
  const processedWithProblems = field(value, "processed_with_problems");
  const unreadable = field(value, "unreadable");
  if (
    !isCount(notProcessed) ||
    !isCount(processedWithProblems) ||
    !isCount(unreadable)
  ) {
    return null;
  }
  return { notProcessed, processedWithProblems, unreadable };
}

/** A state word the interface doesn't know makes the whole status unreadable. */
function parseSearchStatus(value: unknown): SearchStatus | null {
  const state = field(value, "state");
  const nextStartAt = field(value, "next_start_at");
  const needsRebuild = parseNullable(
    field(value, "needs_rebuild"),
    parseNeedsRebuild,
  );
  const restarts = field(value, "restarts");
  const indexing = parseIndexing(field(value, "indexing"));
  const rebuild = parseNullable(field(value, "rebuild"), parseRunningRebuild);
  if (
    !isOneOf(state, SEARCH_STATES) ||
    !isTimeOrNull(nextStartAt) ||
    needsRebuild === undefined ||
    !isCount(restarts) ||
    indexing === null ||
    rebuild === undefined
  ) {
    return null;
  }
  return { state, nextStartAt, needsRebuild, restarts, indexing, rebuild };
}

function parseNeedsRebuild(
  value: unknown,
): SearchStatus["needsRebuild"] | null {
  const cause = field(value, "cause");
  const since = field(value, "since");
  if (!isOneOf(cause, NEEDS_REBUILD_CAUSES) || !isTime(since)) return null;
  return { cause, since };
}

function parseIndexing(value: unknown): SearchStatus["indexing"] | null {
  const state = field(value, "state");
  const waiting = field(value, "waiting");
  const done = field(value, "done");
  const toDo = field(value, "to_do");
  const freeBytes = field(value, "free_bytes");
  const pauseBelowBytes = field(value, "pause_below_bytes");
  const resumeAtBytes = field(value, "resume_at_bytes");
  if (
    !isOneOf(state, INDEXING_STATES) ||
    !isCount(waiting) ||
    !isCountOrNull(done) ||
    !isCountOrNull(toDo) ||
    !isCountOrNull(freeBytes) ||
    !isCount(pauseBelowBytes) ||
    !isCount(resumeAtBytes)
  ) {
    return null;
  }
  return {
    state,
    waiting,
    done,
    toDo,
    freeBytes,
    pauseBelowBytes,
    resumeAtBytes,
  };
}

function parseRunningRebuild(value: unknown): RunningRebuild | null {
  const id = field(value, "id");
  const startedAt = field(value, "started_at");
  const startedBy = field(value, "started_by");
  if (!isCount(id) || !isTime(startedAt) || typeof startedBy !== "string") {
    return null;
  }
  return { id, startedAt, startedBy };
}

/**
 * The contract leaves out keys that don't apply. A word outside the closed
 * lists makes the detail unreadable.
 */
function parseFailureDetail(value: unknown): FailureDetail | null {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return null;
  }
  const count = (name: string): number | null | undefined => {
    const n = field(value, name) ?? null;
    return n === null || isCount(n) ? n : undefined;
  };
  const cause = field(value, "cause") ?? null;
  const operation = field(value, "operation") ?? null;
  const detail = {
    exitStatus: count("exit_status"),
    signal: count("signal"),
    generationId: count("generation_id"),
    rebuildId: count("rebuild_id"),
    firstMessageId: count("first_message_id"),
    lastMessageId: count("last_message_id"),
    freeBytes: count("free_bytes"),
  };
  if (
    Object.values(detail).includes(undefined) ||
    (cause !== null && !isOneOf(cause, FAILURE_CAUSES)) ||
    (operation !== null && !isOneOf(operation, SEARCH_OPERATIONS))
  ) {
    return null;
  }
  return {
    ...(detail as Omit<FailureDetail, "cause" | "operation">),
    cause,
    operation,
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
  const sources = parseAll(field(body, "sources"), parseListedSource);
  if (collection !== "available" && collection !== "no_key") return null;
  return sources === null ? null : { collection, sources };
}

/** A source as the list has it, with its folders as names. */
function parseListedSource(value: unknown): Source | null {
  const fields = parseSourceFields(value);
  const folders = field(value, "folders");
  if (fields === null || !isStringArray(folders)) return null;
  return { ...fields, folders };
}

function parseServerFolders(body: unknown): ServerFolder[] | null {
  return parseAll(field(body, "folders"), (value) => {
    const name = field(value, "name");
    const messages = field(value, "messages");
    if (typeof name !== "string") return null;
    if (messages !== null && !isCount(messages)) return null;
    return { name, messages };
  });
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
  const repeats = field(value, "repeats");
  const lastAt = field(value, "last_at");
  const detail = parseNullable(
    field(value, "detail") ?? null,
    parseFailureDetail,
  );
  if (
    !isTime(at) ||
    !isFailureCategory(category) ||
    (messageId !== null && !isCount(messageId)) ||
    (contentHash !== null && !isContentHash(contentHash)) ||
    typeof restored !== "boolean" ||
    !isCount(repeats) ||
    repeats < 1 ||
    !isTime(lastAt) ||
    detail === undefined
  ) {
    return null;
  }
  let source: FailureSource | null = null;
  if (sourceId !== null || sourceName !== null) {
    if (!isCount(sourceId) || typeof sourceName !== "string") return null;
    source = { id: sourceId, name: sourceName };
  }
  if (source === null && MESSAGE_CATEGORIES.has(category)) return null;
  return {
    at,
    category,
    messageId,
    contentHash,
    source,
    restored,
    repeats,
    lastAt,
    detail,
  };
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
  const processing = field(value, "processing");
  const listDate = field(value, "list_date");
  const listDateIsArchiveTime = field(value, "list_date_is_archive_time");
  const sender = parseNullable(field(value, "sender"), parseSender);
  const subject = field(value, "subject");
  const hasAttachments = field(value, "has_attachments");
  const observationCount = field(value, "observation_count");
  const latest = parseObservation(field(value, "latest"));
  if (
    !isCount(id) ||
    !isContentHash(contentHash) ||
    !isCount(size) ||
    !isOneOf(processing, PROCESSING_STATES) ||
    !isTime(listDate) ||
    typeof listDateIsArchiveTime !== "boolean" ||
    sender === undefined ||
    (subject !== null && typeof subject !== "string") ||
    typeof hasAttachments !== "boolean" ||
    !isCount(observationCount) ||
    latest === null
  ) {
    return null;
  }
  return {
    id,
    contentHash,
    size,
    processing,
    listDate,
    listDateIsArchiveTime,
    sender,
    subject,
    hasAttachments,
    observationCount,
    latest,
  };
}

function parseSender(value: unknown): Sender | null {
  const name = field(value, "name");
  const address = field(value, "address");
  const more = field(value, "more");
  if (
    (name !== null && typeof name !== "string") ||
    (address !== null && typeof address !== "string") ||
    !isCount(more)
  ) {
    return null;
  }
  return { name, address, more };
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

function isCountOrNull(value: unknown): value is number | null {
  return value === null || isCount(value);
}

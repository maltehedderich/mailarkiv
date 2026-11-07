import { field, parseMessage, type Message } from "#lib/api.js";

// FR-032 and FR-033 as one pure function: "How the client classifies a result"
// in contracts/http-api.md. Anything it doesn't recognize is Unconfirmed,
// because the file may be stored and importing it again is safe.

export type RefusedReason = "empty" | "no_mail_header" | "too_large";

/** FR-038's three capture failures, as the server names them. */
export type CaptureFailure =
  "storage_full" | "storage_write_failed" | "catalog_write_failed";

/**
 * Why the file isn't archived: a capture failure MailArkiv reported, a marked
 * 403 from MailArkiv, or a proxy's upload limit (FR-033).
 */
export type FailedReason = CaptureFailure | "refused_by_server" | "proxy_limit";

export type ImportResult =
  | { outcome: "archived"; message: Message }
  | { outcome: "already_archived"; message: Message }
  | { outcome: "restored"; message: Message }
  | { outcome: "refused"; reason: RefusedReason }
  | { outcome: "failed"; reason: FailedReason }
  | { outcome: "unconfirmed" };

/**
 * A marked 401: the session ended before the upload was accepted. The page
 * opens sign-in with the session-ended message instead of showing an outcome.
 */
export type Classified = ImportResult | { outcome: "session_ended" };

/** What the browser got back for an upload. */
export type ImportResponse =
  | { kind: "response"; status: number; marked: boolean; body: string }
  | { kind: "network_error" }
  | { kind: "aborted" }
  | { kind: "timeout" };

const UNCONFIRMED: ImportResult = { outcome: "unconfirmed" };

// The contract's response table: each outcome with the status it comes with.
const STORED = {
  archived: 201,
  already_archived: 200,
  restored: 200,
} as const;

const REFUSED: Record<RefusedReason, number> = {
  empty: 422,
  no_mail_header: 422,
  too_large: 413,
};

const FAILED: Record<CaptureFailure, number> = {
  storage_full: 507,
  storage_write_failed: 500,
  catalog_write_failed: 500,
};

export function classify(response: ImportResponse): Classified {
  if (response.kind !== "response") return UNCONFIRMED;
  if (!response.marked) {
    return response.status === 413
      ? { outcome: "failed", reason: "proxy_limit" }
      : UNCONFIRMED;
  }
  if (response.status === 401) return { outcome: "session_ended" };
  if (response.status === 403) {
    return { outcome: "failed", reason: "refused_by_server" };
  }

  let body: unknown;
  try {
    body = JSON.parse(response.body);
  } catch {
    return UNCONFIRMED;
  }
  const outcome = field(body, "outcome");
  const reason = field(body, "reason");
  const { status } = response;

  if (
    outcome === "archived" ||
    outcome === "already_archived" ||
    outcome === "restored"
  ) {
    const message = parseMessage(field(body, "message"));
    if (message === null || STORED[outcome] !== status) return UNCONFIRMED;
    return { outcome, message };
  }
  if (
    outcome === "refused" &&
    isKey(REFUSED, reason) &&
    REFUSED[reason] === status
  ) {
    return { outcome, reason };
  }
  if (
    outcome === "failed" &&
    isKey(FAILED, reason) &&
    FAILED[reason] === status
  ) {
    return { outcome, reason };
  }
  return UNCONFIRMED;
}

function isKey<K extends string>(
  table: Record<K, number>,
  value: unknown,
): value is K {
  return typeof value === "string" && Object.hasOwn(table, value);
}

# HTTP API changes

This adds to [the Slice 1 API](../../001-import-browse-retrieve/contracts/http-api.md), which still applies: the response headers, JSON errors, times in RFC 3339 UTC with milliseconds, `Content-Length` on JSON answers, 500 `internal_error`, and the server timeouts. The API serves MailArkiv's own interface and isn't published. The Go server and the frontend change together.

## Access

Every new route is `SignedIn`. Non-GET requests pass through `http.CrossOriginProtection` before the session check, as in Slice 1, and `TestEveryRouteDeniesWithoutSession` covers the new routes because it reads the route table (FR-031).

Until the last delivery step, these routes exist only when `serve` runs with `--preview-collection`. Without it, `/api/sources` paths are unknown: 401 without a session and 404 with one, like any other unknown `/api/` path.

| Method | Path | Purpose |
| ------ | ---- | ------- |
| POST | `/api/sources/check` | Sign in with new details and list the server's folders |
| GET | `/api/sources` | List sources |
| POST | `/api/sources` | Add a source |
| GET | `/api/sources/{id}` | Read one source with its folders and problems |
| PATCH | `/api/sources/{id}` | Change name, port, security, password, or folders |
| POST | `/api/sources/{id}/check` | Sign in with the stored or changed details and list folders |
| POST | `/api/sources/{id}/pause` | Pause |
| POST | `/api/sources/{id}/resume` | Resume and sync at once |
| POST | `/api/sources/{id}/sync` | Sync now |
| DELETE | `/api/sources/{id}` | Remove |

An unknown or removed source ID, or one that isn't a number, answers 404 `not_found`.

## Words on the wire

| Field | Values |
| ----- | ------ |
| `security` | `tls`, `starttls` |
| `state` | `waiting`, `syncing`, `retrying`, `needs_attention`, `paused` |
| `category` | `server_not_reachable`, `secure_connection_failed`, `sign_in_rejected`, `password_unreadable`, `timed_out`, `server_error`, `folder_unavailable`, `storage_full`, `storage_write_failed`, `catalog_write_failed`, `message_unavailable`, `message_too_large`, `message_removed` |
| `collection` | `available`, `no_key` |

[The data model](../data-model.md#failure-categories) says where each category comes from. The frontend holds each category's meaning and next step (FR-035).

No response ever carries a password, sealed or not (FR-028). `TestSourceResponsesHaveNoPassword` sends a marker password through every route and searches every answer for it.

## Requests that sign in

The two check routes, `POST /api/sources`, and a `PATCH` that changes port, security, or password sign in to the mail server before answering. Each has 120 seconds from the request's start. The handler raises its write deadline to 130 seconds, and the client waits 150 seconds before it shows Timed out.

A failed sign-in answers:

| Status | Body |
| ------ | ---- |
| 422 | `{ "error": "check_failed", "category": "sign_in_rejected", "reply": "[AUTHENTICATIONFAILED] Invalid credentials" }` |

`category` is one of `server_not_reachable`, `secure_connection_failed`, `sign_in_rejected`, `timed_out`, or `server_error` (User Story 1 scenario 7). `reply` is the server's `NO` or `BAD` text, at most 1,000 characters, or absent when there was none. Nothing is saved.

Request bodies are at most 1 MiB. A body that isn't JSON, has unknown fields, or breaks [an input rule](../data-model.md#input-rules) answers 400 `{ "error": "invalid_fields", "fields": ["port", "password"] }`, naming each field that broke a rule.

When `serve` has no key, any request that would sign in or store a password answers 409 `no_key` (FR-026).

## `POST /api/sources/check`

```json
{
  "host": "imap.example.test",
  "port": 993,
  "security": "tls",
  "user_name": "anna@example.test",
  "password": "…"
}
```

| Status | Body | When |
| ------ | ---- | ---- |
| 200 | Folder list, below | Signed in and listed |
| 409 | `{ "error": "source_exists", "source_id": 3 }` | An active source has this host and user name, ignoring case. Checked before connecting (FR-004) |
| 409 | `no_key` | |
| 422 | `check_failed` | |

```json
{
  "folders": [
    { "name": "INBOX", "messages": 1204 },
    { "name": "Gesendete Objekte", "messages": 311 },
    { "name": "Archiv/2025", "messages": null }
  ]
}
```

`INBOX` comes first, then the server's order. `messages` is null when the server didn't report a count for that folder. Folders the server marks `\Noselect` or `\NonExistent` aren't listed. The client preselects `INBOX` (FR-003).

## `GET /api/sources`

```json
{
  "collection": "available",
  "sources": [
    {
      "id": 3,
      "name": "Anna",
      "host": "imap.example.test",
      "port": 993,
      "security": "tls",
      "user_name": "anna@example.test",
      "state": "retrying",
      "folders": ["INBOX", "Gesendete Objekte"],
      "progress": null,
      "last_success": { "at": "2026-11-08T09:10:00.000Z", "archived": 12 },
      "collected": 18044,
      "next_sync_at": "2026-11-08T09:24:00.000Z",
      "paused_at": null,
      "failure": {
        "category": "timed_out",
        "since": "2026-11-08T09:15:00.000Z",
        "failed_attempts": 3,
        "last_at": "2026-11-08T09:20:00.000Z",
        "reply": null
      },
      "problems": { "unavailable_folders": 0, "waiting_for_retry": 2, "not_archived": 1 }
    }
  ]
}
```

| Field | Rule |
| ----- | ---- |
| `collection` | `no_key` when `serve` started without a key. The screen then says collection needs the key and links to the operator guide |
| `folders` | Chosen folders, in sync order |
| `progress` | `{ "archived": 120, "found": 4000 }` while syncing, otherwise null. `archived` counts only stored messages (FR-034) |
| `last_success` | Null before the first successful sync |
| `collected` | Distinct messages with an entry from this source |
| `next_sync_at` | Null while paused, syncing, or in Needs attention |
| `failure` | Null unless the state is `retrying` or `needs_attention`. For `password_unreadable`, `since` is when it was found, and `failed_attempts`, `last_at`, and `reply` are null |

Sources are ordered by name, then ID. The list has at most 50 entries in practice, so it isn't paged.

## `POST /api/sources`

The check request plus `name` and `folders`:

```json
{
  "name": "Anna",
  "host": "imap.example.test",
  "port": 993,
  "security": "tls",
  "user_name": "anna@example.test",
  "password": "…",
  "folders": ["INBOX", "Gesendete Objekte"]
}
```

The server signs in again before saving, so a direct request can't save details that never worked (FR-002). It doesn't list folders again. A chosen folder that doesn't exist shows as unavailable after the first sync.

| Status | Body | When |
| ------ | ---- | ---- |
| 201 | The source, as in the list | Saved. The first sync has started |
| 409 | `source_exists` with `source_id` | As for the check |
| 409 | `no_key` | |
| 422 | `check_failed` | Nothing saved |

## `GET /api/sources/{id}`

The list entry plus:

```json
{
  "folders": [
    { "name": "INBOX", "unavailable_since": null, "renumbered_at": "2026-11-08T08:00:00.000Z" },
    { "name": "Projekte", "unavailable_since": "2026-11-08T09:00:00.000Z", "renumbered_at": null }
  ],
  "messages": {
    "waiting_for_retry": { "total": 2, "items": [ { "folder": "INBOX", "size": 48211, "received_at": "2026-11-07T17:02:11.000Z", "recorded_at": "2026-11-08T09:00:03.000Z", "category": "message_unavailable" } ] },
    "not_archived": { "total": 1, "items": [ { "folder": "INBOX", "size": 160000000, "received_at": null, "recorded_at": "2026-11-08T09:00:01.000Z", "category": "message_too_large" } ] }
  }
}
```

`folders` replaces the list's array of names. Each `items` array holds the newest 100, and `total` counts all. `size` and `received_at` are null when the server didn't report them (FR-033). A renumbered folder says so on the source's page (FR-013).

## `PATCH /api/sources/{id}`

Any of `name`, `port`, `security`, `password`, and `folders`. `folders` is the full list of chosen names. `host` and `user_name` are refused with 400 `invalid_fields` (FR-005).

- A change to `port`, `security`, or `password` signs in first with the new values. A failure answers 422 `check_failed`, and nothing changes, including the other fields in the request.
- A missing or empty `password` keeps the stored one (FR-028). If the stored password can't be read, a change to `port` or `security` needs a password, and without one the answer is 400 `invalid_fields` naming `password`.
- Saving a password clears a rejected sign-in and an unreadable password, then syncs at once unless paused (FR-022, FR-027).
- Adding a folder syncs at once unless paused (FR-009). Removing one stops collecting it, and its messages stay.

| Status | Body |
| ------ | ---- |
| 200 | The source, as in `GET /api/sources/{id}` |
| 409 | `no_key`, when the request has a password or changes port or security |
| 422 | `check_failed` |

## `POST /api/sources/{id}/check`

For adding folders while editing. Optional `port`, `security`, and `password`. Missing fields use the stored values. Answers as `POST /api/sources/check`, without `source_exists`, and with 404 `not_found` for an unknown source. If the stored password can't be read and the request has none, it answers 400 `invalid_fields` naming `password`.

## `POST /api/sources/{id}/pause`, `/resume`, `/sync`

No body. Each answers 200 with the source as in the list. When `resume` or `sync` starts a sync, the answer's state is `syncing`.

| Route | Refused with | When |
| ----- | ------------ | ---- |
| `pause` | | Never. Pausing a paused source changes nothing. A running sync stops before the answer |
| `resume` | | Never. Resuming an active source starts nothing |
| `sync` | 409 `already_syncing` | A sync is running (User Story 6 scenario 4) |
| `sync` | 409 `paused` | The source is paused |
| `sync` | 409 `no_key` | `serve` has no key |

## `DELETE /api/sources/{id}`

204. A running sync stops first. The source then disappears from the sources list, and its password is gone (FR-029). History entries keep naming it.

## Changed: `GET /api/messages`

`imported_at` becomes `observed_at`, and `import_count` becomes `observation_count`. `file_name` moves into `latest`, the latest history entry, in the same shape as `observations` below.

```json
{
  "items": [
    {
      "id": 17,
      "content_hash": "ea9449bf…",
      "size": 31,
      "observation_count": 3,
      "latest": {
        "source_type": "imap_collection",
        "observed_at": "2026-11-08T09:10:04.123Z",
        "source": { "id": 3, "name": "Anna", "host": "imap.example.test", "user_name": "anna@example.test", "removed": false },
        "folder": "INBOX",
        "received_at": "2026-11-08T09:07:55.000Z"
      }
    }
  ],
  "page": 1,
  "page_size": 50,
  "total": 1
}
```

Order: most recent entry first, as before.

## Changed: `GET /api/messages/{id}`

Each observation is one of:

```json
{ "source_type": "manual_import", "observed_at": "…", "account_email": "admin@example.test", "file_name": "invoice.eml" }
```

```json
{
  "source_type": "imap_collection",
  "observed_at": "…",
  "source": { "id": 3, "name": "Anna", "host": "imap.example.test", "user_name": "anna@example.test", "removed": true },
  "folder": "INBOX",
  "received_at": null
}
```

`received_at` is null when the server reported no time, and the screen shows Unknown (FR-041). A removed source keeps its name, host, user name, and folder.

## Changed: `POST /api/imports`

The `archived`, `already_archived`, and `restored` answers return `message` in the list item shape of [`GET /api/messages`](#changed-get-apimessages), so the client reads both with one parser. Statuses, outcomes, and reasons don't change.

```json
{
  "outcome": "archived",
  "message": {
    "id": 17,
    "content_hash": "ea9449bf…",
    "size": 31,
    "observation_count": 1,
    "latest": { "source_type": "manual_import", "observed_at": "2026-11-08T09:10:04.123Z", "account_email": "admin@example.test", "file_name": "invoice.eml" }
  }
}
```

## Changed: `GET /api/status`

Adds `sources`, and failure events for messages not archived:

```json
{
  "sources": [
    { "id": 3, "name": "Anna", "category": "timed_out", "since": "2026-11-08T09:15:00.000Z", "failed_attempts": 3 },
    { "id": 5, "name": "Office", "category": "folder_unavailable", "since": "2026-11-08T08:00:00.000Z", "failed_attempts": null }
  ],
  "failures": [
    { "at": "2026-11-08T09:00:01.000Z", "category": "message_too_large", "source_id": 3, "source_name": "Anna", "restored": false }
  ]
}
```

Each problem source appears once (FR-037, FR-038). Its category is the sync failure if it's Retrying or in Needs attention, otherwise `folder_unavailable` if a folder is unavailable, otherwise `message_unavailable`. A failing source keeps its sync failure while its next attempt runs, so it stays listed until a sync succeeds. A paused source shows no sync failure. Only chosen folders count, for both unavailable folders and messages waiting for retry. `since` is when that problem began. `failed_attempts` is null for anything but a sync failure. No entry carries a user name, folder name, server reply, or message content.

## Changed: `GET /api/session`

Same shape. `problem_count` adds the number of entries in `sources` above (FR-038).

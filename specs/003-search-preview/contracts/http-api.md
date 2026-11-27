# HTTP API changes

This adds to [the Slice 1 API](../../001-import-browse-retrieve/contracts/http-api.md) and [the Slice 2 changes](../../002-imap-incremental-archive/contracts/http-api.md), which still apply: the response headers, JSON errors, times in RFC 3339 UTC with milliseconds, `Content-Length` on JSON answers, 500 `internal_error`, and the server timeouts. The API serves MailArkiv's own interface and isn't published. The Go server and the frontend change together.

## Access

Every new route is `SignedIn`. Non-GET requests pass through `http.CrossOriginProtection` before the session check, as before. `TestEveryRouteDeniesWithoutSession` covers the new routes because it reads the route table (FR-042).

| Method | Path | Purpose |
| ------ | ---- | ------- |
| POST | `/api/search` | Search |
| GET | `/api/processing-problems` | List messages that are Processed with problems or Couldn't be read |
| GET | `/api/messages/{id}/content` | Read a message's fields, state, problems, bodies, and attachments |
| GET | `/api/messages/{id}/text` | The plain text body |
| GET | `/api/messages/{id}/html` | The HTML preview document, for the sandboxed frame |
| GET | `/api/messages/{id}/attachments/{position}` | Save one attachment |
| POST | `/api/search/rebuild` | Start a rebuild |

An unknown message ID, or one that isn't a number, answers 404 `not_found`, as in Slice 1. An unknown position answers 404 `not_found` too.

No route puts search words or filters in its address (FR-022). The server never logs request bodies or query values.

## Words on the wire

| Field | Values |
| ----- | ------ |
| `processing` | `not_processed`, `processed`, `processed_with_problems`, `unreadable` |
| `kind` (problem) | `unknown_charset`, `invalid_encoding`, `malformed_header`, `malformed_structure`, `cut_short`, `too_many_parts`, `nested_too_deeply`, `body_cut`, `took_too_long` |
| `date_reason` | `missing`, `unreadable`, `ahead` |
| `kind` (attachment) | `file`, `message`, `embedded_image` |
| `search.state` | `available`, `unavailable`, `needs_rebuild` |
| `indexing.state` | `up_to_date`, `catching_up`, `building`, `rebuilding`, `paused`, `stopped` |
| `category` (new) | `search_process_stopped`, `search_process_did_not_start`, `search_process_did_not_answer`, `index_write_failed`, `index_maintenance_failed`, `index_needs_rebuild`, `low_disk_space`, `rebuild_failed` |

[The data model](../data-model.md) says where each value comes from. The frontend holds each category's meaning and next step (FR-046).

## Changed: message rows

`GET /api/messages`, `GET /api/processing-problems`, `POST /api/search`, and the `message` in `POST /api/imports` answers all use this row:

```json
{
  "id": 17,
  "content_hash": "ea9449bf…",
  "size": 18234,
  "processing": "processed",
  "list_date": "2026-11-02T08:14:55.000Z",
  "list_date_is_archive_time": false,
  "sender": { "name": "Anna Schmidt", "address": "anna@example.com", "more": 0 },
  "subject": "Quartalsbericht Q3",
  "has_attachments": true,
  "observation_count": 2,
  "latest": { "source_type": "manual_import", "observed_at": "…", "account_email": "admin@example.test", "file_name": "q3.eml" }
}
```

| Field | Rule |
| ----- | ---- |
| `sender` | Null when the message has no summary, or no From header. `name` is null when the sender gave none or it equals the address ignoring case. `address` is null when From couldn't be read as addresses, and `name` then holds the header's decoded text. `more` counts further From addresses |
| `subject` | Null when the message has no summary or no Subject header |
| `has_attachments` | False when the message has no summary |
| `list_date_is_archive_time` | True when the list date is the first archive time, for any reason |
| `latest` | The latest history entry, as in Slice 2. Rows that are `not_processed` or `unreadable` show it (FR-009) |

Rows with a summary are `processed` or `processed_with_problems`. The interface shows "(no sender)" and "(no subject)" for null values only in those rows (FR-010).

## Changed: `GET /api/messages`

Same query and answer as before, with the new rows. Order: `list_date`, then first archive time, then ID, all newest first (FR-012). This replaces "most recent entry first".

## `GET /api/processing-problems?page=N`

Same query rules and answer shape as `GET /api/messages`, with only rows whose `processing` is `processed_with_problems` or `unreadable`, in the same order (FR-013).

## `POST /api/search`

```json
{
  "words": "quartals müller",
  "from": "anna@example.com",
  "to": "",
  "subject": "",
  "attachment": "",
  "list_date_from": "2026-01-01T00:00:00.000+01:00",
  "list_date_before": "2026-04-01T00:00:00.000+02:00",
  "has_attachments": false,
  "page": 1
}
```

| Field | Rule |
| ----- | ---- |
| `words`, `from`, `to`, `subject`, `attachment` | Strings, at most 1,000 characters each. Empty means no filter. `to` matches To, Cc, and Bcc |
| `list_date_from` | RFC 3339 instant or null. Messages whose list date is at or after it |
| `list_date_before` | RFC 3339 instant or null. Messages whose list date is before it |
| `has_attachments` | `true` keeps only messages with attachments. `false` is no filter |
| `page` | 1 to 2,000 |

The interface turns a day range into instants: midnight at the start of the first day, and midnight at the start of the day after the last, both in the browser's time zone (FR-017). Request bodies are at most 64 KiB. A body that isn't JSON, has unknown fields, or breaks a rule answers 400 `{ "error": "invalid_fields", "fields": ["page"] }`. A page above 2,000 answers 400 `page_too_deep`.

| Status | Body | When |
| ------ | ---- | ---- |
| 200 | Results, below | The search finished |
| 422 | `search_took_too_long` | The search process stopped the search at 10 s (FR-021) |
| 503 | `{ "error": "search_unavailable", "cause": "search_process_stopped", "next_start_at": "…" }` | The search process isn't running. `cause` is the category of its last failure, `search_process_stopped` or `search_process_did_not_start`, so the screen can say why (FR-021). `next_start_at` is null while a start is under way |
| 503 | `search_did_not_answer` | No answer within 12 s. Records `search_process_did_not_answer` |

```json
{
  "items": [],
  "page": 1,
  "page_size": 50,
  "total": 132,
  "not_included": 17,
  "full_list": false,
  "may_be_incomplete": false
}
```

| Field | Rule |
| ----- | ---- |
| `items` | Rows in list order |
| `total` | Every match, not only the first 100,000 |
| `not_included` | Messages Not processed yet plus processed messages the queried index hasn't saved. 0 when `full_list` is true (FR-019) |
| `full_list` | True when the words had no letters or digits and no filter was given. `items` and `total` are then the full list's page, as from `GET /api/messages` (FR-018) |
| `may_be_incomplete` | True while search is Needs rebuild (FR-040) |

Search answers from the building index while no active one exists (FR-035). A message that Couldn't be read never matches (FR-019).

## `GET /api/messages/{id}/content`

For `processed` and `processed_with_problems`, an interactive worker parses the original. The other states answer from the catalog.

```json
{
  "id": 17,
  "content_hash": "ea9449bf…",
  "size": 18234,
  "processing": "processed_with_problems",
  "problems": [
    { "position": "2", "declared_type": "text/plain", "kind": "unknown_charset" },
    { "position": null, "declared_type": null, "kind": "too_many_parts" }
  ],
  "list_date": "2026-11-02T08:14:55.000Z",
  "date_reason": null,
  "date_header": "Mon, 2 Nov 2026 09:14:55 +0100",
  "subject": "Quartalsbericht Q3",
  "from": [{ "name": "Anna Schmidt", "address": "anna@example.com" }],
  "to": [{ "name": null, "address": "team@example.com" }],
  "cc": [],
  "bcc": [],
  "encrypted": false,
  "html": { "truncated": false, "remote_images": true, "embedded_images_left_out": false },
  "text": { "truncated": false },
  "attachments": [
    { "position": "3", "name": "Vertrag_2026.pdf", "declared_type": "application/pdf", "size": 48211, "damaged": false, "kind": "file" },
    { "position": "1.2", "name": null, "declared_type": "image/png", "size": 9120, "damaged": false, "kind": "embedded_image" }
  ]
}
```

| Field | Rule |
| ----- | ---- |
| `problems` | From the catalog, ordered by position. `position` null means the whole message |
| `date_reason`, `date_header` | From the catalog. The view says why the list date is the archive time and shows `date_header` as plain text (FR-023) |
| `subject`, `from`, `to`, `cc`, `bcc` | Full, decoded values (FR-007, FR-011). An address that can't be parsed gives `{ "name": "<decoded text>", "address": null }` |
| `encrypted` | True when the top part is `multipart/encrypted`, or `application/pkcs7-mime` with `smime-type=enveloped-data` |
| `html`, `text` | Null when the message lacks that body. `truncated` means the body passed 2,000,000 bytes (FR-029). `remote_images` means the HTML references `http:` or `https:` images. `embedded_images_left_out` means at least one embedded image wasn't placed because of the 10,000,000-byte limit (FR-027) |
| `attachments` | In part order. `size` is decoded bytes |

For `not_processed` and `unreadable`, every field from `date_header` on is null and `attachments` is `[]`.

| Status | Body | When |
| ------ | ---- | ---- |
| 200 | Above | |
| 404 | `not_found` | Unknown ID |
| 404 | `stored_original_missing` | With the Slice 1 failure event |
| 500 | `stored_original_damaged` | With the Slice 1 failure event |
| 503 | `busy` | No interactive worker was free within 30 s |
| 500 | `could_not_read` | The worker passed 60 s or died. Logs `processing_worker_stopped` |

The handler raises its write deadline to 100 s, covering the 30 s wait and the 60 s limit.

## `GET /api/messages/{id}/text`

The plain text body, at most 2,000,000 bytes, as `text/plain; charset=utf-8`, with `Content-Security-Policy: sandbox; default-src 'none'; frame-ancestors 'none'`. The interface fetches it and shows it as text, never as HTML (FR-028). 404 `no_body` when the message has no text body. Other errors as for `content`.

## `GET /api/messages/{id}/html`

`GET /api/messages/{id}/html?images=1` loads images from the internet. Any other query is 400 `bad_query`.

The answer is the [rewritten HTML](../research.md#html-rewrite) as `text/html; charset=utf-8`. These headers replace the global `Content-Security-Policy` and `Referrer-Policy`, so the response carries exactly one of each:

| Header | Without `images` | With `images=1` |
| ------ | ---------------- | --------------- |
| `Content-Security-Policy` | `sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'self'; base-uri 'none'; form-action 'none'` | The same with `img-src data: https:` and `upgrade-insecure-requests` |
| `Referrer-Policy` | `no-referrer` | `no-referrer` |
| `X-DNS-Prefetch-Control` | `off` | `off` |

The interface shows it in `<iframe sandbox referrerpolicy="no-referrer">` and reloads the frame with `images=1` when the administrator chooses to load images. Leaving the view drops the choice (FR-026).

| Case | Status | Body |
| ---- | ------ | ---- |
| No session, `Sec-Fetch-Dest: document` | 303 | To sign-in, as for the original download |
| No session, anything else, such as the frame | 401 | Empty, with the preview's headers |
| No HTML body | 404 | `no_body` |

Other errors as for `content`, as JSON with the preview's headers.

## `GET /api/messages/{id}/attachments/{position}`

`position` is a [part number](../data-model.md#part-numbers) from `content`. The server hashes the original, an interactive worker decodes the part, and the server streams it.

Success headers: `Content-Type: application/octet-stream`, `Content-Disposition` built by `mime.FormatMediaType("attachment", …)` with the [cleaned name](../data-model.md#attachments), `Content-Security-Policy: sandbox; default-src 'none'; frame-ancestors 'none'`, and `Content-Length`. A damaged part sends the bytes that decoded (FR-032).

The interface fetches the address and saves the body only after every byte has arrived, with the same reader and the same failure table as the original download. No session gives 303 or 401 as for the original download. Errors are those of `content`. After the headers, the write deadline rolls every 60 s, and a worker failure aborts the connection, so the client saves nothing.

## `POST /api/search/rebuild`

No body. Free space is checked, and the rebuild row and new generation are written in one transaction.

| Status | Body | When |
| ------ | ---- | ---- |
| 202 | `{ "rebuild": { "id": 4, "started_at": "…", "started_by": "admin@example.test" } }` | Started |
| 409 | `rebuild_running` | A rebuild already runs (FR-036) |
| 409 | `{ "error": "not_enough_space", "needed_bytes": 8589934592, "free_bytes": 6442450944 }` | Free space isn't above 2 GiB plus the active index (FR-037) |
| 409 | `no_index` | No active index exists, because the first build is still running |
| 503 | `search_unavailable` | The search process isn't running |

## Changed: `GET /api/status`

Adds `processing` and `search`, and new fields on failure events:

```json
{
  "processing": { "not_processed": 1204, "processed_with_problems": 3, "unreadable": 1 },
  "search": {
    "state": "available",
    "next_start_at": null,
    "needs_rebuild": null,
    "restarts": 2,
    "indexing": {
      "state": "rebuilding",
      "waiting": 12,
      "done": 1200000,
      "to_do": 5000000,
      "free_bytes": 41234567890,
      "pause_below_bytes": 2147483648,
      "resume_at_bytes": 3221225472
    },
    "rebuild": { "id": 4, "started_at": "…", "started_by": "admin@example.test" }
  },
  "failures": [
    {
      "at": "2026-11-15T09:00:01.000Z",
      "category": "search_process_stopped",
      "repeats": 3,
      "last_at": "2026-11-15T09:00:09.000Z",
      "detail": { "signal": 9 }
    }
  ]
}
```

| Field | Rule |
| ----- | ---- |
| `next_start_at` | Set while Unavailable and waiting to start again |
| `needs_rebuild` | Null, or `{ "cause": "search_process_stopped" \| "index_maintenance_failed" \| "index_damaged", "since": "…" }` |
| `restarts` | Starts of the search process after the first, since the server started (FR-045) |
| `indexing.waiting` | Processed messages the active index, or the building one when none is active, hasn't saved |
| `indexing.done`, `to_do` | Set while a generation builds, for the first build or a rebuild, also while indexing is Paused or Stopped: the building generation's cursor and the highest `processed_seq`. Null otherwise |
| `indexing.free_bytes` | The latest free space reading where the index lives, taken while indexing runs. Null before the first reading |
| `rebuild` | The running rebuild, or null |
| `repeats`, `last_at` | On every event. `repeats` is 1 and `last_at` equals `at` for events that never repeat |
| `detail` | Only on the new categories. Keys per category, from the [data model](../data-model.md#failure-categories): `exit_status`, `signal`, `cause`, `operation`, `generation_id`, `rebuild_id`, `first_message_id`, `last_message_id`, `free_bytes` |

No field carries a search word, subject, address, name, body text, or attachment name (FR-048). `TestNoMarkersInLogOrStatus` checks this.

## Changed: `GET /api/session`

Same shape. `problem_count` follows the [data model's formula](../data-model.md#problem-count) (FR-047).

# HTTP API

This interface serves MailArkiv's own web interface and isn't a published contract in Slice 1. It is written down because direct requests are the path an attacker would use, so tests send them, and because the Go server and the frontend must change together.

## Every response

| Header | Value | Why |
| ------ | ----- | --- |
| `MailArkiv-Response` | `1` | Tells the client the answer came from MailArkiv and not a proxy (FR-033) |
| `Content-Security-Policy` | `frame-ancestors 'none'; base-uri 'none'; form-action 'self'` | FR-007. Pages add a stricter `<meta>` policy |
| `X-Content-Type-Options` | `nosniff` | |
| `Referrer-Policy` | `same-origin` | Keeps URLs on this site. The download route reads `Referer` |

Every `/api/` response also has `Cache-Control: no-store`.

Errors are JSON: `{ "error": "<code>" }`. Times are RFC 3339 in UTC with milliseconds.

Any route can answer 500 `internal_error` when the server fails outside the import and download tables below. A failed session lookup answers 500 and doesn't run the route. It never answers 401, because the session may still be valid. A failed sign-in answers 500 too and counts against no address. The server logs the route pattern and the error, with no request values (FR-029). These aren't failure events, so the status screen and problem count leave them out (FR-038).

## Access

Each route declares a rule when it is registered: `Public` or `SignedIn`. The zero value, `Undeclared`, answers 403 `no_access_rule` for everyone (User Story 2 scenario 5). Paths under `/api/` with no route answer 401 without a session and 404 with one.

All non-GET requests pass through `http.CrossOriginProtection` before the session check, so a forged request gets 403 `cross_origin` with or without a session. Without `Sec-Fetch-Site`, a foreign `Origin` is refused the same way. The session check for a route runs before its handler reads the body.

| Method | Path | Rule | Purpose |
| ------ | ---- | ---- | ------- |
| POST | `/api/session` | Public | Sign in |
| GET | `/api/session` | SignedIn | Check the session and read the problem count |
| DELETE | `/api/session` | SignedIn | Sign out |
| POST | `/api/imports` | SignedIn | Import one file |
| GET | `/api/messages` | SignedIn | List messages |
| GET | `/api/messages/{id}` | SignedIn | Read one message's history |
| GET | `/api/messages/{id}/original` | SignedIn | Download the original |
| GET | `/api/status` | SignedIn | Read the status screen's data |
| GET, HEAD | anything else | Public | Static application files, or `app.html` for an unknown path. A missing `/_app/` file is 404 |

Without a valid session, a SignedIn route answers 401 `no_session`. The one exception is in [Download](#get-apimessagesidoriginal).

## Sign-in

### `POST /api/session`

Request, at most 16 KiB:

```json
{ "email": "admin@example.test", "password": "correct horse battery" }
```

| Status | Body | Meaning |
| ------ | ---- | ------- |
| 204 | none | Signed in. Sets the session cookie |
| 401 | `invalid_credentials` | Wrong password, unknown address, or malformed address. All three are identical |
| 429 | `sign_in_blocked` | 10 failures for this address within 15 minutes, whether or not the account exists |

Cookie: `__Host-mailarkiv_session=<token>; Path=/; Max-Age=43200; HttpOnly; Secure; SameSite=Strict`.

### `GET /api/session`

The signed-in layout reads this at each page load (FR-039), and the import screen reads it just before an upload starts.

```json
{ "problem_count": 2 }
```

Before an upload, only a marked 401 changes anything: it opens the sign-in page. After any other result, the upload goes ahead and its own answer decides the outcome.

The sign-in page shows the session-ended message, "Your session ended. Sign in again to continue.", only if this tab has already loaded a signed-in page. A first visit with no session opens sign-in without it. The client adds `ended=1` only for such a tab, and `next` only when the page isn't `/`, so a first visit to `/` opens exactly `/sign-in`. The server's download redirect always sends `ended=1`, so the sign-in page checks the tab's flag as well.

### `DELETE /api/session`

204, with the row deleted and the cookie cleared: `__Host-mailarkiv_session=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Strict`.

## Import

### `POST /api/imports`

The request body is the file's exact bytes. The body's `Content-Type` is ignored.

| Header | Value |
| ------ | ----- |
| `MailArkiv-File-Name` | Optional. Percent-encoded UTF-8 file name, decoded like a path segment, so `+` stays a plus. Invalid UTF-8 after decoding is replaced with U+FFFD. An empty header stores an empty name, which isn't the same as no header |

Responses:

| Status | `outcome` | `reason` | Spec outcome |
| ------ | --------- | -------- | ------------ |
| 201 | `archived` | | Archived |
| 200 | `already_archived` | | Already archived |
| 200 | `restored` | | Restored |
| 422 | `refused` | `empty` or `no_mail_header` | Refused. The server reads the rest of the body first |
| 413 | `refused` | `too_large` | Refused. Sent as soon as the limit is passed |
| 507 | `failed` | `storage_full` | Failed |
| 500 | `failed` | `storage_write_failed` or `catalog_write_failed` | Failed |
| 400 | | `bad_file_name` in `error` | The header isn't valid percent-encoding. Only a direct request can cause this |

The first three also return the message:

```json
{
  "outcome": "archived",
  "message": {
    "id": 17,
    "content_hash": "ea9449bf…",
    "size": 31,
    "file_name": "invoice.eml",
    "imported_at": "2026-11-06T15:50:52.123Z",
    "import_count": 1
  }
}
```

`file_name` is `null` when none was supplied.

### File check

A body passes (FR-010) when it starts with a header line, optionally after one line starting with `From `. A header line is one or more bytes from 0x21 to 0x7E, excluding `:`, then optional spaces or tabs, then `:`. The check needs only the first 4 KiB, which holds two lines of RFC 5322's 998-byte maximum. Nothing else about the bytes is inspected (FR-011).

### Limits

The limit is 150,000,000 bytes. A request with exactly that many bytes succeeds. A larger `Content-Length` gets 413 before any read. A chunked body gets 413 when it passes the limit, and the temp file is removed.

A stall of 60 seconds with no bytes ends the request. A connection that drops ends it too, with the temp file removed and no failure event.

### How the client classifies a result

The table is the whole of FR-032 and FR-033, tested as one pure function.

| Response | Result on screen |
| -------- | ---------------- |
| Marked, with an `outcome` listed above | That outcome |
| Marked 401 | Open sign-in with the session-ended message |
| Marked 403 | Failed |
| Marked, anything else | Unconfirmed |
| Unmarked 413 | Failed, naming the proxy's upload limit |
| Unmarked, anything else, including 200, 502, and 504 | Unconfirmed |
| Network error, abort, or timeout | Unconfirmed |

The client aborts and reports Unconfirmed after 120 seconds with no upload progress, or 120 seconds after the upload finishes with no answer. Other API calls time out after 30 seconds.

Any other call, apart from the session check before an upload, shows the failed-load state (FR-045) when it ends in anything but success or a marked 401. That includes a 500, a 400 or 404 from a hand-edited URL, an unmarked response, a network error, a timeout, and a body that doesn't parse. Sign-in's 401 and 429 are its own results. Only a marked 401 from a signed-in route opens sign-in.

## Browse

### `GET /api/messages?page=N`

`page` starts at 1 and defaults to 1. Anything else, including `01`, `+1`, and a repeated `page`, is 400 `bad_page`. A page past the end returns no items with the real total, and the client moves to the last page.

```json
{
  "items": [
    {
      "id": 17,
      "content_hash": "ea9449bf…",
      "size": 31,
      "file_name": "invoice.eml",
      "imported_at": "2026-11-06T15:50:52.123Z",
      "import_count": 3
    }
  ],
  "page": 1,
  "page_size": 50,
  "total": 1
}
```

Order: most recent import first.

### `GET /api/messages/{id}`

404 `not_found` for an unknown ID.

```json
{
  "id": 17,
  "content_hash": "ea9449bf…",
  "size": 31,
  "observations": [
    {
      "source_type": "manual_import",
      "observed_at": "2026-11-06T15:50:52.123Z",
      "account_email": "admin@example.test",
      "file_name": "invoice.eml"
    }
  ]
}
```

Observations come newest first, with no cap.

### `GET /api/messages/{id}/original`

The interface links here with a plain `<a>` that has no `download` attribute. The server hashes the stored file before it sends any header.

| Case | Status | Body |
| ---- | ------ | ---- |
| Stored file matches | 200 | The exact bytes |
| Unknown ID | 404 | `download-failed/missing.html`, with no failure event |
| Stored file missing | 404 | `download-failed/missing.html`, with a `stored_original_missing` event |
| Stored file damaged or unreadable | 500 | `download-failed/damaged.html`, with a `stored_original_damaged` event |
| Catalog can't be read | 500 | `internal_error` as JSON, with no failure event |
| No session, `Sec-Fetch-Mode: navigate` | 303 | To `/sign-in?ended=1&next=<path from a same-origin Referer, or />` |
| No session, any other request | 401 | `no_session` |

Until T094 adds these pages, an unknown ID answers 404 `not_found` as JSON.

A same-origin `Referer` is one whose host equals the request's `Host`. The scheme isn't compared, because TLS may end at a proxy. Only its path and query are used, and the client checks `next` again before following it.

Success headers: `Content-Type: application/octet-stream`, `Content-Disposition: attachment; filename="<content_hash>.eml"`, `Content-Length`. A read that fails after the headers aborts the connection, so the browser shows a failed download, and it records a `stored_original_damaged` event.

## Status

### `GET /api/status`

```json
{
  "started_at": "2026-11-06T15:00:00.000Z",
  "recovery": {
    "finished_at": "2026-11-06T15:00:00.410Z",
    "temp_files_removed": 1,
    "temp_files_not_removed": 0,
    "unreferenced_found": 1
  },
  "unreferenced": [{ "content_hash": "ab12…", "size": 2048 }],
  "failures": [
    {
      "at": "2026-11-06T15:20:00.000Z",
      "category": "stored_original_damaged",
      "message_id": 17,
      "content_hash": "ea9449bf…",
      "restored": false
    }
  ]
}
```

`message_id` and `content_hash` are absent where they don't apply. Categories are listed in [the data model](../data-model.md#in-memory-owner-opstatus). `recovery` holds what startup found. `unreferenced` is the current list, which shrinks as files are imported again. No endpoint here changes the archive (FR-027).

## Server timeouts

`ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `WriteTimeout` 30 s, `IdleTimeout` 120 s, `MaxHeaderBytes` 64 KiB. The import and download handlers replace the read or write deadline with a rolling 60 seconds through `http.ResponseController`, so a slow transfer that keeps moving can finish and a stalled one can't hold a connection.

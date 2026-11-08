# Feature Specification: Import, Browse, and Retrieve One Email

**Feature Branch**: `001-import-browse-retrieve` (not created yet; this repository has no branch hook)

**Created**: 2026-11-06

**Status**: Draft

**Input**: User description: "Lets get started with this", meaning [issue #1](https://github.com/maltehedderich/mailarkiv/issues/1), "Slice 1: Import, browse, and retrieve one email". The issue stays the record of scope and completion requirements.

## Scope

An administrator imports an `.eml` file, sees it listed, and downloads a byte-identical copy. This is MVP slice 1 of 7, and later slices reuse its capture path.

**In scope**

- Administrator accounts identified by email address and created from the server host, with sign-in and sign-out.
- Import of one `.eml` file at a time through the web interface.
- Durable storage of the exact original bytes, with one catalog entry for each distinct set of bytes.
- A record of every accepted import: when, by which account, and under what file name.
- A paged message list, an import history for each message, and download of the original.
- Startup recovery from interrupted writes.
- A read-only status screen, a problem count on every signed-in page, and server log entries for capture and recovery failures.
- Operator documentation.

**Excluded**

| Excluded from this slice                                                           | Where it belongs       |
| ---------------------------------------------------------------------------------- | ---------------------- |
| SMTP journal capture                                                               | Slice 2 (#2)           |
| Message preview, MIME processing, parsed fields such as subject and sender, search | Slice 3 (#3)           |
| IMAP collection                                                                    | Slice 4 (#4)           |
| Selected-message export                                                            | Slice 5 (#5)           |
| Permanent deletion and its audit entries                                           | Slice 6 (#13)          |
| Health dashboard, integrity scan, backup and restore, release packages             | Slice 7 (#8)           |
| Splitting mbox files, PST import                                                   | Future (#12)           |
| Roles and single sign-on                                                           | Future (#7, #10)       |
| Account management in the web interface, account removal                           | No issue owns this yet |
| Changing an account's email address                                                | No issue owns this yet |
| Email sent by MailArkiv, such as address checks or password reset                  | No issue owns this yet |
| Importing several `.eml` files at once                                             | No issue owns this yet |
| Audit entries for sign-in, listing, or download                                    | Not planned in the MVP |

## User Scenarios & Testing *(mandatory)*

All five stories are required before this slice is done. Priorities set the build order.

An **administrator** is anyone with a local account. An **operator** runs the server and can use its host. One person is often both.

### User Story 1 - Import, list, and download one email (Priority: P1)

An administrator signs in, imports one `.eml` file, finds it in the message list, and downloads a file identical to the one imported.

**Why this priority**: This is what the slice promises, and it proves a path through every layer that later slices reuse.

**Independent Test**: On a fresh install, create an account from the host, sign in, import a synthetic `.eml`, download it, and compare the SHA-256 of the two files.

**Acceptance Scenarios**:

1. **Given** a fresh install and an account created from the host, **When** the administrator signs in with the correct password, **Then** an empty message list opens with a way to import a file.
2. **Given** a signed-in administrator, **When** they import a valid `.eml`, **Then** they see a confirmation and the message appears first in the list with its file name, import time, size, and content hash.
3. **Given** a listed message, **When** the administrator downloads it, **Then** the browser saves an `.eml` file with exactly the imported bytes and does not display its content.
4. **Given** a file with broken MIME, non-UTF-8 text, no `Message-ID`, mixed line endings, or no final line break, **When** it is imported and downloaded, **Then** the import succeeds and the download matches byte for byte.
5. **Given** a wrong password or an unknown email address, **When** someone tries to sign in, **Then** sign-in fails with the same message in both cases.
6. **Given** a signed-in administrator, **When** they sign out, **Then** the sign-in page opens and the ended session no longer works.
7. **Given** two accounts, **When** one imports a message, **Then** the other can see and download it.
8. **Given** a chosen file, **When** it has not been imported yet, **Then** the screen shows its name and size and says imported mail can't be removed in this version, and nothing is stored until the administrator presses Import.
9. **Given** an import in progress, **When** the upload reaches 100%, **Then** the screen shows that the file is being stored until the result arrives, and a second submission is not possible.
10. **Given** an import result, **When** the administrator stays on the page, **Then** the result stays on screen until the next import starts.
11. **Given** an import in progress, **When** the administrator tries to leave the page, **Then** they are warned first.
12. **Given** a listed message, **When** the administrator reads its import time, **Then** it is an absolute time in their time zone with the UTC offset shown.
13. **Given** an account created as `Admin@example.com`, **When** the administrator signs in as `admin@example.com` with the correct password, **Then** sign-in succeeds.

---

### User Story 2 - Keep mail closed to anyone not signed in (Priority: P1)

Only a signed-in administrator can import, list, or download mail, including through direct requests that bypass the web interface.

**Why this priority**: The archive holds other people's mail, and exposure can't be undone.

**Independent Test**: Send direct requests to every operation with no session, an expired session, a signed-out session, and a forged cross-site request. Check that each is denied and that the archive has not changed.

**Acceptance Scenarios**:

1. **Given** no session, **When** a direct request tries to import, list, read history, download, or read status, **Then** it is denied, returns no mail data, and stores nothing.
2. **Given** a session that expired, or ended through sign-out or a password change, **When** it is used, **Then** the request is denied the same way.
3. **Given** a signed-in administrator's browser visiting another website, **When** that site submits an import to MailArkiv, **Then** the import is rejected and nothing is stored.
4. **Given** 10 failed sign-ins for one email address within 15 minutes, whether or not the account exists, **When** another attempt arrives, even with the correct password, **Then** it is refused for the next 15 minutes with the same message in both cases.
5. **Given** a server operation with no declared access rule, **When** it is requested, **Then** it is denied.
6. **Given** a fresh install with no accounts, **When** anyone opens the web interface, **Then** they see only the sign-in page and cannot create an account there.
7. **Given** a blocked account, **When** the operator sets a new password, **Then** the account can sign in at once.
8. **Given** another website that embeds a MailArkiv page in a frame, **When** a browser loads it, **Then** the browser refuses to show the page.
9. **Given** a session that ends while a page is open, **When** the administrator takes an action, **Then** the sign-in page opens and says the session ended, and signing in returns them to the same page.

---

### User Story 3 - Never lose a confirmed import or confirm a failed one (Priority: P1)

Once the administrator sees a confirmation, the message survives a crash or power loss. If capture fails, the administrator never sees success. If the outcome is unknown, they are told so, and importing again is safe.

**Why this priority**: This is the guarantee an archive exists to give, and the issue names it as the riskiest assumption.

**Independent Test**: An automated check stops the server at each capture step, restarts it, and compares what the archive holds with what the client was told.

**Acceptance Scenarios**:

1. **Given** a confirmed import, **When** the server loses power immediately afterward, **Then** after restart the message is listed and downloads byte for byte.
2. **Given** capture stops before the original is stored, **When** the server restarts, **Then** no temporary file remains and nothing is listed.
3. **Given** capture stops after the original is stored but before its catalog entry is written, **When** the server restarts, **Then** the message is not listed, no confirmation was sent, the original is kept, and the status screen lists it as unreferenced.
4. **Given** that unreferenced original, **When** the administrator imports the same file again, **Then** the import succeeds without a second stored copy and the original leaves the unreferenced list.
5. **Given** capture stops after the catalog entry is written but before the confirmation arrives, **When** the server restarts, **Then** the administrator has seen Unconfirmed instead of Failed, the message is listed, and importing it again shows Already archived and adds a second history entry.
6. **Given** storage fails during an import, such as a full disk or a denied write, **When** the import ends, **Then** the administrator sees Failed, nothing new is listed, and no partial file remains.
7. **Given** the connection drops during an upload, **When** the server notices, **Then** the administrator sees Unconfirmed, nothing is listed, and the partial upload is removed.
8. **Given** a reverse proxy whose upload limit is below the file's size, **When** the administrator imports the file, **Then** they see Failed with the proxy's limit named as the cause, and nothing is listed.
9. **Given** a reverse proxy that answers an import with a timeout or bad-gateway error, **When** the response arrives, **Then** the administrator sees Unconfirmed.

---

### User Story 4 - Keep every import on record without another copy (Priority: P2)

Importing the same bytes again adds to the message's history and stores no second copy.

**Why this priority**: Source history can't be rebuilt once discarded, and duplicates cost storage. Slices 2 and 4 rely on the same rule.

**Independent Test**: Import one file three times under two names. Check for one list entry, three history entries, and one stored copy.

**Acceptance Scenarios**:

1. **Given** an archived message, **When** the same bytes are imported again, **Then** the confirmation says the message was already archived and this import was recorded, and the list still has one entry with its count raised by one.
2. **Given** the same bytes under a different file name, **When** the administrator opens the message's history, **Then** it shows the full content hash and each import's file name, time, and account email address.
3. **Given** two files that differ by one byte but share all headers, including `Message-ID`, **When** both are imported, **Then** the list has two entries.
4. **Given** two simultaneous imports of the same new file, **When** both finish, **Then** both are confirmed, with one list entry, one stored copy, and two history entries.
5. **Given** a stored original that is missing or damaged, **When** the same bytes are imported again, **Then** the stored original is restored from the incoming bytes, the administrator sees Restored, and the status screen and log report the event.

---

### User Story 5 - See capture and recovery problems without seeing mail (Priority: P2)

Administrators and operators can tell when capture, recovery, or retrieval went wrong, and what they see contains no message content.

**Why this priority**: A failure nobody sees can lose mail. Status and logs must not become a second copy of it.

**Independent Test**: Sign in to an account whose email address is a unique marker string, and import fixtures with unique marker strings in the subject, addresses, body, attachment name, and file name. Force each failure category, then check that each failure appears on the status screen and in the log and that no marker does.

**Acceptance Scenarios**:

1. **Given** a clean start, **When** an administrator opens the status screen, **Then** it shows the start time and that recovery found nothing, and it does not describe the archive as healthy.
2. **Given** a restart after a crash, **When** an administrator opens the status screen, **Then** it shows how many temporary files were removed and each unreferenced original by content hash and size with its next step, and no stored original has been deleted.
3. **Given** a failed import, **When** an administrator opens the status screen or the operator reads the log, **Then** each shows the time and the failure category, with no message content.
4. **Given** a stored original that is missing or does not match its content hash, **When** an administrator downloads it, **Then** the administrator sees the reason in a notification and stays on the list, nothing is saved as if it were the message, and the status screen and log report the failure with the message's internal ID.
5. **Given** a temporary file that recovery cannot remove, **When** startup finishes, **Then** the server runs and the status screen and log report the failure.
6. **Given** a catalog or storage location that cannot be used, **When** the server starts, **Then** it refuses to start and the log says which one failed.
7. **Given** a failure before a restart, **When** an administrator opens the status screen after it, **Then** the failure is gone from the screen, and the screen says it covers only the time since the last start and that older failures are in the server log.
8. **Given** a failure since the last start or an unreferenced original, **When** an administrator opens any signed-in page, **Then** the page shows the number of problems, with no message content.

### Edge Cases

- **Empty file**: The browser refuses it before upload, and the server rejects one sent by direct request. Nothing is stored.
- **Not mail, such as a PDF or ZIP**: Rejected because it does not start with a mail header line. The administrator sees why. This matters because nothing can be deleted until Slice 6.
- **File over the 150,000,000 byte limit**: The browser refuses it before upload. If a direct request sends one, the server rejects it as soon as the limit is passed and removes the partial upload.
- **Several files chosen or dropped at once**: Refused with a message. The interface never picks one silently.
- **An mbox file holding several messages**: It starts with a `From ` line, so it passes the check and is stored as one message with its bytes intact. Splitting it is excluded.
- **Hostile file name, with HTML, control or bidirectional characters, or `../`**: Shown as plain text with those characters neutralized, and never used as a storage location or download name.
- **Very long file name**: Shortened in the middle so its ending stays visible. The import history shows it in full.
- **No file name, possible only through a direct request**: Shown as "(no file name)".
- **Catalog write fails while the server keeps running**: The administrator sees Failed and the original stays stored. Recovery lists it as unreferenced at the next start.
- **Graceful shutdown during an import**: Treated as an interruption unless the confirmation was already sent, so the administrator sees Unconfirmed.
- **Crash during recovery**: The next start runs recovery again with the same result.
- **Password under 12 characters**: Refused when set.
- **Malformed email address**: Refused when the account is created. At sign-in it fails like any unknown address.
- **Email address already in use, even with different capital letters**: Refused when the account is created. The existing account does not change.

## Requirements *(mandatory)*

### Functional Requirements

**Accounts and access**

- **FR-001**: An operator on the server host MUST be able to create an administrator account for an email address and set a new password for an existing account. The web interface MUST NOT offer a way to create an account, and the sign-in page MUST look the same whether or not any account exists.
- **FR-002**: Passwords MUST contain valid UTF-8 text, have at least 12 characters, fit within 4,096 bytes, and be stored only in a form that cannot be reversed. Unsupported password replacements MUST leave the old password and sessions unchanged.
- **FR-003**: Administrators MUST be able to sign in with an email address and password, and to sign out. A failed sign-in MUST NOT reveal whether the account exists, so a wrong password, an unknown address, and a malformed address MUST all show "That email address and password don't match."
- **FR-004**: After 10 failed sign-ins for one submitted email address within 15 minutes, the system MUST refuse sign-in for that address for the next 15 minutes. The limit and its message MUST be the same whether or not the account exists. Setting a new password MUST lift the block. Sign-in MUST admit at most two attempts at once, with no waiting queue. Excess attempts MUST return an unavailable response immediately and MUST NOT count as address failures; admitted attempts still enforce the address block.
- **FR-005**: A session MUST end at sign-out, 12 hours after sign-in, or when the account's password is set, whichever comes first.
- **FR-006**: The server MUST check access on every operation and deny by default. Only sign-in and the static application files, which contain no mail data, may be reached without a session.
- **FR-007**: The system MUST reject state-changing requests forged from another website and MUST NOT let other websites embed its pages.
- **FR-008**: Every account MUST see the whole archive. This slice has no roles.
- **FR-044**: Every account MUST have one email address, which identifies it. The address MUST contain valid UTF-8 text, have one `@` with text on both sides and no spaces, and fit within 1,024 bytes both before and after lowercasing. The system MUST store the address in lowercase, whatever capital letters were typed, and MUST ignore capital letters when comparing addresses, including for the sign-in limit. It MUST refuse a second account for the same address. The system MUST NOT send email to the address or check that it receives mail.

**Import**

- **FR-009**: Administrators MUST be able to import one file at a time, up to 150 MB (150,000,000 bytes), through the web interface.
- **FR-010**: The system MUST reject a file that is empty, over the limit, or does not start with a mail header line, optionally after one mbox-style `From ` line. It MUST explain why and store nothing.
- **FR-011**: The system MUST NOT reject or alter a file for any other defect, including broken MIME, unknown encodings, missing headers, or binary content.
- **FR-012**: The system MUST confirm an import only after the original bytes and the catalog entry are durable, so that a power loss after the confirmation loses neither.
- **FR-013**: A failed import, or one interrupted before its catalog entry is written, MUST NOT appear as successful, MUST NOT create a list entry, and MUST NOT leave a partial stored file.
- **FR-014**: Each accepted import MUST add a source observation recording the source type (manual import), the time, the account, and the supplied file name.
- **FR-015**: Importing bytes already in the archive MUST add a source observation, create no second stored copy, and say in the confirmation that the message was already archived.
- **FR-016**: Files that differ by any byte MUST be separate messages, whatever their headers say.
- **FR-017**: When the same bytes arrive again, the system MUST check that the stored original still matches its content hash. If it is missing or does not match, the system MUST restore it from the incoming bytes and report the event.
- **FR-018**: A new import of an unreferenced original MUST use the stored copy and remove it from the unreferenced list.

**Browse and download**

- **FR-019**: The message list MUST show the most recently imported message first, in pages, so that every message can be reached. Each row MUST show the file name and time of the latest import, the size, the content hash, which may be shortened, and the number of imports when it is above 1.
- **FR-020**: Administrators MUST be able to open a message's import history, showing its full content hash and every source observation.
- **FR-021**: Supplied file names MUST be treated as untrusted. They MUST be shown as plain text with control and bidirectional characters neutralized, and MUST NOT be used to choose a storage location or a download name. A long name MUST be shortened in the middle so its ending stays visible, with the full name in the import history. A missing name MUST show as "(no file name)".
- **FR-022**: Download MUST return the exact stored bytes as a saved file, never displayed in the browser, named from the content hash with an `.eml` extension. Each tab MUST allow at most one active buffered transfer and one waiting request. Further distinct requests MUST be refused visibly. Active and waiting downloads MUST offer Cancel; cancellation saves nothing and releases the occupied place.
- **FR-023**: Before a download starts, the system MUST check the stored original against its content hash. If it is missing or does not match, the download MUST fail: the administrator sees "This message wasn't downloaded" and the reason in a notification on the page they're on, and nothing is saved or presented as the original.

**Recovery and visibility**

- **FR-024**: At startup, before accepting imports, the system MUST remove leftover temporary files and identify stored originals with no catalog entry. Recovery MUST NOT delete a stored original.
- **FR-025**: Recovery MUST give the same result if it is interrupted and run again.
- **FR-026**: The server MUST refuse to start, with a log entry naming the cause, if it cannot use its catalog or storage location.
- **FR-027**: A status screen for signed-in administrators MUST show the latest start time, the recovery result, the temporary files removed, the unreferenced originals by content hash and size, and capture, recovery, and download failures since that start. It MUST say that it covers only the time since that start and that older failures are in the server log. It MUST NOT describe the archive as healthy and MUST NOT offer any action that changes the archive.
- **FR-028**: The recovery result and every capture, recovery, and download failure MUST also go to the server log, with the time and the failure category.
- **FR-029**: Logs and the status screen MUST NOT contain message subjects, message addresses, bodies, attachment names, supplied file names, passwords, session credentials, or account email addresses, including any submitted at sign-in.

**Documentation**

- **FR-030**: Operator documentation MUST state how to run the server, where its data lives, how to create an account and set a password, the size limit, and what a confirmation guarantees.
- **FR-031**: Operator documentation MUST also state what this slice does not promise: no tamper-proof storage, no encryption by the application, no backup until Slice 7, no deletion until Slice 6, no splitting of mbox files, and no email from the application, so no password reset by email. It MUST say that the operator provides HTTPS and a local filesystem that honors flush requests, and MUST give the reverse proxy setting that allows uploads up to the limit.

**Import results and progress**

- **FR-032**: Every import in the web interface MUST end in exactly one of the outcomes below. The result MUST stay on screen until the next import starts or the administrator dismisses it.

| Outcome          | When                                                                                            | What the administrator is told                                                     |
| ---------------- | ----------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| Archived         | The message was not in the list before and is now durable                                       | The file name, the size, and a way to open the message's history                   |
| Already archived | The same bytes were archived before                                                             | This import was added to the history, and no second copy was stored                |
| Restored         | The same bytes arrived and the stored original was missing or damaged                           | The stored copy was restored from this file                                        |
| Refused          | The file is empty, over the limit, or has no mail header line                                   | The reason, and that nothing was stored                                            |
| Failed           | The server or a proxy reports that the import did not finish                                    | The file is not archived                                                           |
| Unconfirmed      | The browser has no answer from MailArkiv, such as after a dropped connection or a proxy timeout | The file may be archived, and importing it again is safe and stores no second copy |

- **FR-033**: The web interface MUST recognize responses that did not come from MailArkiv. A proxy's refusal of a file as too large (HTTP 413) MUST show as Failed and name the proxy's upload limit as the cause. Any other such response, including HTTP 502 and 504, MUST show as Unconfirmed.
- **FR-034**: Import MUST take two steps: choose a file and see its name and size, then press Import. Until Slice 6 ships, the import screen MUST say that imported mail can't be removed in this version.
- **FR-035**: During an import, the web interface MUST show upload progress, then a separate state while the file is being stored, and MUST prevent a second submission.
- **FR-036**: The web interface MUST refuse an empty or oversized file before uploading it, and MUST refuse several files chosen or dropped at once with a message. The server still enforces FR-010.
- **FR-037**: The web interface MUST warn an administrator who tries to leave the page during an import. After accepted in-app navigation, the client MUST let a started upload finish and report any import result through the shared notification store. Its completion, including an expired-session response, MUST NOT move focus, refresh, or navigate the destination page. Leaving before the session check finishes starts no upload; closing the tab may interrupt an upload.

**Failure reporting**

- **FR-038**: Every failure event while the server runs MUST use one of the categories below, and the status screen MUST show each failure's meaning and next step. A dropped upload is not a failure event, because the administrator who sent it sees Unconfirmed.

| Category                   | What happened                                                                          | Next step                                                                                                       |
| -------------------------- | -------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| Storage full               | The disk holding the archive ran out of space during an import                         | Free disk space, then import the file again                                                                     |
| Storage write failed       | The server could not write to its storage location, such as through denied permissions | Fix the storage location, then import the file again                                                            |
| Catalog write failed       | The original was stored but its catalog entry was not written                          | Import the same file again. If this repeats, check the server log                                               |
| Stored original missing    | A download or repeat import found no stored file                                       | Import the same file again, if you still have it, to restore the original                                       |
| Stored original damaged    | The stored file can't be read or does not match its content hash                       | Import the same file again, if you still have it, to restore the original                                       |
| Temporary file not removed | Recovery could not delete a leftover temporary file                                    | Check the permissions on the storage location. The file is harmless, and recovery tries again at the next start |

Unreferenced originals are not failures, but each one MUST show this next step: importing the same file again puts it back in the list, and nothing is deleted.

- **FR-039**: Every signed-in page MUST show the number of problems on the status screen, counting failures since the latest start and unreferenced originals. The count MUST be current at each page load.

**All screens**

- **FR-040**: When a session ends while a page is open, the next action MUST open the sign-in page with a message that the session ended. Signing in MUST return the administrator to the same page.
- **FR-041**: Sign-in, the message list, import, import history, and the status screen MUST meet WCAG 2.2 AA and work by keyboard alone. Import results MUST be announced to screen readers. Drag-and-drop, if offered, MUST be an addition to a file button.
- **FR-042**: The same screens MUST work at 360 px wide with no horizontal page scrolling.
- **FR-043**: Times MUST be absolute, in the viewer's time zone with the UTC offset shown, and never relative only.
- **FR-045**: When a screen's data can't be loaded, or sign-in or sign-out can't finish, because of a server or network failure, the web interface MUST say so and offer a way to try again. It MUST NOT show an empty list, a problem count of zero, or the wrong-password message in its place.

| What didn't finish                      | What the administrator is told                                                                                   |
| --------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| A screen's or the history drawer's data | "This page couldn't be loaded." and "Try again. If this repeats, check the server log.", with a Try again button |
| Sign-in                                 | "Sign-in didn't finish. Try again. If this repeats, check the server log."                                       |
| Sign-out                                | "Sign-out didn't finish, so you're still signed in. Try again."                                                  |

### Key Entities *(include if feature involves data)*

- **Administrator account**: A local account identified by one email address, with a password credential. Accounts have no roles.
- **Session**: A signed-in state tied to one account, with an end time.
- **Message**: One distinct set of original bytes, identified by its SHA-256 content hash, with its size. It has one stored original and one or more source observations. `Message-ID` is not an identifier.
- **Source observation**: One record that a message was seen. In this slice each one is a manual import with a time, an account, and a supplied file name. Slices 2 and 4 add other source types.
- **Unreferenced original**: A stored original with no catalog entry, usually left by a crash between storage and catalog writes. Recovery lists it and never deletes it.
- **Recovery result**: What the latest startup recovery did: its time, the temporary files removed, any failures, and the unreferenced originals found.
- **Failure event**: A time and a failure category, with an internal message ID or content hash where one applies. It holds no message content.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Following the documentation on a fresh install, an administrator gets from first sign-in to a downloaded original in under 2 minutes.
- **SC-002**: For every sample in the test set (valid mail, broken MIME, non-UTF-8 text, no `Message-ID`, mixed line endings, NUL bytes, no final line break, and a file of exactly 150,000,000 bytes), the SHA-256 of the download equals the SHA-256 of the input.
- **SC-003**: When capture is interrupted at each step, 100% of confirmed imports are listed and byte-identical after restart, no import is reported as successful without being stored, and recovery deletes no stored original.
- **SC-004**: None of the requests sent without a valid session (no session, expired, signed out, forged cross-site) returns mail data or changes the archive, across every operation.
- **SC-005**: Importing one file 10 times produces one list entry and 10 history entries, and storage grows by exactly one copy.
- **SC-006**: Every failure category in FR-038, when forced, appears on the status screen and in the log by the next page refresh, the problem count includes it, and no seeded marker string from the fixtures or the account's email address appears in either.
- **SC-007**: With 10,000 messages archived, every message can be reached through the list and each page opens in under 2 seconds.
- **SC-008**: The flow from sign-in to download can be completed by keyboard alone, and at 360 px wide with no horizontal page scrolling.
- **SC-009**: Each outcome in FR-032, when forced, appears exactly as specified. No test shows Failed for a message that is listed, or Archived for a message that is not.

### Issue #1 acceptance criteria

| Issue criterion                                                  | Requirements                     | Outcome        |
| ---------------------------------------------------------------- | -------------------------------- | -------------- |
| Downloaded bytes match the input                                 | FR-011, FR-022, FR-023           | SC-002         |
| Import survives restart                                          | FR-012                           | SC-003         |
| A failed write never appears as successful                       | FR-012, FR-013, FR-032, FR-033   | SC-003, SC-009 |
| Repeat imports keep provenance without unnecessary copies        | FR-014 to FR-018                 | SC-005         |
| Automated interruption between file and catalog writes           | FR-012, FR-024, FR-025           | SC-003         |
| Direct requests without authorization are denied                 | FR-005 to FR-007                 | SC-004         |
| Failures visible without exposing message content                | FR-026 to FR-029, FR-038, FR-039 | SC-006         |
| Recovery removes temp files, keeps originals, lists unreferenced | FR-024, FR-027, FR-028           | SC-003, SC-006 |

## Assumptions

Each choice below filled a gap in the issue. The numbers here and in the success criteria are starting values chosen for this spec, not measurements. None is configurable until someone needs it to be.

- **Decided design**: Issue #1, [the architecture principles](../../docs/ARCHITECTURE.md), and [ADR 0002](../../docs/adr/0002-modernc-sqlite-driver.md) fix the catalog engine, storage layout, capture order, and dependency baseline. The plan carries them, and this spec states only the behavior they must produce.
- **First account**: The operator creates it on the host. A first-run web page would let whoever reaches a new install first claim its whole archive.
- **Email address**: Accounts sign in with an email address, although this slice sends no email. Nothing checks the address, so the operator who creates the account vouches for it. A mistyped address still signs in, typo included, and can't be changed: the operator creates a new account and sets a new password on the wrong one.
- **List columns**: The list shows capture data only. Subject, sender, and date need the parser that Slice 3 adds, and that slice can add those columns.
- **File check**: The header-line check keeps wrong files out while nothing can be deleted. The cost is that a real message with a damaged first line is refused. The administrator still holds that file, so nothing is lost.
- **Two-step import**: Pressing Import costs one extra click per file. That is worth it while a wrong import can't be removed. Slice 6 can drop the notice and revisit the step.
- **Size limit**: 150 MB means 150,000,000 bytes. It is set high on purpose, because refusing real mail costs more than storing a large file. SMTP sets its own cap in Slice 2.
- **Sign-in limits**: 12 characters, 10 failures in 15 minutes, and 12-hour sessions are starting values. An attacker who knows an administrator's email address can use the limit to block that account's sign-in for 15 minutes at a time, and setting a new password lifts the block. Email addresses are easy to find, so this is easier to try than with private account names.
- **Account removal**: Setting a new password ends the account's sessions, and that is how an operator shuts out a departed administrator. Past source observations keep the account's email address, and this slice has no way to remove it.
- **HTTPS**: The operator provides it through a reverse proxy, or reaches the server only on the local machine. The server does not terminate TLS in this slice. A proxy brings its own upload limit, and nginx's default of 1 MiB refuses any larger message, which is why FR-031 and FR-033 exist.
- **Storage**: The data directory is on one local filesystem that honors flush requests. The durability guarantee does not hold without that.
- **API**: The server's request interface serves the web interface and is not a published contract in this slice. The web interface meets the issue's "UI or documented API" condition. Direct requests are still tested, because they are the path an attacker would use.
- **Scale**: FR-024 scans stored originals before the server accepts imports. How long that takes at the 5 million message target is unmeasured, and the sizing test in Slice 3 does not list it.
- **Download errors**: The page fetches the file and saves it once every byte has arrived, so a failure shows as a notification and the page stays in place. The tab allows one active transfer and retains at most one completed file URL for browser handoff. Each payload can reach 150 MB, before browser overhead and Blob construction. The previous URL is released on the next save, and the latest after 40 seconds. The browser's own download list shows a file only when its transfer finishes.
- **Visual design**: `DESIGN.md`, written separately, owns layout, type, color, and control labels. This spec sets behavior and the wording of outcomes and failures.
- **Plan inputs**: The plan ranks credible failures by likelihood and harm, names each required check and its layer, and adds the check commands to `AGENTS.md`. The server must send the embedding rule in FR-007 as a response header, because static pages can't carry it. Test fixtures use synthetic mail only.

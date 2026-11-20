# Feature Specification: Find Messages Quickly and Safely

**Feature Branch**: `003-search-preview` (not created yet; this repository has no branch hook)

**Created**: 2026-11-15

**Status**: Draft

**Input**: User description: "#3 from GH", meaning [issue #3](https://github.com/maltehedderich/mailarkiv/issues/3), "Slice 3: Find messages quickly and safely". The issue stays the record of scope and completion requirements.

## Scope

An administrator searches the archive by sender, recipient, subject, date, attachment name, and message text. They open a result, read it in a preview that can't run code, and save its attachments. The preview shows the message's embedded images. It loads images from the internet only when the administrator asks, and nothing else from the internet at all. This is MVP slice 3 of 7. It adds message processing and a search index on top of the capture paths from Slices 1 and 2 and must not weaken them: when processing or search fails, capture, browsing, and download keep working.

**In scope**

- Background processing that reads each message's sender, recipients, subject, date, text, and attachments from its original, including messages archived before this slice.
- Sender, subject, and date in message list rows, in place of the file name, source, and folder.
- Search by words and by filters, with results in the list's order.
- A message view with a safe preview of its text or HTML, including its embedded images, and its attachments, each of which can be saved.
- Loading a message's images from the internet when the administrator asks, for one message view at a time.
- Processing problems shown on the message, and a way to list every message that has them.
- A search index that can be deleted and rebuilt, held by a separate search process that the server starts and restarts.
- Search and indexing states, failures, and rebuild progress on the status screen, in the problem count, and in the server log.
- A pause in indexing when disk space runs low, so capture keeps its room.
- A sizing test at 5 million messages that sets the documented supported size.
- Operator documentation.

**Excluded**

| Excluded from this slice                                                                                   | Where it belongs       |
| ---------------------------------------------------------------------------------------------------------- | ---------------------- |
| Reading or searching attachment contents, including the text and attachments of an attached message       | Not planned in the MVP |
| Exporting selected messages from the list or from results                                                  | Slice 4 (#5)           |
| Deleting messages, and when deleted text leaves the index files                                            | Slice 5 (#13)          |
| Index checks in integrity verification, backup and restore, release packages with restart policy           | Slice 6 (#8)           |
| SMTP journal capture, and searching journal envelope recipients                                            | Slice 7 (#2)           |
| Giving administrators access to some sources only                                                          | Future (#7)            |
| Phrase, OR, NOT, and wildcard search; ordering by relevance; highlighted matches and text snippets         | No issue owns this yet |
| Saved searches, and searching by size, source, folder, or import file name                                 | No issue owns this yet |
| Ignoring accents, stemming, and other language-specific matching                                           | No issue owns this yet |
| Loading style sheets, fonts, frames, or anything other than images from the internet in a preview          | No issue owns this yet |
| Remembering the choice to load images, or loading them through the server to hide the reader's address     | No issue owns this yet |
| Processing messages again after an upgrade changes how they're read                                        | No issue owns this yet |
| Decrypting encrypted mail (S/MIME, OpenPGP)                                                                | No issue owns this yet |

## User Scenarios & Testing *(mandatory)*

All seven stories are required before this slice is done. Priorities set the build order.

An **administrator** is anyone with a local account. An **operator** runs the server and can use its host. **Processing** reads a message's original to find its sender, recipients, subject, date, text, and attachments. The **index** is search's own copy of processed messages; it can be deleted and rebuilt from the archive. The **search process** is a separate program that holds the index; the server starts it and restarts it. A message is **searchable** once it is processed and its index entry is saved.

### User Story 1 - Find a message by its fields and text (Priority: P1)

An administrator types words or fills in filters and gets the matching messages, with sender, subject, and date on each row.

**Why this priority**: Finding mail is why people keep an archive. Without search, the list is only useful for recent mail.

**Independent Test**: Import synthetic messages with known senders, recipients, subjects, dates, body words, and attachment names. Run a fixed set of searches and compare each result with the expected set of messages.

**Acceptance Scenarios**:

1. **Given** processed messages, **When** the administrator opens the message list, **Then** each row shows the sender's name and address, the subject, and the list date, and no row shows an import file name, source name, or folder.
2. **Given** a message whose body contains "Quartalsbericht", **When** the administrator searches for "quartals", **Then** the message is in the results.
3. **Given** messages from `anna@example.com` and `anna@example.org`, **When** the administrator enters `anna@example.com` in From, **Then** only the messages from `anna@example.com` are found.
4. **Given** messages from several addresses at `example.com`, **When** the administrator enters `example.com` in From, **Then** every message from an address at that domain is found.
5. **Given** a message with an attachment named `Vertrag_2026.pdf`, **When** the administrator enters "vertrag" in Attachment name, or searches for "vertrag" without a filter, **Then** the message is found.
6. **Given** words and a date range, **When** the administrator searches, **Then** only messages that match every word and whose list date falls in the range, both days included, are found.
7. **Given** any search, **When** results arrive, **Then** they show the total number of matches and come in pages of 50, in the list's order.
8. **Given** "müller", **When** the administrator searches, **Then** messages with "Müller" and "MÜLLER" are found, and messages with only "Muller" are not.
9. **Given** a message captured a moment ago and no other message waiting, **When** 60 seconds pass, **Then** a search finds it.
10. **Given** messages that aren't searchable yet, **When** the administrator searches, **Then** the results say how many messages they can't include yet.
11. **Given** results, **When** the administrator opens one and goes back, **Then** the same search, page, and results show again.
12. **Given** a search with no words and no filters, **When** the administrator submits it, **Then** the full message list shows.
13. **Given** a message whose Date header is 3 days later than when MailArkiv first archived it, **When** it is listed, **Then** its row shows the archive time, marked as such, and it sorts by that time.
14. **Given** a message imported again, **When** the administrator opens the list, **Then** it keeps its place, because the list date doesn't change.

---

### User Story 2 - Read a message and its attachments without risk (Priority: P1)

An administrator opens a message and sees its header fields, its text or HTML with its embedded images, and its attachments. Nothing in the message can run code or act on MailArkiv, and nothing loads from the internet unless the administrator asks for the message's images.

**Why this priority**: Archived mail includes phishing and tracking mail. A preview that runs a script or loads a tracking image unasked could expose the administrator's session or tell the sender that someone read the mail. The preview must still show what arrived, and much business mail carries its logos and pasted screenshots as embedded images.

**Independent Test**: In each browser the end-to-end suite runs, open hostile HTML messages (scripts, event handlers, remote images, style sheets, fonts, frames, forms, refresh tags, links, prefetch hints, SVG, embedded objects) while recording network requests. Check that no script runs and no request goes to any host other than MailArkiv. Then load images in each, and check that the only new requests are for images, that none carries the preview's address, and that no script runs. Open messages with embedded images, and check that they show with no request to another host.

**Acceptance Scenarios**:

1. **Given** a processed message, **When** the administrator opens it, **Then** they see its subject, the sender's name and address, To, Cc, and Bcc when present, its date, its attachments, and its body.
2. **Given** a message with both HTML and plain text, **When** it opens, **Then** the HTML shows, and the administrator can switch to the plain text.
3. **Given** HTML with scripts or event handlers, **When** it shows, **Then** no script runs.
4. **Given** HTML that references remote images, style sheets, and fonts, **When** it shows, **Then** the browser requests nothing from any other host, and the preview says that images from the internet aren't loaded and offers to load them.
5. **Given** a link in the HTML, **When** the administrator clicks it, **Then** nothing opens and no request leaves the browser.
6. **Given** a form in the HTML, **When** the administrator submits it, **Then** nothing is sent.
7. **Given** HTML that tries to read the session, open other MailArkiv pages, change the page around it, or open a window or dialog, **When** it shows, **Then** none of that happens.
8. **Given** the preview's own address, with or without images loaded, **When** someone opens it directly in a tab, **Then** it is isolated the same way.
9. **Given** a message with attachments, **When** the administrator opens it, **Then** each attachment is listed with its name, declared type, and size, and Save stores the attachment's decoded bytes under its name without displaying them.
10. **Given** an HTML or SVG attachment, **When** its address is opened directly in a tab, **Then** the browser saves it and doesn't display it.
11. **Given** an attachment name with `../`, HTML, control characters, or bidirectional characters, **When** it is listed and saved, **Then** it shows as plain text with those characters neutralized, and the saved file's name contains none of them.
12. **Given** HTML that shows embedded images, **When** it opens, **Then** the images show in their place, no request goes to another host, and each image also appears in the attachment list.
13. **Given** a text or HTML body over 2,000,000 bytes, **When** it opens, **Then** the first 2,000,000 bytes show, with a notice that the rest is only in the original and a way to download it.
14. **Given** a message without a text or HTML body, **When** it opens, **Then** the preview says "This message has no text."
15. **Given** HTML with images from the internet, **When** the administrator chooses to load them, **Then** they show, no request carries the preview's address, and style sheets, fonts, frames, and links still load nothing.
16. **Given** images loaded for one message, **When** the administrator opens another message, or the same message again, **Then** its images from the internet aren't loaded.
17. **Given** embedded images that together pass 10,000,000 bytes, **When** the HTML opens, **Then** the images within the limit show, the rest don't, and the preview says that some embedded images are only in the attachment list.

---

### User Story 3 - Keep originals and capture safe when processing or search fails (Priority: P1)

Malformed messages, crashes of the search process, and index failures never change a stored original, stop capture, or remove a message from the list.

**Why this priority**: The original bytes are what the archive guarantees. Processing reads untrusted input and the search process is a second program, so both will fail in ways that capture must survive.

**Independent Test**: Archive a set of malformed messages, and stop the search process at random points while imports and IMAP collection continue. Check that every original is byte-identical, every message has a list row, and every capture is confirmed as before.

**Acceptance Scenarios**:

1. **Given** a message with one broken part, **When** it is processed, **Then** the rest of the message shows and is searchable, and the message is Processed with problems and lists each problem with the part it affects.
2. **Given** a message whose header can't be read, **When** it is processed, **Then** it shows Couldn't be read, its row shows its capture data, and it downloads byte for byte.
3. **Given** a message not processed yet, **When** the administrator opens the list, **Then** its row shows its capture data and Not processed yet.
4. **Given** a processed message without a From or Subject header, **When** it is listed, **Then** its row shows "(no sender)" or "(no subject)".
5. **Given** a sender name or subject with HTML, control characters, or bidirectional characters, **When** it is listed or previewed, **Then** it shows as plain text with those characters neutralized.
6. **Given** a message from `x@attacker.example` whose sender name is `ceo@example.com`, or a name of 500 characters, **When** it is listed, **Then** the row shows `x@attacker.example` apart from the name, with `attacker.example` in view.
7. **Given** the search process crashes during indexing, **When** an administrator imports a message or a source syncs, **Then** capture succeeds as before, search shows Unavailable, the server restarts the search process, indexing resumes, and each message is found exactly once.
8. **Given** search is Unavailable, **When** the administrator uses the list, a preview, a download, or an attachment, **Then** each works.
9. **Given** a message that takes too long to process or has more parts than the bound allows, **When** it is processed, **Then** processing stops at the bound, the message shows the problem, and other messages continue.
10. **Given** any processing or index failure, **When** it is over, **Then** every stored original and history entry is unchanged.

---

### User Story 4 - Keep search, previews, and attachments closed to anyone not signed in (Priority: P1)

Only a signed-in administrator can search, view a message or preview, save an attachment, or start a rebuild, including through direct requests.

**Why this priority**: Search reaches any message in a few keystrokes. A gap there exposes the whole archive.

**Independent Test**: Send direct requests to search, the message view, the preview, attachment download, the status screen, and rebuild with no session, an expired session, a signed-out session, and a request forged from another website. Check that each is denied and returns no mail data.

**Acceptance Scenarios**:

1. **Given** no session, **When** a direct request searches, opens a message or preview, or downloads an attachment, **Then** it is denied and returns no mail data.
2. **Given** an expired or signed-out session, **When** it makes the same requests, **Then** they are denied the same way.
3. **Given** a signed-in administrator's browser visiting another website, **When** that site requests a rebuild, **Then** it is rejected and no rebuild starts.
4. **Given** another website, **When** it embeds a MailArkiv preview or attachment in a frame, **Then** the browser refuses to show it.
5. **Given** a host account other than the server's, **When** it tries to connect to the search process, **Then** the connection is refused.

---

### User Story 5 - Rebuild the index without losing search (Priority: P2)

A missing index is built again automatically. An administrator can rebuild a damaged one while the old one keeps answering.

**Why this priority**: The index is a second store that can be damaged or deleted, and a restore from backup leaves none. Rebuilding is the recovery path.

**Independent Test**: Delete the index with the server stopped, start it, and compare a fixed set of searches before and after. Then start a rebuild, stop the server at several points, and check that the old index answers until the new one replaces it and that the rebuild doesn't start over.

**Acceptance Scenarios**:

1. **Given** a stopped server whose index was deleted, **When** it starts, **Then** it builds a new index without administrator action, the status screen shows Building with progress, and searches answer from what is built and say how many messages they can't include yet.
2. **Given** that build finished, **When** the fixed searches run, **Then** they return the same messages as before the deletion.
3. **Given** an administrator who presses Rebuild and confirms, **When** the rebuild runs, **Then** the old index answers every search until the new one is complete, and the status screen shows progress.
4. **Given** a rebuild in progress, **When** the server restarts, **Then** the rebuild continues from where it was, not from the start.
5. **Given** a rebuild that finished, **When** the next search runs, **Then** it uses the new index, no search ever answered from a mix of both or found search unavailable during the switch, and the old index's files are removed.
6. **Given** messages captured during a rebuild, **When** it finishes, **Then** they are searchable.
7. **Given** a rebuild in progress, **When** an administrator presses Rebuild, **Then** no second rebuild starts and the screen says one is running.
8. **Given** too little free disk space for a second index, **When** an administrator presses Rebuild, **Then** it doesn't start, and the screen says how much free space it needs.
9. **Given** the server stopped after the index saved a batch but before MailArkiv recorded it, **When** indexing resumes, **Then** each message in that batch is found once.
10. **Given** free disk space where the index lives falls below 2 GiB, **When** messages arrive, **Then** capture succeeds, indexing shows Paused with the free space, and search keeps answering. When free space reaches 3 GiB, indexing resumes.
11. **Given** an index marked Needs rebuild, **When** a rebuild succeeds, **Then** search shows Available.

---

### User Story 6 - See search and processing problems without seeing mail (Priority: P2)

The status screen and server log show the search and indexing state, crashes, failures, and backlog. They contain no mail content and nothing an administrator searched for.

**Why this priority**: Search that lags or fails unseen makes the archive look emptier than it is. Status screens and logs must not become a copy of the mail or of what administrators looked for.

**Independent Test**: Put marker strings in subjects, addresses, bodies, attachment names, and search words. Force each failure category, then check that each appears on the status screen, in the problem count, and in the log, and that no marker appears on the status screen or in the log.

**Acceptance Scenarios**:

1. **Given** a running server, **When** an administrator opens the status screen, **Then** it shows the search state, the indexing state, the number of messages waiting to be processed and to be indexed, the number processed with problems and that couldn't be read, and how often the search process restarted since the server started.
2. **Given** the search process crashed, **When** an administrator opens the status screen or any signed-in page, **Then** the status screen lists Search process stopped with the time, and the problem count includes it.
3. **Given** the search process crashed 5 times within 30 minutes, **When** an administrator opens the status screen, **Then** search shows Needs rebuild with a Rebuild button, and the problem count includes it until a rebuild succeeds.
4. **Given** index maintenance failed 3 times in a row, **When** an administrator opens the status screen, **Then** search shows Needs rebuild.
5. **Given** messages with processing problems, **When** the administrator follows the link from the status screen, **Then** they see exactly the messages that are Processed with problems or Couldn't be read.
6. **Given** any failure, **When** the operator reads the log, **Then** it has the time, the category, and internal message IDs where they apply, and none of the marker strings.
7. **Given** indexing paused for low disk space, **When** an administrator opens the status screen, **Then** it shows the free space and the 2 GiB threshold.

---

### User Story 7 - Know how large an archive search supports (Priority: P2)

The operator documentation states the largest archive that search supports, measured with MailArkiv's own code.

**Why this priority**: The product's 5 million message target is an estimate, and ADR 0007's numbers came from a benchmark, not from MailArkiv. Operators need a measured limit before they rely on search.

**Independent Test**: Run the sizing test and check that the documentation reports its results and the supported size.

**Acceptance Scenarios**:

1. **Given** 5,000,000 synthetic messages on a host with 4 cores and 8 GiB of memory, **When** the sizing test finishes, **Then** it records search times (median and 95th percentile, newest first, prefix words included), full rebuild time, the search process's peak memory, and index size.
2. **Given** results that meet every target in FR-049, **When** the documentation is updated, **Then** it states 5,000,000 messages as supported, with the results.
3. **Given** a missed target, **When** the documentation is updated, **Then** it states the largest size tested that met every target, and the miss is recorded for a review of ADR 0007.

### Edge Cases

- **Several addresses in From**: The row shows the first, followed by "and N more". The preview shows all.
- **Header text in encoded words or another character set**: Shown as mail clients show it.
- **Unknown or wrong character set**: The text shows with replacement characters, and the message is Processed with problems.
- **Very long subject or sender name**: Shortened at the end in rows, in full in the preview. A long name never hides the sender's domain. Long attachment names are shortened in the middle so the extension stays visible.
- **Sender name that looks like an address**, such as `ceo@example.com` on mail from `x@attacker.example`: The row shows both, set apart, so the name can't pass for the address.
- **Sender name equal to the address**: The row shows the address once.
- **Very long sender address**: Shortened before its `@` in rows, so the domain stays visible. A domain too long for its space loses its start, because the end names who owns it.
- **No Date header, an unreadable one, or one more than 24 hours later than the first archive time**: The list date is the first archive time, marked as such. The preview says why and shows the header's text, if any.
- **Date header up to 24 hours later than the first archive time**: Kept, because senders' clocks and time zones are often wrong by hours.
- **Date header far in the past, such as 1970**: Kept. The message sorts low, and the preview shows the header's text.
- **A message processed after it was listed**: It moves from its first archive time to its sent date in the list.
- **Text only in HTML**: Searchable through the words in the HTML's text, not its markup.
- **Text beyond the first 2,000,000 bytes of a body**: Not searchable, and not in the preview.
- **More than 1,000 parts, or parts nested more than 20 levels deep**: Processing stops at the bound. What was read before it shows and is searchable, and the message is Processed with problems.
- **Processing passes 60 seconds**: Stopped. The message shows Couldn't be read, and other messages continue.
- **Attached message**: Listed as one attachment and saved as an `.eml` file. Its own text and attachments aren't searchable.
- **Attachment with damaged encoding**: Listed and marked damaged. Save stores the bytes that could be decoded.
- **Attachment without a name**: Shown as "(no name)" and saved as `attachment-<position>`, with `.eml` for an attached message.
- **Two attachments with the same name**: Both listed and both can be saved.
- **Microsoft TNEF (`winmail.dat`)**: Listed as one attachment. Its contents aren't read.
- **Encrypted mail (S/MIME or OpenPGP)**: The header fields show and are searchable. The encrypted part is listed as an attachment, and the preview says the message is encrypted.
- **Signed mail**: Its text shows. The signature is listed as an attachment and isn't checked.
- **Embedded image in another format, such as SVG or TIFF, or with damaged encoding**: Not shown in the HTML. It stays in the attachment list.
- **HTML that refers to an embedded image the message doesn't contain**: The rest of the HTML shows without it.
- **Embedded image that the HTML shows twice**: Shown in both places, and counted once toward the 10,000,000-byte limit.
- **Image from the internet at an `http:` address**: After loading, it is requested only over an encrypted connection, so it shows only if its host serves it that way.
- **Same bytes captured again**: One message, one index entry. Processing doesn't run again.
- **Stored original missing or damaged when processing reaches it**: The message stays Not processed yet. The failure shows under the Slice 1 category, and processing tries again at the next start and after an import restores the original.
- **A word of one character**: Matches only that word, not longer words.
- **Punctuation only, or quotes and operators such as `OR` and `-`**: Treated as ordinary text. Punctuation separates words and matches nothing on its own.
- **Search too broad to finish within 10 seconds**: Stopped, and the screen says "This search took too long. Add words or filters."
- **Search while the index is Building**: Answers from what is built, with the count of messages it can't include yet.
- **Index deleted while the server runs**: Not supported. The documentation says to stop the server first.
- **Search process program missing or unable to run**: Search shows Unavailable with Search process didn't start. Capture, list, preview, and download work.
- **Server upgraded from Slice 2 with messages already archived**: Each is processed and indexed in the background. Rows show capture data until then.

## Requirements *(mandatory)*

### Functional Requirements

**Processing**

- **FR-001**: The system MUST process every archived message in the background, reading its sender, recipients (To, Cc, Bcc), subject, date, text and HTML bodies, and attachments from the stored original. Messages archived before this slice MUST be processed too.
- **FR-002**: Processing and indexing MUST NOT delay or refuse any capture, and MUST NOT change, move, or delete a stored original or a history entry.
- **FR-003**: Each message MUST be in exactly one processing state:

| State                   | Meaning                                                                                 | List row shows                              |
| ----------------------- | --------------------------------------------------------------------------------------- | ------------------------------------------- |
| Not processed yet       | Processing hasn't run, or is waiting for a stored original to be restored               | Capture data and Not processed yet          |
| Processed               | Every part was read                                                                     | Sender, subject, list date                  |
| Processed with problems | Some parts couldn't be read or a bound was reached; the rest shows and is searchable    | Sender, subject, list date, a problem mark  |
| Couldn't be read        | The header couldn't be read, or processing passed its time limit                        | Capture data and Couldn't be read           |

- **FR-004**: A broken part MUST NOT stop the rest of the message from being processed. Each problem MUST be recorded with the part's position and declared type and one of these kinds: unknown character set, invalid encoding, malformed header, malformed structure, message cut short, too many parts, nested too deeply, body cut at the size limit, processing took too long. Problems MUST NOT include message content.
- **FR-005**: Processing of one message MUST stop after 60 seconds, after 1,000 parts, or at 20 levels of nesting. Search and preview MUST use at most the first 2,000,000 bytes of each text and HTML body.
- **FR-006**: The system MUST NOT process a message again once it is Processed, Processed with problems, or Couldn't be read. A message waiting for a stored original MUST be tried again at each start and after an import restores the original.
- **FR-007**: Header text MUST be decoded from encoded words, encoded parameters, and declared character sets, and shown as mail clients show it.

**Message list**

- **FR-008**: A row for a processed message MUST show the sender's name and address, the subject, the list date, the size, and a mark when the message has attachments. Embedded images MUST NOT set the mark or match Has attachments. When the sender gives no name, or a name equal to the address ignoring case, the row MUST show the address once. File names, source names, and folders MUST appear only in the message's history. This replaces the row content set by Slice 1 (FR-019) and Slice 2 (FR-040).
- **FR-009**: A row for a message that is Not processed yet or Couldn't be read MUST show its capture data as Slices 1 and 2 do, and its processing state.
- **FR-010**: A missing sender MUST show as "(no sender)" and a missing subject as "(no subject)". When From holds several addresses, the row MUST show the first, followed by "and N more".
- **FR-011**: Sender names, addresses, subjects, and attachment names MUST be treated as untrusted. They MUST be shown as plain text with control and bidirectional characters neutralized, as Slice 1 does for file names. Rows MUST shorten long subjects and sender names at the end and long attachment names in the middle. A long sender name MUST NOT push the address's domain out of view: a long address MUST shorten before its `@`, and a domain too long for its space MUST lose its start, so its end stays visible. The row MUST set the name and address apart, so a name can't pass for an address. The preview MUST show them in full.
- **FR-012**: Each message MUST have a list date, used for ordering, for the row's date, and for the date filter. It MUST be the date in the Date header, unless that header is missing or can't be read, or its date is more than 24 hours later than the time MailArkiv first archived the message; then it MUST be that first archive time. Messages that are Not processed yet or Couldn't be read MUST use the first archive time. The list and search results MUST be ordered by list date, newest first, then by first archive time, newest first, then in a fixed order, so pages never repeat or skip a message. A row whose list date is the first archive time MUST mark it as the archive time. This replaces Slice 1's order, newest import first (FR-019).
- **FR-013**: Administrators MUST be able to list exactly the messages that are Processed with problems or Couldn't be read, reached from the status screen.

**Search**

- **FR-014**: Search MUST offer a words field matching subject, sender, recipients, text, and attachment names, and filters for From, To (any of To, Cc, and Bcc), Subject, Attachment name, a date range, and Has attachments. A message MUST match every word and every filter given.
- **FR-015**: Matching MUST ignore capital letters and MUST NOT ignore accents. Spaces and punctuation separate words. Each word of two or more characters MUST also match longer words that start with it. A word of one character MUST match only itself. Quotes and operators MUST be treated as ordinary text.
- **FR-016**: A complete address in From or To MUST find the messages with exactly that address in that field and no others. A domain such as `example.com` MUST find every address at that domain.
- **FR-017**: The date range MUST include both its first and last day, in the administrator's time zone, and MUST apply to the list date.
- **FR-018**: Results MUST use the list's rows and order, come in pages of 50, and show the total number of matches. A search with no words and no filters MUST show the full list.
- **FR-019**: Results MUST say how many messages they can't include yet, because those are Not processed yet, waiting to be indexed, or not yet reached by a build. Messages that Couldn't be read MUST NOT match any search with words or filters; FR-013 lists them.
- **FR-020**: When nothing else waits to be processed or indexed and indexing isn't paused, a newly captured message MUST be searchable within 60 seconds.
- **FR-021**: A search that doesn't finish within 10 seconds MUST stop, and the screen MUST say "This search took too long. Add words or filters." When search is Unavailable, the screen MUST say so and why, and the list, previews, downloads, and attachments MUST keep working.
- **FR-022**: Search words and filters MUST NOT appear in page addresses, request addresses, the status screen, or the log. Going back from a message to the results MUST show the same search, page, and results.

**Message view and preview**

- **FR-023**: Opening a message MUST show its subject, the sender's name and address, To, Cc, and Bcc when present, its date, its processing state and problems, its attachments, its body, and ways to download the original and open its history. When the list date is the first archive time, the view MUST say why and show the Date header's text, if any, as plain text.
- **FR-024**: The body MUST show as HTML when the message has an HTML body, otherwise as plain text, and the administrator MUST be able to switch to the plain text when both exist. A message with neither MUST show "This message has no text."
- **FR-025**: The HTML preview MUST run no script or plugin, submit no form, open no window or dialog, and MUST NOT navigate MailArkiv, change the page around it, or read the session or any other MailArkiv page or data. Opening the preview's own address directly, with or without images loaded, MUST give the same isolation.
- **FR-026**: The preview MUST NOT cause any request to another host, including for images, style sheets, fonts, frames, prefetch hints, refresh tags, and clicked links, until the administrator chooses to load images for the message being viewed. After that choice, requests to other hosts MUST be for images only, over encrypted connections, and MUST NOT carry the preview's address. The choice MUST apply only to that view of that message and MUST NOT be remembered. When the HTML refers to images on the internet, the preview MUST say that they aren't loaded, that loading them can tell the sender when and from which network address the message was opened, and that they weren't archived, so they may differ from what arrived or be gone. It MUST offer to load them. A clicked link MUST open nothing. Styles written inside the message MUST apply.
- **FR-027**: Embedded images in PNG, JPEG, GIF, or WebP format MUST show in their place in the HTML preview, with no request to another host. An image that would bring the decoded bytes of the images shown past 10,000,000 MUST NOT show, and the preview MUST then say that some embedded images are only in the attachment list. Embedded images in other formats or with damaged encoding MUST NOT show. Every embedded image MUST stay listed as an attachment.
- **FR-028**: Plain text MUST show as text, with its line breaks, never interpreted as HTML. Links in it MUST NOT be clickable.
- **FR-029**: A body over 2,000,000 bytes MUST show its first 2,000,000 bytes, a notice that the rest is only in the original, and a way to download the original.
- **FR-030**: Opening a message that is Not processed yet or Couldn't be read MUST show its capture data, its state and problems, and the download of its original.

**Attachments**

- **FR-031**: Every part other than the displayed text and HTML bodies MUST be listed as an attachment, including embedded images, attached messages, and encrypted or signature parts. Each MUST show its name, its declared type, its decoded size, and whether its encoding is damaged. A missing name MUST show as "(no name)".
- **FR-032**: Saving an attachment MUST deliver its decoded bytes as a saved file, never displayed in the browser, including through a direct request. The file name MUST be the attachment's name with control characters, bidirectional characters, and path separators removed, or `attachment-<position>` when it has none, with `.eml` for an attached message. A damaged attachment MUST save the bytes that could be decoded.
- **FR-033**: The system MUST NOT read or search attachment contents.

**Index**

- **FR-034**: The index MUST hold at most one entry for each message. Indexing a message again, including after an interruption, MUST NOT create a second entry, and search MUST NOT return a message twice.
- **FR-035**: The archive MUST stay the source of truth. When the server starts with no index, it MUST build one without administrator action, and search MUST answer from what is built so far.
- **FR-036**: An administrator MUST be able to start a rebuild from the status screen after confirming it. At most one rebuild MUST run at a time. A rebuild MUST work in bounded batches, show its progress as messages done out of messages to do, and continue after a restart from its last saved batch. The old index MUST answer searches until the new one is complete, and then the new one MUST replace it in one step and the old index's files MUST be removed. Messages captured during the rebuild MUST be searchable once it finishes.
- **FR-037**: A rebuild MUST NOT start unless free space where the index lives exceeds 2 GiB plus the current index's size. A refused rebuild MUST say how much free space it needs.
- **FR-038**: When free space where the index lives falls below 2 GiB, indexing and rebuilding MUST pause and show Paused. They MUST resume when free space reaches 3 GiB. Capture MUST continue, and search MUST keep answering.
- **FR-039**: When the search process stops, the server MUST restart it after 1 second, doubling the wait after each further failure up to 60 seconds. The wait MUST return to 1 second once the search process has run for 10 minutes. While it is down, search MUST show Unavailable. After a restart, indexing MUST resume with no message lost or indexed twice.
- **FR-040**: After 5 search process crashes within 30 minutes, or 3 failed index maintenance runs in a row, the system MUST mark the index Needs rebuild and stop indexing. Search MUST keep answering when it can, with a notice that results may be incomplete. This state MUST survive a restart and MUST be cleared only by a successful rebuild.
- **FR-041**: Every call to the search process MUST have a time limit. A call that passes it MUST count as a failure.

**Access**

- **FR-042**: Searching, opening a message or preview, saving an attachment, and starting a rebuild MUST require a signed-in administrator, deny by default, and reject requests forged from another website, under the Slice 1 rules. Other websites MUST NOT be able to embed a preview or attachment. Every administrator MUST be able to search every message.
- **FR-043**: Only the server's own host account MUST be able to connect to the search process.

**Status and log**

- **FR-044**: Search MUST be in exactly one of these states, and indexing in exactly one of the indexing states:

| Search state  | Meaning                                                                        | Shown with                                         |
| ------------- | ------------------------------------------------------------------------------ | -------------------------------------------------- |
| Available     | Search answers                                                                 | Nothing extra                                      |
| Unavailable   | The search process isn't running, and the server is starting it                | When the next start attempt is                     |
| Needs rebuild | Repeated crashes or maintenance failures stopped indexing                      | The failure category, since when, and Rebuild      |

| Indexing state | Meaning                                                                           | Shown with                                     |
| -------------- | --------------------------------------------------------------------------------- | ---------------------------------------------- |
| Up to date     | No processed message waits to be indexed                                          | Nothing extra                                  |
| Catching up    | Processed messages wait to be indexed                                             | How many                                       |
| Building       | No index existed, and a first one is being built                                  | Messages done out of messages to do            |
| Rebuilding     | An administrator started a rebuild, and the old index answers                     | Messages done out of messages to do            |
| Paused         | Free space is below the threshold                                                 | Free space, the 2 GiB threshold, and progress  |
| Stopped        | Search is Unavailable or Needs rebuild                                            | The search state                               |

- **FR-045**: The status screen MUST show the search state, the indexing state, the number of messages Not processed yet, waiting to be indexed, Processed with problems, and Couldn't be read, the number of search process restarts since the server started, and each failure below since the server started, with its meaning and next step.
- **FR-046**: Every search and index failure MUST use one of these categories:

| Category                     | What happened                                                                    | What MailArkiv does                                                          | Next step                                                            |
| ---------------------------- | -------------------------------------------------------------------------------- | ---------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| Search process stopped       | The search process exited or crashed                                             | Restarts it with growing waits. Capture continues                            | None if it passes. If it repeats, check the server log               |
| Search process didn't start  | The search process couldn't be started, such as when its program is missing      | Tries again with growing waits                                               | Check the installation and the server log                            |
| Search process didn't answer | A call passed its time limit                                                     | Ends the call. Indexing retries the batch                                    | If it repeats, check the server log and the host's free memory       |
| Index write failed           | A batch couldn't be saved to the index, such as through a denied write           | Retries the batch with growing waits                                         | Fix the index location, such as its permissions                      |
| Index maintenance failed     | Background merging of index files failed                                         | Tries again at the next maintenance. After 3 in a row, marks Needs rebuild   | If search shows Needs rebuild, start a rebuild                       |
| Index needs rebuild          | Repeated crashes or maintenance failures                                         | Stops indexing. Search answers when it can, with a notice                    | Start a rebuild on the status screen                                 |
| Low disk space               | Free space where the index lives fell below 2 GiB                                | Pauses indexing and rebuilding until 3 GiB is free. Capture continues        | Free disk space                                                      |
| Rebuild failed               | A rebuild stopped with an error other than low disk space or a restart           | Discards the new index. The old index keeps answering                        | Start the rebuild again. If it repeats, check the server log         |

- **FR-047**: The problem count on every signed-in page MUST include search while Unavailable or Needs rebuild, indexing while Paused, and each failure under FR-046 since the server started. Messages that are Processed with problems or Couldn't be read MUST NOT count as problems.
- **FR-048**: Every failure MUST go to the server log with the time, the category, and internal message IDs where they apply. The log and the status screen MUST NOT contain search words, filters, subjects, addresses, names, body text, or attachment names, including in anything the search process reports.

**Sizing**

- **FR-049**: A sizing test MUST run MailArkiv's own processing, indexing, and search, with the search process as shipped, on 5,000,000 synthetic messages on a host with 4 cores and 8 GiB of memory. It MUST record median and 95th percentile search times for newest-first searches with and without prefix words, full rebuild time, the search process's peak memory, and index size. The targets are a 95th percentile under 1 second, a full rebuild within 8 hours, and under 1 GiB of memory allocated by search. The operator documentation MUST state the largest size tested that met every target as the supported size, with the results.

**All screens**

- **FR-050**: Search, results, the message view, and the search parts of the status screen MUST meet the Slice 1 rules for accessibility (WCAG 2.2 AA, keyboard alone), width (360 px with no horizontal page scrolling), absolute times with the UTC offset, and data that can't be loaded (a message and Try again, never an empty list or a zero count).

**Documentation**

- **FR-051**: Operator documentation MUST explain how search matches words (capital letters ignored, accents not, prefixes, no phrases or operators) and what it covers and doesn't (attachment names but not contents, the first 2,000,000 bytes of each body, no attached messages' contents). It MUST state what the preview guarantees: no scripts; nothing loaded from the internet until an administrator loads one message's images, and then only images; links that don't open; and embedded images shown in place, with their formats and size limit, and always listed as attachments. It MUST say that loading images can tell the sender that a message was opened, and that it shows the images as they are now, not as they arrived.
- **FR-052**: Operator documentation MUST explain where the index lives, that it holds copies of message text and needs the same protection as the data directory, that backups can skip it, that a missing index is built at start, how to rebuild one, and that the server must be stopped before deleting it. It MUST give the disk threshold, the search process and its restart behavior, the new log categories, the processing bounds, and the supported size from the sizing test.

### Key Entities *(include if feature involves data)*

- **Processed message**: What processing read from one message: sender, recipients, subject, date, text, HTML, the attachment list, its processing state, and its problems. It can be produced again from the original.
- **Processing problem**: One problem in one message: the part's position and declared type, and the problem's kind. It holds no message content.
- **Attachment**: One listed part of a message: its position, name, declared type, decoded size, and whether its encoding is damaged. It is read from the original when saved.
- **Index**: Search's copy of processed messages, with at most one entry per message. It can be deleted and built again.
- **Index entry**: One message's searchable fields and text, and the fact that it was saved to a given index.
- **Rebuild**: One run that builds a new index while the old one answers: who started it, when, its progress, and how it ended.
- **Search process**: The separate program that holds the index. It has a state, its restarts, and its failures since the server started.
- **Failure event**: As in Slice 1, a time and a category, with internal message IDs where they apply. FR-046 adds categories.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: For a fixed set of searches over a synthetic archive, covering words in text, subject, sender, recipients, and attachment names, each filter, prefixes, capital letters, accents, date ranges, and missing, unreadable, and future Date headers, every result contains exactly the expected messages in the expected order.
- **SC-002**: Across the hostile HTML set, in every browser the end-to-end suite runs, no script runs and the browser sends no request to any host other than MailArkiv, including after clicking every link. After loading images in each message, every request to another host is for an image over an encrypted connection, none carries the preview's address, and the rest still holds.
- **SC-003**: For every message in a malformed set (broken MIME, unknown character sets, bad encodings, missing headers, deep nesting, thousands of parts, truncated files), the original downloads byte for byte, the message has a list row, and its state and problems match the expected ones.
- **SC-004**: None of the requests without a valid session (no session, expired, signed out, forged cross-site) returns results, message content, preview content, or attachment bytes, or starts a rebuild.
- **SC-005**: When the search process is killed during a search, during an indexing batch, after the index saved a batch but before MailArkiv recorded it, and during a rebuild, every capture in that period succeeds, search answers again within 70 seconds of each kill, and after indexing catches up each message is found exactly once.
- **SC-006**: After deleting the index with the server stopped and starting it again, the fixed searches return the same messages as before once the build finishes.
- **SC-007**: When a rebuild is interrupted at 3 points, it never starts over, searches during it return every message they returned before it started, and after it finishes the fixed searches return the same messages plus any captured during it.
- **SC-008**: With free space pushed below 2 GiB, indexing shows Paused by the next page refresh, every capture still succeeds, and indexing resumes once 3 GiB is free.
- **SC-009**: Each category in FR-046, when forced, appears on the status screen and in the log by the next page refresh, and the problem count includes it. No marker string from subjects, addresses, bodies, attachment names, or search words appears on the status screen, in the log, or in any request address.
- **SC-010**: With nothing waiting, a newly captured message is searchable within 60 seconds in every trial.
- **SC-011**: The sizing test at 5,000,000 messages has run, its results are in the documentation, and the documented supported size follows from them.
- **SC-012**: Searching, opening a result, and saving an attachment can be done by keyboard alone, and at 360 px wide with no horizontal page scrolling.
- **SC-013**: Across a set of messages with embedded images (pasted screenshots, signature logos, an image shown twice, a reference to a missing image, unsupported formats, damaged encoding, and images past the size limit), in every browser the end-to-end suite runs, each image FR-027 allows shows in place, no other one does, and no request goes to another host.

### Issue #3 acceptance criteria

| Issue criterion                                                                                 | Requirements                   | Outcome        |
| ----------------------------------------------------------------------------------------------- | ------------------------------ | -------------- |
| Indexing failure does not affect stored originals                                               | FR-002, FR-035                 | SC-003, SC-005 |
| A deleted index can be rebuilt                                                                  | FR-035, FR-036                 | SC-006         |
| Malformed messages remain downloadable and their processing errors are visible                  | FR-003, FR-004, FR-013, FR-030 | SC-003         |
| Rows show sender, subject, and date; the file name appears only in the import history           | FR-008, FR-012                 | SC-001         |
| Unparsed or failed messages keep a row with capture data; missing fields show a placeholder     | FR-009, FR-010                 | SC-003         |
| Sender and subject are plain text with control and bidirectional characters neutralized         | FR-011                         | SC-003         |
| Untrusted HTML can't execute active content, and remote content is blocked by default           | FR-025, FR-026, FR-027         | SC-002, SC-013 |
| Direct requests can't bypass authorization for search, preview, or attachments                  | FR-042, FR-043                 | SC-004         |
| Merge errors and crashes appear in operator status; repeated errors mark the index failed       | FR-040, FR-045, FR-046         | SC-009         |
| A crash stops search and indexing only; capture continues and the server restarts the process  | FR-002, FR-039                 | SC-005         |
| Re-indexing leaves one document per message                                                     | FR-034                         | SC-005, SC-007 |
| Rebuild runs in bounded batches, resumes after restart, and the old index serves until the swap | FR-036                         | SC-007         |
| Indexing pauses visibly on low disk space while capture continues                               | FR-038                         | SC-008         |
| The sizing test sets the documented supported size                                              | FR-049                         | SC-011         |
| A user can complete the workflow through the UI                                                 | FR-014, FR-023, FR-032         | SC-001, SC-012 |

## Assumptions

Each choice below fills a gap in the issue. The numbers are starting values chosen for this spec, not measurements. None is configurable until someone needs it to be.

- **Decided design**: Issue #3, [the architecture principles](../../docs/ARCHITECTURE.md), [ADR 0003](../../docs/adr/0003-mail-libraries.md), and [ADR 0007](../../docs/adr/0007-tantivy-search-process.md) fix the parser, the search engine and its version, the separate search process and its local connection, lowercasing without stemming, prefix matching, and the preview's isolation through its own address and a sandboxed frame without a sanitizer. The plan carries these decisions, and this spec states only the behavior they must produce. On 2026-11-15 the user allowed embedded images and, on request, images from the internet within that isolation. The architecture principles say so, but issue #3's design notes predate the change.
- **Search syntax**: Words and filter fields, with every word required. Phrases, OR, NOT, and wildcards would need a query language that administrators must learn. Prefix matching already finds word variants, which lowercasing alone misses.
- **Accents**: Lowercasing only, as decided, so "muller" doesn't find "Müller". Ignoring accents would also merge words that differ in meaning, and changing it later costs one rebuild.
- **Single characters**: A one-character prefix matches a large share of all words. Letting it match only itself keeps such searches within the time targets.
- **Addresses**: A complete address must match exactly, because "all mail from this person" is the most common archive search. Domain matching covers "all mail from this customer".
- **Search words outside addresses**: Reverse proxies and browser history record page and request addresses. Search words name people and topics in other people's mail, so they stay out of both. The cost is that a search can't be bookmarked or shared as a link.
- **Embedded images**: Chosen by the user on 2026-11-15. Mail clients store pasted screenshots and signature logos as embedded images, so HTML mail without them shows gaps. They travel inside the message, so showing them sends no request anywhere. The preview's isolation still blocks every load except images carried inside the preview itself, so an image the preview fails to place leaves a gap, never a request. Other formats stay attachments only: browsers disagree on formats such as TIFF, and SVG is markup. The 10,000,000-byte limit keeps a message full of large images from slowing the preview.
- **Images from the internet**: Chosen by the user on 2026-11-15. Most mail clients offer to load them, and much newsletter and marketing mail can't be read without them. Loading stays a choice for each message view, because a loaded image can tell the sender or a tracking service when and from which network address the message was opened. In an archive, the reader is often an administrator opening someone else's mail, and for phishing and spam a load confirms that the mailbox is read. Remote images weren't archived either: years later they may be gone or replaced, so the preview shows them as they are now, not as they arrived. Only images load, because style sheets, fonts, and frames add more ways to reach the internet for little gain in reading. Remembering the choice per sender would add stored state. Loading images through the server would hide the reader's address, but it makes the server fetch any address found in untrusted mail, including hosts on the local network.
- **Links**: Opening a link from archived mail can confirm to the sender that it was read, or lead to a phishing page. The administrator can copy a link from the plain text, which shows addresses as text.
- **Processing bounds**: 60 seconds, 1,000 parts, 20 levels, and 2,000,000 bytes per body keep one hostile or broken message from holding up the rest. Real business mail stays far below each.
- **No automatic reprocessing**: Processing is final for each message in this slice. A later release that reads messages better needs its own way to reprocess them.
- **Automatic build, manual rebuild**: A missing index is always safe to build, and a restore from backup leaves none. A damaged one needs an administrator to choose when to spend hours on a rebuild, and an automatic rebuild after a crash loop could loop itself.
- **Failure thresholds**: 5 crashes in 30 minutes and 3 maintenance failures in a row separate a passing problem from a damaged index. Restart waits double from 1 to 60 seconds.
- **Disk threshold**: 2 GiB, resuming at 3 GiB so indexing doesn't flap. The index at 5,000,000 messages was 5.6 GiB, and a rebuild holds two indexes at once, which is why FR-037 adds the current index's size.
- **Processing problems aren't system problems**: Malformed mail, mostly spam, is common and permanent. Counting it would keep the problem count above zero for good and hide real failures. The status screen still counts and links to it.
- **Attached messages and TNEF**: Treated as attachments, because reading their contents is the attachment-content work excluded from the MVP.
- **List date**: Chosen by the user on 2026-11-15. The Date header orders mail as mail clients do and makes the date filter mean when mail was sent, even after a first sync collects years of mail in one day. The sender writes that header, so it can be missing or false. Capping it at the first archive time stops a future date from pinning a message to the top, and the 24-hour allowance covers wrong clocks and time zones. A false past date still sorts a message too low; the preview shows the header so an administrator can spot it. Ordering by archive time alone was rejected because it says nothing about when mail was sent, and a sort switch was rejected because it adds a second order to the index and the sizing test. The mail server's received time from IMAP isn't used, because imports have none.
- **Sender in rows**: Chosen by the user on 2026-11-15. Anyone can write any name in From, and phishing mail borrows the names of banks and colleagues. A row with the name alone hides the one part a reader can check, and a name such as `ceo@example.com` reads as an address. Names also repeat across mailboxes ("Support", "Info"), and the From filter takes addresses, so the row shows what to filter on. The domain's end stays in view because it names the owner: `example.com.attacker.example` belongs to `attacker.example`. Showing the address doesn't prove who sent a message, because the From address can be forged too. Checking that needs the receiving server's SPF, DKIM, and DMARC results, which this slice doesn't read.
- **Date and time display**: Absolute, in the viewer's time zone with the UTC offset, under the Slice 1 rule.
- **API**: As in Slices 1 and 2, the server's request interface serves the web interface and is not a published contract. The web interface meets the issue's "UI or documented API" condition. Direct requests are still tested, because they are the path an attacker would use.
- **Visual design**: `DESIGN.md` owns layout, type, color, and control labels. This spec sets behavior and the wording of states and failures.
- **Plan inputs**: The plan ranks credible failures by likelihood and harm and names each required check and its layer. It justifies the search process boundary against its operational cost under constitution principle II, adds the search process's toolchain, tests, and dependency audit to the required checks and `AGENTS.md`, and records the dependency advisory as a principle IV exception, as ADR 0007 requires. It tests whether automatic index maintenance retries after a failed merge, which ADR 0007 left unmeasured, and how the search process's output is kept free of mail content before it reaches the status screen and log. Credible preview leaks to check include DNS prefetching, link navigation inside the frame, the preview's address reaching image hosts, and requests other than images after images are loaded. Tests use the real catalog, storage, and search process, and synthetic mail only. The sizing test runs outside the default check suite. Behavior stays behind a flag or unrouted until the slice is complete, or the plan records the exception to constitution VI as Slice 2 did.

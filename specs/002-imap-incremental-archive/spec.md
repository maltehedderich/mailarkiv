# Feature Specification: Archive an IMAP Mailbox Incrementally

**Feature Branch**: `002-imap-incremental-archive` (not created yet; this repository has no branch hook)

**Created**: 2026-11-08

**Status**: Draft

**Input**: User description: "gh issue #4", meaning [issue #4](https://github.com/maltehedderich/mailarkiv/issues/4), "Slice 2: Archive an IMAP mailbox incrementally". The issue stays the record of scope and completion requirements.

## Scope

An administrator connects an IMAP mailbox. MailArkiv archives the messages already in it, then each new one, and shows progress and failures. This is MVP slice 2 of 7 and the first way to collect mail without importing files. It reuses the Slice 1 capture path, so the durability and deduplication rules for imports also apply to collected mail.

**In scope**

- Sources: IMAP mailboxes that administrators add with a server, a user name, a password, and the folders to collect.
- A key file, created by the operator outside the data directory, that encrypts every stored IMAP password.
- A first sync that archives every message already in the chosen folders, then a sync every 5 minutes for new ones.
- A checkpoint for each folder that moves past a message only after the message is archived.
- A timeout on every network operation, and automatic retries with growing waits.
- Pausing, resuming, editing, removing, and syncing a source on demand.
- A sources screen with each source's state, progress, and failures. IMAP failures also appear on the status screen, in the problem count, and in the server log.
- Collected messages in the message list and in each message's history.
- Operator documentation.

**Excluded**

| Excluded from this slice                                                                   | Where it belongs              |
| ------------------------------------------------------------------------------------------ | ----------------------------- |
| OAuth 2.0 sign-in, which Microsoft 365 requires, and Microsoft Graph                       | Future (#9)                   |
| Parsed fields such as sender and subject, message preview, search                          | Slice 3 (#3)                  |
| Selected-message export                                                                    | Slice 4 (#5)                  |
| Deleting archived mail                                                                     | Slice 5 (#13)                 |
| Health dashboard, integrity scan, backup and restore, release packages with restart policy | Slice 6 (#8)                  |
| SMTP journal capture                                                                       | Slice 7 (#2)                  |
| Restoring mail into a mailbox                                                              | Future (#6)                   |
| Giving administrators access to some sources only                                          | Future (#7)                   |
| Changing the mailbox, such as marking messages read or deleting them after archiving       | Not planned in the MVP        |
| Pointing an existing source at another server or user name                                 | Not planned; add a new source |
| Collecting the moment mail arrives (IMAP IDLE), and a configurable sync interval           | No issue owns this yet        |
| Accepting a server certificate that the host doesn't trust                                 | No issue owns this yet        |
| Replacing the key file while keeping the stored passwords                                  | No issue owns this yet        |
| POP3                                                                                       | No issue owns this yet        |

## User Scenarios & Testing *(mandatory)*

All six stories are required before this slice is done. Priorities set the build order.

An **administrator** is anyone with a local account. An **operator** runs the server and can use its host. A **source** is one IMAP mailbox that an administrator added. A **sync** is one run that collects new messages from one source.

### User Story 1 - Connect a mailbox and see its mail archived (Priority: P1)

An administrator adds a source and chooses folders. The messages already in those folders appear in the message list, and new mail follows without further action.

**Why this priority**: This is what the slice promises, and it proves a path through every layer that the other stories build on.

**Independent Test**: Against a test IMAP server holding synthetic messages in two folders, add a source, wait for the first sync to finish, and check that every message is listed and downloads byte for byte. Then deliver a new message and check that it is listed within 6 minutes.

**Acceptance Scenarios**:

1. **Given** an operator who created the key and started the server with it, **When** an administrator enters a server, connection security, user name, and password and presses Connect, **Then** MailArkiv signs in and lists the server's folders with each folder's message count, with the inbox preselected.
2. **Given** that folder list, **When** the administrator saves the source, **Then** the first sync starts at once, and the source shows how many messages it has archived out of how many it found.
3. **Given** a finished first sync, **When** the administrator opens the message list, **Then** every message from the chosen folders is listed with the source name and folder where an import shows its file name, and each downloads with exactly the bytes the server returned.
4. **Given** an active source, **When** a new message arrives in a chosen folder, **Then** it is listed within 6 minutes without any action.
5. **Given** a message already archived through an import, another folder, or another source, **When** a sync finds the same bytes, **Then** the list keeps one entry, no second copy is stored, and the message's history gains an entry for this source and folder.
6. **Given** a collected message, **When** the administrator opens its history, **Then** the collection entry shows the source name, server, user name, folder, the time the mail server received the message, and when MailArkiv collected it.
7. **Given** wrong connection details, **When** the administrator presses Connect, **Then** they see which step failed (server not reachable, secure connection failed, sign-in rejected, or timed out), and nothing is saved.
8. **Given** the add-source screen, **When** the administrator reaches Save, **Then** the screen says that archived mail can't be removed in this version.
9. **Given** a finished sync, **When** someone checks the mailbox with a mail client, **Then** nothing changed: no message was marked read, flagged, moved, or deleted.
10. **Given** an untouched add-source drawer, **When** the administrator clicks outside it, **Then** it closes and focus returns to Add a source. After entering details, the same click, Escape, Close, Cancel, or Back asks whether to discard them; Keep editing preserves the form, and Discard source leaves it. Reopening starts empty with focus in Server.
11. **Given** a pending Save, **When** the administrator tries to close the drawer or navigate within the app, **Then** it stays open until the result arrives. Reloading or closing the tab warns through the browser. Session expiry can always open sign-in.

---

### User Story 2 - Never skip or double-count a message (Priority: P1)

Interrupting a sync at any point and starting again archives every message still on the server. Repeating a sync adds nothing already recorded.

**Why this priority**: A message skipped while the user still has it is lost to the archive once the user deletes it. History entries that repeat mislead anyone who relies on provenance. The issue names both.

**Independent Test**: An automated check stops the server at each step of collecting a message, restarts it, and compares the archive with the test server's mailbox.

**Acceptance Scenarios**:

1. **Given** a sync stopped after a message was downloaded but before it was stored, **When** the server restarts, **Then** the next sync archives that message and every message after it.
2. **Given** a sync stopped after a message was stored but before the checkpoint moved, **When** the server restarts, **Then** the message is collected again, no second copy is stored, and the message still has one history entry for this source and folder.
3. **Given** a finished sync and no new mail, **When** the next sync runs, **Then** it downloads no message and adds no history entry.
4. **Given** a folder that the mail server renumbered (its UIDVALIDITY changed), **When** the next sync runs, **Then** MailArkiv collects the whole folder again, adds no copy or history entry for messages already recorded, archives any it was missing, and the source says the folder was collected again because the server renumbered it.
5. **Given** two copies of the same bytes in one folder, **When** the folder is collected, **Then** the message has one history entry for that folder.
6. **Given** the same bytes in two chosen folders, **When** both are collected, **Then** the list has one entry, storage holds one copy, and the history has one entry for each folder.
7. **Given** a message the mail server fails to return, **When** the sync continues, **Then** later messages are archived, the failed one is tried again at each following sync, and the source lists it as waiting for retry until it is archived.
8. **Given** a message waiting for retry, **When** it is removed from the mailbox before MailArkiv could archive it, **Then** the source lists it as not archived because it was removed, with its folder, size, and server time, and stops trying.
9. **Given** a sync in progress, **When** the server shuts down gracefully and starts again, **Then** collection resumes from the checkpoint without administrator action.
10. **Given** archived messages, **When** they are deleted from the mailbox and the next sync runs, **Then** they stay in the archive.

---

### User Story 3 - Recover from network and server failures on its own (Priority: P1)

A stalled connection times out. A failed sync is retried with growing waits. A rejected password stops collection until an administrator fixes it.

**Why this priority**: Network trouble is the most likely failure. A hung sync collects nothing while looking busy, and repeated wrong passwords can get the mailbox locked by its provider.

**Independent Test**: A test server stalls or disconnects at each step: connecting, the secure handshake, the greeting, sign-in, opening a folder, listing messages, and partway through a message. Check that each one times out, is shown, and leaves the stalled message to be archived by the next sync.

**Acceptance Scenarios**:

1. **Given** a server that stops sending data at any step, **When** the time limit passes, **Then** the sync ends, the source shows Timed out with the time, nothing partial is stored, and every checkpoint is unchanged.
2. **Given** a sync that failed, **When** the following attempts fail too, **Then** MailArkiv waits 1, 2, 4, 8, and so on minutes between attempts, never more than 60, and shows when the next attempt starts.
3. **Given** a source that is retrying, **When** a sync succeeds, **Then** the source goes back to a sync every 5 minutes and shows the time of its last successful sync.
4. **Given** a mail server that says the user name or password is wrong, **When** sign-in is rejected, **Then** collection stops, the source shows Needs attention, and MailArkiv makes no further sign-in attempt until an administrator saves a new password or presses Sync now.
5. **Given** a mail server that says sign-in is temporarily unavailable, **When** sign-in is rejected that way, **Then** MailArkiv retries as for a network failure.
6. **Given** a server certificate that the host doesn't trust, has expired, or names another server, **When** MailArkiv connects, **Then** it sends no password and shows Secure connection failed.
7. **Given** a source set to STARTTLS, **When** the server doesn't offer it, **Then** MailArkiv sends no password and shows Secure connection failed.
8. **Given** one source that is failing or stalled, **When** other sources are due, **Then** they sync on schedule.
9. **Given** a full disk during a sync, **When** storing a message fails, **Then** the source shows Storage full, the checkpoint stays before that message, and after space is freed the next attempt archives it.
10. **Given** a chosen folder that no longer exists on the server, **When** a sync runs, **Then** the other folders are still collected on schedule, and the source shows that folder as unavailable.

---

### User Story 4 - Keep mailbox passwords secret (Priority: P1)

MailArkiv stores each IMAP password encrypted with a key kept outside the data directory. It never shows a password again, and uses it only for its own source.

**Why this priority**: A mailbox password opens the whole mailbox, including mail sent after the archive's last sync. Copies of the data directory end up in backups with weaker protection than the server.

**Independent Test**: Add sources whose passwords are unique marker strings. Copy the data directory, then search the copy, the log, the status screen, and every response sent to the browser for each marker.

**Acceptance Scenarios**:

1. **Given** a copy of the data directory without the key, **When** someone searches it for the passwords, **Then** none of them can be found or recovered.
2. **Given** a saved source, **When** an administrator edits it, **Then** the password field is empty, saving with the field empty keeps the stored password, and no response contains the password.
3. **Given** a new password for a source, **When** the administrator saves it, **Then** MailArkiv first signs in with it. If sign-in fails, the old password stays and the administrator sees why.
4. **Given** a key location inside the data directory, **When** the operator creates the key there or starts the server with it, **Then** both refuse and name the key location as the cause.
5. **Given** a server started without a key, **When** an administrator opens the sources screen, **Then** imports, browsing, and downloads still work, the screen says collection needs the key and where the documentation explains it, no source can be added, and existing sources show Needs attention.
6. **Given** a server started with a different key than the one that encrypted the passwords, **When** a source syncs, **Then** it shows Password can't be read and Needs attention, and entering the password again resumes collection.
7. **Given** no session, an expired or signed-out session, or a request forged from another website, **When** a direct request lists, adds, edits, pauses, resumes, syncs, or removes a source, **Then** it is denied, nothing changes, and no source details are returned.
8. **Given** a source, **When** an administrator removes it, **Then** its stored password is deleted.
9. **Given** a new key, **When** the operator creates it, **Then** only the file's owner can read or write it.
10. **Given** no key location, **When** the operator starts the server, **Then** the log says that collection is unavailable because no key was given.

---

### User Story 5 - See progress and problems without seeing mail (Priority: P2)

The sources screen shows each source's state, progress counted from archived messages, and failures with next steps. The status screen and the log show IMAP problems without passwords, user names, folder names, or mail content.

**Why this priority**: A sync that fails unseen loses mail once users delete it. Status screens and logs must not become a second copy of mailbox data.

**Independent Test**: Use marker strings as the password, user name, folder names, and message content, and in the server's error replies. Force each failure category, then check that each appears on the sources screen, the status screen, and in the log, and that no marker appears on the status screen or in the log.

**Acceptance Scenarios**:

1. **Given** a sync in progress, **When** the administrator watches the source, **Then** the archived count rises only when a message is durably stored. A message downloaded but not yet stored doesn't count.
2. **Given** a sync that timed out partway through a message, **When** the administrator reads the source, **Then** the archived count equals the number of messages this sync stored.
3. **Given** any source, **When** the administrator opens the sources screen, **Then** each source shows its state, its last successful sync, the messages that sync archived, the total it has collected, and when it next syncs.
4. **Given** a failing source, **When** the administrator opens it, **Then** they see the failure category with its meaning and next step, when the failures began, how many attempts failed, and the mail server's own reply as plain text.
5. **Given** a failing source, **When** an administrator opens the status screen or any signed-in page, **Then** the status screen lists the source with its name, failure category, when failures began, and the number of failed attempts, and the problem count includes it once.
6. **Given** any failed attempt, **When** the operator reads the log, **Then** it has the time, the source's internal ID, and the category, and none of the marker strings.
7. **Given** a message that can't be archived, **When** the administrator opens the source, **Then** it is listed with its folder, size, server time, and reason, and the status screen lists it as a failure since the last start.

---

### User Story 6 - Manage sources (Priority: P2)

Administrators can pause, resume, and sync a source on demand, rename it, change its folders or password, and remove it.

**Why this priority**: Mail server maintenance, password changes, and departing staff are routine. Without these controls a source keeps failing or keeps a password nobody should hold.

**Independent Test**: Run each action on a source during a sync and while it waits, and check the source's state, its checkpoints, and the archive afterward.

**Acceptance Scenarios**:

1. **Given** a sync in progress, **When** the administrator pauses the source, **Then** the sync stops at once, a message partway through transfer is not stored, checkpoints stay safe, and no sync runs until the source is resumed.
2. **Given** a paused source, **When** the administrator resumes it, **Then** a sync starts at once.
3. **Given** a source that is waiting, retrying, or needs attention, **When** the administrator presses Sync now, **Then** a sync starts at once.
4. **Given** a sync already running, **When** the administrator presses Sync now, **Then** no second sync starts and the screen says one is running.
5. **Given** a source, **When** the administrator adds a folder, **Then** that folder's first sync collects every message in it.
6. **Given** a collected folder, **When** the administrator removes it from the source, **Then** MailArkiv stops collecting it and its archived messages stay.
7. **Given** a source, **When** the administrator removes it and confirms, **Then** collection stops, its password is deleted, and its archived messages stay, with its name, server, user name, and folders still shown in their history.
8. **Given** an existing source, **When** an administrator adds another with the same server and user name, **Then** it is refused, and the screen points to the existing source.

### Edge Cases

- **Message over 150,000,000 bytes, as the server reports its size**: Not downloaded and not retried. The source lists it as not archived because it is too large.
- **Server sends a message whose length doesn't match what it announced, or exceeds the limit during transfer**: Nothing is stored. The message is treated as one the server failed to return.
- **Connection drops partway through a message**: Nothing partial is stored. The sync fails as a server error and is retried.
- **Server sends endless or oversized replies, such as a huge folder list**: The sync fails as a server error once a bound is passed, and stores nothing from that reply.
- **Folder name in a non-Latin script or with accents**: Shown as mail clients show it.
- **Folder name or server reply with HTML, control or bidirectional characters**: Shown as plain text with those characters neutralized. Long text is shortened in the middle.
- **Folder renamed on the server**: The old name shows as unavailable. The administrator adds the new name. Its messages gain a history entry for the new folder, and no copy is stored.
- **Folder created on the server after the source was saved**: Not collected until an administrator adds it. Mail that server rules file there in the meantime isn't archived.
- **Message moved between two chosen folders**: Gains a history entry for the new folder. No copy is stored.
- **Empty folder**: The first sync finishes with 0 messages, and the source waits for the next sync.
- **Very large first sync, such as 100,000 messages**: Runs until done across restarts, because each archived message moves the checkpoint. Messages appear in the list as they are archived.
- **Message deleted from the mailbox before any sync saw it**: Not archived. The documentation says that collection on a schedule can't capture such messages.
- **Server reports no received time for a message**: The history shows the received time as unknown.
- **Same bytes imported by hand while a sync stores them**: Both succeed, with one stored copy and two history entries, as Slice 1 requires for simultaneous imports.
- **Password changed while a sync runs**: The running sync continues. The new password applies at the next sign-in.
- **Key location given but the file is missing, unreadable, or not a valid key**: The server refuses to start and the log names the key as the cause.
- **Source removed while a sync runs**: The sync stops as for pausing, then the source is removed.

## Requirements *(mandatory)*

### Functional Requirements

**Sources**

- **FR-001**: Administrators MUST be able to add a source with a name of 1 to 100 characters, a server host name or IP address, a port, connection security (TLS from the start, or STARTTLS), a user name, and a password. The port MUST default to 993 for TLS and 143 for STARTTLS.
- Source names, hosts, and user names MUST lose surrounding Unicode whitespace, U+200B, and U+FEFF before validation, sign-in, storage, and duplicate checks. The frontend MUST apply this rule on submission, including to port text. Internal characters, passwords, and folder names MUST remain unchanged. A name or user name that becomes empty MUST be refused.
- **FR-002**: Adding a source MUST take two steps. First, Connect signs in and lists the server's folders with the message count the server reports for each. Second, the administrator chooses folders and saves. A source MUST be saved only after a successful sign-in.
- **FR-003**: Each source MUST collect the folders the administrator chose from the server's list, at least one. The inbox MUST be preselected. Administrators MUST be able to add and remove folders after saving. A folder created on the server later MUST NOT be collected until an administrator adds it.
- **FR-004**: The system MUST refuse a second source with the same server and user name, ignoring capital letters in both.
- **FR-005**: A source's server and user name MUST NOT change after it is saved. Its name, port, connection security, password, and folders MAY change. A change to the port, connection security, or password MUST pass a sign-in check before it is saved, and a failed check MUST leave the source as it was.
- **FR-006**: Administrators MUST be able to pause, resume, and remove a source, and to start a sync at once with Sync now. Removing MUST ask for confirmation naming the source.
- **FR-007**: Until Slice 5 ships, the add-source screen and the add-folder action MUST say that archived mail can't be removed in this version.

**Collection**

- **FR-008**: The first sync of a folder MUST archive every message in it. Later syncs MUST archive every message added since.
- **FR-009**: A sync MUST start when a source is saved or resumed, when a folder is added, at each server start after recovery, 5 minutes after the source's previous successful sync finished, and on Sync now. Paused sources and sources that need attention MUST NOT sync on schedule. Each source MUST run at most one sync at a time, over at most one connection to its server.
- **FR-010**: The system MUST archive each message's complete bytes exactly as the server returns them, under the Slice 1 capture rules: a message counts as archived only once it is durable, distinct bytes are stored once, and a stored original found missing or damaged is restored from the incoming bytes.
- **FR-011**: The system MUST NOT change the mailbox. It MUST NOT mark messages read, set or clear flags, move, copy, delete, or upload messages, or create, rename, or delete folders.
- **FR-012**: Each folder MUST have a durable checkpoint. The checkpoint MUST move past a message only after the message is durable or recorded as not archived. A sync MUST resume from the checkpoint after any interruption.
- **FR-013**: When the server renumbers a folder, the system MUST collect the whole folder again and say so on the source.
- **FR-014**: The system MUST record at most one history entry for each combination of source, folder, and message. Each entry MUST record the source, the folder, the time the server reports receiving the message, and the time of collection. Bytes already archived through any other source, folder, or import MUST gain an entry and MUST NOT be stored again.
- **FR-015**: A message the server fails to return MUST NOT stop later messages from being archived. The system MUST try it again at each sync and list it on the source as waiting for retry until it is archived. If it is no longer in the folder, the system MUST list it as not archived because it was removed, and stop trying.
- **FR-016**: A message whose size, as the server reports it, is over 150,000,000 bytes MUST NOT be downloaded. The system MUST list it as not archived because it is too large, and MUST NOT retry it.
- **FR-017**: A message whose transfer didn't complete, whose length differs from what the server announced, or that passes the size limit during transfer MUST NOT be stored.
- **FR-018**: Messages removed from the mailbox MUST stay in the archive.

**Connections, timeouts, and retries**

- **FR-019**: The system MUST connect with TLS from the start or upgrade with STARTTLS, and MUST NOT send a password over an unencrypted connection. It MUST refuse a certificate that the host doesn't trust, that has expired, or that doesn't match the server name. If STARTTLS is chosen and the server doesn't offer it, the connection MUST fail.
- **FR-020**: Connecting and completing the secure handshake MUST finish within 30 seconds. Every later step MUST fail when the server sends no data for 60 seconds. A timed-out sync MUST end, store nothing partial, and leave every checkpoint where it was.
- **FR-021**: After a failed sync, the system MUST try again after 1 minute, doubling the wait after each further failure, up to 60 minutes. A successful sync MUST return the source to its 5-minute schedule.
- **FR-022**: When the server rejects sign-in because the user name or password is wrong, the system MUST stop collecting from that source and show Needs attention. It MUST NOT sign in again until an administrator saves a password or presses Sync now, and this state MUST survive a restart. A rejection that the server marks as temporary MUST be retried under FR-021.
- **FR-023**: A failing or stalled source MUST NOT delay any other source.
- **FR-024**: On graceful shutdown, syncs MUST stop within the server's shutdown grace period. A message partway through transfer MUST NOT be stored.

**Passwords and the key**

- **FR-025**: An operator on the server host MUST be able to create a key file at a location they choose. The command MUST refuse a location inside the data directory and MUST NOT overwrite an existing file. It MUST create the file so that only its owner can read or write it.
- **FR-026**: The operator MUST be able to give the server the key's location at start. The server MUST refuse to start, with a log entry naming the key as the cause, if that location is inside the data directory or the file is missing, unreadable, or not a valid key. Without a key location, the server MUST start with collection unavailable and log at start that no key was given. The sources screen MUST then say that collection needs the key and point to the documentation, MUST NOT allow adding a source or saving a password, and every existing source MUST show Needs attention.
- **FR-027**: The system MUST store each IMAP password encrypted with the key, so that the data directory alone, including the catalog, its journal files, and temporary files, does not reveal it. A password that can't be decrypted with the current key MUST put its source in Needs attention with Password can't be read. Saving the password again MUST fix it. The system MUST check again at each start, so a source resumes once the server starts with the right key.
- **FR-028**: A stored password MUST NOT be shown, sent to the browser, or written to the log. The edit form MUST leave the password field empty, and saving with it empty MUST keep the stored password. A password MUST be used only to sign in to its own source's server as its own user name.
- **FR-029**: Removing a source MUST delete its stored password.
- **FR-030**: An IMAP password MUST be valid UTF-8, not empty, and at most 1,024 bytes.

**Access**

- **FR-031**: Every source operation MUST require a signed-in administrator, deny by default, and reject requests forged from another website, under the Slice 1 rules. Every administrator MUST see and manage every source.

**Progress and problems**

- **FR-032**: Each source MUST be in exactly one of these states:

| State           | Meaning                                                                                  | Shown with                                            |
| --------------- | ---------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| Waiting         | The last sync succeeded                                                                  | When the next sync starts                             |
| Syncing         | A sync is running                                                                        | Messages archived out of messages found this sync     |
| Retrying        | The last sync failed and another attempt is scheduled                                    | The failure category and when the next attempt starts |
| Needs attention | Collection stopped until an administrator acts: sign-in rejected, or password unreadable | The failure category and its next step                |
| Paused          | An administrator paused the source                                                       | When it was paused                                    |

- **FR-033**: The sources screen MUST show for each source: its name, server, user name, state, chosen folders, last successful sync, the messages that sync archived, the total messages it has collected, and when it next syncs. For a source with a failure it MUST also show the category, its meaning and next step, when the failures began, the number of failed attempts, and the mail server's last reply. It MUST list unavailable folders, messages waiting for retry, and messages not archived, each with its folder, size, server time, and reason.
- **FR-034**: A sync's progress MUST count a message only after it is durable.
- **FR-035**: Every IMAP failure MUST use one of these categories, and the sources screen MUST show each one's meaning and next step:

| Category                       | What happened                                                                                                        | What MailArkiv does                            | Next step                                                                          |
| ------------------------------ | -------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------- | ---------------------------------------------------------------------------------- |
| Server not reachable           | The server name couldn't be resolved, or the connection was refused or got no route                                  | Retries                                        | Check the server address and the network                                           |
| Secure connection failed       | The handshake failed, the certificate isn't trusted, has expired, or names another server, or STARTTLS isn't offered | Retries                                        | Fix the server's certificate, or add its issuer to the host's trusted certificates |
| Sign-in rejected               | The server says the user name or password is wrong                                                                   | Stops until an administrator acts              | Save the mailbox's current password                                                |
| Password can't be read         | The server has no key, or the stored password can't be decrypted with its key                                        | Stops until an administrator acts              | Start the server with the right key, or save the password again                    |
| Timed out                      | The server sent no data within the time limit                                                                        | Retries                                        | If this repeats, check the network and the mail server                             |
| Server error                   | The server returned an error or an unexpected reply, or closed the connection                                        | Retries                                        | Read the server's reply on the source. If this repeats, check the mail server      |
| Folder unavailable             | A chosen folder doesn't exist or can't be opened                                                                     | Tries again each sync; other folders continue  | Add the folder's new name, or remove it                                            |
| Storage full                   | The disk holding the archive ran out of space                                                                        | Retries                                        | Free disk space                                                                    |
| Storage write failed           | The server could not write to its storage location                                                                   | Retries                                        | Fix the storage location, such as its permissions                                  |
| Catalog write failed           | The message was stored but its catalog entry was not written                                                         | Retries                                        | If this repeats, check the server log                                              |
| Message unavailable            | The server didn't return one message                                                                                 | Tries again each sync; later messages continue | None if it passes. If it repeats, check the message on the mail server             |
| Message too large              | A message is over 150,000,000 bytes                                                                                  | Doesn't retry                                  | Keep the message in the mailbox, or save it elsewhere                              |
| Removed before it was archived | A message waiting for retry was removed from the mailbox                                                             | Doesn't retry                                  | None. The archive doesn't have this message                                        |

- **FR-036**: Every failed attempt and every message not archived MUST go to the server log with the time, the source's internal ID, and the category. The log MUST NOT contain IMAP passwords, user names, source names, folder names, the mail server's replies, or message content.
- **FR-037**: The status screen MUST list each source that is retrying or needs attention, or has an unavailable folder or messages waiting for retry, with its name, the failure category, when the failures began, and the number of failed attempts. It MUST list each message not archived since the last start as a failure. It MUST NOT show IMAP passwords, user names, folder names, the mail server's replies, or message content.
- **FR-038**: The problem count on every signed-in page MUST include each source listed under FR-037 once, and each message not archived since the last start.
- **FR-039**: Folder names and server replies MUST be treated as untrusted. They MUST be shown as plain text with control and bidirectional characters neutralized, and shortened in the middle when long. Folder names MUST be shown as mail clients show them, not in the protocol's encoding.

**Message list and history**

- **FR-040**: A list row whose latest history entry is a collection MUST show the source name and folder where an import shows its file name, and the collection time. The entry count MUST include collections.
- **FR-041**: A message's history MUST show each collection with the source name, server, user name, folder, the time the server reports receiving the message, or "Unknown", and the collection time. These entries MUST stay unchanged after their source is removed.

**All screens**

- **FR-042**: The sources screen and the add, edit, and remove flows MUST meet the Slice 1 rules for accessibility (WCAG 2.2 AA, keyboard alone), width (360 px with no horizontal page scrolling), absolute times with the UTC offset, and data that can't be loaded (a message and Try again, never an empty list or a zero count).

**Documentation**

- **FR-043**: Operator documentation MUST explain how to create the key, where to keep it, which user must own it so the server can read it, how to start the server with it, and that it needs its own backup, separate from the data directory. It MUST say that a lost key means saving every IMAP password again, and that anyone who can read both the key and the data directory can read the passwords. It MUST explain how to replace the key: create a new one, start the server with it, and save every IMAP password again.
- **FR-044**: Operator documentation MUST state what collection guarantees and what it doesn't: it never changes the mailbox; it collects only the chosen folders, and a folder created later needs adding; messages removed from the mailbox stay archived; messages deleted before a sync saw them are not archived; Microsoft 365 isn't supported because it requires OAuth 2.0; and a server certificate must be trusted by the host, with how to add a private issuer. It MUST give the sync schedule, timeouts, retry waits, the new log categories, the new startup cause, and the log entry for a server started without a key.

### Key Entities *(include if feature involves data)*

- **Source**: An IMAP mailbox an administrator added: a name, a server, a port, connection security, a user name, an encrypted password, its chosen folders, and its state. A removed source loses its password and keeps the rest for message history.
- **Folder checkpoint**: For one source and folder, the server's numbering of the folder (UIDVALIDITY) and the point through which every message is archived or recorded as not archived.
- **Collection history entry**: A kind of Slice 1 source observation. It records the source, the folder, the time the server reports receiving the message, and the time of collection. There is at most one for each source, folder, and message.
- **Message waiting for retry**: A message the server failed to return, identified by source, folder, and its position in the folder, with its size and server time. It holds no message content.
- **Message not archived**: A message that was too large or was removed before it could be archived, with the same fields and a reason.
- **Sync attempt**: When a sync ran, how it ended, how many messages it archived, and for a failure its category and the server's reply. The reply appears only on the sources screen.
- **Key**: One per installation, outside the data directory. It encrypts every stored IMAP password.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Following the documentation on a fresh install, an operator and an administrator get from creating the key to seeing the first collected message in the list in under 10 minutes.
- **SC-002**: When the server is stopped at each step of collecting a message and restarted, 100% of the messages on the test server are listed and byte-identical after the next sync, each has exactly one history entry per source and folder, and storage holds one copy of each distinct message.
- **SC-003**: Across repeated syncs, a renumbered folder, the same bytes in two folders, two copies in one folder, and the same bytes imported by hand, the number of history entries and stored copies matches the expected count exactly.
- **SC-004**: At every point sampled during a sync with injected stalls and failures, the archived count shown equals the number of messages from that sync that are durably stored.
- **SC-005**: For each stall point, the sync ends within 70 seconds of the last data received, the source shows Timed out, the checkpoint is unchanged, and the next sync archives the stalled message.
- **SC-006**: None of the marker passwords appears in a copy of the data directory, the log, the status screen, or any response sent to the browser. Started with a different key, the server recovers none of them, and each source shows Password can't be read.
- **SC-007**: None of the requests without a valid session (no session, expired, signed out, forged cross-site) reads source details or changes a source, across every source operation.
- **SC-008**: After a full sync of a test mailbox, its messages, flags, and folders are identical to before.
- **SC-009**: On a source whose previous sync took under 30 seconds, a new message in a chosen folder appears in the list within 6 minutes in every trial.
- **SC-010**: Every failure category in FR-035, when forced, appears on the sources screen, on the status screen, and in the log by the next page refresh, and the problem count includes it. No marker string for a password, user name, folder name, server reply, or message content appears on the status screen or in the log.
- **SC-011**: Adding a source and following its first sync can be done by keyboard alone, and at 360 px wide with no horizontal page scrolling.

### Issue #4 acceptance criteria

| Issue criterion                                                                   | Requirements                   | Outcome        |
| --------------------------------------------------------------------------------- | ------------------------------ | -------------- |
| Interrupting sync and restarting does not skip safely available messages          | FR-008, FR-012, FR-015, FR-024 | SC-002         |
| Re-sync avoids incorrect duplicates                                               | FR-010, FR-013, FR-014         | SC-003         |
| Progress reflects stored messages rather than merely fetched headers              | FR-034                         | SC-004         |
| Stalled network calls time out, expose the failure, and leave the checkpoint safe | FR-020, FR-021, FR-035         | SC-005, SC-010 |
| A copy of the data directory without the key does not reveal IMAP passwords       | FR-025 to FR-028               | SC-006         |
| A user can complete the workflow through the UI                                   | FR-001, FR-002, FR-033         | SC-001         |

## Assumptions

Each choice below fills a gap in the issue. The numbers are starting values chosen for this spec, not measurements. None is configurable until someone needs it to be.

- **Decided design**: Issue #4, [the architecture principles](../../docs/ARCHITECTURE.md), and [ADR 0003](../../docs/adr/0003-mail-libraries.md) fix the IMAP library and its version, the rule that a message is stored before the checkpoint moves, the capture order, and that only IMAP passwords are encrypted, with a key outside the data directory. The plan carries them, and this spec states only the behavior they must produce.
- **Chosen folders**: The administrator picks folders, so one source covers a journal mailbox (inbox only), a business mailbox (inbox, Sent, and folders that server rules fill), and Gmail (All Mail only, so each message downloads once instead of once per label). Collecting every folder automatically was rejected: it would archive Trash and Junk, and repeat downloads on Gmail can hit its daily IMAP limit. The cost is that a folder created later isn't collected until an administrator adds it, and mail that server rules file there in the meantime is missed.
- **Schedule**: A sync every 5 minutes, counted from the end of the previous one, with no push from the server. New mail therefore waits up to about 5 minutes. IMAP IDLE would shorten that, at the cost of a connection held open per source.
- **Retry waits**: 1 minute, doubling to 60 minutes. Folder and message problems are retried at each scheduled sync instead, so one bad folder or message doesn't slow the rest of the source.
- **Timeouts**: 30 seconds to connect and finish the handshake, then 60 seconds without data for any later step. A slow transfer that keeps sending data is not cut off, so a large message on a slow link still arrives.
- **Rejected sign-in**: Collection stops instead of retrying, because many providers lock an account after repeated failed sign-ins. The cost is that a provider outage reported as a wrong password needs an administrator to press Sync now.
- **Certificates**: The server's certificate must be trusted by the host. There is no exception or pinning in this slice. An on-premises server with a private certificate needs its issuer added to the host's trusted certificates, which the documentation explains.
- **Fixed server and user name**: Changing either would point the source at a different mailbox while keeping checkpoints from the old one, which could skip messages. A new source collects everything again, adding history entries but no stored copies.
- **Read-only access**: MailArkiv never changes the mailbox, so users see no effect of archiving, and an archive bug can't damage mail on the server.
- **What scheduled collection can't promise**: Mail deleted from the mailbox before a sync saw it is not archived, as rule 7 in [the architecture principles](../../docs/ARCHITECTURE.md) states. A user who deletes mail within minutes of its arrival, or during an outage, can keep it out of the archive.
- **One key per installation**: The operator creates it on the host, like an account. Replacing the key means saving every IMAP password again. The key protects copies of the data directory, not a server where someone can read both the key and the data.
- **Size limit**: 150,000,000 bytes, the same as imports. Most mail servers refuse messages far smaller than that.
- **Server addresses**: Administrators are trusted to choose servers, including local ones, because a mail server on the same host is a valid source. MailArkiv doesn't restrict which addresses it connects to.
- **Providers**: Whether a given hosted provider still accepts password sign-in for IMAP isn't verified here. The documentation lists the servers that were actually tested. Microsoft 365 doesn't accept it.
- **Scale**: Up to 50 sources, one per person at the product's target size, each holding at most one connection to its server.
- **List columns**: Until Slice 3 adds sender, subject, and date, a collected message's row names its source and folder.
- **API**: As in Slice 1, the server's request interface serves the web interface and is not a published contract. The web interface meets the issue's "UI or documented API" condition. Direct requests are still tested, because they are the path an attacker would use.
- **Visual design**: `DESIGN.md` owns layout, type, color, and control labels. This spec sets behavior and the wording of states and failures.
- **Plan inputs**: The plan ranks credible failures by likelihood and harm and names each required check and its layer. Tests use a test double for the IMAP server, to stall, disconnect, renumber folders, and fail single messages on demand, and use the real catalog and storage. Collection is part of normal server startup. Administrators reach it through Sources in the main navigation. MailArkiv is a greenfield application with no users yet; the plan records the exception to constitution VI. Test fixtures use synthetic mail only.

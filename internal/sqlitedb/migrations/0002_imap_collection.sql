-- IMAP collection. Times are integer Unix milliseconds in UTC, as in 0001.
-- The runner turns foreign keys off around this file and checks every
-- reference before the commit, because source_observation is rebuilt below.

CREATE TABLE imap_source (
    -- Rows are never deleted, so IDs are never reused.
    id                    INTEGER PRIMARY KEY,
    -- 1 to 100 characters, counted as Unicode code points, after trimming
    -- spaces. collect enforces it.
    name                  TEXT NOT NULL,
    -- Lowercase. Fixed after saving.
    host                  TEXT NOT NULL,
    port                  INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),
    security              TEXT NOT NULL CHECK (security IN ('tls', 'starttls')),
    -- As entered, because servers may compare it exactly. Fixed after saving.
    user_name             TEXT NOT NULL,
    -- strings.ToLower(user_name), for the one-source-per-mailbox rule.
    user_key              TEXT NOT NULL,
    -- AES-256-GCM output under the key file: nonce, ciphertext, and tag.
    -- NULL exactly when removed_at is set.
    sealed_password       BLOB,
    created_at            INTEGER NOT NULL,
    paused_at             INTEGER,
    sign_in_rejected_at   INTEGER,
    -- Failed syncs in a row. Sets the retry wait.
    failed_attempts       INTEGER NOT NULL DEFAULT 0 CHECK (failed_attempts >= 0),
    failing_since         INTEGER,
    last_failure_category TEXT CHECK (last_failure_category IN (
        'server_not_reachable', 'secure_connection_failed', 'sign_in_rejected', 'timed_out',
        'server_error', 'storage_full', 'storage_write_failed', 'catalog_write_failed')),
    last_failure_at       INTEGER,
    -- At most 1,000 characters, keeping the start and end. NULL when there
    -- was none.
    last_server_reply     TEXT,
    last_success_at       INTEGER,
    last_success_archived INTEGER,
    removed_at            INTEGER,
    CHECK ((sealed_password IS NULL) = (removed_at IS NOT NULL)),
    -- The three failure columns are NULL exactly when failed_attempts is 0.
    CHECK ((failing_since IS NULL) = (failed_attempts = 0)),
    CHECK ((last_failure_category IS NULL) = (failed_attempts = 0)),
    CHECK ((last_failure_at IS NULL) = (failed_attempts = 0))
) STRICT;

-- One active source per mailbox. A removed source keeps its row, so the same
-- mailbox can be added again.
CREATE UNIQUE INDEX imap_source_once_per_mailbox ON imap_source (host, user_key) WHERE removed_at IS NULL;

CREATE TABLE imap_folder (
    id                INTEGER PRIMARY KEY,
    source_id         INTEGER NOT NULL REFERENCES imap_source (id),
    -- Decoded from modified UTF-7. Valid UTF-8, 1 to 1,000 bytes.
    name              TEXT NOT NULL,
    chosen            INTEGER NOT NULL CHECK (chosen IN (0, 1)),
    -- NULL until the folder is first opened.
    uid_validity      INTEGER,
    -- Every UID up to this one, under uid_validity, is archived or recorded in
    -- imap_message_problem. 0 while uid_validity is NULL.
    last_uid          INTEGER NOT NULL DEFAULT 0 CHECK (uid_validity IS NOT NULL OR last_uid = 0),
    renumbered_at     INTEGER,
    unavailable_since INTEGER,
    UNIQUE (source_id, name)
) STRICT;

-- A message waiting for retry or not archived. Holds no message content.
CREATE TABLE imap_message_problem (
    folder_id    INTEGER NOT NULL REFERENCES imap_folder (id),
    uid_validity INTEGER NOT NULL,
    uid          INTEGER NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('waiting_for_retry', 'too_large', 'removed')),
    -- NULL when the server reported neither RFC822.SIZE nor a literal.
    size         INTEGER,
    -- NULL when the server reported no INTERNALDATE.
    received_at  INTEGER,
    recorded_at  INTEGER NOT NULL,
    PRIMARY KEY (folder_id, uid_validity, uid)
) STRICT;

CREATE TABLE collected_message_count (
    imap_source_id INTEGER PRIMARY KEY REFERENCES imap_source (id),
    -- Distinct messages with at least one entry from this source.
    messages       INTEGER NOT NULL
) STRICT;

-- Widen source_observation to hold collections. SQLite can't change a CHECK
-- or a NOT NULL in place, so the table is rebuilt. message.latest_observation_id
-- keeps its reference text, which then names the new table.
CREATE TABLE source_observation_new (
    id             INTEGER PRIMARY KEY,
    message_id     INTEGER NOT NULL REFERENCES message (id),
    source_type    TEXT NOT NULL CHECK (source_type IN ('manual_import', 'imap_collection')),
    -- Import time, or collection time.
    observed_at    INTEGER NOT NULL,
    -- Imports only: the account's lowercase address, copied when the import
    -- is recorded.
    account_email  TEXT,
    -- Imports only. NULL means no name was supplied. Stored as received, in
    -- valid UTF-8.
    file_name      TEXT,
    imap_source_id INTEGER REFERENCES imap_source (id),
    imap_folder_id INTEGER REFERENCES imap_folder (id),
    -- Collections only. NULL when the server reported no INTERNALDATE.
    received_at    INTEGER,
    CHECK ((source_type = 'manual_import') = (account_email IS NOT NULL)),
    CHECK ((source_type = 'imap_collection') = (imap_source_id IS NOT NULL)),
    CHECK ((source_type = 'imap_collection') = (imap_folder_id IS NOT NULL)),
    CHECK (file_name IS NULL OR source_type = 'manual_import'),
    CHECK (received_at IS NULL OR source_type = 'imap_collection')
) STRICT;

-- Slice 1 recorded only imports.
INSERT INTO source_observation_new (id, message_id, source_type, observed_at, account_email, file_name)
SELECT id, message_id, source_type, observed_at, account_email, file_name
FROM source_observation
WHERE source_type = 'manual_import';

DROP TABLE source_observation;
ALTER TABLE source_observation_new RENAME TO source_observation;

CREATE INDEX source_observation_by_message ON source_observation (message_id, id);

-- At most one entry per folder and message, so collecting a message again
-- adds nothing.
CREATE UNIQUE INDEX source_observation_once_per_folder
    ON source_observation (imap_folder_id, message_id) WHERE imap_folder_id IS NOT NULL;

-- Whether an entry is a source's first for a message, for the per-source count.
CREATE INDEX source_observation_by_source
    ON source_observation (imap_source_id, message_id) WHERE imap_source_id IS NOT NULL;

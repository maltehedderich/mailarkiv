-- Search and preview. Times are integer Unix milliseconds in UTC, as in 0001.
-- The runner turns foreign keys off around this file and checks every
-- reference before the commit, because message is rebuilt below.

-- Add the dates and processing columns to message. SQLite can't add a NOT
-- NULL column without a default in place, so the table is rebuilt.
-- source_observation keeps its reference text, which then names the new table.
CREATE TABLE message_new (
    -- Never reused, so a stale ID in a log or a failure report can't name another message.
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Lowercase hex SHA-256. Identifies the message.
    content_hash          TEXT NOT NULL UNIQUE,
    -- Bytes, from 1 to 150,000,000.
    size                  INTEGER NOT NULL,
    -- Set in the same transaction that creates the row, so committed rows are never NULL.
    latest_observation_id INTEGER REFERENCES source_observation (id),
    -- Time of the message's first entry. Set at capture and never changed.
    first_archived_at     INTEGER NOT NULL,
    -- Set to first_archived_at at capture. Processing sets it once.
    list_date             INTEGER NOT NULL,
    processing_state      TEXT NOT NULL CHECK (processing_state IN (
        'not_processed', 'processed', 'processed_with_problems', 'unreadable')),
    -- Assigned as the highest existing value plus 1 when processing records
    -- its result, so values have no gaps. NULL exactly when processing_state
    -- is not_processed.
    processed_seq         INTEGER,
    CHECK ((processing_state = 'not_processed') = (processed_seq IS NULL))
) STRICT;

-- Every message starts as not processed, dated by its first entry. A message
-- without an entry would break the NOT NULL rule and stop the migration,
-- instead of being left out.
INSERT INTO message_new (id, content_hash, size, latest_observation_id, first_archived_at, list_date, processing_state)
SELECT m.id, m.content_hash, m.size, m.latest_observation_id, first.observed_at, first.observed_at, 'not_processed'
FROM message m
LEFT JOIN (SELECT message_id, MIN(observed_at) AS observed_at FROM source_observation GROUP BY message_id) first
    ON first.message_id = m.id;

-- Carry the sequence over, so an ID above the highest row is never handed out again.
DELETE FROM sqlite_sequence WHERE name = 'message_new';
INSERT INTO sqlite_sequence (name, seq) SELECT 'message_new', seq FROM sqlite_sequence WHERE name = 'message';

-- Dropping message also drops message_by_latest_observation. The list now
-- uses message_list_order.
DROP TABLE message;
ALTER TABLE message_new RENAME TO message;

CREATE INDEX message_list_order ON message (list_date DESC, first_archived_at DESC, id DESC);

CREATE INDEX message_with_problems ON message (list_date DESC, first_archived_at DESC, id DESC)
    WHERE processing_state IN ('processed_with_problems', 'unreadable');

CREATE UNIQUE INDEX message_by_processed_seq ON message (processed_seq) WHERE processed_seq IS NOT NULL;

-- Processing takes the newest capture first.
CREATE INDEX message_not_processed ON message (id DESC) WHERE processing_state = 'not_processed';

-- One row for each message that is processed or processed_with_problems,
-- written in the same transaction as its state. mailparse enforces the
-- character limits.
CREATE TABLE message_summary (
    message_id      INTEGER PRIMARY KEY REFERENCES message (id),
    -- The first From address's display name, decoded. NULL when there is
    -- none, or when it equals the address ignoring case. At most 500
    -- characters.
    sender_name     TEXT,
    -- The first From address. At most 320 characters. NULL when there is no
    -- From header, or when From can't be read as addresses.
    sender_address  TEXT,
    -- Further From addresses, for "and N more".
    more_senders    INTEGER NOT NULL CHECK (more_senders >= 0),
    -- Decoded. At most 1,000 characters. NULL when there is no Subject.
    subject         TEXT,
    -- The Date header's decoded text, at most 200 characters. NULL when
    -- there is none.
    date_header     TEXT,
    -- NULL when list_date is the Date header's time.
    date_reason     TEXT CHECK (date_reason IN ('missing', 'unreadable', 'ahead')),
    -- 1 when the message lists an attachment that isn't an embedded image.
    has_attachments INTEGER NOT NULL CHECK (has_attachments IN (0, 1))
) STRICT;

-- Written with the message's result and never changed.
CREATE TABLE message_problem (
    message_id    INTEGER NOT NULL REFERENCES message (id),
    -- The part number, or an empty string for the whole message.
    position      TEXT NOT NULL,
    -- The part's declared media type, lowercased, without parameters. At
    -- most 255 characters. NULL when the part declares none.
    declared_type TEXT,
    kind          TEXT NOT NULL CHECK (kind IN (
        'unknown_charset', 'invalid_encoding', 'malformed_header', 'malformed_structure', 'cut_short',
        'too_many_parts', 'nested_too_deeply', 'body_cut', 'took_too_long')),
    PRIMARY KEY (message_id, position, kind)
) STRICT;

CREATE TABLE search_rebuild (
    id         INTEGER PRIMARY KEY,
    started_by INTEGER NOT NULL REFERENCES account (id),
    started_at INTEGER NOT NULL,
    -- NULL while it runs. A restart doesn't end a rebuild.
    ended_at   INTEGER,
    -- NULL while it runs.
    outcome    TEXT CHECK (outcome IN ('finished', 'failed')),
    CHECK ((ended_at IS NULL) = (outcome IS NULL))
) STRICT;

-- One row for each index that exists or is being built.
CREATE TABLE search_generation (
    -- Also the directory name under index/. Never reused.
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    role           TEXT NOT NULL CHECK (role IN ('active', 'building')),
    -- 1 in this slice. The search process refuses to open another version.
    schema_version INTEGER NOT NULL,
    -- The highest processed_seq saved in this index. Written only after the
    -- search process confirms a commit.
    cursor         INTEGER NOT NULL DEFAULT 0,
    -- NULL for a build that started because no index existed.
    rebuild_id     INTEGER REFERENCES search_rebuild (id),
    created_at     INTEGER NOT NULL
) STRICT;

-- At most one active and one building generation.
CREATE UNIQUE INDEX search_generation_one_per_role ON search_generation (role);

-- Present while the index is marked Needs rebuild. It survives restarts, and
-- only a finished rebuild deletes it.
CREATE TABLE search_needs_rebuild (
    id       INTEGER PRIMARY KEY CHECK (id = 1),
    category TEXT NOT NULL CHECK (category IN (
        'search_process_stopped', 'index_maintenance_failed', 'index_damaged')),
    since    INTEGER NOT NULL
) STRICT;

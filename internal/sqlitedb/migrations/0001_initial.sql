-- Times are integer Unix milliseconds in UTC. STRICT makes SQLite refuse a
-- value of the wrong type instead of storing it.

CREATE TABLE account (
    id            INTEGER PRIMARY KEY,
    -- strings.ToLower of the address given at creation. The only stored form.
    email         TEXT NOT NULL UNIQUE,
    -- Argon2id in PHC format.
    password_hash TEXT NOT NULL,
    created_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE session (
    -- SHA-256 of the cookie token.
    token_hash BLOB NOT NULL PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES account (id),
    created_at INTEGER NOT NULL,
    -- created_at plus 12 hours, never extended.
    expires_at INTEGER NOT NULL
) STRICT;

CREATE TABLE sign_in_failure (
    -- SHA-256 of the lowercased submitted address.
    address_key BLOB NOT NULL,
    failed_at   INTEGER NOT NULL
) STRICT;

CREATE INDEX sign_in_failure_by_address ON sign_in_failure (address_key, failed_at);

CREATE TABLE sign_in_block (
    address_key   BLOB NOT NULL PRIMARY KEY,
    blocked_until INTEGER NOT NULL
) STRICT;

CREATE TABLE message (
    -- Never reused, so a stale ID in a log or a failure report can't name another message.
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Lowercase hex SHA-256. Identifies the message.
    content_hash          TEXT NOT NULL UNIQUE,
    -- Bytes, from 1 to 150,000,000.
    size                  INTEGER NOT NULL,
    -- Set in the same transaction that creates the row, so committed rows are never NULL.
    latest_observation_id INTEGER REFERENCES source_observation (id)
) STRICT;

-- The list shows the most recent import first.
CREATE INDEX message_by_latest_observation ON message (latest_observation_id DESC);

CREATE TABLE source_observation (
    id            INTEGER PRIMARY KEY,
    message_id    INTEGER NOT NULL REFERENCES message (id),
    source_type   TEXT NOT NULL CHECK (source_type IN ('manual_import')),
    observed_at   INTEGER NOT NULL,
    -- The account's lowercase address, copied when the import is recorded.
    account_email TEXT NOT NULL,
    -- NULL means no name was supplied. Stored as received, in valid UTF-8.
    file_name     TEXT
) STRICT;

CREATE INDEX source_observation_by_message ON source_observation (message_id, id);

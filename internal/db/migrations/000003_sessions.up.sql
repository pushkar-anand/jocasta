-- Session storage for the web UI, written and read only by the scs SQLite
-- store in build-with-go (security/session/sqlitestore). No jocasta query
-- touches this table, so its shape is the store's contract, not ours: expiry
-- is a Unix nanosecond count, unlike the ISO-8601 text timestamps elsewhere.
CREATE TABLE IF NOT EXISTS sessions
(
    token  TEXT    PRIMARY KEY,
    data   BLOB    NOT NULL,
    expiry INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expiry);

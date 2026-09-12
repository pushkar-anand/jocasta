ALTER TABLE users
    ADD COLUMN totp_secret TEXT;
ALTER TABLE users
    ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0 CHECK (totp_enabled IN (0, 1));
ALTER TABLE users
    ADD COLUMN totp_confirmed_at TEXT;

-- Single-use codes for an account locked out of its authenticator. Cascades
-- with the user, the same as api_tokens.
CREATE TABLE user_recovery_codes
(
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT    NOT NULL UNIQUE,
    created_at TEXT    NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now')),
    used_at    TEXT
);

CREATE INDEX idx_user_recovery_codes_user_id ON user_recovery_codes (user_id);

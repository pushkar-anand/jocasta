-- name: CreateUser :one
INSERT INTO users (username, password_hash, role)
VALUES (?, ?, ?)
RETURNING *;

-- name: CreateFirstUser :one
-- Inserts the admin only into an empty table, checked and written in one
-- statement, so setup run twice at once makes one account. No row comes back
-- when an account already exists.
INSERT INTO users (username, password_hash, role)
SELECT @username, @password_hash, 'admin'
WHERE NOT EXISTS (SELECT 1 FROM users)
RETURNING *;

-- name: GetUserByUsername :one
SELECT *
FROM users
WHERE username = ?;

-- name: GetUserByID :one
SELECT *
FROM users
WHERE id = ?;

-- name: CountUsers :one
SELECT COUNT(*)
FROM users;

-- name: ListUsers :many
SELECT *
FROM users
ORDER BY created_at, id;

-- name: SetUserTOTPSecret :execrows
-- totp_enabled = 0 keeps the secret of an account with 2FA on out of reach of
-- a session, which has not shown the password. No row changes then.
UPDATE users
SET totp_secret = ?
WHERE id = ?
  AND totp_enabled = 0;

-- name: EnableUserTOTP :execrows
-- totp_enabled = 0 makes a second confirmation, from another tab, change no
-- row, so only one of them mints recovery codes.
UPDATE users
SET totp_enabled      = 1,
    totp_confirmed_at = ?
WHERE id = ?
  AND totp_enabled = 0;

-- name: DisableUserTOTP :exec
UPDATE users
SET totp_enabled      = 0,
    totp_secret       = NULL,
    totp_confirmed_at = NULL
WHERE id = ?;

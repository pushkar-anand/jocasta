-- name: CreateUser :one
INSERT INTO users (username, password_hash, role)
VALUES (?, ?, ?)
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
ORDER BY created_at;

-- name: SetUserTOTPSecret :exec
UPDATE users
SET totp_secret = ?
WHERE id = ?;

-- name: EnableUserTOTP :exec
UPDATE users
SET totp_enabled      = 1,
    totp_confirmed_at = ?
WHERE id = ?;

-- name: DisableUserTOTP :exec
UPDATE users
SET totp_enabled      = 0,
    totp_secret       = NULL,
    totp_confirmed_at = NULL
WHERE id = ?;

-- name: CreateAPIToken :one
INSERT INTO api_tokens (user_id, name, token_hash, scope, expires_at)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: ListAPITokensByUser :many
SELECT *
FROM api_tokens
WHERE user_id = ?
ORDER BY created_at DESC, id DESC;

-- name: TouchAPITokenByHash :one
-- Runs on every API request: finding the row and recording its use in one
-- statement keeps that cost to a single round trip. An expired token matches
-- no row, the same as a revoked one.
UPDATE api_tokens
SET last_used_at = sqlc.arg(now)
WHERE token_hash = sqlc.arg(token_hash)
  AND (expires_at IS NULL OR expires_at > sqlc.arg(now))
RETURNING *;

-- name: DeleteAPIToken :exec
DELETE
FROM api_tokens
WHERE id = ?
  AND user_id = ?;

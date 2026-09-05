-- name: CreateAPIToken :one
INSERT INTO api_tokens (user_id, name, token_hash, scope)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: ListAPITokensByUser :many
SELECT *
FROM api_tokens
WHERE user_id = ?
ORDER BY created_at DESC;

-- name: TouchAPITokenByHash :one
-- Runs on every API request: finding the row and recording its use in one
-- statement keeps that cost to a single round trip.
UPDATE api_tokens
SET last_used_at = ?
WHERE token_hash = ?
RETURNING *;

-- name: DeleteAPIToken :exec
DELETE
FROM api_tokens
WHERE id = ?
  AND user_id = ?;

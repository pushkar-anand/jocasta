-- name: CreateRecoveryCode :one
INSERT INTO user_recovery_codes (user_id, code_hash)
VALUES (?, ?)
RETURNING *;

-- name: DeleteRecoveryCodesByUser :exec
DELETE
FROM user_recovery_codes
WHERE user_id = ?;

-- name: RedeemRecoveryCode :one
-- used_at IS NULL in the WHERE clause is what makes redemption single-use
-- atomically: a second attempt at the same code finds no row rather than a
-- race against a read-then-write.
UPDATE user_recovery_codes
SET used_at = ?
WHERE user_id = ?
  AND code_hash = ?
  AND used_at IS NULL
RETURNING *;

-- name: CountUnusedRecoveryCodesByUser :one
SELECT COUNT(*)
FROM user_recovery_codes
WHERE user_id = ?
  AND used_at IS NULL;

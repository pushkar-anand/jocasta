-- name: CreateUser :one
INSERT INTO users(username, password_hash)
VALUES (?, ?)
RETURNING *;

-- name: GetUserByUsername :one
SELECT *
FROM users
WHERE username = ?;

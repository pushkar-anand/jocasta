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

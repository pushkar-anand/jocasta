-- name: NotifyRules :many
SELECT kind
FROM notify_rules
WHERE destination = ?
ORDER BY kind;

-- name: DeleteNotifyRules :exec
DELETE
FROM notify_rules
WHERE destination = ?;

-- name: CreateNotifyRule :exec
INSERT INTO notify_rules (destination, kind)
VALUES (?, ?);

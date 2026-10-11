-- name: UpsertDNS :exec
-- One read adds to the hour's running counts.
INSERT INTO dns_hourly (source_id, device_id, hour, name, lookups, blocked, failed)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (device_id, hour, source_id, name) DO UPDATE
    SET lookups = lookups + excluded.lookups,
        blocked = blocked + excluded.blocked,
        failed  = failed + excluded.failed;

-- name: DeleteDNSBefore :execrows
DELETE
FROM dns_hourly
WHERE hour < ?;

-- name: MoveDNS :exec
-- A folded device's hours follow it. An hour both rows recorded for the same
-- name holds lookups from two of the device's addresses, so the counts add up.
INSERT INTO dns_hourly (source_id, device_id, hour, name, lookups, blocked, failed)
SELECT ghost.source_id, sqlc.arg(into_id), ghost.hour, ghost.name, ghost.lookups, ghost.blocked, ghost.failed
FROM dns_hourly ghost
WHERE ghost.device_id = sqlc.arg(from_id)
ON CONFLICT (device_id, hour, source_id, name) DO UPDATE
    SET lookups = lookups + excluded.lookups,
        blocked = blocked + excluded.blocked,
        failed  = failed + excluded.failed;

-- name: UpsertDNSAnswer :exec
-- One read adds to how often a name was answered with an address.
INSERT INTO dns_answers (device_id, addr, name, lookups, last_hour)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (device_id, addr, name) DO UPDATE
    SET lookups   = lookups + excluded.lookups,
        last_hour = MAX(last_hour, excluded.last_hour);

-- name: DeleteDNSAnswersBefore :execrows
DELETE
FROM dns_answers
WHERE last_hour < ?;

-- name: MoveDNSAnswers :exec
-- A folded device's answers follow it, adding up with the answers it shares
-- with the device it was folded into.
INSERT INTO dns_answers (device_id, addr, name, lookups, last_hour)
SELECT sqlc.arg(into_id), ghost.addr, ghost.name, ghost.lookups, ghost.last_hour
FROM dns_answers ghost
WHERE ghost.device_id = sqlc.arg(from_id)
ON CONFLICT (device_id, addr, name) DO UPDATE
    SET lookups   = lookups + excluded.lookups,
        last_hour = MAX(last_hour, excluded.last_hour);

-- name: SourceCursor :one
SELECT c.position
FROM source_cursors c
         JOIN sources s ON s.id = c.source_id
WHERE s.name = ?;

-- name: SetSourceCursor :exec
INSERT INTO source_cursors (source_id, position)
VALUES (?, ?)
ON CONFLICT (source_id) DO UPDATE SET position = excluded.position;

-- name: DeviceDNS :many
-- The names one device looked up since a given hour, busiest first, summed
-- across sources: each node of a resolver cluster answers its own share of a
-- device's lookups.
SELECT name,
       CAST(SUM(lookups) AS INTEGER) AS lookups,
       CAST(SUM(blocked) AS INTEGER) AS blocked,
       CAST(SUM(failed) AS INTEGER)  AS failed,
       CAST(MAX(hour) AS TEXT)       AS last_hour
FROM dns_hourly
WHERE device_id = ?
  AND hour >= ?
GROUP BY name
ORDER BY SUM(lookups) DESC, name
LIMIT ?;

-- name: DeviceDNSTotals :one
-- What one device looked up since a given hour, added up over every name,
-- including those past DeviceDNS's limit.
SELECT CAST(COALESCE(SUM(lookups), 0) AS INTEGER) AS lookups,
       CAST(COUNT(DISTINCT name) AS INTEGER)      AS names,
       CAST(COALESCE(SUM(blocked), 0) AS INTEGER) AS blocked,
       CAST(COALESCE(SUM(failed), 0) AS INTEGER)  AS failed
FROM dns_hourly
WHERE device_id = ?
  AND hour >= ?;

-- name: AnyDNSRead :one
-- Whether any resolver's log has been read, which is how a view tells "this
-- device looked nothing up" from "nothing reads a resolver".
SELECT CAST(EXISTS (SELECT 1
                    FROM source_cursors c
                             JOIN sources s ON s.id = c.source_id
                    WHERE s.kind = 'DNS') AS INTEGER) AS read;

-- name: DeviceDNSAnswers :many
-- The addresses one device's lookups were answered with since a given hour,
-- each address's names most looked up first.
SELECT addr, name
FROM dns_answers
WHERE device_id = ?
  AND last_hour >= ?
ORDER BY addr, lookups DESC, name;

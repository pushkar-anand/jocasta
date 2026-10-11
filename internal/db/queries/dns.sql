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

-- name: SourceCursor :one
SELECT c.position
FROM source_cursors c
         JOIN sources s ON s.id = c.source_id
WHERE s.name = ?;

-- name: SetSourceCursor :exec
INSERT INTO source_cursors (source_id, position)
VALUES (?, ?)
ON CONFLICT (source_id) DO UPDATE SET position = excluded.position;

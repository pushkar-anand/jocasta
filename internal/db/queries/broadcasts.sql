-- name: UpsertBroadcast :exec
-- One flush adds to the hour's totals for a device, group and port.
INSERT INTO broadcasts_hourly (source_id, device_id, hour, dst_ip, kind, protocol, port, bytes, packets)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (device_id, hour, source_id, dst_ip, protocol, port) DO UPDATE
    SET bytes   = bytes + excluded.bytes,
        packets = packets + excluded.packets;

-- name: DeleteBroadcastsBefore :execrows
DELETE
FROM broadcasts_hourly
WHERE hour < ?;

-- name: DeviceBroadcasts :many
-- What one device sent to everyone since a given hour, one row per group,
-- protocol and port, most packets first.
SELECT CAST(dst_ip AS TEXT)          AS dst_ip,
       CAST(MAX(kind) AS TEXT)       AS kind,
       protocol,
       port,
       CAST(SUM(bytes) AS INTEGER)   AS bytes,
       CAST(SUM(packets) AS INTEGER) AS packets,
       CAST(MAX(hour) AS TEXT)       AS last_hour
FROM broadcasts_hourly
WHERE device_id = ?
  AND hour >= ?
GROUP BY dst_ip, protocol, port
ORDER BY SUM(packets) DESC, dst_ip, protocol, port;

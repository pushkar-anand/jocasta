-- name: UpsertTraffic :exec
-- One flush adds to the hour's running totals. The peer's identity columns
-- take the newest non-empty answer: a name that failed to resolve on one
-- flush should not erase one that resolved on the last.
INSERT INTO traffic_hourly (source_id, device_id, hour, peer_device_id, peer_ip, peer_name, peer_asn,
                            protocol, service_port, bytes_out, bytes_in, packets_out, packets_in, connections)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (device_id, hour, source_id, peer_ip, protocol, service_port) DO UPDATE
    SET peer_device_id = COALESCE(excluded.peer_device_id, peer_device_id),
        peer_name      = COALESCE(excluded.peer_name, peer_name),
        peer_asn       = COALESCE(excluded.peer_asn, peer_asn),
        bytes_out      = bytes_out + excluded.bytes_out,
        bytes_in       = bytes_in + excluded.bytes_in,
        packets_out    = packets_out + excluded.packets_out,
        packets_in     = packets_in + excluded.packets_in,
        connections    = connections + excluded.connections;

-- name: DeleteTrafficBefore :execrows
DELETE
FROM traffic_hourly
WHERE hour < ?;

-- name: DeviceTraffic :many
-- One device's conversations since a given hour, one row per peer and
-- service, busiest first. A peer that was a known device carries that
-- device's naming fields so the page can link to it; the grouping keeps an
-- address that changed hands between two devices as two peers.
SELECT CAST(COALESCE(t.peer_device_id, 0) AS INTEGER)    AS peer_device_id,
       CAST(t.peer_ip AS TEXT)                           AS peer_ip,
       CAST(COALESCE(MAX(t.peer_name), '') AS TEXT)      AS peer_name,
       CAST(COALESCE(MAX(t.peer_asn), 0) AS INTEGER)     AS peer_asn,
       CAST(COALESCE(MAX(pd.label), '') AS TEXT)         AS peer_label,
       CAST(COALESCE(MAX(pd.hostname), '') AS TEXT)      AS peer_hostname,
       CAST(COALESCE(MAX(pd.mac), '') AS TEXT)           AS peer_mac,
       t.protocol,
       t.service_port,
       CAST(SUM(t.bytes_out) AS INTEGER)                 AS bytes_out,
       CAST(SUM(t.bytes_in) AS INTEGER)                  AS bytes_in,
       CAST(SUM(t.connections) AS INTEGER)               AS connections,
       CAST(MAX(t.hour) AS TEXT)                         AS last_hour
FROM traffic_hourly t
         LEFT JOIN devices pd ON pd.id = t.peer_device_id
WHERE t.device_id = ?
  AND t.hour >= ?
GROUP BY COALESCE(t.peer_device_id, 0), t.peer_ip, t.protocol, t.service_port
ORDER BY SUM(t.bytes_out + t.bytes_in) DESC, t.peer_ip, t.service_port
LIMIT ?;

-- name: AnyTraffic :one
-- Whether any traffic has been recorded at all, which is how a view tells
-- "nothing was exchanged" from "nothing is collecting".
SELECT CAST(EXISTS (SELECT 1 FROM traffic_hourly) AS INTEGER) AS recorded;

-- name: AttemptPorts :one
-- The ports already on record for one device, peer and hour, so a flush can
-- merge its own into them.
SELECT port_count, ports
FROM attempts_hourly
WHERE device_id = ?
  AND hour = ?
  AND source_id = ?
  AND peer_ip = ?
  AND protocol = ?;

-- name: UpsertAttempts :exec
-- One flush adds to the hour's attempt counts. The caller has already merged
-- the port sample with the one on record, so it replaces it; port_count only
-- ever grows, since a restarted process counts again from nothing.
INSERT INTO attempts_hourly (source_id, device_id, hour, peer_device_id, peer_ip, peer_asn,
                             protocol, attempts, answered, port_count, ports)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (device_id, hour, source_id, peer_ip, protocol) DO UPDATE
    SET peer_device_id = COALESCE(excluded.peer_device_id, peer_device_id),
        peer_asn       = COALESCE(excluded.peer_asn, peer_asn),
        attempts       = attempts + excluded.attempts,
        answered       = answered + excluded.answered,
        port_count     = MAX(port_count, excluded.port_count),
        ports          = excluded.ports;

-- name: DeleteAttemptsBefore :execrows
DELETE
FROM attempts_hourly
WHERE hour < ?;

-- name: DeviceAttempts :many
-- What one device tried to reach since a given hour, one row per peer and
-- protocol, most attempts first. ports is every hour's sample joined; the
-- caller merges them.
SELECT CAST(COALESCE(a.peer_device_id, 0) AS INTEGER) AS peer_device_id,
       CAST(a.peer_ip AS TEXT)                        AS peer_ip,
       CAST(COALESCE(MAX(a.peer_asn), 0) AS INTEGER)  AS peer_asn,
       CAST(COALESCE(MAX(pd.label), '') AS TEXT)      AS peer_label,
       CAST(COALESCE(MAX(pd.hostname), '') AS TEXT)   AS peer_hostname,
       CAST(COALESCE(MAX(pd.mac), '') AS TEXT)        AS peer_mac,
       a.protocol,
       CAST(SUM(a.attempts) AS INTEGER)               AS attempts,
       CAST(SUM(a.answered) AS INTEGER)               AS answered,
       CAST(MAX(a.port_count) AS INTEGER)             AS port_count,
       CAST(GROUP_CONCAT(a.ports, ',') AS TEXT)       AS ports,
       CAST(MAX(a.hour) AS TEXT)                      AS last_hour
FROM attempts_hourly a
         LEFT JOIN devices pd ON pd.id = a.peer_device_id
WHERE a.device_id = ?
  AND a.hour >= ?
GROUP BY COALESCE(a.peer_device_id, 0), a.peer_ip, a.protocol
ORDER BY SUM(a.attempts) DESC, a.peer_ip
LIMIT ?;

-- name: ProbingHours :many
-- Each hour a device tried many local addresses, or many ports on one of
-- them: what a scan of the home network looks like. Internet peers are left
-- out -- a device failing to reach a busy service's many addresses is not
-- scanning anything.
SELECT a.device_id,
       CAST(COALESCE(d.label, '') AS TEXT)    AS label,
       CAST(COALESCE(d.hostname, '') AS TEXT) AS hostname,
       CAST(COALESCE(d.mac, '') AS TEXT)      AS mac,
       CAST(a.hour AS TEXT)                   AS hour,
       CAST(COUNT(DISTINCT a.peer_ip) AS INTEGER) AS peers,
       CAST(MAX(a.port_count) AS INTEGER)     AS max_ports,
       CAST(SUM(a.attempts) AS INTEGER)       AS attempts,
       CAST(SUM(a.answered) AS INTEGER)       AS answered,
       CAST(GROUP_CONCAT(a.ports, ',') AS TEXT) AS ports
FROM attempts_hourly a
         JOIN devices d ON d.id = a.device_id
WHERE a.hour >= sqlc.arg(since)
  AND a.peer_asn IS NULL
  AND d.is_ignored = 0
  AND (CAST(sqlc.narg(group_name) AS TEXT) IS NULL OR d.group_name = CAST(sqlc.narg(group_name) AS TEXT))
GROUP BY a.device_id, a.hour
HAVING COUNT(DISTINCT a.peer_ip) >= CAST(sqlc.arg(min_peers) AS INTEGER)
    OR MAX(a.port_count) >= CAST(sqlc.arg(min_ports) AS INTEGER)
ORDER BY a.hour DESC, a.device_id;

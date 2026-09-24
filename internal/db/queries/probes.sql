-- name: ProbePorts :one
-- The ports already on record for one device, peer and hour, so a flush can
-- merge its own into them.
SELECT port_count, ports
FROM probes_hourly
WHERE device_id = ?
  AND hour = ?
  AND source_id = ?
  AND peer_ip = ?
  AND protocol = ?;

-- name: UpsertProbes :exec
-- One flush adds to the hour's probe counts. The caller has already merged the
-- port sample with the one on record, so it replaces it.
INSERT INTO probes_hourly (source_id, device_id, hour, peer_ip, peer_asn, protocol,
                           attempts, answered, port_count, ports, outside)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (device_id, hour, source_id, peer_ip, protocol) DO UPDATE
    SET peer_asn   = COALESCE(excluded.peer_asn, peer_asn),
        attempts   = attempts + excluded.attempts,
        answered   = answered + excluded.answered,
        port_count = MAX(port_count, excluded.port_count),
        ports      = excluded.ports,
        outside    = MAX(outside, excluded.outside);

-- name: AnswerProbes :exec
-- A device's answer that arrived a flush after the probe it answers.
UPDATE probes_hourly
SET answered = MIN(attempts, answered + CAST(sqlc.arg(late) AS INTEGER))
WHERE device_id = sqlc.arg(device_id)
  AND hour = sqlc.arg(hour)
  AND source_id = sqlc.arg(source_id)
  AND peer_ip = sqlc.arg(peer_ip)
  AND protocol = sqlc.arg(protocol);

-- name: DeleteProbesBefore :execrows
DELETE
FROM probes_hourly
WHERE hour < ?;

-- name: ProbedDevices :many
-- The devices the internet probed since a given hour, one row per device and
-- whether its outside address was what was tried, most probes first. ports is
-- every hour's sample joined; the caller merges them.
SELECT d.id,
       CAST(COALESCE(d.label, '') AS TEXT)        AS label,
       CAST(COALESCE(d.hostname, '') AS TEXT)     AS hostname,
       CAST(COALESCE(d.mac, '') AS TEXT)          AS mac,
       p.outside,
       CAST(COUNT(DISTINCT p.peer_ip) AS INTEGER) AS peers,
       CAST(COUNT(DISTINCT p.peer_asn) AS INTEGER) AS orgs,
       CAST(SUM(p.attempts) AS INTEGER)           AS attempts,
       CAST(SUM(p.answered) AS INTEGER)           AS answered,
       CAST(MAX(p.port_count) AS INTEGER)         AS max_ports,
       CAST(GROUP_CONCAT(p.ports, ',') AS TEXT)   AS ports,
       CAST(MAX(p.hour) AS TEXT)                  AS last_hour
FROM probes_hourly p
         JOIN devices d ON d.id = p.device_id
WHERE p.hour >= sqlc.arg(since)
  AND d.is_ignored = 0
  AND (CAST(sqlc.narg(group_name) AS TEXT) IS NULL OR d.group_name = CAST(sqlc.narg(group_name) AS TEXT))
GROUP BY d.id, p.outside
ORDER BY SUM(p.attempts) DESC, d.id;

-- name: UpsertOutsideAddress :exec
-- A router named this outside address again.
INSERT INTO outside_addresses (source_id, address, first_seen, last_seen)
VALUES (?, ?, ?, ?)
ON CONFLICT (source_id, address) DO UPDATE
    SET last_seen = MAX(last_seen, excluded.last_seen);

-- name: OutsideAddresses :many
-- The outside addresses any router named since a given moment.
SELECT DISTINCT CAST(address AS TEXT) AS address
FROM outside_addresses
WHERE last_seen >= ?
ORDER BY address;

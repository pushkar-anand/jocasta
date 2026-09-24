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
-- "nothing was exchanged" from "nothing is collecting". Attempts and
-- broadcasts count: a network whose only traffic so far is a scan, or
-- devices announcing themselves, is still being collected.
SELECT CAST(EXISTS (SELECT 1 FROM traffic_hourly)
    OR EXISTS (SELECT 1 FROM attempts_hourly)
    OR EXISTS (SELECT 1 FROM broadcasts_hourly) AS INTEGER) AS recorded;

-- name: BusiestDevices :many
-- The devices that moved the most data since a given hour. A conversation
-- between two devices counts toward both, since each of them did move it.
SELECT d.id,
       CAST(COALESCE(d.label, '') AS TEXT)    AS label,
       CAST(COALESCE(d.hostname, '') AS TEXT) AS hostname,
       CAST(COALESCE(d.mac, '') AS TEXT)      AS mac,
       CAST(SUM(t.bytes_out) AS INTEGER)      AS bytes_out,
       CAST(SUM(t.bytes_in) AS INTEGER)       AS bytes_in
FROM traffic_hourly t
         JOIN devices d ON d.id = t.device_id
WHERE t.hour >= sqlc.arg(since)
  AND d.is_ignored = 0
  AND (CAST(sqlc.narg(group_name) AS TEXT) IS NULL OR d.group_name = CAST(sqlc.narg(group_name) AS TEXT))
GROUP BY d.id
ORDER BY SUM(t.bytes_out + t.bytes_in) DESC, d.id
LIMIT sqlc.arg(limit_rows);

-- name: FirstContacts :many
-- Each device's first exchange with an organisation, when it fell at or after
-- a given hour: the organisations a device started talking to lately. Keyed on
-- the organisation rather than the address, so a service moving between the
-- addresses of one provider is not news.
SELECT d.id,
       CAST(COALESCE(d.label, '') AS TEXT)    AS label,
       CAST(COALESCE(d.hostname, '') AS TEXT) AS hostname,
       CAST(COALESCE(d.mac, '') AS TEXT)      AS mac,
       CAST(t.peer_asn AS INTEGER)            AS peer_asn,
       CAST(MIN(t.peer_ip) AS TEXT)           AS peer_ip,
       CAST(MIN(t.hour) AS TEXT)              AS first_hour,
       CAST(SUM(t.bytes_out + t.bytes_in) AS INTEGER) AS bytes
FROM traffic_hourly t
         JOIN devices d ON d.id = t.device_id
WHERE t.peer_asn IS NOT NULL
  AND d.is_ignored = 0
  AND (CAST(sqlc.narg(group_name) AS TEXT) IS NULL OR d.group_name = CAST(sqlc.narg(group_name) AS TEXT))
GROUP BY d.id, t.peer_asn
HAVING MIN(t.hour) >= sqlc.arg(since)
ORDER BY MIN(t.hour) DESC, d.id
LIMIT sqlc.arg(limit_rows);

-- name: TopOrganisations :many
-- The organisations the whole network exchanged the most with since a given
-- hour, and how many devices reached each.
SELECT CAST(t.peer_asn AS INTEGER)              AS peer_asn,
       CAST(MIN(t.peer_ip) AS TEXT)             AS peer_ip,
       CAST(SUM(t.bytes_out) AS INTEGER)        AS bytes_out,
       CAST(SUM(t.bytes_in) AS INTEGER)         AS bytes_in,
       CAST(COUNT(DISTINCT t.device_id) AS INTEGER) AS devices
FROM traffic_hourly t
         JOIN devices d ON d.id = t.device_id
WHERE t.peer_asn IS NOT NULL
  AND t.hour >= sqlc.arg(since)
  AND d.is_ignored = 0
  AND (CAST(sqlc.narg(group_name) AS TEXT) IS NULL OR d.group_name = CAST(sqlc.narg(group_name) AS TEXT))
GROUP BY t.peer_asn
ORDER BY SUM(t.bytes_out + t.bytes_in) DESC, t.peer_asn
LIMIT sqlc.arg(limit_rows);

-- name: OrganisationDevices :many
-- What each device exchanged with each organisation since a given hour, for
-- the breakdown under the busiest organisations. Every pair comes back; the
-- caller keeps the organisations it shows.
SELECT CAST(t.peer_asn AS INTEGER)            AS peer_asn,
       d.id,
       CAST(COALESCE(d.label, '') AS TEXT)    AS label,
       CAST(COALESCE(d.hostname, '') AS TEXT) AS hostname,
       CAST(COALESCE(d.mac, '') AS TEXT)      AS mac,
       CAST(SUM(t.bytes_out) AS INTEGER)      AS bytes_out,
       CAST(SUM(t.bytes_in) AS INTEGER)       AS bytes_in
FROM traffic_hourly t
         JOIN devices d ON d.id = t.device_id
WHERE t.peer_asn IS NOT NULL
  AND t.hour >= sqlc.arg(since)
  AND d.is_ignored = 0
  AND (CAST(sqlc.narg(group_name) AS TEXT) IS NULL OR d.group_name = CAST(sqlc.narg(group_name) AS TEXT))
GROUP BY t.peer_asn, d.id
ORDER BY t.peer_asn, SUM(t.bytes_out + t.bytes_in) DESC, d.id;

-- name: EarliestTraffic :one
-- The first hour anything was recorded, or empty when nothing has been: how
-- far back "first contact" can honestly look.
SELECT CAST(COALESCE(MIN(hour), '') AS TEXT) AS first_hour
FROM traffic_hourly;

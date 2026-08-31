-- name: UpsertSource :one
INSERT INTO sources (kind, name, created_at)
VALUES (?, ?, ?)
ON CONFLICT (name) DO UPDATE SET kind = excluded.kind
RETURNING *;

-- name: UpsertNetwork :one
INSERT INTO networks (cidr, created_at)
VALUES (?, ?)
ON CONFLICT (cidr) DO UPDATE SET cidr = excluded.cidr
RETURNING *;

-- name: CreateScan :one
INSERT INTO scans (source_id, kind, network_id, started_at)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: FinishScan :exec
UPDATE scans
SET status      = sqlc.arg(status),
    error       = sqlc.arg(error),
    found_count = sqlc.arg(found_count),
    finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id);

-- name: GetDeviceByMAC :one
SELECT *
FROM devices
WHERE mac = ?;

-- name: GetDeviceByCurrentIP :one
SELECT sqlc.embed(d)
FROM devices d
         JOIN addresses a ON a.device_id = d.id
WHERE a.ip = ?
  AND a.is_current = 1;

-- name: CreateDevice :one
INSERT INTO devices (mac, identity_source, is_randomised, vendor, hostname, hostname_source,
                     first_seen, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: IdentifyDevice :exec
UPDATE devices
SET mac             = sqlc.arg(mac),
    identity_source = 'MAC',
    is_randomised   = sqlc.arg(is_randomised),
    vendor          = sqlc.arg(vendor)
WHERE id = sqlc.arg(id);

-- name: SetDeviceHostname :exec
UPDATE devices
SET hostname        = sqlc.arg(hostname),
    hostname_source = sqlc.arg(hostname_source)
WHERE id = sqlc.arg(id);

-- name: TouchDevice :exec
UPDATE devices
SET last_seen = sqlc.arg(last_seen)
WHERE id = sqlc.arg(id);

-- A device folded into another may carry a label the user set before its MAC
-- was known, and the earlier of the two first_seen values is the true one.
-- name: AdoptCuration :exec
UPDATE devices
SET label      = COALESCE(label, sqlc.narg(folded_label)),
    notes      = COALESCE(notes, sqlc.narg(folded_notes)),
    group_name = COALESCE(group_name, sqlc.narg(folded_group_name)),
    first_seen = sqlc.arg(first_seen)
WHERE id = sqlc.arg(id);

-- Rows the surviving device already holds for the same address are left behind
-- and go with the folded device when it is deleted.
-- name: MoveAddresses :exec
UPDATE OR IGNORE addresses
SET device_id = sqlc.arg(into_id)
WHERE device_id = sqlc.arg(from_id);

-- name: MoveEvents :exec
UPDATE events
SET device_id = sqlc.arg(into_id)
WHERE device_id = sqlc.arg(from_id);

-- name: DeleteDevice :exec
DELETE
FROM devices
WHERE id = ?;

-- name: GetAddress :one
SELECT *
FROM addresses
WHERE device_id = ?
  AND ip = ?;

-- Only one device may hold an address as current, so the previous holder is
-- released before the new claim rather than colliding with the partial index.
-- name: ReleaseAddress :exec
UPDATE addresses
SET is_current = 0
WHERE ip = sqlc.arg(ip)
  AND is_current = 1
  AND device_id <> sqlc.arg(device_id);

-- name: InsertAddress :one
INSERT INTO addresses (device_id, network_id, ip, first_seen, last_seen)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: RefreshAddress :exec
UPDATE addresses
SET is_current = 1,
    network_id = sqlc.narg(network_id),
    last_seen  = sqlc.arg(last_seen)
WHERE id = sqlc.arg(id);

-- name: CreateEvent :exec
INSERT INTO events (device_id, scan_id, kind, old_value, new_value, detail, occurred_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

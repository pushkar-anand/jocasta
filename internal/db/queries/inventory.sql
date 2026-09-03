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

-- name: AllNetworks :many
-- Every recorded network, for matching an address to the prefix containing it.
-- SQLite cannot test containment, so the comparison happens in Go and this
-- returns the whole (small) table rather than filtering.
SELECT id, cidr
FROM networks
ORDER BY id;

-- What a source says a segment is. name and vlan_id are assigned rather than
-- coalesced: a VLAN renamed on the router is renamed here, and one that loses
-- its tag loses it here too.
-- name: UpsertNetworkIdentity :exec
INSERT INTO networks (cidr, name, vlan_id, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (cidr) DO UPDATE SET name    = excluded.name,
                                 vlan_id = excluded.vlan_id;

-- name: CreateScan :one
INSERT INTO scans (source_id, kind, network_id, started_at)
VALUES (?, ?, ?, ?)
RETURNING *;

-- name: LatestSuccessfulScanFinishedAt :one
-- When a scan of this kind last finished with something to show for it, which
-- is the anchor the poller schedules from: it waits an interval after the work
-- ends, not after it begins, so the two agree on what an interval measures.
--
-- Only scans that succeeded count. A failed one gathered nothing and a scan
-- whose process died mid-run never wrote a finish at all; crediting either
-- would hold the next run off for an interval that produced no data, which is
-- the staleness the interval exists to bound.
SELECT finished_at
FROM scans
WHERE kind = ?
  AND status = 'OK'
  AND finished_at IS NOT NULL
ORDER BY finished_at DESC, id DESC
LIMIT 1;

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

-- Upserted rather than replaced, so first_seen survives every later reading.
--
-- A name only moves when this source offers one: an empty reading leaves the
-- last name it knew in place rather than clearing it. A sweep resolves a name
-- per address, so a multi-homed host with a PTR on one interface and none on
-- another would otherwise thrash its name every pass; and a device that stops
-- answering has not been renamed, it has gone quiet. last_seen still ages, so
-- how stale the kept name is stays visible. A different non-empty name, from
-- this source or a higher one, still replaces it.
-- name: UpsertDeviceSource :exec
INSERT INTO device_sources (device_id, source_id, hostname, hostname_source, detail, first_seen, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (device_id, source_id)
    DO UPDATE SET hostname        = CASE
                                        WHEN excluded.hostname IS NOT NULL THEN excluded.hostname
                                        ELSE device_sources.hostname END,
                  hostname_source = CASE
                                        WHEN excluded.hostname IS NOT NULL THEN excluded.hostname_source
                                        ELSE device_sources.hostname_source END,
                  detail          = excluded.detail,
                  last_seen       = excluded.last_seen;

-- Election reads this to pick the name the device list shows and searches on;
-- the device page reads it to show the claims that lost.
-- name: ListDeviceSources :many
SELECT sqlc.embed(ds), s.name AS source_name, s.kind AS source_kind
FROM device_sources ds
         JOIN sources s ON s.id = ds.source_id
WHERE ds.device_id = ?
ORDER BY ds.last_seen DESC, s.name;

-- Claims follow the device on a fold. A source that filed against both rows
-- collides on the primary key, and the claim that survives takes the newer
-- reading's name with the outer bounds of both sightings.
-- name: MoveDeviceSources :exec
INSERT INTO device_sources (device_id, source_id, hostname, hostname_source, detail, first_seen, last_seen)
SELECT sqlc.arg(into_id), ghost.source_id, ghost.hostname, ghost.hostname_source, ghost.detail,
       ghost.first_seen, ghost.last_seen
FROM device_sources ghost
WHERE ghost.device_id = sqlc.arg(from_id)
ON CONFLICT (device_id, source_id)
    DO UPDATE SET hostname        = IIF(excluded.last_seen > device_sources.last_seen,
                                        excluded.hostname, device_sources.hostname),
                  hostname_source = IIF(excluded.last_seen > device_sources.last_seen,
                                        excluded.hostname_source, device_sources.hostname_source),
                  detail          = IIF(excluded.last_seen > device_sources.last_seen,
                                        excluded.detail, device_sources.detail),
                  first_seen      = MIN(device_sources.first_seen, excluded.first_seen),
                  last_seen       = MAX(device_sources.last_seen, excluded.last_seen);

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

-- Every address a device holds right now, for deciding which of them it has
-- moved off after a sweep that answered on another.
-- name: CurrentAddresses :many
SELECT *
FROM addresses
WHERE device_id = ?
  AND is_current = 1;

-- Only one device may hold an address as current, so the previous holder is
-- released before the new claim rather than colliding with the partial index.
-- name: ReleaseAddress :exec
UPDATE addresses
SET is_current = 0
WHERE ip = sqlc.arg(ip)
  AND is_current = 1
  AND device_id <> sqlc.arg(device_id);

-- Drop an address a device has moved off: the sweep answered for it elsewhere
-- in the prefix while this one stayed silent past the grace window. The row
-- stays so the history does, and last_seen keeps its last real sighting.
-- name: RetireAddress :exec
UPDATE addresses
SET is_current = 0
WHERE id = ?;

-- name: InsertAddress :one
INSERT INTO addresses (device_id, network_id, ip, first_seen, last_seen)
VALUES (?, ?, ?, ?, ?)
RETURNING *;

-- name: RefreshAddress :exec
-- COALESCE, not assignment: a source that cannot say which network an address
-- is on must leave the one a sweep established rather than erase it.
UPDATE addresses
SET is_current = 1,
    network_id = COALESCE(sqlc.narg(network_id), network_id),
    last_seen  = sqlc.arg(last_seen)
WHERE id = sqlc.arg(id);

-- name: CreateEvent :exec
INSERT INTO events (device_id, scan_id, kind, old_value, new_value, detail, occurred_at)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- Reads.

-- The current addresses come back on the device's own row rather than through a
-- join, so one query answers the list. The open ports and the ids of the
-- networks those addresses sit on ride along the same way, so a list can say
-- what a device exposes and where it lives without a query per row.
-- GROUP_CONCAT has no ordering worth relying on and the address column is TEXT,
-- which sorts 192.0.2.9 after 192.0.2.100, so the caller splits and orders
-- what it needs ordered.
--
-- is_ignored compares against the argument rather than testing a flag: passing
-- false leaves the clause admitting only unignored rows, and passing true makes
-- the second half admit the rest.
-- name: ListDevices :many
SELECT sqlc.embed(d),
       CAST(COALESCE((SELECT GROUP_CONCAT(a.ip, ' ')
                      FROM addresses a
                      WHERE a.device_id = d.id
                        AND a.is_current = 1), '') AS TEXT) AS current_ips,
       CAST(COALESCE((SELECT GROUP_CONCAT(p.port, ' ')
                      FROM device_ports p
                      WHERE p.device_id = d.id
                        AND p.state = 'open'), '') AS TEXT) AS open_ports,
       CAST(COALESCE((SELECT GROUP_CONCAT(DISTINCT a.network_id)
                      FROM addresses a
                      WHERE a.device_id = d.id
                        AND a.is_current = 1
                        AND a.network_id IS NOT NULL), '') AS TEXT) AS network_ids
FROM devices d
WHERE (d.is_ignored = 0 OR d.is_ignored = sqlc.arg(include_ignored))
  AND (CAST(sqlc.narg(group_name) AS TEXT) IS NULL OR d.group_name = CAST(sqlc.narg(group_name) AS TEXT))
  AND (CAST(sqlc.narg(network_id) AS INTEGER) IS NULL
    OR EXISTS (SELECT 1
               FROM addresses a
               WHERE a.device_id = d.id
                 AND a.is_current = 1
                 AND a.network_id = CAST(sqlc.narg(network_id) AS INTEGER)))
  AND (CAST(sqlc.narg(q) AS TEXT) IS NULL
    OR d.label LIKE '%' || CAST(sqlc.narg(q) AS TEXT) || '%'
    OR d.hostname LIKE '%' || CAST(sqlc.narg(q) AS TEXT) || '%'
    OR d.vendor LIKE '%' || CAST(sqlc.narg(q) AS TEXT) || '%'
    OR d.mac LIKE '%' || CAST(sqlc.narg(q) AS TEXT) || '%'
    OR EXISTS (SELECT 1
               FROM addresses a
               WHERE a.device_id = d.id
                 AND a.is_current = 1
                 AND a.ip LIKE '%' || CAST(sqlc.narg(q) AS TEXT) || '%'))
ORDER BY d.last_seen DESC;

-- name: GetDevice :one
SELECT *
FROM devices
WHERE id = ?;

-- Current addresses first, then the ones the device has released: "where did
-- this used to live" is the reason the released rows are kept.
-- name: ListDeviceAddresses :many
SELECT *
FROM addresses
WHERE device_id = ?
ORDER BY is_current DESC, last_seen DESC;

-- Timestamps only resolve to the millisecond, so id breaks the ties within one
-- scan, which writes every one of its events on the same stamp.
-- name: ListDeviceEvents :many
SELECT *
FROM events
WHERE device_id = ?
ORDER BY occurred_at DESC, id DESC
LIMIT ?;

-- The device columns come from a LEFT JOIN because an event outlives the device
-- it described: deleting one sets events.device_id to NULL rather than taking
-- the record with it.
-- The two logs are read with a query builder rather than from here: paging
-- them seeks past the row the last page ended on, and the seek is a clause that
-- is present or absent. See internal/db/models/inventory_page.go.

-- name: DeviceStats :one
SELECT COUNT(*)                                                                       AS total,
       CAST(COALESCE(SUM(CASE WHEN is_ignored = 1 THEN 1 ELSE 0 END), 0) AS INTEGER)  AS ignored,
       CAST(COALESCE(SUM(CASE WHEN last_seen >= sqlc.arg(online_since) THEN 1 ELSE 0 END), 0) AS INTEGER) AS online,
       CAST(COALESCE(SUM(CASE WHEN first_seen >= sqlc.arg(new_since) THEN 1 ELSE 0 END), 0) AS INTEGER)   AS discovered
FROM devices;

-- Every network a sweep has recorded, with how many devices hold an address on
-- it now. A device counts on each network it currently holds an address on:
-- the overview asks what is on a prefix, not how the inventory divides into
-- disjoint parts. A network no sweep has found anything on still lists, at
-- zero -- that it is quiet is the fact worth showing.
--
-- Ignored devices are left out, so the count agrees with the list the network
-- page draws below it: the join drops them and the counts are over d.id rather
-- than a.device_id, which would still be set for an address whose device the
-- join excluded.
-- name: ListNetworks :many
SELECT n.id,
       n.cidr,
       n.name,
       n.vlan_id,
       CAST(COUNT(DISTINCT d.id) AS INTEGER) AS total,
       CAST(COUNT(DISTINCT CASE
                               WHEN d.last_seen >= sqlc.arg(online_since) THEN d.id
           END) AS INTEGER)                  AS online
FROM networks n
         LEFT JOIN addresses a ON a.network_id = n.id AND a.is_current = 1
         LEFT JOIN devices d ON d.id = a.device_id AND d.is_ignored = 0
GROUP BY n.id
ORDER BY n.cidr;

-- name: ListGroups :many
SELECT DISTINCT CAST(group_name AS TEXT) AS group_name
FROM devices
WHERE group_name IS NOT NULL
  AND group_name <> ''
ORDER BY group_name;

-- Writes.

-- Every user-owned column is set rather than merged: the form submits all of
-- them, so an omitted one means cleared, not unchanged.
-- name: UpdateDeviceCuration :one
UPDATE devices
SET label       = sqlc.narg(label),
    notes       = sqlc.narg(notes),
    group_name  = sqlc.narg(group_name),
    device_type = sqlc.narg(device_type),
    is_ignored  = sqlc.arg(is_ignored)
WHERE id = sqlc.arg(id)
RETURNING *;

-- Classification.

-- The classifier's guess and its confidence, kept apart from the user's own
-- device_type so an override is never overwritten by a scan.
-- name: SetDeviceClass :exec
UPDATE devices
SET device_class            = sqlc.narg(device_class),
    device_class_confidence = sqlc.narg(device_class_confidence)
WHERE id = sqlc.arg(id);

-- The names of the segments a device currently sits on, for the classifier: a
-- VLAN called "IoT" or "cameras" is a hint about what belongs on it. Usually one
-- row; a multi-homed device on two named segments returns both.
-- name: DeviceNetworkNames :many
SELECT DISTINCT n.name AS name
FROM addresses a
         JOIN networks n ON n.id = a.network_id
WHERE a.device_id = ?
  AND a.is_current = 1
  AND n.name IS NOT NULL
  AND n.name <> '';

-- Ports.

-- Every address a port scan should probe: the current address of every device
-- the user has not ignored. The scan works from what discovery has already
-- found rather than sweeping, so this is its whole target list.
-- name: AllCurrentAddresses :many
SELECT a.device_id, a.ip
FROM addresses a
         JOIN devices d ON d.id = a.device_id
WHERE a.is_current = 1
  AND d.is_ignored = 0
ORDER BY a.device_id, a.ip;

-- The ports currently recorded open on a device, for a fresh scan to diff
-- itself against. Closed rows are history and left out: a scan no longer seeing
-- a port only matters for one we thought was open.
-- name: ListDeviceOpenPorts :many
SELECT *
FROM device_ports
WHERE device_id = ?
  AND state = 'open';

-- Every port ever seen open on a device, for the device page. Open ones lead --
-- 'open' sorts after 'closed', so DESC puts them first -- then by number. A
-- closed row is where a service used to answer, which is worth seeing beside
-- the ones that still do.
-- name: ListDevicePorts :many
SELECT *
FROM device_ports
WHERE device_id = ?
ORDER BY state DESC, port;

-- A port answered. A new row, or a closed one coming back: first_seen holds the
-- first time it was ever open and changed_at moves only on a real transition,
-- so "open since" and "state changed" stay distinct.
-- name: UpsertOpenPort :exec
INSERT INTO device_ports (device_id, port, state, service, first_seen, last_seen, changed_at)
VALUES (sqlc.arg(device_id), sqlc.arg(port), 'open', sqlc.narg(service),
        sqlc.arg(seen_at), sqlc.arg(seen_at), sqlc.arg(seen_at))
ON CONFLICT (device_id, port)
    DO UPDATE SET state      = 'open',
                  service    = excluded.service,
                  last_seen  = excluded.last_seen,
                  changed_at = IIF(device_ports.state <> 'open',
                                   excluded.changed_at, device_ports.changed_at);

-- A port we had open did not answer this run. Keep the row, flip the state,
-- record when it went.
-- name: ClosePort :exec
UPDATE device_ports
SET state      = 'closed',
    last_seen  = sqlc.arg(seen_at),
    changed_at = sqlc.arg(seen_at)
WHERE device_id = sqlc.arg(device_id)
  AND port = sqlc.arg(port)
  AND state = 'open';

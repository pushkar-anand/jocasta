-- name: UpsertTopologyNode :exec
INSERT INTO topology_nodes (source_id, identity, gateway, own_macs, read_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (source_id) DO UPDATE
    SET identity = excluded.identity,
        gateway  = excluded.gateway,
        own_macs = excluded.own_macs,
        read_at  = excluded.read_at;

-- name: DeleteTopologyPorts :exec
DELETE
FROM topology_ports
WHERE source_id = ?;

-- name: InsertTopologyPort :exec
INSERT INTO topology_ports (source_id, name, kind, pvid, tagged, untagged, running)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: UpsertTopologySighting :exec
-- A read that sees the address again moves its last sighting on, and takes
-- what the radio says now about the network it joined.
INSERT INTO topology_sightings (source_id, port, mac, vlan, wifi, ssid, band, first_seen, last_seen)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source_id, mac, port, vlan) DO UPDATE
    SET wifi      = excluded.wifi,
        ssid      = excluded.ssid,
        band      = excluded.band,
        last_seen = excluded.last_seen;

-- name: DeleteTopologyNeighbours :exec
DELETE
FROM topology_neighbours
WHERE source_id = ?;

-- name: InsertTopologyNeighbour :exec
INSERT INTO topology_neighbours (source_id, port, mac, identity, platform, board, their_port, address)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (source_id, port, mac, identity) DO NOTHING;

-- name: ListTopologyNodes :many
SELECT n.*, s.name AS source_name
FROM topology_nodes n
         JOIN sources s ON s.id = n.source_id
ORDER BY s.name;

-- name: ListTopologyPorts :many
SELECT *
FROM topology_ports
ORDER BY source_id, name;

-- name: ListTopologySightings :many
SELECT *
FROM topology_sightings
ORDER BY source_id, port, mac, vlan;

-- name: ListTopologyNeighbours :many
SELECT *
FROM topology_neighbours
ORDER BY source_id, port, mac, identity;

-- name: LastTopologyReadAt :one
-- When any source was last read for its topology. No row when none has been.
SELECT read_at
FROM topology_nodes
ORDER BY read_at DESC
LIMIT 1;

-- name: DeleteTopologySightingsBefore :execrows
DELETE
FROM topology_sightings
WHERE last_seen < ?;

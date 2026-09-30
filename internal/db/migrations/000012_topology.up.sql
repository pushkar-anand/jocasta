-- What each router, switch and access point says is plugged into it. A
-- source read for its topology has one node row; its ports and neighbours
-- are replaced on every read, and its sightings are kept, so a device that
-- has gone quiet keeps the place it was last seen in.
CREATE TABLE topology_nodes
(
    source_id INTEGER PRIMARY KEY REFERENCES sources (id) ON DELETE CASCADE,

    -- The device's own name for itself, as it announces it to neighbours.
    identity  TEXT,

    -- Set on the device that routes the network, which the tree hangs from.
    gateway   INTEGER NOT NULL DEFAULT 0 CHECK (gateway IN (0, 1)),

    -- The device's own hardware addresses, comma-separated, which is how
    -- another device's tables recognise it.
    own_macs  TEXT    NOT NULL DEFAULT '',

    read_at   TEXT    NOT NULL
);

CREATE TABLE topology_ports
(
    source_id INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,
    name      TEXT    NOT NULL,
    kind      TEXT    NOT NULL CHECK (kind IN ('wired', 'wifi', 'virtual')),

    -- The VLAN untagged traffic on the port belongs to, zero when unknown,
    -- and the VLANs the port carries each way, comma-separated.
    pvid      INTEGER NOT NULL DEFAULT 0,
    tagged    TEXT    NOT NULL DEFAULT '',
    untagged  TEXT    NOT NULL DEFAULT '',

    running   INTEGER NOT NULL DEFAULT 0 CHECK (running IN (0, 1)),

    PRIMARY KEY (source_id, name)
);

-- A hardware address one source learned on one of its ports, in one VLAN.
CREATE TABLE topology_sightings
(
    source_id  INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,
    port       TEXT    NOT NULL,
    mac        TEXT    NOT NULL
        CHECK (mac GLOB
               '[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]'),

    -- Zero when the source does not say.
    vlan       INTEGER NOT NULL DEFAULT 0,

    -- Set for a Wi-Fi client, with the network it joined and the band, as
    -- the router renders it ("5ghz-ax").
    wifi       INTEGER NOT NULL DEFAULT 0 CHECK (wifi IN (0, 1)),
    ssid       TEXT,
    band       TEXT,

    first_seen TEXT    NOT NULL,
    last_seen  TEXT    NOT NULL,

    PRIMARY KEY (source_id, mac, port, vlan)
);

-- The prune.
CREATE INDEX idx_topology_sightings_last_seen ON topology_sightings (last_seen);

-- A device that announced itself on one of a source's ports.
CREATE TABLE topology_neighbours
(
    source_id  INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,
    port       TEXT    NOT NULL,

    -- Empty when the neighbour gave no hardware address, only a name.
    mac        TEXT    NOT NULL DEFAULT '',
    identity   TEXT    NOT NULL DEFAULT '',
    platform   TEXT,
    board      TEXT,
    their_port TEXT,
    address    TEXT,

    PRIMARY KEY (source_id, port, mac, identity)
);

-- Connections the internet tried on a device that never carried data: a
-- forwarded port knocked on, a SYN to the router's outside address, a ping.
-- attempts_hourly keeps what a device tried; this keeps what was tried on it,
-- which is what a scan of the network from outside leaves behind. Counted per
-- peer, with the lowest ports tried kept as a sample, as attempts are.
CREATE TABLE probes_hourly
(
    source_id  INTEGER NOT NULL REFERENCES sources (id) ON DELETE RESTRICT,

    -- The device tried: the one a forwarded port leads to, or the router
    -- when its outside address was tried.
    device_id  INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,

    -- The start of the hour, UTC.
    hour       TEXT    NOT NULL,

    -- Who tried, and the organisation announcing it.
    peer_ip    TEXT    NOT NULL,
    peer_asn   INTEGER,

    protocol   INTEGER NOT NULL,

    attempts   INTEGER NOT NULL DEFAULT 0,
    answered   INTEGER NOT NULL DEFAULT 0 CHECK (answered <= attempts),

    port_count INTEGER NOT NULL DEFAULT 0,
    ports      TEXT    NOT NULL DEFAULT '',

    -- Whether the router's outside address was what was tried, rather than
    -- a port forwarded to the device.
    outside    INTEGER NOT NULL DEFAULT 0 CHECK (outside IN (0, 1)),

    PRIMARY KEY (device_id, hour, source_id, peer_ip, protocol)
);

-- The prune, and every "last N hours" view across the network.
CREATE INDEX idx_probes_hourly_hour ON probes_hourly (hour);

-- The addresses a router translated flows to on their way out: its outside
-- address. On a router with a public address that is it; behind the ISP's
-- carrier-grade NAT, or another router, it is a private one, and nothing on
-- the internet can open a connection to the network over it.
CREATE TABLE outside_addresses
(
    source_id  INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,
    address    TEXT    NOT NULL,
    first_seen TEXT    NOT NULL,
    last_seen  TEXT    NOT NULL,

    PRIMARY KEY (source_id, address)
);

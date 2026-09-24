-- Who each device talks to, rolled up by the hour from the flows a router
-- exports. Raw flows are never stored: a busy network exports thousands a
-- minute, and the question asked of them is "with whom, how much, when",
-- which an hourly total answers.
CREATE TABLE traffic_hourly
(
    -- RESTRICT for the same reason as scans: dropping a source from config
    -- must not take the history it produced with it.
    source_id      INTEGER NOT NULL REFERENCES sources (id) ON DELETE RESTRICT,

    -- The device on this side of the conversation, resolved when the flow
    -- arrived, so the history stays with the device after its address moves.
    device_id      INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,

    -- The start of the hour, UTC.
    hour           TEXT    NOT NULL,

    -- The other side. peer_ip is always the address seen; peer_device_id is
    -- set when that address belonged to a known device at the time, and a
    -- conversation between two devices is written once from each side.
    peer_device_id INTEGER REFERENCES devices (id) ON DELETE CASCADE,
    peer_ip        TEXT    NOT NULL,

    -- Reverse DNS for a peer that is not a known device, when it resolves.
    peer_name      TEXT,

    -- The autonomous system announcing an internet peer. Only the number is
    -- kept; the organisation's name comes from the embedded table when shown,
    -- so a refreshed table renames old rows too.
    peer_asn       INTEGER,

    -- IANA protocol number: 6 TCP, 17 UDP, 1 ICMP.
    protocol       INTEGER NOT NULL,

    -- The port that names the service, whichever side offered it. Zero for a
    -- protocol without ports.
    service_port   INTEGER NOT NULL CHECK (service_port BETWEEN 0 AND 65535),

    bytes_out      INTEGER NOT NULL DEFAULT 0,
    bytes_in       INTEGER NOT NULL DEFAULT 0,
    packets_out    INTEGER NOT NULL DEFAULT 0,
    packets_in     INTEGER NOT NULL DEFAULT 0,
    connections    INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY (device_id, hour, source_id, peer_ip, protocol, service_port)
);

-- The prune and every "last N hours" view across the network.
CREATE INDEX idx_traffic_hourly_hour ON traffic_hourly (hour);

-- A device page lists the conversations others started with it, too.
CREATE INDEX idx_traffic_hourly_peer_device ON traffic_hourly (peer_device_id, hour);

-- "First contact": the earliest hour a device reached an organisation.
CREATE INDEX idx_traffic_hourly_first_contact ON traffic_hourly (device_id, peer_asn, hour);

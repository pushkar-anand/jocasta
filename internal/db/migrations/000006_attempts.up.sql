-- Connections a device tried that never carried data: a port knocked on and
-- refused, a SYN nobody answered, a ping, a UDP datagram with no reply. They
-- are what a device scanning the network leaves behind, and they are kept
-- apart from traffic_hourly because a scan is thousands of them an hour:
-- one row per port would bury every real conversation, so they are counted
-- per peer instead, with the lowest ports tried kept as a sample.
CREATE TABLE attempts_hourly
(
    source_id      INTEGER NOT NULL REFERENCES sources (id) ON DELETE RESTRICT,

    -- The device that tried, resolved when the attempt arrived.
    device_id      INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,

    -- The start of the hour, UTC.
    hour           TEXT    NOT NULL,

    -- What it tried to reach, as in traffic_hourly.
    peer_device_id INTEGER REFERENCES devices (id) ON DELETE CASCADE,
    peer_ip        TEXT    NOT NULL,
    peer_asn       INTEGER,

    -- IANA protocol number: 6 TCP, 17 UDP, 1 ICMP.
    protocol       INTEGER NOT NULL,

    -- How many attempts, and how many of them the peer answered: a TCP port
    -- that accepted the handshake, a ping that came back, a UDP reply.
    attempts       INTEGER NOT NULL DEFAULT 0,
    answered       INTEGER NOT NULL DEFAULT 0 CHECK (answered <= attempts),

    -- How many distinct ports were tried, and the lowest of them, comma
    -- separated, at most twenty: enough to tell what a scan was looking for.
    port_count     INTEGER NOT NULL DEFAULT 0,
    ports          TEXT    NOT NULL DEFAULT '',

    PRIMARY KEY (device_id, hour, source_id, peer_ip, protocol)
);

-- The prune, and every "last N hours" view across the network.
CREATE INDEX idx_attempts_hourly_hour ON attempts_hourly (hour);

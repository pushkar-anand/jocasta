-- What a device sent to everyone rather than to one host: a broadcast to its
-- subnet or to 255.255.255.255, or a packet to a multicast group. Discovery
-- protocols -- mDNS, SSDP, DHCP, a sync tool finding its peers -- announce a
-- device this way, which says what it runs and looks for. There is no peer to
-- file them under in traffic_hourly, so they are kept per group and port.
CREATE TABLE broadcasts_hourly
(
    source_id INTEGER NOT NULL REFERENCES sources (id) ON DELETE RESTRICT,

    -- The device that sent it, resolved when the flow arrived.
    device_id INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,

    -- The start of the hour, UTC.
    hour      TEXT    NOT NULL,

    -- Where it went: the subnet's broadcast address, 255.255.255.255, or the
    -- multicast group, and which of the three that is.
    dst_ip    TEXT    NOT NULL,
    kind      TEXT    NOT NULL CHECK (kind IN ('subnet', 'all', 'multicast')),

    -- IANA protocol number, and the destination port; zero when the
    -- protocol has none (IGMP, ICMP).
    protocol  INTEGER NOT NULL,
    port      INTEGER NOT NULL,

    bytes     INTEGER NOT NULL DEFAULT 0,
    packets   INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY (device_id, hour, source_id, dst_ip, protocol, port)
);

-- The prune.
CREATE INDEX idx_broadcasts_hourly_hour ON broadcasts_hourly (hour);

-- What a device is listening on. Written only by the port scan, which probes
-- the addresses discovery has already found rather than sweeping a prefix, so
-- every row here hangs off a device some earlier scan recorded.
CREATE TABLE device_ports
(
    device_id  INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    port       INTEGER NOT NULL CHECK (port BETWEEN 1 AND 65535),

    -- A port that has been open keeps its row when it closes, so "this was open
    -- until Tuesday" stays one read rather than a walk back through events.
    state      TEXT    NOT NULL CHECK (state IN ('open', 'closed')),

    -- Best-effort name from a static port-to-service map, null when the port is
    -- not in it. Never authoritative: a service on a non-standard port is named
    -- wrong, which is why the scan does not try to fingerprint.
    service    TEXT,

    first_seen TEXT    NOT NULL, -- first scan that saw this port open
    last_seen  TEXT    NOT NULL, -- most recent scan that had an opinion on it
    changed_at TEXT    NOT NULL, -- when state last flipped

    PRIMARY KEY (device_id, port)
);

CREATE INDEX idx_device_ports_device ON device_ports (device_id);

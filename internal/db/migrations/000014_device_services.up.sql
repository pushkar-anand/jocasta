-- What a device advertises over DNS-SD. Written only by a sweep, which browses
-- the services of the hosts that answered it.
CREATE TABLE device_services
(
    device_id  INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,

    -- The service type without the domain, such as _googlecast._tcp.
    type       TEXT    NOT NULL,

    -- The name the device gives the service, empty when the browse found it no
    -- fit to show. Not null, so the primary key holds one row per unnamed
    -- instance too.
    instance   TEXT    NOT NULL DEFAULT '',

    -- Zero when no SRV record came with the service.
    port       INTEGER NOT NULL DEFAULT 0 CHECK (port BETWEEN 0 AND 65535),

    -- The name a Google Cast device shows its owner and its model, from its TXT
    -- record. Null for every other type.
    label      TEXT,
    model      TEXT,

    -- A sweep that does not hear a service leaves its row alone, since mDNS
    -- runs over multicast and drops packets. The prune deletes a row once
    -- last_seen falls out of the history window.
    first_seen TEXT    NOT NULL,
    last_seen  TEXT    NOT NULL,

    PRIMARY KEY (device_id, type, instance)
);

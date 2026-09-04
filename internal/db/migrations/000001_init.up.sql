CREATE TABLE IF NOT EXISTS users
(
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TEXT NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- Timestamps are ISO-8601 UTC with milliseconds so they sort lexicographically
-- and can order events that land within the same second of a scan.

-- Where a fact came from. A row per configured instance rather than per kind,
-- so a second router or a second scanner stays distinguishable in provenance.
-- Whether a source runs is a config question; this table exists to give scans
-- and events a stable foreign key.
CREATE TABLE sources
(
    id         INTEGER PRIMARY KEY AUTOINCREMENT,

    -- What sort of thing produced the fact, not which implementation did it.
    -- RouterOS is one router among the several this could speak to, and which
    -- one a row means is the instance's own business.
    kind       TEXT NOT NULL CHECK (kind IN ('SWEEP', 'ROUTER', 'DNS', 'MANUAL')),
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE networks
(
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    cidr       TEXT NOT NULL UNIQUE,
    name       TEXT,
    vlan_id    INTEGER,
    created_at TEXT NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

-- The stable entity. An address is a lease, not an identity, so everything the
-- user curates hangs here and survives the device moving.
CREATE TABLE devices
(
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Null until a MAC is learned. SQLite treats NULLs as distinct in a UNIQUE
    -- index, so any number of not-yet-identified devices can coexist. The GLOB
    -- admits only lowercase colon-separated form, so a lookup by MAC never has
    -- to consider that the same address was stored two ways.
    mac                     TEXT UNIQUE
        CHECK (mac IS NULL OR mac GLOB
                              '[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]'),
    identity_source         TEXT    NOT NULL DEFAULT 'IP' CHECK (identity_source IN ('MAC', 'IP')),

    -- Set when the MAC is locally administered: the device generated it for
    -- itself, so it names no vendor and will not survive the next association.
    is_randomised           INTEGER NOT NULL DEFAULT 0 CHECK (is_randomised IN (0, 1)),

    vendor                  TEXT,
    hostname                TEXT,
    hostname_source         TEXT,
    device_type             TEXT,
    device_class            TEXT,
    device_class_confidence TEXT,

    -- User-owned. No scan or plugin may write these.
    label                   TEXT,
    notes                   TEXT,
    group_name              TEXT,
    is_ignored              INTEGER NOT NULL DEFAULT 0 CHECK (is_ignored IN (0, 1)),

    first_seen              TEXT    NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen               TEXT    NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_devices_last_seen ON devices (last_seen DESC);

-- A device holds several addresses at once when it has more than one interface,
-- and different ones over time as leases change.
CREATE TABLE addresses
(
    id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
    device_id  INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    network_id INTEGER REFERENCES networks (id) ON DELETE SET NULL,
    ip         TEXT    NOT NULL,
    is_current INTEGER NOT NULL DEFAULT 1 CHECK (is_current IN (0, 1)),
    first_seen TEXT    NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen  TEXT    NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (device_id, ip)
);

-- Answering "who holds this IP right now" is the hot lookup on every sweep
-- result, and only current rows can answer it.
CREATE UNIQUE INDEX idx_addresses_current_ip ON addresses (ip) WHERE is_current = 1;
CREATE INDEX idx_addresses_device ON addresses (device_id);

CREATE TABLE scans
(
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    -- RESTRICT, not CASCADE: dropping a source from config must not take the
    -- scan history it produced with it.
    source_id   INTEGER NOT NULL REFERENCES sources (id) ON DELETE RESTRICT,
    kind        TEXT    NOT NULL CHECK (kind IN ('DISCOVERY', 'PORTS', 'IMPORT')),
    network_id  INTEGER REFERENCES networks (id) ON DELETE SET NULL,
    status      TEXT    NOT NULL DEFAULT 'RUNNING' CHECK (status IN ('RUNNING', 'OK', 'FAILED', 'CANCELLED')),
    error       TEXT,
    found_count INTEGER NOT NULL DEFAULT 0,
    started_at  TEXT    NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now')),
    finished_at TEXT
);

CREATE INDEX idx_scans_started ON scans (started_at DESC);

-- The permanent record of what changed. Kind is deliberately unconstrained:
-- new kinds are added in Go, and a CHECK here would make each one a migration.
CREATE TABLE events
(
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id   INTEGER REFERENCES devices (id) ON DELETE SET NULL,
    scan_id     INTEGER REFERENCES scans (id) ON DELETE SET NULL,
    kind        TEXT    NOT NULL,
    old_value   TEXT,
    new_value   TEXT,
    detail      TEXT,
    occurred_at TEXT    NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_events_occurred ON events (occurred_at DESC);
CREATE INDEX idx_events_device ON events (device_id, occurred_at DESC);

-- What one source claims about one device, kept beside the resolution on
-- devices rather than merged into it, so two sources naming the same box
-- differently can both be shown.
CREATE TABLE device_sources
(
    device_id       INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    source_id       INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,

    -- An empty claim overwrites its predecessor, so a source with nothing left
    -- to say about a name can retract it.
    hostname        TEXT,

    -- Per claim rather than read off sources.kind: one router offers both a
    -- static lease name and a dynamic one, and they are not worth the same.
    hostname_source TEXT,

    -- What only this source knows, as JSON: a lease comment, the router
    -- interface. Never merged into devices.
    detail          TEXT CHECK (detail IS NULL OR JSON_VALID(detail)),

    -- Per-source presence, so "there is a bound lease but nothing has answered
    -- a ping in three days" is two claims disagreeing rather than one of them
    -- being wrong.
    first_seen      TEXT    NOT NULL,
    last_seen       TEXT    NOT NULL,
    PRIMARY KEY (device_id, source_id)
);

-- Answers "what does this source still hold, and how recently", the read behind
-- a source going quiet. The primary key answers the per-device direction.
CREATE INDEX idx_device_sources_source ON device_sources (source_id, last_seen DESC);

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



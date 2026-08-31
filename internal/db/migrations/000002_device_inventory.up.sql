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
    id              INTEGER PRIMARY KEY AUTOINCREMENT,

    -- Null until a MAC is learned. SQLite treats NULLs as distinct in a UNIQUE
    -- index, so any number of not-yet-identified devices can coexist. The GLOB
    -- admits only lowercase colon-separated form, so a lookup by MAC never has
    -- to consider that the same address was stored two ways.
    mac             TEXT UNIQUE
        CHECK (mac IS NULL OR mac GLOB
                              '[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]:[0-9a-f][0-9a-f]'),
    identity_source TEXT    NOT NULL DEFAULT 'IP' CHECK (identity_source IN ('MAC', 'IP')),

    -- Set when the MAC is locally administered: the device generated it for
    -- itself, so it names no vendor and will not survive the next association.
    is_randomised   INTEGER NOT NULL DEFAULT 0 CHECK (is_randomised IN (0, 1)),

    vendor          TEXT,
    hostname        TEXT,
    hostname_source TEXT,
    device_type     TEXT,

    -- User-owned. No scan or plugin may write these.
    label           TEXT,
    notes           TEXT,
    group_name      TEXT,
    is_ignored      INTEGER NOT NULL DEFAULT 0 CHECK (is_ignored IN (0, 1)),

    first_seen      TEXT    NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen       TEXT    NOT NULL DEFAULT (STRFTIME('%Y-%m-%dT%H:%M:%fZ', 'now'))
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

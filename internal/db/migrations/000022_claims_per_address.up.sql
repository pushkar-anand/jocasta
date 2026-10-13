-- A source speaks about addresses, so a device holding several gets one claim
-- per address from each source, each with its own name and detail. Keyed by
-- device and source alone, a router's five addresses overwrote one claim and
-- its name flipped with whichever address was read last.
--
-- SQLite cannot change a primary key in place, so the table is rebuilt. Each
-- existing claim takes the device's lowest current address, or its most
-- recently seen one when none is current. A claim about a device with no
-- address at all is left out; the source's next reading files it again.

CREATE TABLE device_sources_new
(
    device_id       INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    source_id       INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,

    -- The address the source said this about.
    address         TEXT    NOT NULL,

    -- An empty claim overwrites its predecessor, so a source with nothing left
    -- to say about a name can retract it.
    hostname        TEXT,

    -- Per claim, since sources.kind cannot say it: one router offers both a
    -- static lease name and a dynamic one, and they are not worth the same.
    hostname_source TEXT,

    -- What only this source knows, as JSON: a lease comment, the router
    -- interface. Never merged into devices.
    detail          TEXT CHECK (detail IS NULL OR JSON_VALID(detail)),

    -- Per-source presence, so "there is a bound lease but nothing has answered
    -- a ping in three days" is two claims that disagree, both of them true.
    first_seen      TEXT    NOT NULL,
    last_seen       TEXT    NOT NULL,
    PRIMARY KEY (device_id, source_id, address)
);

INSERT INTO device_sources_new (device_id, source_id, address, hostname, hostname_source, detail,
                                first_seen, last_seen)
SELECT ds.device_id,
       ds.source_id,
       COALESCE((SELECT MIN(a.ip) FROM addresses a WHERE a.device_id = ds.device_id AND a.is_current = 1),
                (SELECT a.ip
                 FROM addresses a
                 WHERE a.device_id = ds.device_id
                 ORDER BY a.last_seen DESC, a.ip
                 LIMIT 1)),
       ds.hostname,
       ds.hostname_source,
       ds.detail,
       ds.first_seen,
       ds.last_seen
FROM device_sources ds
WHERE EXISTS (SELECT 1 FROM addresses a WHERE a.device_id = ds.device_id);

DROP TABLE device_sources;
ALTER TABLE device_sources_new RENAME TO device_sources;

CREATE INDEX idx_device_sources_source ON device_sources (source_id, last_seen DESC);

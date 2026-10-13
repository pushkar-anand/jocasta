-- One claim per device and source again: of a source's claims about a device,
-- the most recently heard is kept.

CREATE TABLE device_sources_old
(
    device_id       INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    source_id       INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,
    hostname        TEXT,
    hostname_source TEXT,
    detail          TEXT CHECK (detail IS NULL OR JSON_VALID(detail)),
    first_seen      TEXT    NOT NULL,
    last_seen       TEXT    NOT NULL,
    PRIMARY KEY (device_id, source_id)
);

INSERT INTO device_sources_old (device_id, source_id, hostname, hostname_source, detail, first_seen, last_seen)
SELECT device_id, source_id, hostname, hostname_source, detail, first_seen, last_seen
FROM (SELECT ds.*,
             ROW_NUMBER() OVER (PARTITION BY device_id, source_id ORDER BY last_seen DESC, address) AS n
      FROM device_sources ds)
WHERE n = 1;

DROP TABLE device_sources;
ALTER TABLE device_sources_old RENAME TO device_sources;

CREATE INDEX idx_device_sources_source ON device_sources (source_id, last_seen DESC);

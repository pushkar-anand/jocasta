-- Deleting a device clears it as the peer of other devices' traffic and
-- attempts and keeps the rows: they are the other device's history, and
-- peer_ip still says who the peer was. SQLite cannot change a foreign key in
-- place, so each table is rebuilt, and its indexes with it.
--
-- The copy leaves out rows whose own device is gone, and clears a peer that is
-- gone. Neither can exist while foreign keys are enforced, but a database
-- written without enforcement may hold them, and copying one would fail the
-- migration and stop startup.

CREATE TABLE traffic_hourly_new
(
    source_id      INTEGER NOT NULL REFERENCES sources (id) ON DELETE RESTRICT,
    device_id      INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    hour           TEXT    NOT NULL,

    -- Null when the address belonged to no known device at the time, or the
    -- device it belonged to has since been deleted.
    peer_device_id INTEGER REFERENCES devices (id) ON DELETE SET NULL,
    peer_ip        TEXT    NOT NULL,
    peer_name      TEXT,
    peer_asn       INTEGER,
    protocol       INTEGER NOT NULL,
    service_port   INTEGER NOT NULL CHECK (service_port BETWEEN 0 AND 65535),
    bytes_out      INTEGER NOT NULL DEFAULT 0,
    bytes_in       INTEGER NOT NULL DEFAULT 0,
    packets_out    INTEGER NOT NULL DEFAULT 0,
    packets_in     INTEGER NOT NULL DEFAULT 0,
    connections    INTEGER NOT NULL DEFAULT 0,
    connections_in INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY (device_id, hour, source_id, peer_ip, protocol, service_port)
);

INSERT INTO traffic_hourly_new (source_id, device_id, hour, peer_device_id, peer_ip, peer_name, peer_asn,
                                protocol, service_port, bytes_out, bytes_in, packets_out, packets_in,
                                connections, connections_in)
SELECT source_id,
       device_id,
       hour,
       CASE WHEN peer_device_id IN (SELECT id FROM devices) THEN peer_device_id END,
       peer_ip,
       peer_name,
       peer_asn,
       protocol,
       service_port,
       bytes_out,
       bytes_in,
       packets_out,
       packets_in,
       connections,
       connections_in
FROM traffic_hourly
WHERE device_id IN (SELECT id FROM devices);

DROP TABLE traffic_hourly;

ALTER TABLE traffic_hourly_new RENAME TO traffic_hourly;

CREATE INDEX idx_traffic_hourly_hour ON traffic_hourly (hour);
CREATE INDEX idx_traffic_hourly_peer_device ON traffic_hourly (peer_device_id, hour);
CREATE INDEX idx_traffic_hourly_first_contact ON traffic_hourly (device_id, peer_asn, hour);

CREATE TABLE attempts_hourly_new
(
    source_id      INTEGER NOT NULL REFERENCES sources (id) ON DELETE RESTRICT,
    device_id      INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    hour           TEXT    NOT NULL,

    -- Null as in traffic_hourly.
    peer_device_id INTEGER REFERENCES devices (id) ON DELETE SET NULL,
    peer_ip        TEXT    NOT NULL,
    peer_asn       INTEGER,
    protocol       INTEGER NOT NULL,
    attempts       INTEGER NOT NULL DEFAULT 0,
    answered       INTEGER NOT NULL DEFAULT 0 CHECK (answered <= attempts),
    port_count     INTEGER NOT NULL DEFAULT 0,
    ports          TEXT    NOT NULL DEFAULT '',

    PRIMARY KEY (device_id, hour, source_id, peer_ip, protocol)
);

INSERT INTO attempts_hourly_new (source_id, device_id, hour, peer_device_id, peer_ip, peer_asn,
                                 protocol, attempts, answered, port_count, ports)
SELECT source_id,
       device_id,
       hour,
       CASE WHEN peer_device_id IN (SELECT id FROM devices) THEN peer_device_id END,
       peer_ip,
       peer_asn,
       protocol,
       attempts,
       answered,
       port_count,
       ports
FROM attempts_hourly
WHERE device_id IN (SELECT id FROM devices);

DROP TABLE attempts_hourly;

ALTER TABLE attempts_hourly_new RENAME TO attempts_hourly;

CREATE INDEX idx_attempts_hourly_hour ON attempts_hourly (hour);

-- Deleting a device clears it from every row naming it as the peer, which
-- without this index reads the whole table once per device.
CREATE INDEX idx_attempts_hourly_peer_device ON attempts_hourly (peer_device_id);

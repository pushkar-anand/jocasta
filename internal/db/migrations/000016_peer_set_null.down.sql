-- Rebuilds both tables with peer_device_id back to ON DELETE CASCADE. Rows
-- whose peer was cleared stay, with no peer device.

CREATE TABLE traffic_hourly_old
(
    source_id      INTEGER NOT NULL REFERENCES sources (id) ON DELETE RESTRICT,
    device_id      INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    hour           TEXT    NOT NULL,
    peer_device_id INTEGER REFERENCES devices (id) ON DELETE CASCADE,
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

INSERT INTO traffic_hourly_old (source_id, device_id, hour, peer_device_id, peer_ip, peer_name, peer_asn,
                                protocol, service_port, bytes_out, bytes_in, packets_out, packets_in,
                                connections, connections_in)
SELECT source_id,
       device_id,
       hour,
       peer_device_id,
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
FROM traffic_hourly;

DROP TABLE traffic_hourly;

ALTER TABLE traffic_hourly_old RENAME TO traffic_hourly;

CREATE INDEX idx_traffic_hourly_hour ON traffic_hourly (hour);
CREATE INDEX idx_traffic_hourly_peer_device ON traffic_hourly (peer_device_id, hour);
CREATE INDEX idx_traffic_hourly_first_contact ON traffic_hourly (device_id, peer_asn, hour);

CREATE TABLE attempts_hourly_old
(
    source_id      INTEGER NOT NULL REFERENCES sources (id) ON DELETE RESTRICT,
    device_id      INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    hour           TEXT    NOT NULL,
    peer_device_id INTEGER REFERENCES devices (id) ON DELETE CASCADE,
    peer_ip        TEXT    NOT NULL,
    peer_asn       INTEGER,
    protocol       INTEGER NOT NULL,
    attempts       INTEGER NOT NULL DEFAULT 0,
    answered       INTEGER NOT NULL DEFAULT 0 CHECK (answered <= attempts),
    port_count     INTEGER NOT NULL DEFAULT 0,
    ports          TEXT    NOT NULL DEFAULT '',

    PRIMARY KEY (device_id, hour, source_id, peer_ip, protocol)
);

INSERT INTO attempts_hourly_old (source_id, device_id, hour, peer_device_id, peer_ip, peer_asn,
                                 protocol, attempts, answered, port_count, ports)
SELECT source_id,
       device_id,
       hour,
       peer_device_id,
       peer_ip,
       peer_asn,
       protocol,
       attempts,
       answered,
       port_count,
       ports
FROM attempts_hourly;

DROP TABLE attempts_hourly;

ALTER TABLE attempts_hourly_old RENAME TO attempts_hourly;

CREATE INDEX idx_attempts_hourly_hour ON attempts_hourly (hour);

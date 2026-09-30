-- How fast each port's link and each Wi-Fi client's connection run, as the
-- latest read found them. Rates are in bits per second and signal in dBm,
-- and zero is unknown in each.

-- The rate the link came up at, the fastest both ends offer, and its duplex.
ALTER TABLE topology_ports ADD COLUMN rate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE topology_ports ADD COLUMN capable INTEGER NOT NULL DEFAULT 0;
ALTER TABLE topology_ports ADD COLUMN full_duplex INTEGER NOT NULL DEFAULT 0 CHECK (full_duplex IN (0, 1));

-- The rate the radio sends to the client at and receives from it at, and the
-- client's signal at the radio.
ALTER TABLE topology_sightings ADD COLUMN tx_rate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE topology_sightings ADD COLUMN rx_rate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE topology_sightings ADD COLUMN signal INTEGER NOT NULL DEFAULT 0;

ALTER TABLE topology_sightings DROP COLUMN signal;
ALTER TABLE topology_sightings DROP COLUMN rx_rate;
ALTER TABLE topology_sightings DROP COLUMN tx_rate;
ALTER TABLE topology_ports DROP COLUMN full_duplex;
ALTER TABLE topology_ports DROP COLUMN capable;
ALTER TABLE topology_ports DROP COLUMN rate;

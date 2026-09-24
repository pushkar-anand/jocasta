-- Which side started a conversation. connections counts the ones the device
-- started, to the peer's service; connections_in the ones the peer started, to
-- the device's. A row can carry both: a device and a peer may each open the
-- same service on the other. Rows written before this column count none in.
ALTER TABLE traffic_hourly ADD COLUMN connections_in INTEGER NOT NULL DEFAULT 0;

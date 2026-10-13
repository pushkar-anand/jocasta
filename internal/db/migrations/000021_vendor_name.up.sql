-- The OUI table's short name keeps its column as vendor_short, and vendor
-- holds the registered name. vendor starts empty: only the OUI lookup in Go
-- can fill it, which each device's next sighting does.
ALTER TABLE devices RENAME COLUMN vendor TO vendor_short;
ALTER TABLE devices ADD COLUMN vendor TEXT;

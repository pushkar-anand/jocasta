-- What one scan changed is read by its scan id, once per finished scan.
CREATE INDEX idx_events_scan ON events (scan_id);

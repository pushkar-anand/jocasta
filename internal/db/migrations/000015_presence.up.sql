-- When the device's current run of sightings began. Null while it is quiet, so
-- going quiet and coming back are each announced once.
ALTER TABLE devices ADD COLUMN present_since TEXT;

-- Set by the owner: log, and send, when this device goes quiet or comes back.
ALTER TABLE devices ADD COLUMN is_watched INTEGER NOT NULL DEFAULT 0 CHECK (is_watched IN (0, 1));

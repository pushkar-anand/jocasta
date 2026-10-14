-- What a scan covered: FULL, every target it was set to read, or NEW_DEVICES,
-- only the devices a sweep had just discovered. A NEW_DEVICES port scan is
-- not the last port scan the overview shows.
ALTER TABLE scans ADD COLUMN scope TEXT NOT NULL DEFAULT 'FULL' CHECK (scope IN ('FULL', 'NEW_DEVICES'));

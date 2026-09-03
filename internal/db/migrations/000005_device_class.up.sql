-- The classifier's guess at what kind of device this is, and how much evidence
-- stood behind it ('low' | 'medium' | 'high'). Written only by a scan's
-- classify pass, from the vendor, the name, the open ports and the segment.
--
-- device_type, above, is the user's own answer and outranks this: a surface
-- shows device_type when it is set and falls back to device_class otherwise.
-- Neither column carries a CHECK -- the set of classes is added to in Go, and a
-- CHECK here would make every new one a migration.
ALTER TABLE devices
    ADD COLUMN device_class TEXT;

ALTER TABLE devices
    ADD COLUMN device_class_confidence TEXT;

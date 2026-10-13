ALTER TABLE devices DROP COLUMN vendor;
ALTER TABLE devices RENAME COLUMN vendor_short TO vendor;

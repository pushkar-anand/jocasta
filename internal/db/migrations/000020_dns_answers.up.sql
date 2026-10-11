-- The addresses a resolver answered each device's lookups with, so a traffic
-- peer can carry the name the device asked for before it reached the address.
-- Kept per device: a CDN address serves many names, and only the device's own
-- lookups say which one it wanted.
CREATE TABLE dns_answers
(
    device_id INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,

    -- An address in the answer.
    addr      TEXT    NOT NULL,

    -- The name the device looked up, lowercase, without the trailing dot. A
    -- name answered through a CNAME keeps the name asked.
    name      TEXT    NOT NULL,

    lookups   INTEGER NOT NULL DEFAULT 0,

    -- The start of the latest hour an answer for the name held the address,
    -- UTC.
    last_hour TEXT    NOT NULL,

    PRIMARY KEY (device_id, addr, name)
);

-- The prune.
CREATE INDEX idx_dns_answers_last_hour ON dns_answers (last_hour);

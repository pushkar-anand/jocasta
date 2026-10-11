-- What each device looked up, rolled up by the hour from a resolver's query
-- log. Log lines are never copied: the question asked of them is "which
-- names, how often, when", which an hourly count answers.
CREATE TABLE dns_hourly
(
    -- RESTRICT for the same reason as scans: dropping a source from config
    -- must not take the history it produced with it.
    source_id INTEGER NOT NULL REFERENCES sources (id) ON DELETE RESTRICT,

    -- The device that asked, resolved from the client address when the
    -- lookups were recorded.
    device_id INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,

    -- The start of the hour, UTC.
    hour      TEXT    NOT NULL,

    -- The name looked up, lowercase, without the trailing dot.
    name      TEXT    NOT NULL,

    lookups   INTEGER NOT NULL DEFAULT 0,

    -- Of those lookups, how many the resolver blocked, and how many found
    -- nothing or failed upstream. A blocked lookup is never counted as failed.
    blocked   INTEGER NOT NULL DEFAULT 0,
    failed    INTEGER NOT NULL DEFAULT 0,

    PRIMARY KEY (device_id, hour, source_id, name)
);

-- The prune.
CREATE INDEX idx_dns_hourly_hour ON dns_hourly (hour);

-- Where a polled source's last read left off, in whatever form the source
-- defines. It is written in the same transaction as the counts that read
-- produced, so a restart neither drops nor counts twice what was read.
CREATE TABLE source_cursors
(
    source_id INTEGER PRIMARY KEY REFERENCES sources (id) ON DELETE CASCADE,
    position  TEXT    NOT NULL
);

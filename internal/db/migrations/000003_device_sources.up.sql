-- What one source claims about one device, kept beside the resolution on
-- devices rather than merged into it, so two sources naming the same box
-- differently can both be shown.
CREATE TABLE device_sources
(
    device_id       INTEGER NOT NULL REFERENCES devices (id) ON DELETE CASCADE,
    source_id       INTEGER NOT NULL REFERENCES sources (id) ON DELETE CASCADE,

    -- An empty claim overwrites its predecessor, so a source with nothing left
    -- to say about a name can retract it.
    hostname        TEXT,

    -- Per claim rather than read off sources.kind: one router offers both a
    -- static lease name and a dynamic one, and they are not worth the same.
    hostname_source TEXT,

    -- What only this source knows, as JSON: a lease comment, the router
    -- interface. Never merged into devices.
    detail          TEXT CHECK (detail IS NULL OR JSON_VALID(detail)),

    -- Per-source presence, so "there is a bound lease but nothing has answered
    -- a ping in three days" is two claims disagreeing rather than one of them
    -- being wrong.
    first_seen      TEXT NOT NULL,
    last_seen       TEXT NOT NULL,
    PRIMARY KEY (device_id, source_id)
);

-- Answers "what does this source still hold, and how recently", the read behind
-- a source going quiet. The primary key answers the per-device direction.
CREATE INDEX idx_device_sources_source ON device_sources (source_id, last_seen DESC);

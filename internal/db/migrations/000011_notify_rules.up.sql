-- Which kinds of change a notification destination is sent. Destinations are
-- named in the config file, so destination is that name; one with no rows is
-- sent nothing.
CREATE TABLE notify_rules
(
    destination TEXT NOT NULL,
    kind        TEXT NOT NULL,

    PRIMARY KEY (destination, kind)
);

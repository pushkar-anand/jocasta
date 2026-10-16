# Back up and upgrade

Jocasta keeps all its data in one SQLite database: inventory, change log,
traffic, accounts, tokens and notification choices. Back up this database
and your config file.

## Back up

In the container, the database is `/data/jocasta.db`. Recent writes can be
in `jocasta.db-wal`, next to it. So stop Jocasta and copy the whole
directory:

```bash
docker stop jocasta
docker run --rm -v jocasta-data:/data -v "$PWD":/backup alpine \
  cp -a /data /backup/jocasta-backup
docker start jocasta
```

The copy contains password hashes and two-factor secrets. Keep it safe.

To restore:

1. Stop Jocasta.
2. Put the files back.
3. Start Jocasta.

## Upgrade

Jocasta is pre-1.0. A release can change the config format or the schema with
no upgrade path.

1. Read the [upgrade notes](upgrading.md) for every release after yours.
2. Back up.
3. Pull the new tag, such as `:v0.11.1`.
4. Recreate the container.

Jocasta updates the schema when it starts.

## Downgrade

Restore the backup from before the upgrade. If you start an older release
on a newer database, it migrates the schema down. This deletes the newer
data.

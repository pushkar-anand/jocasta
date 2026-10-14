# Back up and upgrade

Everything lives in one SQLite database: inventory, change log, traffic,
accounts, tokens and notification choices. Keep it and your config file.

## Back up

The database is `/data/jocasta.db` in the container. Recent writes can sit in
`jocasta.db-wal` beside it, so stop Jocasta and copy the whole directory:

```bash
docker stop jocasta
docker run --rm -v jocasta-data:/data -v "$PWD":/backup alpine \
  cp -a /data /backup/jocasta-backup
docker start jocasta
```

The copy holds password hashes and two-factor secrets.

To restore, stop Jocasta, put the files back, and start it.

## Upgrade

Jocasta is pre-1.0. A release can change the config format or the schema with
no upgrade path.

1. Read the [upgrade notes](upgrading.md) for every release after yours.
2. Back up.
3. Pull the new tag, such as `:v0.11.1`, and recreate the container.

The schema updates at startup.

## Downgrade

Restore the backup from before the upgrade. An older release started on a
newer database migrates the schema down and drops the newer data.

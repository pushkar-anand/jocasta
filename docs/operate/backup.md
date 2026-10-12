# Back up and upgrade

Jocasta keeps everything in one SQLite database: the inventory, the change
log, traffic and lookup counts, accounts, API tokens and notification choices.
The configuration file is the only other thing to keep.

## Back up

The database is `db.name` in the directory `db.path`, which is
`/data/jocasta.db` in the container. Jocasta writes it in SQLite's WAL mode,
so recent writes can sit in `jocasta.db-wal` beside it. Stop Jocasta, then
copy every file in the directory. With the `jocasta-data` volume from
[Your first inventory](../tutorial.md):

```bash
docker stop jocasta
docker run --rm -v jocasta-data:/data -v "$PWD":/backup alpine \
  cp -a /data /backup/jocasta-backup
docker start jocasta
```

The database holds password hashes and two-factor secrets. Keep the backup
where you would keep a password file.

To restore, stop Jocasta, replace the files in the directory with the ones
you backed up, and start Jocasta.

## Upgrade

Jocasta is pre-1.0, so a release can change the configuration format or the
database schema with no upgrade path. Before each upgrade:

1. Read the [release notes](https://github.com/pushkar-anand/jocasta/releases)
   for every release since yours.
2. Back up the database.
3. Pull the new release and recreate the container. Pin the version tag,
   such as `:v0.11.1`, so an upgrade happens only when you choose.

Jocasta brings the database schema up to date when it starts. The docs for
each release are in the version menu at the top of this page.

To go back to an older release, restore the backup you took before upgrading.
An older release started on a newer database moves the schema back down to its
own version, and drops the data the newer release added.

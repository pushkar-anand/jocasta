# Upgrade notes

What to change or expect when moving to a release, newest first. Read every section after the release you run, and [back up](<https://pushkar-anand.github.io/jocasta/dev/operate/backup/#back-up>) first. A release not listed here needs only the [usual steps](<https://pushkar-anand.github.io/jocasta/dev/operate/backup/#upgrade>).

## Unreleased

### Upgrade notes

- inventory: Stop using claims at addresses a device has left ([\#512](<https://github.com/pushkar-anand/jocasta/pull/512>))

  The first scan after the upgrade can rename devices that moved to a new address. Each rename logs a HOSTNAME\_CHANGED event.

## 0.4.0

Released 2026-09-25.

### Breaking changes

- config: Group retention, location, server settings ([\#205](<https://github.com/pushkar-anand/jocasta/pull/205>))

  a config that sets inventory.retention, auth.\* or mcp.enabled, or the matching JOCASTA\_ variables, now has those settings ignored. Move them to the new paths.

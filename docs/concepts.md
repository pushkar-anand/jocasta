# How Jocasta works

## Devices

A device is identified by its hardware (MAC) address. Its IP address is a
lease, so your label and notes stay put when the IP changes.

A sweep reads hardware addresses on its own segment only. A device on another
segment is known by IP until a router reports its hardware address. The device
page says which: **by hardware address** or **by IP address**. If a record for
that hardware address already exists, the two merge and keep your labels.

Phones, tablets and laptops make up a hardware address per Wi-Fi network.
Such an address names no vendor, and each new one is a new device. Devices you
never touch are deleted 90 days after they were last seen.

An address the device stops answering on is dropped once the device has
answered elsewhere in the same network for 6 hours.

## Networks

A network is an IP prefix, such as `192.0.2.0/24`. Jocasta sweeps the
prefixes under `networks`. A [router plugin](plugins/index.md) adds the
segments the router serves, with names and VLAN tags.

## Scans

A scan is one recorded reading:

- **Sweep.** An ICMP echo to every address in `networks`, then a name lookup
  for each one that answers. Every 5 minutes.
- **Port scan.** A TCP connection to a list of ports on every known address.
  Off until `scan.ports.enabled: true`, then every 6 hours.
- **Plugin read.** A router's ARP and DHCP tables, read with each sweep. A DNS
  resolver's query log, read every minute.

The Scans page lists them. To run one by hand, use
[`jocasta scan`](cli.md#scan), [`jocasta ports`](cli.md#ports) or
[`jocasta plugin run`](cli.md#plugin-run).

## Sources

Each scan files its facts under a source: `sweep:<host>` for the sweep, or the
plugin's instance name, such as `gateway`. The device page lists what each
source calls the device.

When sources disagree on a name, the higher standing wins. A static DHCP lease
beats a name the device announces. See [How names are chosen](naming.md).

## Presence

A device is seen recently if a scan saw it in the last 15 minutes, and quiet
otherwise. Both come from past scans. Opening a page starts none.

Watched devices log when they go quiet and come back. Others do not: phones
and laptops would fill the log. See
[Get told when a device goes quiet](notifications/watch.md).

## The change log

The Events page lists every change: new devices, addresses gained and
dropped, renames, ports opened and closed, type changes, and your own edits.
[Notifications](notifications/index.md) send them on.

## What you own

Label, group, type, notes, ignored and watched are yours. Scans never write
them. See [Label, group and ignore devices](devices.md).

Without a type from you, Jocasta guesses one. See
[How a device gets its type](classification.md).

## Retention

| Record | Kept for | Setting |
|---|---|---|
| Change log and scans | 90 days | `retention.history` |
| Hourly traffic totals | 30 days | `retention.traffic` |
| Hourly DNS lookup counts | 30 days | `retention.dns` |
| Devices you never touched, after last seen | 90 days | `retention.devices` |

`0s` keeps a record forever.

## Limits

- An open port is one that accepted a TCP connection. The service name is a
  guess from the port number.
- No recorded ports can mean port scanning is off.
- Traffic covers only what crossed the router while flow export was on.
- An internet address is named after the network that announces it. For a
  cloud address, that is the provider.

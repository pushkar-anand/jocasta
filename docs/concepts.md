# How Jocasta works

## Devices

Jocasta identifies a device by its hardware (MAC) address. An IP address is
only a lease. So your label and notes stay with the device when its IP
address changes.

A sweep reads hardware addresses only on its own segment. Jocasta knows a
device on another segment by its IP address until a router reports its
hardware address. The device page shows which: **by hardware address** or
**by IP address**. If a record for that hardware address already exists,
Jocasta merges the two and keeps your labels.

Phones, tablets and laptops make up a new hardware address for each Wi-Fi
network. Such an address names no vendor, and each new address is a new
device. Jocasta deletes a device 90 days after a scan last saw it, unless you
gave it a label, group, type or notes, or ignored or watched it.

If a device stops answering on an address, Jocasta removes that address
after the device answers on a different address in the same network for 6
hours.

## Networks

A network is an IP prefix, such as `192.0.2.0/24`. Jocasta sweeps the
prefixes under `networks`. If you list none, it sweeps the network of the
host. A [router plugin](plugins/index.md) adds the
segments that the router serves, with their names and VLAN tags.

## Scans

A scan is one recorded reading:

- **Sweep.** An ICMP echo to every address in `networks`, then a name lookup
  for each address that answers. Every 5 minutes.
- **Port scan.** A TCP connection to a list of ports on every known address.
  Off until you set `scan.ports.enabled: true`, then every 6 hours. Jocasta
  also scans a new device when a sweep or a router read finds it.
- **Plugin read.** A router's ARP and DHCP tables, read with each sweep. A DNS
  resolver's query log, read every minute.

The Scans page lists them. To start a scan yourself, use
[`jocasta scan`](cli.md#scan), [`jocasta ports`](cli.md#ports) or
[`jocasta plugin run`](cli.md#plugin-run).

## Sources

Each scan records its facts under a source. The sweep's source is
`sweep:<host>`. A plugin's source is its instance name, such as `gateway`.
The device page lists the name that each source gives the device.

If two sources give different names, the source with the higher standing
wins. For example, a static DHCP lease wins over a name that the device
announces. See [How names are chosen](naming.md).

## Presence

A device is seen recently if a scan saw it in the last 15 minutes. If not,
it is quiet. Both states come from past scans. A page that you open does not
start a scan.

A watched device logs an event when it goes quiet and when it comes back.
Other devices do not, because phones and laptops would fill the log. See
[Get told when a device goes quiet](notifications/watch.md).

## The change log

The Events page lists every change: new devices, addresses added and
removed, new names, ports opened and closed, type changes, and your own
edits. [Notifications](notifications/index.md) send these events to other
services.

## What you own

The label, group, type, notes, ignored flag and watched flag are yours.
Scans never change them. See [Label, group and ignore devices](devices.md).

If you do not give a device a type, Jocasta guesses one. See
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

- An open port is a port that accepted a TCP connection. Jocasta guesses the
  service name from the port number.
- If a device has no recorded ports, port scanning can be off.
- Traffic shows only what passed the router while flow export was on.
- Jocasta names an internet address after the network that announces it. For
  a cloud address, that is the provider.

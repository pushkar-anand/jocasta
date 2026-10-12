# How Jocasta works

This page explains the ideas the rest of the docs build on. Read it once you
have an inventory running, and come back when a word on another page needs
explaining.

## Devices

Jocasta keeps one record for each device it finds. A device is identified by
its hardware (MAC) address. An IP address is a lease the device holds for now,
so the label, group and notes you give a device stay with it when its address
changes.

A sweep can read hardware addresses only on its own segment. A device on
another segment is known by its IP address alone until a router reports the
hardware address behind it. The device page says which: **by hardware
address** or **by IP address**. When a source reports the hardware address of
a device known only by IP, the device is identified by it and the change log
records **Hardware address learned**. If a record for that hardware address
already exists, the two records are merged and keep the labels you gave either
one (**Duplicates merged**).

Phones, tablets and laptops make up a hardware address for each Wi-Fi network,
and some change it from time to time. The device page notes such an address.
It names no vendor, and a new one makes a new device. A device you have never
labelled, grouped, typed, noted, ignored or watched is deleted once no scan has
seen it for `retention.devices`, 90 days by default, so these leftovers clear
themselves.

An address a device stops answering on is kept for `inventory.address_grace`,
6 hours by default. If the device answers on another address in the same
network for that long, the old address is dropped (**Address dropped**).

## Networks

A network is an IP prefix, such as `192.0.2.0/24`. The prefixes listed under
`networks` are swept. A [router plugin](plugins/index.md) adds the segments the
router serves, with their names and VLAN tags. Each address is filed under the
network that contains it.

## Scans

A scan is one recorded reading of the network. There are three kinds:

- A **sweep** sends an ICMP echo to every address in `networks`, reads the
  hardware address of each one that answers on its own segment, and asks each
  one for its name. It runs every `scan.devices.interval`, 5 minutes by
  default.
- A **port scan** tries to open a TCP connection to a list of ports on every
  address the inventory holds on your networks. It is off until you set
  `scan.ports.enabled: true`, then runs every `scan.ports.interval`, 6 hours by
  default.
- A **plugin read** fetches what a router or other source knows, such as its
  ARP and DHCP tables. Routers are read with each sweep, and DNS resolvers
  every minute.

The Scans page lists every scan with what it found. To run one by hand, use
[`jocasta scan`](cli.md#scan), [`jocasta ports`](cli.md#ports) or
[`jocasta plugin run`](cli.md#plugin-run).

## Sources

Each scan files what it found under a source. The sweep's source is
`sweep:` and the host's name, or `scan.source` if you set one. A plugin's source
is the instance name you gave it, such as `gateway`. The device page's Sources
section lists what each source calls the device and when it last heard from
it.

Two sources can disagree. For a device's name, the source with the highest
standing wins: a static DHCP lease outranks a name a device announces for
itself. [How names are chosen](naming.md) lists the order.

## Presence

A device is **seen recently** when a scan saw it within the online window,
15 minutes by default (`inventory.online_window`), and **quiet** after that.
Neither is a live check: both say what past scans recorded.

Watch a device to be told when it goes quiet and when it comes back. Only
watched devices log these changes, because phones and laptops come and go all
day. See [Get told when a device goes quiet](notifications/watch.md).

## The change log

Every change a scan finds is written to the change log, which the Events page
and each device page show: a new device, a new or dropped address, a changed
name, a port opened or closed, a changed type, and a watched device going
quiet or coming back. Your own edits are logged too.
[Notifications](notifications/index.md) send these changes to your phone or chat.

## What you own

Each device has fields that only you set: its label, group, type, notes, and
whether it is ignored or watched. No scan or plugin writes them, so they
survive a new address, a new name and a merge. See
[Label, group and ignore devices](devices.md).

Jocasta guesses each device's type from its vendor, name, ports and the
services it advertises. A type you set replaces the guess. See
[How a device gets its type](classification.md).

## How long records are kept

| Record | Kept for | Setting |
|---|---|---|
| Change log and scans | 90 days | `retention.history` |
| Hourly traffic totals | 30 days | `retention.traffic` |
| Hourly DNS lookup counts | 30 days | `retention.dns` |
| Devices you never curated, after they were last seen | 90 days | `retention.devices` |

Set any of them to `0s` to keep that kind of record forever.

## What the records cannot tell you

- An open port is a TCP port that accepted a connection. The service name
  shown is the one usually found on that port number. Jocasta does not check
  which software answers.
- A device with no recorded ports may still listen on some: port scanning may
  be off, or may not have reached it.
- Traffic covers only what passed the router while flow export was on. A
  device with no traffic recorded may still have talked to devices on its own
  segment.
- An internet address is shown by the organisation that announces it. For a
  cloud or CDN address that is the provider, and the service behind it is
  unknown.

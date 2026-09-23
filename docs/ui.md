# The web UI

`jocasta serve` runs a web interface over the same store the JSON API reads.
Pages are server-rendered; htmx handles in-place updates and the periodic
refresh.

The screenshots below use synthetic data: addresses from RFC 5737, hardware
addresses from RFC 7042, invented names.

## Overview

Per-network counts, a presence summary, the last sweep, and recent changes.

![Overview](img/overview.png)

## Device list

Filter by group, network or presence. Search by name, address or vendor.
Ignored devices are hidden unless you ask for them.

![Device list](img/devices.png)

## Device page

Shows the identity Jocasta resolved, the user-owned fields (label, group, type,
notes, which no scan or plugin writes), every address the device holds and its
network, and what each source calls the device. Once a port scan has reached the
device (see [CLI](cli.md#ports)), a Ports section lists the TCP ports it was
found listening on, and the ones it has since stopped answering on as closed.

When traffic collection is set up (see
[Seeing who devices talk to](setup.md#seeing-who-devices-talk-to)), a Talks to
section lists who the device exchanged data with over the last 24 hours, 7 days
or 30 days: other devices on your network, linked to their pages, and internet
addresses grouped by the organisation that announces them, with how much was
sent and received each way. Each peer is one row whatever services it was
reached on, and local addresses no device holds collapse into one row per
subnet. The section can be narrowed to your network or the internet, to one
service, or by a search over names, addresses and organisations.

Connections the device started that never carried data -- a port that
refused, one nobody answered, a ping -- are listed apart under "Tried, but
nothing came of it", with how many were answered and the ports tried. When
they add up to probing the network, a notice at the top of the section says
so.

![Device page](img/device.png)

## Traffic page

When traffic collection is set up, the Traffic page summarises the whole
network over the last 24 hours, 7 days or 30 days: the busiest devices, the
organisations the network exchanges the most with, and which devices started
talking to an organisation for the first time this week. Each organisation is
one row that opens to the devices behind it, and the page can be narrowed to
one group's devices.

Its first card, "Probing your network", lists devices that within one hour
tried 20 or more addresses on your network, or 20 or more ports on one of
them, without the connections carrying data: what a scan looks like. A host
you run scans from appears there too.

## Network page

One segment, its VLAN tag and name, and the devices on it.

![Network page](img/network.png)

## Events

The full change log.

![Events](img/events.png)

## Light theme

A toggle in the header switches themes; the choice is stored per browser.

![Light theme](img/overview-light.png)

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
sent and received each way.

![Device page](img/device.png)

## Network page

One segment, its VLAN tag and name, and the devices on it.

![Network page](img/network.png)

## Events

The full change log.

![Events](img/events.png)

## Light theme

A toggle in the header switches themes; the choice is stored per browser.

![Light theme](img/overview-light.png)

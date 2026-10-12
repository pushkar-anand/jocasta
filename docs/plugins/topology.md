# Show what is plugged in where

The Topology page draws the network from the internet down: the router, each
switch and access point on the port it hangs from, and each device on its port
or Wi-Fi network. It is built from the bridge and Wi-Fi tables of every
[MikroTik](routeros.md) and [OpenWrt](openwrt.md) router Jocasta reads, so the
router alone already gives a first picture. A switch or access point that
announces itself to the router appears there with the devices behind it.

To see which port of a switch or access point each device is on, add it as
another instance with `topology_only: true`, under `routeros` or `openwrt`:

```yaml
plugins:
  routeros:
    gateway:
      enabled: true
      host: "192.0.2.1"
      # ...
    switch_core:
      enabled: true
      host: "192.0.2.2"
      user: "jocasta"
      password: "change-me"
      topology_only: true    # read what is plugged into it; the router lists the devices
```

A `topology_only` source is left out of device discovery, so its management
address cannot rename a network. Every RouterOS and OpenWrt source is read
for its topology on `scan.devices.interval`.

What each router's tables add is on its page:
[RouterOS](routeros.md#topology) and [OpenWrt](openwrt.md#topology).

Check a source with `jocasta plugin run switch_core`, which prints its ports,
the addresses learned on each, and its neighbours.

How to read the page is in [Tour the web UI](../ui.md#topology).

# Show what is plugged in where

The Topology page draws the network from the internet down: router, switches, access points, and each device on its port or Wi-Fi network. It comes from the bridge and Wi-Fi tables of each [MikroTik](<https://pushkar-anand.github.io/jocasta/0.12/plugins/routeros/index.md>) and [OpenWrt](<https://pushkar-anand.github.io/jocasta/0.12/plugins/openwrt/index.md>) router Jocasta reads. A switch or access point that announces itself to the router shows up with the devices behind it.

To see which switch port each device is on, read the switch too, with `topology_only: true`:

```
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
      topology_only: true    # ports only; the router lists the devices
```

A `topology_only` source finds no devices, so its management address cannot rename a network. Every RouterOS and OpenWrt source is read on `scan.devices.interval`.

`jocasta plugin run switch_core` prints its ports, the addresses on each, and its neighbours.

What each router's tables add: [RouterOS](<https://pushkar-anand.github.io/jocasta/0.12/plugins/routeros/#topology>), [OpenWrt](<https://pushkar-anand.github.io/jocasta/0.12/plugins/openwrt/#topology>). How to read the page: [Tour the web UI](<https://pushkar-anand.github.io/jocasta/0.12/ui/#topology>).

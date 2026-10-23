# Show what is plugged in where

The Topology page shows the network from the internet down. It shows the router, switches, access points, and each device on its port or Wi-Fi network. The data comes from the bridge and Wi-Fi tables of each [MikroTik](<https://pushkar-anand.github.io/jocasta/0.15/plugins/routeros/index.md>) and [OpenWrt](<https://pushkar-anand.github.io/jocasta/0.15/plugins/openwrt/index.md>) router that Jocasta reads. If a switch or access point announces itself to the router, the page shows it with the devices behind it.

To see the switch port of each device, also read the switch, with `topology_only: true`:

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

A `topology_only` source finds no devices. So its management address cannot change the name of a network. Jocasta reads every RouterOS and OpenWrt source at each `scan.devices.interval`.

`jocasta plugin run switch_core` prints the ports of the switch, the addresses on each port, and the neighbours of the switch.

For what the tables of each router add, see [RouterOS](<https://pushkar-anand.github.io/jocasta/0.15/plugins/routeros/#topology>) and [OpenWrt](<https://pushkar-anand.github.io/jocasta/0.15/plugins/openwrt/#topology>). For how to read the page, see [Tour the web UI](<https://pushkar-anand.github.io/jocasta/0.15/ui/#topology>).

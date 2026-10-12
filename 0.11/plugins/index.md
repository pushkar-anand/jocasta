# Plugins

A sweep from one machine only sees hardware addresses on its own segment. On a network split into VLANs, reading the router's ARP and DHCP tables identifies the rest. Plugins read these and other sources a sweep cannot reach: routers for devices and what is plugged in where, flow exports for who devices talk to, and DNS resolvers for what devices look up.

| Plugin | Devices and networks | Topology | Traffic | DNS lookups |
| --- | --- | --- | --- | --- |
| [MikroTik RouterOS](<https://pushkar-anand.github.io/jocasta/0.11/plugins/routeros/index.md>) | Yes | Yes | Through [NetFlow](<https://pushkar-anand.github.io/jocasta/0.11/plugins/netflow/index.md>) |  |
| [OpenWrt](<https://pushkar-anand.github.io/jocasta/0.11/plugins/openwrt/index.md>) | Yes | Yes | Through [NetFlow](<https://pushkar-anand.github.io/jocasta/0.11/plugins/netflow/index.md>) |  |
| [NetFlow](<https://pushkar-anand.github.io/jocasta/0.11/plugins/netflow/index.md>) |  |  | Yes |  |
| [Technitium](<https://pushkar-anand.github.io/jocasta/0.11/plugins/technitium/index.md>) |  |  |  | Yes |
| [Pi-hole](<https://pushkar-anand.github.io/jocasta/0.11/plugins/pihole/index.md>) |  |  |  | Yes |
| [AdGuard Home](<https://pushkar-anand.github.io/jocasta/0.11/plugins/adguard/index.md>) |  |  |  | Yes |

## Configure a plugin

Each plugin is a block under `plugins`, keyed by its kind and then by an instance name of your choosing:

```
plugins:
  routeros:
    gateway:                 # instance name, shown as the source
      enabled: true
      host: "192.0.2.1"
      # ...
```

The instance name becomes the source its facts are filed under, so two routers stay separate. Router and DNS resolver names are shared across their kinds, so a `routeros` instance and a `technitium` instance cannot both be called `gateway`. A `netflow` listener can share its router's name.

Any setting can come from the environment instead, which keeps passwords and tokens out of the file. The variable is the setting's path in capitals, with a double underscore between levels:

```
JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD=change-me
```

Every setting for every plugin, with its default, is listed in the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.11/reference/configuration/#plugins>).

With `ssl: false` a password or token crosses the network unencrypted, so Jocasta logs a warning at startup for each RouterOS, OpenWrt, Technitium, Pi-hole or AdGuard Home source set that way.

Check a plugin before starting the server by reading it once, by its instance name:

```
jocasta plugin run gateway
```

It prints what the source claims and changes nothing. See [CLI](<https://pushkar-anand.github.io/jocasta/0.11/cli/#plugin-run>).

## Topology

The Topology page draws the network from the internet down: the router, each switch and access point on the port it hangs from, and each device on its port or Wi-Fi network. It is built from the bridge and Wi-Fi tables of every [MikroTik](<https://pushkar-anand.github.io/jocasta/0.11/plugins/routeros/index.md>) and [OpenWrt](<https://pushkar-anand.github.io/jocasta/0.11/plugins/openwrt/index.md>) router Jocasta reads, so the router alone already gives a first picture. A switch or access point that announces itself to the router appears there with the devices behind it.

To see which port of a switch or access point each device is on, add it as another instance with `topology_only: true`, under `routeros` or `openwrt`:

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
      topology_only: true    # read what is plugged into it; the router lists the devices
```

A `topology_only` source is left out of device discovery, so its management address cannot rename a network. Every RouterOS and OpenWrt source is read for its topology on `scan.devices.interval`.

What each router's tables add is on its page: [RouterOS](<https://pushkar-anand.github.io/jocasta/0.11/plugins/routeros/#topology>) and [OpenWrt](<https://pushkar-anand.github.io/jocasta/0.11/plugins/openwrt/#topology>).

Check a source with `jocasta plugin run switch_core`, which prints its ports, the addresses learned on each, and its neighbours.

## DNS resolvers

Jocasta can record which names each device looks up, and which of those lookups were blocked, from the query log of a [Technitium DNS Server](<https://pushkar-anand.github.io/jocasta/0.11/plugins/technitium/index.md>), a [Pi-hole](<https://pushkar-anand.github.io/jocasta/0.11/plugins/pihole/index.md>) v6 or an [AdGuard Home](<https://pushkar-anand.github.io/jocasta/0.11/plugins/adguard/index.md>). It keeps hourly counts per device and name, never individual lookups, and the addresses each name was answered with, for `retention.dns` (30 days by default). With traffic collection on as well, a device's traffic names each address by the names that device looked up for it. Pi-hole logs no answer addresses, so its lookups name no traffic.

|  | Technitium | Pi-hole | AdGuard Home |
| --- | --- | --- | --- |
| Answer addresses, to name traffic | Yes | No | Yes |
| Login that can only read | Yes | No | No |
| Lookups taken per read | 20,000 | 20,000 | 20,000 |

Lookups count against the device whose address asked. When the router forwards its clients' lookups to the resolver, every lookup comes from the router. Hand out the resolver's address as the DNS server in the router's DHCP settings so each device asks it directly.

To check the setup, read the log once without starting the server, by the instance name under `plugins`:

```
jocasta plugin run resolver
```

It prints the last hour's lookups per device address and name.

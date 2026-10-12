# Plugins

A sweep from one machine sees hardware addresses only on its own segment.
Plugins read the sources a sweep cannot reach: routers for the devices on
every segment and what is plugged in where, flow exports for who devices talk
to, and DNS resolvers for what devices look up.

| Plugin | Devices and networks | Topology | Traffic | DNS lookups |
|---|---|---|---|---|
| [MikroTik RouterOS](routeros.md) | Yes | Yes | Through [NetFlow](netflow.md) | |
| [OpenWrt](openwrt.md) | Yes | Yes | Through [NetFlow](netflow.md) | |
| [NetFlow](netflow.md) | | | Yes | |
| [Technitium](technitium.md) | | | | Yes |
| [Pi-hole](pihole.md) | | | | Yes |
| [AdGuard Home](adguard.md) | | | | Yes |

## Configure a plugin

Each plugin is a block under `plugins`, keyed by its kind and then by an
instance name of your choosing:

```yaml
plugins:
  routeros:
    gateway:                 # instance name, shown as the source
      enabled: true
      host: "192.0.2.1"
      # ...
```

The instance name becomes the [source](../concepts.md#sources) its facts are
filed under, so two routers stay separate. Router and DNS resolver names are
shared across their kinds, so a `routeros` instance and a `technitium`
instance cannot both be called `gateway`. A `netflow` listener can share its
router's name.

Keep passwords and tokens out of the file by setting them in the environment,
as in `JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD`. See
[Configure](../configuration.md). Every setting for every plugin, with its
default, is in the
[configuration reference](../reference/configuration.md#plugins).

With `ssl: false` a password or token crosses the network unencrypted, so
Jocasta logs a warning at startup for each RouterOS, OpenWrt, Technitium,
Pi-hole or AdGuard Home source set that way.

## Check a plugin

Read a source once, by its instance name, before starting the server:

```sh
jocasta plugin run gateway
```

It prints what the source claims and changes nothing. See
[CLI](../cli.md#plugin-run).

## Topology

A MikroTik or OpenWrt router gives the Topology page its first picture. To
see which port of each switch and access point a device is on, read those as
well. See [Show what is plugged in where](topology.md).

## DNS resolvers

Technitium, Pi-hole and AdGuard Home give each device's lookups. See
[Record what devices look up](dns.md).

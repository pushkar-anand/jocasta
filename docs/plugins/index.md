# Plugins

Plugins read what a sweep cannot reach:

- A router gives the devices on every segment, and what is connected where.
- A flow export gives who talks to whom.
- A DNS resolver gives what devices look up.

| Plugin | Devices and networks | Topology | Traffic | DNS lookups |
|---|---|---|---|---|
| [MikroTik RouterOS](routeros.md) | Yes | Yes | Through [NetFlow](netflow.md) | |
| [OpenWrt](openwrt.md) | Yes | Yes | Through [NetFlow](netflow.md) | |
| [NetFlow](netflow.md) | | | Yes | |
| [Technitium](technitium.md) | | | | Yes |
| [Pi-hole](pihole.md) | | | | Yes |
| [AdGuard Home](adguard.md) | | | | Yes |

## Configure a plugin

Each plugin is a block under `plugins`. The first key is the plugin kind.
The second key is an instance name that you choose:

```yaml
plugins:
  routeros:
    gateway:                 # instance name, shown as the source
      enabled: true
      host: "192.0.2.1"
      # ...
```

The instance name is the [source](../concepts.md#sources) of the facts that
the plugin records. Routers and DNS resolvers share one set of names. For
example, a `routeros` instance and a `technitium` instance cannot both be
`gateway`. A `netflow` listener can have the same name as its router.

Put passwords in the environment, such as
`JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD`. See
[Configure](../configuration.md). The
[configuration reference](../reference/configuration.md#plugins) lists every
setting.

A source with `ssl: false` sends its password as plain text. Jocasta shows a
warning when it starts.

## Check a plugin

```sh
jocasta plugin run gateway
```

This command reads the source once and prints what the source reports. It
changes nothing. See [CLI](../cli.md#plugin-run).

## Topology

The Topology page comes from a MikroTik or OpenWrt router. To see the port
of each device, also read the switches and access points. See
[Show what is plugged in where](topology.md).

## DNS resolvers

Technitium, Pi-hole and AdGuard Home give the lookups of each device. See
[Record what devices look up](dns.md).

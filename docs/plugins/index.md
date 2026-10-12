# Plugins

Plugins read what a sweep cannot reach: routers for devices on every segment
and what is plugged in where, flow exports for who talks to whom, and DNS
resolvers for what devices look up.

| Plugin | Devices and networks | Topology | Traffic | DNS lookups |
|---|---|---|---|---|
| [MikroTik RouterOS](routeros.md) | Yes | Yes | Through [NetFlow](netflow.md) | |
| [OpenWrt](openwrt.md) | Yes | Yes | Through [NetFlow](netflow.md) | |
| [NetFlow](netflow.md) | | | Yes | |
| [Technitium](technitium.md) | | | | Yes |
| [Pi-hole](pihole.md) | | | | Yes |
| [AdGuard Home](adguard.md) | | | | Yes |

## Configure a plugin

Each plugin is a block under `plugins`, keyed by kind, then by an instance
name you choose:

```yaml
plugins:
  routeros:
    gateway:                 # instance name, shown as the source
      enabled: true
      host: "192.0.2.1"
      # ...
```

The instance name is the [source](../concepts.md#sources) its facts are filed
under. Router and DNS resolver names share one namespace: a `routeros` and a
`technitium` instance cannot both be `gateway`. A `netflow` listener can share
its router's name.

Put passwords in the environment, such as
`JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD`; see
[Configure](../configuration.md). Every setting is in the
[configuration reference](../reference/configuration.md#plugins).

A source with `ssl: false` sends its password in the clear, and Jocasta warns
at startup.

## Check a plugin

```sh
jocasta plugin run gateway
```

reads the source once and prints what it claims. It changes nothing. See
[CLI](../cli.md#plugin-run).

## Topology

A MikroTik or OpenWrt router draws the Topology page. Read its switches and
access points too for each device's port. See
[Show what is plugged in where](topology.md).

## DNS resolvers

Technitium, Pi-hole and AdGuard Home give each device's lookups. See
[Record what devices look up](dns.md).

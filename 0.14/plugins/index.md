# Plugins

Plugins read what a sweep cannot reach: routers for devices on every segment and what is plugged in where, flow exports for who talks to whom, and DNS resolvers for what devices look up.

| Plugin | Devices and networks | Topology | Traffic | DNS lookups |
| --- | --- | --- | --- | --- |
| [MikroTik RouterOS](<https://pushkar-anand.github.io/jocasta/0.14/plugins/routeros/index.md>) | Yes | Yes | Through [NetFlow](<https://pushkar-anand.github.io/jocasta/0.14/plugins/netflow/index.md>) |  |
| [OpenWrt](<https://pushkar-anand.github.io/jocasta/0.14/plugins/openwrt/index.md>) | Yes | Yes | Through [NetFlow](<https://pushkar-anand.github.io/jocasta/0.14/plugins/netflow/index.md>) |  |
| [NetFlow](<https://pushkar-anand.github.io/jocasta/0.14/plugins/netflow/index.md>) |  |  | Yes |  |
| [Technitium](<https://pushkar-anand.github.io/jocasta/0.14/plugins/technitium/index.md>) |  |  |  | Yes |
| [Pi-hole](<https://pushkar-anand.github.io/jocasta/0.14/plugins/pihole/index.md>) |  |  |  | Yes |
| [AdGuard Home](<https://pushkar-anand.github.io/jocasta/0.14/plugins/adguard/index.md>) |  |  |  | Yes |

## Configure a plugin

Each plugin is a block under `plugins`, keyed by kind, then by an instance name you choose:

```
plugins:
  routeros:
    gateway:                 # instance name, shown as the source
      enabled: true
      host: "192.0.2.1"
      # ...
```

The instance name is the [source](<https://pushkar-anand.github.io/jocasta/0.14/concepts/#sources>) its facts are filed under. Router and DNS resolver names share one namespace: a `routeros` and a `technitium` instance cannot both be `gateway`. A `netflow` listener can share its router's name.

Put passwords in the environment, such as `JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD`; see [Configure](<https://pushkar-anand.github.io/jocasta/0.14/configuration/index.md>). Every setting is in the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.14/reference/configuration/#plugins>).

A source with `ssl: false` sends its password in the clear, and Jocasta warns at startup.

## Check a plugin

```
jocasta plugin run gateway
```

reads the source once and prints what it claims. It changes nothing. See [CLI](<https://pushkar-anand.github.io/jocasta/0.14/cli/#plugin-run>).

## Topology

A MikroTik or OpenWrt router draws the Topology page. Read its switches and access points too for each device's port. See [Show what is plugged in where](<https://pushkar-anand.github.io/jocasta/0.14/plugins/topology/index.md>).

## DNS resolvers

Technitium, Pi-hole and AdGuard Home give each device's lookups. See [Record what devices look up](<https://pushkar-anand.github.io/jocasta/0.14/plugins/dns/index.md>).

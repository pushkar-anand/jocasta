# OpenWrt

Jocasta reads OpenWrt over ubus, the JSON-RPC endpoint LuCI uses, at `http://<router>/ubus`. Any router with LuCI installed serves it, so there is nothing to install. Jocasta reads the neighbour table, the DHCP leases and the static leases, and the interfaces with their VLANs. For the Topology page it also reads the ports, the bridge table and the Wi-Fi clients. It changes nothing on the router.

Give Jocasta a login of its own that can only read. On the router:

1. Copy [`openwrt/jocasta.json`](<https://pushkar-anand.github.io/jocasta/0.13/openwrt/jocasta.json>) to `/usr/share/rpcd/acl.d/jocasta.json`. It grants the reads Jocasta makes and nothing else. LuCI's own groups do not grant the bridge table, so root cannot read it without this file either.
2. Hash a password for the login:

```
uhttpd -m 'change-me'
```

1. Add the login to `/etc/config/rpcd`, with the hash the last step printed:

```
config login
 option username 'jocasta'
 option password '$1$...'
 list read 'jocasta'
```

1. Reload rpcd:

```
/etc/init.d/rpcd reload
```

Then add the router to Jocasta:

```
plugins:
  openwrt:
    gateway:                 # instance name, shown as the source
      enabled: true
      host: "192.0.2.1"
      user: "jocasta"
      password: "change-me"  # or JOCASTA_PLUGINS__OPENWRT__GATEWAY__PASSWORD
      ssl: false             # true once luci-ssl is installed
      insecure: false        # true for uhttpd's self-signed cert
```

Check it with `jocasta plugin run gateway`. A login rpcd refuses, or one missing the ACL file, reads as credentials rejected.

A name you set under Network → DHCP and DNS → Static Leases ranks above the name a device asks for, as a static lease does on RouterOS. A network is named after its OpenWrt interface, such as `lan` or `iot`, and an interface on a VLAN device carries its tag.

## Topology

What each OpenWrt table adds to the [Topology page](<https://pushkar-anand.github.io/jocasta/0.13/plugins/topology/index.md>):

- The kernel's bridge table says which port each hardware address is behind. On a router with a DSA switch, as most current ones have, each switch port is a device of its own, so the table names the port. On an older router with a swconfig switch, every switch port is one device, so the table names only that device. rpcd reads at most 256 entries of the table. A bridge that holds more shows the first 256, and the read logs a warning saying so.
- The bridge-vlan sections of the network configuration say which VLANs each port carries.
- iwinfo's station list marks Wi-Fi clients and names their network and band, with the rate each way and the signal.
- LuCI's device list gives the rate each port's link runs at.

OpenWrt runs no neighbour discovery by default, so an OpenWrt source names no neighbours. The router still finds an OpenWrt access point among the addresses on its ports when the access point is read too.

To read an OpenWrt access point for its ports, add it as another instance with `topology_only: true`. See [Show what is plugged in where](<https://pushkar-anand.github.io/jocasta/0.13/plugins/topology/index.md>).

## Export traffic flows

To record who devices talk to, install softflowd and point it at the Jocasta host (here `192.0.2.10`), then set up the [NetFlow listener](<https://pushkar-anand.github.io/jocasta/0.13/plugins/netflow/index.md>):

```
opkg update && opkg install softflowd    # OpenWrt 24.10 and older
apk update && apk add softflowd          # OpenWrt 25.12 and newer
uci set softflowd.@softflowd[0].enabled='1'
uci set softflowd.@softflowd[0].host_port='192.0.2.10:2055'
uci set softflowd.@softflowd[0].export_version='9'
uci set softflowd.@softflowd[0].track_ipv6='1'
uci set softflowd.@softflowd[0].timeout='maxlife=300'
uci delete softflowd.@softflowd[0].sampling_rate
uci commit softflowd
/etc/init.d/softflowd enable
/etc/init.d/softflowd start
```

Each line changes a default that would cost you traffic:

- `export_version` 9 carries IPv6, which the default, 5, cannot. IPFIX (`10`) works too.
- `track_ipv6` records IPv6 conversations as well as IPv4.
- `maxlife=300` reports a long download every five minutes. Without it, a conversation is reported only when it ends, so a stream that runs all evening lands in the hour it stopped.
- Deleting `sampling_rate` counts every packet. The default, `100`, counts one in 100. Jocasta scales sampled counts back up, but a conversation of a few packets may not be counted at all.

softflowd watches `br-lan`, the default LAN bridge. To record another segment, such as a VLAN on `br-lan.20`, add a softflowd section for its interface, with a `pid_file` and `control_socket` of its own. List the router's LAN address in `exporters`: that is the address the exports come from.

## All settings

Every `plugins.openwrt` setting, with its default and allowed values, is in the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.13/reference/configuration/#pluginsopenwrt>).

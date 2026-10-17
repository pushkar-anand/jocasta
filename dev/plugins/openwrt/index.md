# OpenWrt

Jocasta reads OpenWrt over ubus, at `http://<router>/ubus`. ubus is the JSON-RPC endpoint that LuCI uses. Every router with LuCI serves it, so you do not need to install anything. Jocasta reads the neighbour table, the DHCP leases, the static leases, and the interfaces with their VLANs. For the Topology page, it also reads the ports, the bridge table and the Wi-Fi clients. It changes nothing on the router.

Give Jocasta its own login, which can only read. On the router:

1. Copy [`openwrt/jocasta.json`](<https://pushkar-anand.github.io/jocasta/dev/openwrt/jocasta.json>) to `/usr/share/rpcd/acl.d/jocasta.json`. This file allows the reads that Jocasta makes and nothing else. The groups of LuCI do not allow reads of the bridge table. So without this file, root also cannot read it.
2. Hash a password for the login:

```
uhttpd -m 'change-me'
```

1. Add the login to `/etc/config/rpcd`, with the hash from the last step:

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

Test the source with `jocasta plugin run gateway`. If rpcd refuses the login, or the ACL file is missing, Jocasta reports rejected credentials.

A name that you set under Network → DHCP and DNS → Static Leases ranks above the name that a device asks for. A static lease on RouterOS works the same way. Jocasta names a network after its OpenWrt interface, such as `lan` or `iot`. An interface on a VLAN device has the VLAN tag.

## Topology

Each OpenWrt table adds this to the [Topology page](<https://pushkar-anand.github.io/jocasta/dev/plugins/topology/index.md>):

- **Bridge table of the kernel**: the port behind which each hardware address is. Most current routers have a DSA switch. On a DSA switch, each switch port is a separate device, so the table names the port. An older router has a swconfig switch. On a swconfig switch, all switch ports are one device, so the table names only that device. rpcd reads at most 256 entries of the table. If a bridge has more, Jocasta shows the first 256 and logs a warning.
- **bridge-vlan sections** of the network configuration: the VLANs on each port.
- **Station list of iwinfo**: which devices are Wi-Fi clients, with their network, band, signal and rate in each direction.
- **Device list of LuCI**: the link rate of each port.

By default, OpenWrt runs no neighbour discovery. So an OpenWrt source names no neighbours. But if Jocasta also reads an OpenWrt access point, the router finds the access point among the addresses on its ports.

To read the ports of an OpenWrt access point, add it as another instance with `topology_only: true`. See [Show what is plugged in where](<https://pushkar-anand.github.io/jocasta/dev/plugins/topology/index.md>).

## Export traffic flows

To record who devices talk to, install softflowd and send its exports to the Jocasta host. Then set up the [NetFlow listener](<https://pushkar-anand.github.io/jocasta/dev/plugins/netflow/index.md>). In this example, the Jocasta host is `192.0.2.10`:

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

Each line changes a default. With the defaults, Jocasta does not see some of the traffic:

- `export_version` 9 carries IPv6. The default, 5, cannot. IPFIX (`10`) also works.
- `track_ipv6` records IPv6 conversations, not only IPv4.
- `maxlife=300` reports a long download every five minutes. Without it, softflowd reports a conversation only when it ends. So a stream that runs all evening shows in the hour when it stopped.
- When you delete `sampling_rate`, softflowd counts every packet. The default, `100`, counts one packet in 100. Jocasta scales sampled counts back up. But it can miss a conversation of only a few packets.

softflowd watches `br-lan`, the default LAN bridge. To record another segment, such as a VLAN on `br-lan.20`, add a softflowd section for its interface. Give that section its own `pid_file` and `control_socket`. In `exporters`, list the LAN address of the router, because the exports come from that address.

## All settings

The [configuration reference](<https://pushkar-anand.github.io/jocasta/dev/reference/configuration/#pluginsopenwrt>) lists every `plugins.openwrt` setting, with its default and allowed values.

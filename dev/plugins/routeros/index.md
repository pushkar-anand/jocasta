# MikroTik RouterOS

Jocasta reads RouterOS over its REST API:

```
plugins:
  routeros:
    gateway:                 # instance name, shown as the source
      enabled: true
      host: "192.0.2.1"
      user: "jocasta"        # a read-only user is enough
      password: "change-me"  # or JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD
      ssl: true
      insecure: true         # RouterOS serves a self-signed cert unless you import one
```

Before you start the server, test the source with `jocasta plugin run gateway`. See [CLI](<https://pushkar-anand.github.io/jocasta/dev/cli/#plugin-run>).

## Topology

Each RouterOS table adds this to the [Topology page](<https://pushkar-anand.github.io/jocasta/dev/plugins/topology/index.md>):

- **Bridge host table**: the port behind which each hardware address is. If the bridge has VLAN filtering on, it also gives the VLAN.
- **Neighbour discovery** (MNDP or LLDP): the name of the switch or access point on each port. By default, RouterOS runs it on the `LAN` interface list. See `/ip neighbor discovery-settings`.
- **Wi-Fi registration table**: which devices are Wi-Fi clients, with their network, band, signal and rate in each direction. It comes from the wifi package or the wireless package. A router that manages access points with CAPsMAN also lists their clients.
- **Ethernet monitor**: the link rate of each Ethernet and SFP port, and the fastest rate that both ends offer.

To read the ports of a MikroTik switch or access point, add it as another instance with `topology_only: true`. See [Show what is plugged in where](<https://pushkar-anand.github.io/jocasta/dev/plugins/topology/index.md>).

## Export traffic flows

To record who devices talk to, send Traffic Flow to the Jocasta host. Then set up the [NetFlow listener](<https://pushkar-anand.github.io/jocasta/dev/plugins/netflow/index.md>). In this example, the Jocasta host is `192.0.2.10`:

```
/ip traffic-flow set enabled=yes interfaces=all
/ip traffic-flow target add dst-address=192.0.2.10 port=2055 version=ipfix
```

## All settings

The [configuration reference](<https://pushkar-anand.github.io/jocasta/dev/reference/configuration/#pluginsrouteros>) lists every `plugins.routeros` setting, with its default and allowed values.

# MikroTik RouterOS

Jocasta reads RouterOS over its REST API:

```yaml
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

Check it with `jocasta plugin run gateway` before starting the server. See
[CLI](../cli.md#plugin-run).

## Topology

What each RouterOS table adds to the [Topology page](index.md#topology):

- The bridge host table says which port each hardware address is behind, and
  in which VLAN when the bridge has VLAN filtering on.
- Neighbour discovery (MNDP or LLDP) names the switch or access point on each
  port. RouterOS runs it on the `LAN` interface list by default; see
  `/ip neighbor discovery-settings`.
- The Wi-Fi registration table marks Wi-Fi clients and names their network
  and band, from the wifi or the wireless package, with the rate each way and
  the signal. A router managing access points with CAPsMAN lists their
  clients too.
- The Ethernet monitor gives the rate each Ethernet and SFP port's link runs
  at, and the fastest rate both ends offer.

To read a MikroTik switch or access point for its ports, add it as another
instance with `topology_only: true`. See [Topology](index.md#topology).

## Export traffic flows

To record who devices talk to, point Traffic Flow at the Jocasta host (here
`192.0.2.10`) and set up the [NetFlow listener](netflow.md):

```
/ip traffic-flow set enabled=yes interfaces=all
/ip traffic-flow target add dst-address=192.0.2.10 port=2055 version=ipfix
```

## All settings

Every `plugins.routeros` setting, with its default and allowed values, is in the
[configuration reference](../reference/configuration.md#pluginsrouteros).

# NetFlow

Jocasta can record which devices talk to which, and to where on the internet, from the flow records your router exports (NetFlow v5, v9 or IPFIX). It keeps hourly totals per device, never individual connections, for `retention.traffic` (30 days by default).

```
plugins:
  netflow:
    gateway:
      enabled: true
      listen: ":2055"
      exporters:
        - "192.0.2.1"        # the router's address; anything else is dropped
```

`exporters` is required. Only datagrams from the listed addresses are read. A device on the same network can forge that address, so bind `listen` to an address only the router reaches, or allow the port only from the router in your firewall.

With host networking the listener is reachable as it is. On a bridge network, publish the port as UDP: `-p 2055:2055/udp`.

## Set up the router

Point the router's flow export at the Jocasta host and port:

- [MikroTik RouterOS](<https://pushkar-anand.github.io/jocasta/0.11/plugins/routeros/#export-traffic-flows>): Traffic Flow, as IPFIX.
- [OpenWrt](<https://pushkar-anand.github.io/jocasta/0.11/plugins/openwrt/#export-traffic-flows>): softflowd, as NetFlow v9.

Export from one router only. Two routers that both see a conversation both report it, and it is counted twice.

Internet addresses are shown by the organisation that announces them, and placed on the map by the country they are registered in, using [IP to ASN data](<https://db-ip.com/db/download/ip-to-asn-lite>) and [IP to Country data](<https://db-ip.com/db/download/ip-to-country-lite>) by [DB-IP](<https://db-ip.com>), licensed under [CC BY 4.0](<https://creativecommons.org/licenses/by/4.0/>).

The world map draws a line from the network's country to each country it talked to. Jocasta finds its country from the router's outside address. When that address is private, because the ISP puts the router behind carrier-grade NAT, name the country yourself, by its two-letter code. A country named here is used whatever the outside address says:

```
location:
  country: "AU"
```

Only the country is used, to start the lines from its middle; nothing finer is asked for or stored.

## All settings

Every `plugins.netflow` setting, with its default and allowed values, is in the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.11/reference/configuration/#pluginsnetflow>).

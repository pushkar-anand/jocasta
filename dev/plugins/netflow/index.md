# NetFlow

Jocasta can record which devices talk to each other, and to where on the internet. It reads the flow records that your router exports (NetFlow v5, v9 or IPFIX). It keeps hourly totals for each device for `retention.traffic`, which is 30 days by default. It does not store single connections.

```
plugins:
  netflow:
    gateway:
      enabled: true
      listen: ":2055"
      exporters:
        - "192.0.2.1"        # the router's address; anything else is dropped
```

You must set `exporters`. Jocasta reads only datagrams from the listed addresses. A device on the same network can forge such an address. So bind `listen` to an address that only the router reaches. Or, in your firewall, allow the port only from the router.

With host networking, no more setup is necessary. On a bridge network, publish the port as UDP: `-p 2055:2055/udp`.

## Set up the router

Send the flow export of the router to the Jocasta host and port:

- [MikroTik RouterOS](<https://pushkar-anand.github.io/jocasta/dev/plugins/routeros/#export-traffic-flows>): Traffic Flow, as IPFIX.
- [OpenWrt](<https://pushkar-anand.github.io/jocasta/dev/plugins/openwrt/#export-traffic-flows>): softflowd, as NetFlow v9.

Export from one router only. If two routers see the same conversation, both report it, and Jocasta counts it twice.

Jocasta shows an internet address by the organisation that announces it. It puts the address on the map by the country where it is registered. For this, it uses [IP to ASN data](<https://db-ip.com/db/download/ip-to-asn-lite>) and [IP to Country data](<https://db-ip.com/db/download/ip-to-country-lite>) by [DB-IP](<https://db-ip.com>), licensed under [CC BY 4.0](<https://creativecommons.org/licenses/by/4.0/>).

The world map draws a line from the country of the network to each country that it talked to. Jocasta finds the country of the network from the outside address of the router. This address is private if the ISP puts the router behind carrier-grade NAT. In that case, set the country yourself, by its two-letter code. Jocasta then uses this country, whatever the outside address is:

```
location:
  country: "AU"
```

Jocasta uses only the country, to start the lines from its middle. It does not ask for or store a more exact location.

## All settings

The [configuration reference](<https://pushkar-anand.github.io/jocasta/dev/reference/configuration/#pluginsnetflow>) lists every `plugins.netflow` setting, with its default and allowed values.

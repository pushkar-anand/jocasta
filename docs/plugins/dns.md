# Record what devices look up

Jocasta can record which names each device looks up, and which of those
lookups were blocked, from the query log of a
[Technitium DNS Server](technitium.md), a [Pi-hole](pihole.md) v6 or an
[AdGuard Home](adguard.md). It keeps hourly counts per device and name, and
the addresses each name was answered with, for `retention.dns` (30 days by
default). Individual lookups are not stored. With traffic collection on as
well, a device's traffic names each address by the names that device looked
up for it.

| | Technitium | Pi-hole | AdGuard Home |
|---|---|---|---|
| Answer addresses, to name traffic | Yes | No | Yes |
| Login that can only read | Yes | No | No |

Each resolver is read every minute, and each read takes at most 20,000
lookups.

## Route lookups to the resolver

Lookups count against the device whose address asked. When the router
forwards its clients' lookups to the resolver, every lookup comes from the
router. Hand out the resolver's address as the DNS server in the router's DHCP
settings so each device asks it directly.

## Check the setup

Read the log once without starting the server, by the instance name under
`plugins`:

```sh
jocasta plugin run resolver
```

It prints the last hour's lookups per device address and name.

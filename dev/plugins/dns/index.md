# Record what devices look up

Jocasta reads the query log of [Technitium](<https://pushkar-anand.github.io/jocasta/dev/plugins/technitium/index.md>), [Pi-hole](<https://pushkar-anand.github.io/jocasta/dev/plugins/pihole/index.md>) v6 or [AdGuard Home](<https://pushkar-anand.github.io/jocasta/dev/plugins/adguard/index.md>), and keeps hourly counts of each name each device looked up, blocked or not. With traffic collection on, a device's traffic names each address after the names it looked up.

|  | Technitium | Pi-hole | AdGuard Home |
| --- | --- | --- | --- |
| Answer addresses, to name traffic | Yes | No | Yes |
| Read-only login | Yes | No | No |

Each resolver is read every minute, up to 20,000 lookups a read. Counts are kept for `retention.dns`, 30 days by default.

## Point devices at the resolver

A lookup counts against the address that asked. If the router forwards its clients' lookups, they all count against the router. Hand out the resolver's address as the DNS server in the router's DHCP settings.

## Check it

```
jocasta plugin run resolver
```

prints the last hour's lookups per device and name.

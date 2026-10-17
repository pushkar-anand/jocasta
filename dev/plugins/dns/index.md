# Record what devices look up

Jocasta reads the query log of [Technitium](<https://pushkar-anand.github.io/jocasta/dev/plugins/technitium/index.md>), [Pi-hole](<https://pushkar-anand.github.io/jocasta/dev/plugins/pihole/index.md>) v6 or [AdGuard Home](<https://pushkar-anand.github.io/jocasta/dev/plugins/adguard/index.md>). For each device, it keeps hourly counts of each name that the device looked up, blocked or not. If traffic collection is on, the traffic of a device names each address after the names that the device looked up.

|  | Technitium | Pi-hole | AdGuard Home |
| --- | --- | --- | --- |
| Answer addresses, to name traffic | Yes | No | Yes |
| Read-only login | Yes | No | No |

Jocasta reads each resolver every minute, up to 20,000 lookups each time. It keeps the counts for `retention.dns`, which is 30 days by default.

## Point devices at the resolver

Jocasta counts a lookup against the address that asked. If the router forwards the lookups of its clients, Jocasta counts them all against the router. So in the DHCP settings of the router, give out the address of the resolver as the DNS server.

## Check it

```
jocasta plugin run resolver
```

This command prints the lookups of the last hour, for each device and name.

# Fix common problems

Not listed here? Ask in [GitHub issues](<https://github.com/pushkar-anand/jocasta/issues>) with the log lines around the problem.

## Devices have no hardware address or vendor

On a bridge network, the container reads its own neighbour table, which holds none of your devices. Use `--network host`.

Devices on other segments need a [router plugin](<https://pushkar-anand.github.io/jocasta/dev/plugins/index.md>).

## One phone shows up as several devices

It makes up a new hardware address now and then, and each one is a new device. Untouched ones are deleted after 90 days. On your own phone, turn off the private address for your home Wi-Fi.

## A device is missing

The sweep finds devices that answer ping. Windows drops ping by default. A [router plugin](<https://pushkar-anand.github.io/jocasta/dev/plugins/index.md>) lists such devices from the router's tables.

## "no usable ICMP socket"

A binary run without root needs unprivileged ICMP for its group:

```
sysctl net.ipv4.ping_group_range
```

Widen the range to cover Jocasta's group, or use the container image.

## No mDNS, DNS-SD or SSDP names

These reach Jocasta's own segment only, and DNS-SD and SSDP need multicast. Use `--network host`. See [How names are chosen](<https://pushkar-anand.github.io/jocasta/dev/naming/index.md>).

## The sign-in page keeps coming back

The session cookie is HTTPS-only. See [Serve over HTTPS](<https://pushkar-anand.github.io/jocasta/dev/operate/https/index.md>).

## A form shows "Cross-origin request refused"

Your reverse proxy changes the `Host` header. See [Serve over HTTPS](<https://pushkar-anand.github.io/jocasta/dev/operate/https/#other-proxies>).

## A plugin reads as "source rejected credentials"

Test it alone: `jocasta plugin run gateway`.

- RouterOS: the user needs read access, and the `www-ssl` service on, or `www` with `ssl: false`.
- OpenWrt: the login needs the [ACL file](<https://pushkar-anand.github.io/jocasta/dev/plugins/openwrt/index.md>).
- Technitium: the token's user needs View on Logs.
- An environment variable beats the file. Check both.

## The Traffic page stays empty

- `exporters` must list the address the exports come from. The log warns "dropping datagrams from a sender not listed in exporters" with that address.
- On a bridge network, publish the port as UDP: `-p 2055:2055/udp`.
- Open the listen port in the host's firewall.

## Every DNS lookup is counted against the router

The router forwards its clients' lookups. Hand out the resolver's address in the router's DHCP settings. See [Record what devices look up](<https://pushkar-anand.github.io/jocasta/dev/plugins/dns/index.md>).

## Times are hours off

The container runs in UTC. Set `location.timezone`, such as `Australia/Sydney`.

## A notification did not arrive

The account menu's **Notifications** page shows the last error under the destination. **Send test** tries again. Failed messages are not resent.

## An MCP client gets a 404

Set `server.mcp.enabled: true` and restart.

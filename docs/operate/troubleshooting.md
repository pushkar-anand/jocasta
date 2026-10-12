# Fix common problems

Find the symptom, then try the fix under it. If none fits, ask in
[GitHub issues](https://github.com/pushkar-anand/jocasta/issues) with the log
lines around the problem.

## Devices have no hardware address or vendor

A sweep reads hardware addresses only from its own machine's neighbour table,
so it gets them only for devices on its own segment.

- In a container on a bridge network (`-p 8080:8080`), the neighbour table is
  the container's own and holds none of your devices. Run the container with
  `--network host`.
- For devices on other segments or VLANs, connect your router with a
  [router plugin](../plugins/index.md). The router sees every segment.

## One phone shows up as several devices

Phones, tablets and laptops make up a hardware address for each Wi-Fi network,
and some change it from time to time. Each new address is a new device. Ones
you never curate are deleted 90 days after they were last seen
(`retention.devices`). For a phone of your own, turn off the private or
random address for your home network in its Wi-Fi settings.

## A device is missing from the sweep

The sweep finds devices that answer an ICMP echo. A device whose firewall
drops it, as Windows does by default, does not answer. A
[router plugin](../plugins/index.md) lists it from the router's tables.

## The sweep fails with "no usable ICMP socket"

Run without root, the sweep needs the kernel to allow unprivileged ICMP for
the user's group. Check the range of allowed groups:

```bash
sysctl net.ipv4.ping_group_range
```

If it does not cover the group Jocasta runs as, widen it, or run the
container image, which needs no change.

## Devices have no mDNS, DNS-SD or SSDP name

These lookups reach only Jocasta's own segment, and DNS-SD and SSDP need
multicast, which a bridge network does not pass. Run the container with
`--network host`. A device on another segment can still get its name from
reverse DNS or the router's DHCP leases. See
[How names are chosen](../naming.md).

## The sign-in page keeps coming back

The session cookie is sent over HTTPS only. Over plain HTTP from anywhere but
`localhost`, the browser drops it. Set `server.auth.cookie_secure: false`, or
[serve Jocasta over HTTPS](https.md).

## A form shows "Cross-origin request refused"

Behind a reverse proxy, the proxy is changing the `Host` header. Pass it
through unchanged; see [Serve over HTTPS](https.md#use-another-proxy).

## A plugin reads as "source rejected credentials"

Check the source on its own, with its instance name:

```bash
jocasta plugin run gateway
```

- RouterOS: the user needs at least read access, and the REST API needs the
  `www-ssl` service, or `www` with `ssl: false`.
- OpenWrt: the login needs the ACL file. See
  [OpenWrt](../plugins/openwrt.md).
- Technitium: the token's user needs View on the Logs section.
- A password in an environment variable overrides the one in the file. Check
  both.

## The Traffic page stays empty

- `exporters` must list the address the flow exports come from, which may
  differ from the router's address on your network. The log warns "dropping
  datagrams from a sender not listed in exporters" with the address that sent
  them.
- On a bridge network, publish the port as UDP: `-p 2055:2055/udp`.
- A firewall on the Jocasta host must allow UDP to the listen port.

See [NetFlow](../plugins/netflow.md).

## Every DNS lookup is counted against the router

The router forwards its clients' lookups, so each one reaches the resolver
from the router's address. Hand out the resolver's address as the DNS server
in the router's DHCP settings. See
[Record what devices look up](../plugins/dns.md).

## Times are off by hours

Times are shown in the server's time zone, which in a container is UTC. Set
`location.timezone`, such as `Australia/Sydney`.

## A notification did not arrive

In the account menu, open **Notifications**. A destination whose last message
failed shows the error under its name. Select **Send test** to try again.
Jocasta does not resend a failed message.

## An MCP client gets a 404

The MCP server is off by default. Set `server.mcp.enabled: true` and restart
Jocasta.

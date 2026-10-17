# Fix common problems

If your problem is not on this page, ask in [GitHub issues](<https://github.com/pushkar-anand/jocasta/issues>). Include the log lines around the problem.

## Devices have no hardware address or vendor

On a bridge network, the container reads its own neighbour table. This table contains none of your devices. Use `--network host`.

For devices on other segments, add a [router plugin](<https://pushkar-anand.github.io/jocasta/0.14/plugins/index.md>).

## One phone shows up as several devices

The phone makes up a new hardware address from time to time. Each address is a new device. Jocasta deletes the devices that you did not edit after 90 days. On your own phone, turn off the private address for your home Wi-Fi.

## A device is missing

The sweep finds only devices that answer ping. By default, Windows does not answer ping. A [router plugin](<https://pushkar-anand.github.io/jocasta/0.14/plugins/index.md>) finds such devices in the tables of the router.

## "no usable ICMP socket"

If you run the binary without root, its group must be allowed to use unprivileged ICMP. To see the allowed groups, run:

```
sysctl net.ipv4.ping_group_range
```

Make the range include the group of Jocasta, or use the container image.

## No mDNS, DNS-SD or SSDP names

These lookups reach only the segment of Jocasta. DNS-SD and SSDP also need multicast. Use `--network host`. See [How names are chosen](<https://pushkar-anand.github.io/jocasta/0.14/naming/index.md>).

## The sign-in page keeps coming back

By default, the session cookie works only over HTTPS. See [Serve over HTTPS](<https://pushkar-anand.github.io/jocasta/0.14/operate/https/index.md>).

## A form shows "Cross-origin request refused"

Your reverse proxy changes the `Host` header. See [Serve over HTTPS](<https://pushkar-anand.github.io/jocasta/0.14/operate/https/#other-proxies>).

## A plugin reads as "source rejected credentials"

Test the plugin alone: `jocasta plugin run gateway`.

- RouterOS: the user needs read access. The `www-ssl` service must be on. Or the `www` service must be on, with `ssl: false`.
- OpenWrt: the login needs the [ACL file](<https://pushkar-anand.github.io/jocasta/0.14/plugins/openwrt/index.md>).
- Technitium: the user of the token needs View on Logs.
- An environment variable wins over the file. Check both.

## The Traffic page stays empty

- `exporters` must list the address that the exports come from. If it does not, the log shows the warning "dropping datagrams from a sender not listed in exporters", with that address.
- On a bridge network, publish the port as UDP: `-p 2055:2055/udp`.
- Open the listen port in the firewall of the host.

## Every DNS lookup is counted against the router

The router forwards the lookups of its clients. In the DHCP settings of the router, give out the address of the resolver. See [Record what devices look up](<https://pushkar-anand.github.io/jocasta/0.14/plugins/dns/index.md>).

## Times are hours off

The container runs in UTC. Set `location.timezone`, such as `Australia/Sydney`.

## A notification did not arrive

In the account menu, the **Notifications** page shows the last error under the destination. To try again, select **Send test**. Jocasta does not send a failed message again.

## An MCP client gets a 404

Set `server.mcp.enabled: true`, then restart Jocasta.

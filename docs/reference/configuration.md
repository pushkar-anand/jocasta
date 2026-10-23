# Configuration reference

This page lists every setting that Jocasta reads, with its type, default and
allowed values. [Configure](../configuration.md) tells how Jocasta reads and
overrides settings. [`jocasta.example.yaml`](https://github.com/pushkar-anand/jocasta/blob/main/jocasta.example.yaml)
has the same settings, in a file that you can copy.

Each setting can also come from an environment variable. The name is
`JOCASTA_`, then the path of the setting in capitals, with a double
underscore between levels. A single underscore stays part of the key. For
example, `scan.devices.resolve_mdns` is `JOCASTA_SCAN__DEVICES__RESOLVE_MDNS`.
For a plugin or notification setting, the instance or destination name is
one level of the path. For example, `plugins.routeros.gateway.password` is
`JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD`.

Types:

- **duration**: a number and a unit, such as `90s`, `15m` or `6h`. The units
  are `s`, `m` and `h`. There is no unit for days, so 30 days is `720h`.
- **list**: a YAML list.
- **map**: YAML keys and values.

## server

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `server.host` | string | `localhost` | An address or host name | Where the web server listens. `0.0.0.0` listens on every interface. The container image sets `0.0.0.0`. |
| `server.port` | int | `8080` | 1 to 65535 | The port the web server listens on. |
| `server.trusted_proxies` | list | empty | Addresses or CIDR prefixes, such as `192.0.2.1` or `10.0.0.0/8` | Reverse proxy addresses whose forwarding headers the server trusts to identify the client. Empty reads the client address from the connection directly. |
| `server.cors.allowed_origins` | list | empty | Origins as `scheme://host[:port]` | Browser origins besides the server's own that may read `/api`. Empty sends no CORS headers. |
| `server.auth.session_lifetime` | duration | `168h` | A duration | The longest a sign-in lasts, whatever the activity. |
| `server.auth.idle_timeout` | duration | `24h` | A duration | How long a session survives with no requests. |
| `server.auth.cookie_secure` | bool | `true` | `true`, `false` | Sends the session cookie over HTTPS only. Set `false` to sign in over plain HTTP from anywhere but `localhost`. |
| `server.mcp.enabled` | bool | `false` | `true`, `false` | Serves the [MCP endpoint](../integrations/mcp.md) at `/mcp`. While off, `/mcp` answers 404. |

## db

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `db.path` | string | `.` | A directory | Where the SQLite file lives. The container image sets `/data`. |
| `db.name` | string | `jocasta.db` | A file name | The SQLite file's name. |

## logger

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `logger.level` | string | `info` | `debug`, `info`, `warn`, `error` | The lowest level logged. Any other value logs at `info`. |
| `logger.format` | string | `json` | `json`, `text` | How each log line is written. Any value but `text` writes JSON. |

## inventory

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `inventory.online_window` | duration | `15m` | A duration | How long after its last sighting a device counts as online. Past it, a [watched device](../notifications/watch.md) goes quiet. |
| `inventory.address_grace` | duration | `6h` | A duration | How long an address can go unseen before a later sweep that finds the device elsewhere retires it. Raise it on a network with long DHCP leases. |

## retention

Once an hour, Jocasta deletes the records that are older than their window.
`0s` keeps that kind of record forever.

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `retention.history` | duration | `2160h` (90 days) | A duration; `0s` keeps forever | How long Jocasta keeps the change log and the scan log. |
| `retention.traffic` | duration | `720h` (30 days) | A duration; `0s` keeps forever | How long Jocasta keeps the hourly traffic totals. |
| `retention.dns` | duration | `720h` (30 days) | A duration; `0s` keeps forever | How long Jocasta keeps the hourly lookup counts, and the addresses in the answers to lookups. |
| `retention.devices` | duration | `2160h` (90 days) | A duration; `0s` keeps forever | How long Jocasta keeps a device after a scan last saw it. Jocasta keeps a device that you labelled, grouped, typed, noted, ignored or watched. |

## location

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `location.country` | string | empty | A two-letter country code, such as `AU` | The country from which the world map draws its lines. If empty, Jocasta uses the outside address of the router. This address cannot find the country of a network behind carrier-grade NAT. See [NetFlow](../plugins/netflow.md). |
| `location.timezone` | string | empty | An IANA zone name, such as `Australia/Sydney` | The zone in which Jocasta shows times. If empty, Jocasta uses `TZ` or the zone of the system, which is UTC in a container. Jocasta always stores times in UTC. |

## networks

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `networks` | list | the host's network | Prefixes, such as `192.0.2.0/24` | The prefixes that the sweep covers at each interval. Jocasta records each address that it finds under the network that contains it. If the list is empty, Jocasta sweeps the network of the host's default route. It does this only if the network is a `/22` or smaller. |

## scan

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `scan.source` | string | `sweep:<hostname>` | Any name | The source name of the sweeps that run at each interval. Set it in a container, where the host name changes on every run. A new name starts a new source. Past scans stay under the old name. |
| `scan.devices.enabled` | bool | `true` | `true`, `false` | Sweeps `networks` on a timer. |
| `scan.devices.interval` | duration | `5m` | A duration | How often the sweep runs. |
| `scan.devices.rate` | int | `1000` | A whole number | The most ICMP probes sent per second. |
| `scan.devices.rounds` | int | `2` | A whole number | The number of probes to each address before Jocasta counts the address as down. |
| `scan.devices.wait` | duration | `2s` | A duration | How long to wait for replies after the last probe. |
| `scan.devices.resolve_names` | bool | `true` | `true`, `false` | Looks up reverse DNS for each address that answered. See [Name devices](../naming.md). |
| `scan.devices.resolve_macs` | bool | `true` | `true`, `false` | Reads the neighbour table for hardware addresses. |
| `scan.devices.resolve_mdns` | bool | `true` | `true`, `false` | Asks each device without a reverse DNS name for its name over mDNS. |
| `scan.devices.resolve_netbios` | bool | `true` | `true`, `false` | Asks each device still without a name over NetBIOS. |
| `scan.devices.resolve_dnssd` | bool | `true` | `true`, `false` | Asks for the services that devices advertise. Names each device that still has no name after the name that it gives its services. Needs host networking. |
| `scan.devices.resolve_ssdp` | bool | `true` | `true`, `false` | Searches over SSDP. Names each device that still has no name after its UPnP `friendlyName`. Needs host networking. |
| `scan.ports.enabled` | bool | `false` | `true`, `false` | Probes every known address for open TCP ports on a timer. |
| `scan.ports.interval` | duration | `6h` | A duration | How often the port scan runs. |
| `scan.ports.custom` | string | empty | Ports and ranges, such as `22,80,443,8000-8100`; `1-65535` for every port | The ports to probe. If empty, Jocasta uses a preset. Jocasta also probes each address on the ports that its device advertises. |
| `scan.ports.concurrency` | int | `64` | A whole number | The most connections a scan opens at once. Lower it on a router with a small connection table. |

## plugins

By default, plugins are off. Each instance is under its kind, with a name
that you choose as its key. An instance is off, unless it has
`enabled: true`. See
[Plugins](../plugins/index.md).

### plugins.routeros

A [MikroTik RouterOS](../plugins/routeros.md) router, switch or access point.

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `plugins.routeros.<instance>.enabled` | bool | `false` | `true`, `false` | Reads this source. |
| `plugins.routeros.<instance>.host` | string | none, required | An address or host name, without a port | Where the REST API is. |
| `plugins.routeros.<instance>.port` | int | `80`, or `443` with `ssl` | 1 to 65535 | The REST API's port. |
| `plugins.routeros.<instance>.user` | string | empty | A RouterOS user | A read-only user is enough. |
| `plugins.routeros.<instance>.password` | string | empty | Any | The user's password. |
| `plugins.routeros.<instance>.ssl` | bool | `false` | `true`, `false` | Uses HTTPS. Without it, the password goes over the network as plain text, and Jocasta logs a warning. |
| `plugins.routeros.<instance>.insecure` | bool | `false` | `true`, `false` | Skips certificate checks, for RouterOS's self-signed certificate. |
| `plugins.routeros.<instance>.timeout` | duration | `10s` | A duration | The longest one read may take. |
| `plugins.routeros.<instance>.topology_only` | bool | `false` | `true`, `false` | Reads only what is connected to this switch or access point. The router lists its devices. See [Show what is plugged in where](../plugins/topology.md). |

### plugins.openwrt

An [OpenWrt](../plugins/openwrt.md) router or access point.

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `plugins.openwrt.<instance>.enabled` | bool | `false` | `true`, `false` | Reads this source. |
| `plugins.openwrt.<instance>.host` | string | none, required | An address or host name, without a port | Where ubus is served. |
| `plugins.openwrt.<instance>.port` | int | `80`, or `443` with `ssl` | 1 to 65535 | uhttpd's port. |
| `plugins.openwrt.<instance>.user` | string | empty | An rpcd login | A login with the Jocasta ACL. |
| `plugins.openwrt.<instance>.password` | string | empty | Any | The login's password. |
| `plugins.openwrt.<instance>.ssl` | bool | `false` | `true`, `false` | Uses HTTPS. Needs `luci-ssl`. Without it, the password goes over the network as plain text, and Jocasta logs a warning. |
| `plugins.openwrt.<instance>.insecure` | bool | `false` | `true`, `false` | Skips certificate checks, for uhttpd's self-signed certificate. |
| `plugins.openwrt.<instance>.timeout` | duration | `10s` | A duration | The longest one read may take. |
| `plugins.openwrt.<instance>.topology_only` | bool | `false` | `true`, `false` | Reads only what is connected to this access point. The router lists its devices. See [Show what is plugged in where](../plugins/topology.md). |

### plugins.netflow

A [NetFlow](../plugins/netflow.md) listener for a router's flow exports.

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `plugins.netflow.<instance>.enabled` | bool | `false` | `true`, `false` | Runs this listener. |
| `plugins.netflow.<instance>.listen` | string | `:2055` | A UDP address, such as `:2055` or `192.0.2.10:2055` | Where the listener binds. |
| `plugins.netflow.<instance>.exporters` | list | none, required | IP addresses | The routers that can send. Jocasta discards datagrams from all other addresses. |

### plugins.technitium

A [Technitium DNS Server](../plugins/technitium.md) node.

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `plugins.technitium.<instance>.enabled` | bool | `false` | `true`, `false` | Reads this server's query log. |
| `plugins.technitium.<instance>.host` | string | none, required | An address or host name, without a port | The address of this node. Do not use a virtual address that the nodes share. It reaches only one node at a time. |
| `plugins.technitium.<instance>.port` | int | `5380`, or `53443` with `ssl` | 1 to 65535 | The web service's port. |
| `plugins.technitium.<instance>.token` | string | empty | An API token | A token for a user granted Logs: View. |
| `plugins.technitium.<instance>.ssl` | bool | `false` | `true`, `false` | Uses HTTPS. Without it, the token goes over the network as plain text, and Jocasta logs a warning. |
| `plugins.technitium.<instance>.insecure` | bool | `false` | `true`, `false` | Skips certificate checks, for a self-signed certificate. |
| `plugins.technitium.<instance>.log_app` | string | empty | An app name, as the Apps page lists it | The app whose log Jocasta reads. Necessary only if more than one installed app logs queries. |
| `plugins.technitium.<instance>.timeout` | duration | `10s` | A duration | The longest one read may take. |

### plugins.pihole

A [Pi-hole](../plugins/pihole.md) v6.

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `plugins.pihole.<instance>.enabled` | bool | `false` | `true`, `false` | Reads this server's query log. |
| `plugins.pihole.<instance>.host` | string | none, required | An address or host name, without a port | Where Pi-hole's web server is. |
| `plugins.pihole.<instance>.port` | int | `80`, or `443` with `ssl` | 1 to 65535 | The web server's port. |
| `plugins.pihole.<instance>.password` | string | empty | An app password | Empty reads a Pi-hole with no password set. |
| `plugins.pihole.<instance>.ssl` | bool | `false` | `true`, `false` | Uses HTTPS. Without it, the password goes over the network as plain text, and Jocasta logs a warning. |
| `plugins.pihole.<instance>.insecure` | bool | `false` | `true`, `false` | Skips certificate checks, for Pi-hole's self-signed certificate. |
| `plugins.pihole.<instance>.timeout` | duration | `10s` | A duration | The longest one read may take. |

### plugins.adguard

An [AdGuard Home](../plugins/adguard.md).

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `plugins.adguard.<instance>.enabled` | bool | `false` | `true`, `false` | Reads this server's query log. |
| `plugins.adguard.<instance>.host` | string | none, required | An address or host name, without a port | Where the web interface is. |
| `plugins.adguard.<instance>.port` | int | `80`, or `443` with `ssl` | 1 to 65535 | The web interface's port. |
| `plugins.adguard.<instance>.user` | string | empty | The web interface login | Empty reads a server with no login set. |
| `plugins.adguard.<instance>.password` | string | empty | Any | The login's password. |
| `plugins.adguard.<instance>.ssl` | bool | `false` | `true`, `false` | Uses HTTPS. Without it, the password goes over the network as plain text, and Jocasta logs a warning. |
| `plugins.adguard.<instance>.insecure` | bool | `false` | `true`, `false` | Skips certificate checks, for a self-signed certificate. |
| `plugins.adguard.<instance>.timeout` | duration | `10s` | A duration | The longest one read may take. |

## notify

Each destination is under `notify`, with a name that you choose as its key.
It has exactly one service block: `ntfy`, `webhook` or `http`. To select the
changes that a destination gets, use the Notifications page in the account
menu. See
[Notifications](../notifications/index.md).

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `notify.<name>.enabled` | bool | `true` | `true`, `false` | Sends to this destination. A destination is on unless it says `false`. |
| `notify.<name>.timeout` | duration | `10s` | A duration | The longest one send may take. |

### ntfy

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `notify.<name>.ntfy.url` | string | none, required | The topic's URL, as ntfy shows it | Where Jocasta publishes messages, such as `https://ntfy.example.com/jocasta`. |
| `notify.<name>.ntfy.token` | string | empty | An ntfy access token | For a server that requires one. |
| `notify.<name>.ntfy.priority` | int | `0` | `0` to `5` | ntfy's priority, from 1 (min) to 5 (max). `0` leaves it to the server. |

### webhook

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `notify.<name>.webhook.url` | string | none, required | An HTTP or HTTPS URL | Where the signed JSON POST goes. |
| `notify.<name>.webhook.secret` | string | none, required | Any | Keys the signature on each request. See [Check a webhook's signature](../notifications/webhook.md). |

### http

| Key | Type | Default | Allowed values | Description |
|---|---|---|---|---|
| `notify.<name>.http.url` | string | none, required | An HTTP or HTTPS URL | Where the request goes. It can contain the token of the service. Jocasta never logs or shows it. |
| `notify.<name>.http.headers` | map | empty | Header names and values | Sent with every request, such as `Authorization`. A `Content-Type` here replaces `application/json`. |
| `notify.<name>.http.body` | string | none, required | A Go template | The request body. See [Send to another service](../notifications/other-services.md) for recipes. |

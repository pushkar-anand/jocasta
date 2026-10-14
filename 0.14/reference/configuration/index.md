# Configuration reference

Every setting Jocasta reads, with its type, default and the values it takes. [Configure](<https://pushkar-anand.github.io/jocasta/0.14/configuration/index.md>) explains how settings are read and overridden. [`jocasta.example.yaml`](<https://github.com/pushkar-anand/jocasta/blob/main/jocasta.example.yaml>) holds the same settings as a file to copy from.

Each setting can also come from an environment variable: `JOCASTA_`, then the setting's path in capitals with a double underscore between levels. A single underscore stays part of the key, so `scan.devices.resolve_mdns` is `JOCASTA_SCAN__DEVICES__RESOLVE_MDNS`. For a plugin or notification setting, the instance or destination name is one level of the path: `plugins.routeros.gateway.password` is `JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD`.

Types:

- **duration**: a number and a unit, such as `90s`, `15m` or `6h`. The units are `s`, `m` and `h`; there is no unit for days, so 30 days is `720h`.
- **list**: a YAML list.
- **map**: YAML keys and values.

## server

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `server.host` | string | `localhost` | An address or host name | Where the web server listens. `0.0.0.0` listens on every interface. The container image sets `0.0.0.0`. |
| `server.port` | int | `8080` | 1 to 65535 | The port the web server listens on. |
| `server.trusted_proxies` | list | empty | Addresses or CIDR prefixes, such as `192.0.2.1` or `10.0.0.0/8` | Reverse proxy addresses whose forwarding headers the server trusts to identify the client. Empty reads the client address from the connection directly. |
| `server.cors.allowed_origins` | list | empty | Origins as `scheme://host[:port]` | Browser origins besides the server's own that may read `/api`. Empty sends no CORS headers. |
| `server.auth.session_lifetime` | duration | `168h` | A duration | The longest a sign-in lasts, whatever the activity. |
| `server.auth.idle_timeout` | duration | `24h` | A duration | How long a session survives with no requests. |
| `server.auth.cookie_secure` | bool | `true` | `true`, `false` | Sends the session cookie over HTTPS only. Set `false` to sign in over plain HTTP from anywhere but `localhost`. |
| `server.mcp.enabled` | bool | `false` | `true`, `false` | Serves the [MCP endpoint](<https://pushkar-anand.github.io/jocasta/0.14/integrations/mcp/index.md>) at `/mcp`. While off, `/mcp` answers 404. |

## db

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `db.path` | string | `.` | A directory | Where the SQLite file lives. The container image sets `/data`. |
| `db.name` | string | `jocasta.db` | A file name | The SQLite file's name. |

## logger

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `logger.level` | string | `info` | `debug`, `info`, `warn`, `error` | The lowest level logged. Any other value logs at `info`. |
| `logger.format` | string | `json` | `json`, `text` | How each log line is written. Any value but `text` writes JSON. |

## inventory

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `inventory.online_window` | duration | `15m` | A duration | How long after its last sighting a device counts as online. Past it, a [watched device](<https://pushkar-anand.github.io/jocasta/0.14/notifications/watch/index.md>) goes quiet. |
| `inventory.address_grace` | duration | `6h` | A duration | How long an address can go unseen before a later sweep that finds the device elsewhere retires it. Raise it on a network with long DHCP leases. |

## retention

Records older than their window are deleted once an hour. `0s` keeps that kind of record forever.

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `retention.history` | duration | `2160h` (90 days) | A duration; `0s` keeps forever | How long the change log and the scan log are kept. |
| `retention.traffic` | duration | `720h` (30 days) | A duration; `0s` keeps forever | How long hourly traffic totals are kept. |
| `retention.dns` | duration | `720h` (30 days) | A duration; `0s` keeps forever | How long hourly lookup counts, and the addresses lookups were answered with, are kept. |
| `retention.devices` | duration | `2160h` (90 days) | A duration; `0s` keeps forever | How long a device nobody labelled, grouped, typed, noted or ignored is kept after a scan last saw it. |

## location

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `location.country` | string | empty | A two-letter country code, such as `AU` | The country the world map draws its lines from. Empty uses the router's outside address, which cannot place a network behind carrier-grade NAT. See [NetFlow](<https://pushkar-anand.github.io/jocasta/0.14/plugins/netflow/index.md>). |
| `location.timezone` | string | empty | An IANA zone name, such as `Australia/Sydney` | The zone times are shown in. Empty uses `TZ` or the system's zone, which is UTC in a container. Times are stored in UTC either way. |

## networks

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `networks` | list | empty | Prefixes, such as `192.0.2.0/24` | The prefixes the sweep covers on a timer. Each address found is filed under the network that contains it. |

## scan

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `scan.source` | string | `sweep:<hostname>` | Any name | The origin recorded for timed sweeps. Set it in a container, where the host name changes on every run. A new name starts a new source and leaves past scans under the old one. |
| `scan.devices.enabled` | bool | `true` | `true`, `false` | Sweeps `networks` on a timer. |
| `scan.devices.interval` | duration | `5m` | A duration | How often the sweep runs. |
| `scan.devices.rate` | int | `1000` | A whole number | The most ICMP probes sent per second. |
| `scan.devices.rounds` | int | `2` | A whole number | Probes sent to each address before it counts as down. |
| `scan.devices.wait` | duration | `2s` | A duration | How long to wait for replies after the last probe. |
| `scan.devices.resolve_names` | bool | `true` | `true`, `false` | Looks up reverse DNS for each address that answered. See [Name devices](<https://pushkar-anand.github.io/jocasta/0.14/naming/index.md>). |
| `scan.devices.resolve_macs` | bool | `true` | `true`, `false` | Reads the neighbour table for hardware addresses. |
| `scan.devices.resolve_mdns` | bool | `true` | `true`, `false` | Asks each device without a reverse DNS name for its name over mDNS. |
| `scan.devices.resolve_netbios` | bool | `true` | `true`, `false` | Asks each device still without a name over NetBIOS. |
| `scan.devices.resolve_dnssd` | bool | `true` | `true`, `false` | Browses the services devices advertise, and names the rest after the name they give them. Needs host networking. |
| `scan.devices.resolve_ssdp` | bool | `true` | `true`, `false` | Searches over SSDP, and names the rest after their UPnP `friendlyName`. Needs host networking. |
| `scan.ports.enabled` | bool | `false` | `true`, `false` | Probes every known address for open TCP ports on a timer. |
| `scan.ports.interval` | duration | `6h` | A duration | How often the port scan runs. |
| `scan.ports.custom` | string | empty | Ports and ranges, such as `22,80,443,8000-8100`; `1-65535` for every port | The ports to probe. Empty uses a curated preset. Each address is also probed on the ports its device advertises. |
| `scan.ports.concurrency` | int | `64` | A whole number | The most connections a scan opens at once. Lower it on a router with a small connection table. |

## plugins

Plugins are off by default. Each instance sits under its kind, keyed by a name you choose, and is off unless it says `enabled: true`. See [Plugins](<https://pushkar-anand.github.io/jocasta/0.14/plugins/index.md>).

### plugins.routeros

A [MikroTik RouterOS](<https://pushkar-anand.github.io/jocasta/0.14/plugins/routeros/index.md>) router, switch or access point.

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `plugins.routeros.<instance>.enabled` | bool | `false` | `true`, `false` | Reads this source. |
| `plugins.routeros.<instance>.host` | string | none, required | An address or host name, without a port | Where the REST API is. |
| `plugins.routeros.<instance>.port` | int | `80`, or `443` with `ssl` | 1 to 65535 | The REST API's port. |
| `plugins.routeros.<instance>.user` | string | empty | A RouterOS user | A read-only user is enough. |
| `plugins.routeros.<instance>.password` | string | empty | Any | The user's password. |
| `plugins.routeros.<instance>.ssl` | bool | `false` | `true`, `false` | Uses HTTPS. Without it, the password crosses the network unencrypted and Jocasta logs a warning. |
| `plugins.routeros.<instance>.insecure` | bool | `false` | `true`, `false` | Skips certificate checks, for RouterOS's self-signed certificate. |
| `plugins.routeros.<instance>.timeout` | duration | `10s` | A duration | The longest one read may take. |
| `plugins.routeros.<instance>.topology_only` | bool | `false` | `true`, `false` | Reads only what is plugged into this switch or access point, and leaves its devices to the router. See [Show what is plugged in where](<https://pushkar-anand.github.io/jocasta/0.14/plugins/topology/index.md>). |

### plugins.openwrt

An [OpenWrt](<https://pushkar-anand.github.io/jocasta/0.14/plugins/openwrt/index.md>) router or access point.

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `plugins.openwrt.<instance>.enabled` | bool | `false` | `true`, `false` | Reads this source. |
| `plugins.openwrt.<instance>.host` | string | none, required | An address or host name, without a port | Where ubus is served. |
| `plugins.openwrt.<instance>.port` | int | `80`, or `443` with `ssl` | 1 to 65535 | uhttpd's port. |
| `plugins.openwrt.<instance>.user` | string | empty | An rpcd login | A login with the Jocasta ACL. |
| `plugins.openwrt.<instance>.password` | string | empty | Any | The login's password. |
| `plugins.openwrt.<instance>.ssl` | bool | `false` | `true`, `false` | Uses HTTPS, once `luci-ssl` is installed. Without it, the password crosses the network unencrypted and Jocasta logs a warning. |
| `plugins.openwrt.<instance>.insecure` | bool | `false` | `true`, `false` | Skips certificate checks, for uhttpd's self-signed certificate. |
| `plugins.openwrt.<instance>.timeout` | duration | `10s` | A duration | The longest one read may take. |
| `plugins.openwrt.<instance>.topology_only` | bool | `false` | `true`, `false` | Reads only what is plugged into this access point, and leaves its devices to the router. See [Show what is plugged in where](<https://pushkar-anand.github.io/jocasta/0.14/plugins/topology/index.md>). |

### plugins.netflow

A [NetFlow](<https://pushkar-anand.github.io/jocasta/0.14/plugins/netflow/index.md>) listener for a router's flow exports.

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `plugins.netflow.<instance>.enabled` | bool | `false` | `true`, `false` | Runs this listener. |
| `plugins.netflow.<instance>.listen` | string | `:2055` | A UDP address, such as `:2055` or `192.0.2.10:2055` | Where the listener binds. |
| `plugins.netflow.<instance>.exporters` | list | none, required | IP addresses | The routers allowed to send. Datagrams from any other address are dropped. |

### plugins.technitium

A [Technitium DNS Server](<https://pushkar-anand.github.io/jocasta/0.14/plugins/technitium/index.md>) node.

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `plugins.technitium.<instance>.enabled` | bool | `false` | `true`, `false` | Reads this server's query log. |
| `plugins.technitium.<instance>.host` | string | none, required | An address or host name, without a port | The node's own address. A virtual address shared between nodes reaches one node at a time. |
| `plugins.technitium.<instance>.port` | int | `5380`, or `53443` with `ssl` | 1 to 65535 | The web service's port. |
| `plugins.technitium.<instance>.token` | string | empty | An API token | A token for a user granted Logs: View. |
| `plugins.technitium.<instance>.ssl` | bool | `false` | `true`, `false` | Uses HTTPS. Without it, the token crosses the network unencrypted and Jocasta logs a warning. |
| `plugins.technitium.<instance>.insecure` | bool | `false` | `true`, `false` | Skips certificate checks, for a self-signed certificate. |
| `plugins.technitium.<instance>.log_app` | string | empty | An app name, as the Apps page lists it | The app whose log is read. Needed only when more than one installed app logs queries. |
| `plugins.technitium.<instance>.timeout` | duration | `10s` | A duration | The longest one read may take. |

### plugins.pihole

A [Pi-hole](<https://pushkar-anand.github.io/jocasta/0.14/plugins/pihole/index.md>) v6.

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `plugins.pihole.<instance>.enabled` | bool | `false` | `true`, `false` | Reads this server's query log. |
| `plugins.pihole.<instance>.host` | string | none, required | An address or host name, without a port | Where Pi-hole's web server is. |
| `plugins.pihole.<instance>.port` | int | `80`, or `443` with `ssl` | 1 to 65535 | The web server's port. |
| `plugins.pihole.<instance>.password` | string | empty | An app password | Empty reads a Pi-hole with no password set. |
| `plugins.pihole.<instance>.ssl` | bool | `false` | `true`, `false` | Uses HTTPS. Without it, the password crosses the network unencrypted and Jocasta logs a warning. |
| `plugins.pihole.<instance>.insecure` | bool | `false` | `true`, `false` | Skips certificate checks, for Pi-hole's self-signed certificate. |
| `plugins.pihole.<instance>.timeout` | duration | `10s` | A duration | The longest one read may take. |

### plugins.adguard

An [AdGuard Home](<https://pushkar-anand.github.io/jocasta/0.14/plugins/adguard/index.md>).

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `plugins.adguard.<instance>.enabled` | bool | `false` | `true`, `false` | Reads this server's query log. |
| `plugins.adguard.<instance>.host` | string | none, required | An address or host name, without a port | Where the web interface is. |
| `plugins.adguard.<instance>.port` | int | `80`, or `443` with `ssl` | 1 to 65535 | The web interface's port. |
| `plugins.adguard.<instance>.user` | string | empty | The web interface login | Empty reads a server with no login set. |
| `plugins.adguard.<instance>.password` | string | empty | Any | The login's password. |
| `plugins.adguard.<instance>.ssl` | bool | `false` | `true`, `false` | Uses HTTPS. Without it, the password crosses the network unencrypted and Jocasta logs a warning. |
| `plugins.adguard.<instance>.insecure` | bool | `false` | `true`, `false` | Skips certificate checks, for a self-signed certificate. |
| `plugins.adguard.<instance>.timeout` | duration | `10s` | A duration | The longest one read may take. |

## notify

Each destination sits under `notify`, keyed by a name you choose, and holds exactly one service block: `ntfy`, `webhook` or `http`. Which changes it is sent is picked on the account menu's Notifications page. See [Notifications](<https://pushkar-anand.github.io/jocasta/0.14/notifications/index.md>).

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `notify.<name>.enabled` | bool | `true` | `true`, `false` | Sends to this destination. A destination is on unless it says `false`. |
| `notify.<name>.timeout` | duration | `10s` | A duration | The longest one send may take. |

### ntfy

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `notify.<name>.ntfy.url` | string | none, required | The topic's URL, as ntfy shows it | Where messages are published, such as `https://ntfy.example.com/jocasta`. |
| `notify.<name>.ntfy.token` | string | empty | An ntfy access token | For a server that requires one. |
| `notify.<name>.ntfy.priority` | int | `0` | `0` to `5` | ntfy's priority, from 1 (min) to 5 (max). `0` leaves it to the server. |

### webhook

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `notify.<name>.webhook.url` | string | none, required | An HTTP or HTTPS URL | Where the signed JSON POST goes. |
| `notify.<name>.webhook.secret` | string | none, required | Any | Keys the signature on each request. See [Check a webhook's signature](<https://pushkar-anand.github.io/jocasta/0.14/notifications/webhook/index.md>). |

### http

| Key | Type | Default | Allowed values | Description |
| --- | --- | --- | --- | --- |
| `notify.<name>.http.url` | string | none, required | An HTTP or HTTPS URL | Where the request goes. It can hold the service's token; it is never logged or shown. |
| `notify.<name>.http.headers` | map | empty | Header names and values | Sent with every request, such as `Authorization`. A `Content-Type` here replaces `application/json`. |
| `notify.<name>.http.body` | string | none, required | A Go template | The request body. See [Send to another service](<https://pushkar-anand.github.io/jocasta/0.14/notifications/other-services/index.md>) for recipes. |

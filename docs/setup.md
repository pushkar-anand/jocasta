# Setup

## Install

### Prebuilt binaries

Every release publishes binaries for Linux, macOS and Windows, on amd64 and
arm64, with a `checksums.txt`, on the [releases page][releases]. Download the
archive for your platform, check it against the checksum, and extract `jocasta`.
`jocasta version` prints the build it came from.

[releases]: https://github.com/pushkar-anand/jocasta/releases

### Container image

Multi-arch images (amd64 and arm64) are published to the GitHub Container
Registry on each release, tagged `latest` and with the version:

```bash
docker pull ghcr.io/pushkar-anand/jocasta:latest   # or a version tag, e.g. :v0.2.0
```

The image is distroless, static, about 16 MB, and runs as a non-root user. It
listens on `0.0.0.0:8080` and keeps its SQLite file on the `/data` volume.

```bash
docker run -d --name jocasta \
  -p 8080:8080 \
  -v jocasta-data:/data \
  ghcr.io/pushkar-anand/jocasta:latest
```

To see hardware addresses and interface data, the container needs host
networking (`--network host`). On a bridge network the sweep still finds hosts,
but the only neighbour table it can read is the container's own. No added
capability or sysctl is needed: Docker's default `net.ipv4.ping_group_range`
already allows the unprivileged ICMP socket.

### From source

Needs Go 1.27 or newer.

```bash
git clone https://github.com/pushkar-anand/jocasta.git
cd jocasta
make build            # -> bin/jocasta
make docker           # -> jocasta:latest
```

The database schema is embedded. Migrations run the first time the binary opens
the database file.

## Configuration

Jocasta reads three sources, each overriding the last:

1. built-in defaults
2. a YAML file (`jocasta.yaml` by default; set another path with `--config`)
3. environment variables prefixed `JOCASTA_`

A missing config file is fine if the defaults and environment cover everything.

### Example `jocasta.yaml`

```yaml
mcp:
  enabled: false

server:
  host: "127.0.0.1"     # 0.0.0.0 to listen on every interface
  port: 3090
  cors:
    allowed_origins: [] # browser origins besides the server's own that may read /api; e.g. ["https://dashboard.example"]

db:
  path: "."             # directory the SQLite file lives in
  name: "jocasta.db"

logger:
  level: "info"         # debug | info | warn | error
  format: "text"        # text | json

inventory:
  online_window: "15m"  # how long after its last sighting a device counts as online
  address_grace: "0s"   # how long a stale address is kept before a later sweep retires it

# Segments the poller sweeps on a timer. Also used to match each discovered
# address to the network that contains it.
networks:
  - "192.0.2.0/24"
  - "198.51.100.0/24"
  - "203.0.113.0/24"

scan:
  source: ""            # origin recorded for timed sweeps; defaults to this host's name
  devices:
    enabled: true
    interval: "5m"
    rate: 1000           # max ICMP probes per second
    rounds: 2            # probes per address before calling it down
    wait: "2s"           # how long to wait for replies after the last probe
    resolve_names: true  # reverse DNS on addresses that answered
    resolve_macs: true   # read the neighbour table for hardware addresses
  ports:
    enabled: false
    interval: "6h"
    custom: ""           # ports to probe, e.g. "22,80,443,8000-8100"; blank uses a curated preset
    concurrency: 64      # max connections a scan opens at once; lower it on a cheap router

# Sources beyond the sweep. Each block is keyed by an instance name, which
# becomes the source its facts are filed under, so two routers stay separate.
plugins:
  routeros:
    gateway:
      enabled: true
      host: "192.0.2.1"
      port: 443
      user: "jocasta"
      password: "change-me"    # a read-only RouterOS user is enough
      ssl: true
      insecure: true           # RouterOS serves a self-signed cert unless you import one
      timeout: "15s"
```

Keep real addresses, hostnames and credentials out of any `jocasta.yaml` you
commit. `jocasta.yaml` and `*.db` are already in `.gitignore`.

### Environment overrides

A double underscore is a level of nesting. A single underscore stays part of
the key.

```bash
JOCASTA_SERVER__HOST=0.0.0.0
JOCASTA_SERVER__PORT=8080
JOCASTA_SERVER__CORS__ALLOWED_ORIGINS=https://dashboard.example
JOCASTA_DB__PATH=/data
JOCASTA_INVENTORY__ONLINE_WINDOW=30m
JOCASTA_SCAN__DEVICES__INTERVAL=10m
JOCASTA_SCAN__PORTS__ENABLED=true
JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD=change-me
```

## CLI

```
jocasta <command> [flags]

  serve              Start the web server and the sweep poller.  (default)
  scan <cidr>        Sweep a prefix once and print the result.
  ports [target]     Probe TCP ports on an address, a prefix, or the inventory.
  plugin run <name>  Read one configured source and print what it claims.
  version            Print build information.

  -c, --config       Path to the config file (default "jocasta.yaml")
      --log-level    debug | info | warn | error
      --log-format   text | json
```

### `serve`

```bash
jocasta serve                 # host and port from config
jocasta serve -p 9000         # override the port
```

Starts the HTTP server. When `scan.devices.enabled` is set, it also starts a
poller that sweeps every network in `networks` on `scan.devices.interval`. When
`scan.ports.enabled` is set, a second poller port-scans every address the
inventory holds on `scan.ports.interval`.

### `scan`

Runs one sweep and prints a table, or JSON with `--json`. With `--save` it also
records the sweep in the inventory, under the name from `--source`, then
`scan.source`, then this host's name.

```
$ jocasta scan 192.0.2.0/24
IP           MAC                VENDOR   HOSTNAME              RTT     DETAILS
192.0.2.1    00:00:5e:00:53:01  -        gateway.example      1.2ms
192.0.2.10   00:00:5e:00:53:02  -        nas.example          0.8ms
192.0.2.20   00:00:5e:00:53:04  -        -                    3.1ms   self (eth0)

$ jocasta scan 192.0.2.0/24 --save --rate 500 --no-resolve-names
```

The sweep uses a raw ICMP socket where it can and an unprivileged datagram
socket otherwise, so it runs without root.

### `ports`

Probes TCP ports with a plain `connect()`, so it needs no privileges and cannot
change the target. With no argument it scans every current address in the
inventory; give an address or a prefix to scan that instead. `--ports` takes a
spec like `22,80,443,8000-8100`; the default is a curated preset of about a
hundred ports a homelab commonly runs. `--concurrency` caps how many
connections are open at once (64 by default). `--save` records what it finds
against the matching devices.

```
$ jocasta ports 192.0.2.10
ADDRESS      PORT   SERVICE
192.0.2.10   22     ssh
192.0.2.10   443    https

$ jocasta ports --save                      # every known address, recorded
$ jocasta ports 192.0.2.0/24 --ports 1-1024 --json
```

### `plugin run`

Reads one configured source without starting the server. Use it to check a
credential or a firewall rule against the real router. It reads the instance
even when it is disabled in config.

```
$ jocasta plugin run gateway
NETWORK           VLAN  NAME
192.0.2.0/24      -     Trusted
198.51.100.0/24   20    IoT

ADDRESS          MAC                VENDOR  HOSTNAME    STANDING     PRESENT
192.0.2.1        00:00:5e:00:53:01  -       gateway     DHCP_STATIC  true
198.51.100.10    00:00:5e:00:53:09  -       thermostat  DHCP_LEASE   true

$ jocasta plugin run gateway --json --save
```

## HTTP API

Mounted under `/api`. Except for `/api/livez`, requests require an API token in
`Authorization: Bearer <token>`. Read-only tokens allow reads; device edits
require a read-write token. Browser requests must come from the same origin.

| Method and path | Purpose |
|---|---|
| `GET /api/livez` | Liveness probe. |
| `GET /api/stats` | Inventory counts. |
| `GET /api/groups` | The groups devices are filed under. |
| `GET /api/devices` | List devices, with filters. |
| `GET /api/devices/{id}` | One device with address history and recorded TCP port observations. |
| `PATCH /api/devices/{id}` | Update the user-owned fields (label, group, type, notes, ignored). |
| `GET /api/devices/{id}/events` | One device's change log. |
| `GET /api/events` | The whole change log. |
| `GET /api/scans` | Sweep and source-read history. |
| `GET /api/networks` | Recorded networks, names, VLANs, and device counts. |
| `GET /api/networks/{id}` | One recorded network. |
| `GET /api/devices/{id}/ports` | Recorded TCP port states and timestamps. |
| `GET /api/devices/{id}/sources` | Discovery-source claims and observation timestamps. |
| `GET /api/ports/overview` | Open-port totals, recent transitions, and common services. |

Device lists accept `q`, `group`, `status=online|offline`,
`sort=last_seen|name|address|type`, `include_ignored`, `network_id`, and `type`.
`type` matches the effective classification, including a user override.
Use a network ID from `/api/networks` to list its devices. Networks with no
recorded devices are included in the network list.

Events, device events, and scans accept `limit` (default 50, maximum 500) and
`cursor`. Send back `next_cursor` with the same filters to get the following
page; its absence marks the end. Global events also accept `device_id`,
`exclude_ignored`, and repeated `event_kinds`, for example:
`/api/events?event_kinds=PORT_OPENED&event_kinds=PORT_CLOSED`.
Scans accept `kind=DISCOVERY|PORTS|IMPORT`.

Device ports accept `state=open|closed`; omit it to include all recorded states.
The port overview accepts `service_limit` (default 10, maximum 100), excludes
ignored devices, and counts opened/closed transitions over the last 24 hours.
New collection responses contain the named collection and `count`.

`PATCH /api/devices/{id}` replaces all user-owned fields. Omitted strings are
cleared and omitted `ignored` becomes false. Field limits are 200 characters
for `label`, 100 for `group`, and 2000 for `notes`. An empty `type` restores the
automatic classification.

## MCP for agents

MCP is disabled by default. Enable it in your configuration and restart Jocasta:

```yaml
mcp:
  enabled: true
```

The environment equivalent is `JOCASTA_MCP__ENABLED=true`. Setting it to false
or omitting the setting leaves `/mcp` unavailable (HTTP 404); the web UI and
JSON API continue to work normally.

Sign in to the web UI and create a token on the **API tokens** page. Choose
read-only for inventory questions, or read-write to allow device curation.
Copy the token when it is displayed; it cannot be retrieved later. Revoking
it blocks subsequent MCP requests, including calls from an existing client.

Connect a client supporting **Streamable HTTP** to
`http://localhost:8080/mcp`, replacing the host, port, and scheme with your
instance's address. Supply the token in the `Authorization: Bearer` header.
The agent's connection must be able to reach your self-hosted instance; a
cloud agent cannot reach your machine's localhost. Browser OAuth sign-in,
stdio, and the legacy SSE transport are not provided.

For Codex, set `JOCASTA_API_TOKEN` in the environment of the process running
Codex, then add this to its MCP configuration:

```toml
[mcp_servers.jocasta]
url = "http://localhost:8080/mcp"
bearer_token_env_var = "JOCASTA_API_TOKEN"
```

See the [Codex MCP configuration reference](https://learn.chatgpt.com/docs/extend/mcp?surface=cli).
For other clients, use the same endpoint and bearer header in their HTTP MCP
settings. Native clients can omit Origin; browser clients must send the same
scheme and host as the endpoint. Reverse proxies must preserve the public Host;
forwarded headers are not trusted for Origin validation.

Available tools:

| Tool | Purpose |
|---|---|
| `get_stats`, `list_groups` | Inventory counts and assigned groups. |
| `list_devices`, `get_device` | Filtered inventory and detailed device history. |
| `list_networks`, `get_network` | Networks, VLANs, and membership counts. |
| `get_device_ports`, `get_port_overview` | Recorded TCP observations and service summaries. |
| `get_device_sources` | What each discovery source reported about a device. |
| `get_device_events`, `list_events`, `list_scans` | Paginated change and scan history. |
| `update_device_curation` | Replace label, group, type, notes, and ignored status. |

Tool filters match the JSON API; `event_kinds` is an array in MCP. Curation
requires **all five fields** plus `id`, and a read-write token. Read the device
first to preserve values you do not intend to change. Both token scopes see
the same tool list; a read-only token cannot execute the curation tool.

Try asking your agent to list networks and their VLANs, show the recorded open
ports on a device, or explain recent port changes using the change log.

These tools read stored observations and do not start scans. "Online" means
seen within the configured inventory window. Ports are recorded TCP probe
results, and service names are conventional port-number labels, not detected
software. Empty port data does not prove that every port is closed. Check
observation timestamps and scan history when freshness matters.

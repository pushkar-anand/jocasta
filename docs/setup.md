# Setup

## Install

### From source

Needs Go 1.27 or newer.

```bash
git clone https://github.com/pushkar-anand/jocasta.git
cd jocasta
make build            # -> bin/jocasta
```

The database schema is embedded. Migrations run the first time the binary opens
the database file.

### Docker

```bash
make docker                       # builds jocasta:latest
# or
docker build -t jocasta:latest .
```

The image is distroless, static, about 16 MB, and runs as a non-root user. It
listens on `0.0.0.0:8080` and keeps its SQLite file on the `/data` volume.

```bash
docker run -d --name jocasta \
  -p 8080:8080 \
  -v jocasta-data:/data \
  jocasta:latest
```

To see hardware addresses and interface data, the container needs host
networking (`--network host`). On a bridge network the sweep still finds hosts,
but the only neighbour table it can read is the container's own. No added
capability or sysctl is needed: Docker's default `net.ipv4.ping_group_range`
already allows the unprivileged ICMP socket.

## Configuration

Jocasta reads three sources, each overriding the last:

1. built-in defaults
2. a YAML file (`jocasta.yaml` by default; set another path with `--config`)
3. environment variables prefixed `JOCASTA_`

A missing config file is fine if the defaults and environment cover everything.

### Example `jocasta.yaml`

```yaml
server:
  host: "127.0.0.1"     # 0.0.0.0 to listen on every interface
  port: 3090

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
JOCASTA_DB__PATH=/data
JOCASTA_INVENTORY__ONLINE_WINDOW=30m
JOCASTA_SCAN__DEVICES__INTERVAL=10m
JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD=change-me
```

## CLI

```
jocasta <command> [flags]

  serve              Start the web server and the sweep poller.  (default)
  scan <cidr>        Sweep a prefix once and print the result.
  plugin run <name>  Read one configured source and print what it claims.

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
poller that sweeps every network in `networks` on `scan.devices.interval`.

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

Mounted under `/api`. Non-GET requests must come from the same origin.

| Method and path | Purpose |
|---|---|
| `GET /api/livez` | Liveness probe. |
| `GET /api/stats` | Inventory counts. |
| `GET /api/groups` | The groups devices are filed under. |
| `GET /api/devices` | List devices, with filters. |
| `GET /api/devices/{id}` | One device with its addresses and sources. |
| `PATCH /api/devices/{id}` | Update the user-owned fields (label, group, type, notes, ignored). |
| `GET /api/devices/{id}/events` | One device's change log. |
| `GET /api/events` | The whole change log. |
| `GET /api/scans` | Sweep and source-read history. |

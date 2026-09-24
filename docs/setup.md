# Setup

The [quick start](../README.md#quick-start) covers the common case: the
container on the host network. This page covers the other ways to install,
how configuration is read, and reading devices from your router.

## Install

### Container image

Multi-arch images (amd64 and arm64) are on the GitHub Container Registry,
tagged `latest` and with each release version, such as `:v0.3.0`. The image is
distroless and runs as a non-root user. It listens on `0.0.0.0:8080`, keeps its
SQLite file on the `/data` volume, and reads `/data/jocasta.yaml` if one is
mounted.

Use host networking (`--network host`) if you can. On a bridge network
(`-p 8080:8080`) the sweep still finds hosts, but it can only read the
container's own neighbour table, so devices have no hardware address, and
Jocasta identifies devices by hardware address. [Reading the router](#read-devices-from-your-router)
fills this gap. No added capability or sysctl is needed either way.

### Prebuilt binaries

Every release publishes archives for Linux, macOS and Windows, on amd64 and
arm64, with a `checksums.txt`, on the [releases page][releases]. Extract
`jocasta`, put a `jocasta.yaml` next to it, and run `jocasta serve`. It runs
without root.

The binary listens on `localhost:8080` by default. Set `server.host: 0.0.0.0`
to reach it from other machines.

[releases]: https://github.com/pushkar-anand/jocasta/releases

### From source

Needs Go 1.27 or newer.

```bash
git clone https://github.com/pushkar-anand/jocasta.git
cd jocasta
make build            # -> bin/jocasta
make docker           # -> jocasta:latest
```

## First run

On first visit the web UI asks you to create the admin account. Further users
and API tokens are under Settings.

The session cookie is HTTPS-only by default. If you reach Jocasta over plain
HTTP from anywhere but `localhost`, sign-in does not stick until you set
`server.auth.cookie_secure: false`. Putting it behind a TLS reverse proxy is better.

The database schema is embedded, and migrations run when the binary opens the
database. Jocasta is pre-1.0, so check the release notes before upgrading.

## Configuration

The only setting you need is `networks`, the prefixes to sweep. Everything
else has a default. [`jocasta.example.yaml`](../jocasta.example.yaml) lists
every setting with its default and a line on what it does.

Settings are read in this order, each overriding the last:

1. built-in defaults
2. a YAML file, `jocasta.yaml` in the working directory, or the path given to
   `--config`
3. environment variables prefixed `JOCASTA_`

In an environment variable, a double underscore separates levels and a single
underscore stays part of the key:

```bash
JOCASTA_SCAN__DEVICES__INTERVAL=10m      # scan.devices.interval
JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD=change-me
```

Times are shown in the server's time zone, which in a container is UTC. Name
yours to see them in local time. This changes only how times are shown; they are
stored in UTC.

```yaml
location:
  timezone: "Australia/Sydney"   # an IANA zone name
```

Do not commit a `jocasta.yaml` that holds real addresses or credentials.
`jocasta.yaml` and `*.db` are already in `.gitignore`.

## Read devices from your router

A sweep from one machine only sees hardware addresses on its own segment. On a
network split into VLANs, reading the router's ARP and DHCP tables identifies
the rest. MikroTik RouterOS is supported, over its REST API:

```yaml
plugins:
  routeros:
    gateway:                 # instance name, shown as the source
      enabled: true
      host: "192.0.2.1"
      user: "jocasta"        # a read-only user is enough
      password: "change-me"  # or JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD
      ssl: true
      insecure: true         # RouterOS serves a self-signed cert unless you import one
```

Check it with `jocasta plugin run gateway` before starting the server. See
[CLI](cli.md#plugin-run).

## Record who devices talk to

Jocasta can record which devices talk to which, and to where on the internet,
from the flow records your router exports (NetFlow v5, v9 or IPFIX). It keeps
hourly totals per device, never individual connections, for
`retention.traffic` (30 days by default).

```yaml
plugins:
  netflow:
    gateway:
      enabled: true
      listen: ":2055"
      exporters:
        - "192.0.2.1"        # the router's address; anything else is dropped
```

`exporters` is required. Flow records arrive over UDP, which anyone on the
network can forge, so only the listed routers are read.

With host networking the listener is reachable as it is. On a bridge network,
publish the port as UDP: `-p 2055:2055/udp`.

On MikroTik RouterOS, point Traffic Flow at the Jocasta host (here
`192.0.2.10`):

```
/ip traffic-flow set enabled=yes interfaces=all
/ip traffic-flow target add dst-address=192.0.2.10 port=2055 version=ipfix
```

Export from one router only. Two routers that both see a conversation both
report it, and it is counted twice.

Internet addresses are shown by the organisation that announces them, and
placed on the map by the country they are registered in, using
[IP to ASN data](https://db-ip.com/db/download/ip-to-asn-lite) and
[IP to Country data](https://db-ip.com/db/download/ip-to-country-lite) by
[DB-IP](https://db-ip.com), licensed under
[CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).

The world map draws a line from the network's country to each country it
talked to. Jocasta finds its country from the router's outside address. When that
address is private, because the ISP puts the router behind carrier-grade NAT,
name the country instead, by its two-letter code. A country named here is
used whatever the outside address says:

```yaml
location:
  country: "AU"
```

Only the country is used, to start the lines from its middle; nothing finer
is asked for or stored.

## Optional features

- **Port scanning**: set `scan.ports.enabled: true` to probe every known
  address for open TCP ports on a timer. See [CLI](cli.md#ports) for one-off
  scans.
- **Traffic**: who each device talks to, from your router's flow exports,
  with a Traffic page and a live Map. See
  [Record who devices talk to](#record-who-devices-talk-to) and
  [the web UI](ui.md#traffic-page).
- **MCP server**: lets AI agents query the inventory. See [MCP](mcp.md).
- **JSON API**: under `/api`, for scripts and dashboards. It takes the same API
  tokens as MCP, sent as `Authorization: Bearer <token>`.

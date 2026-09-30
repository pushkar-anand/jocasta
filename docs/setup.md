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
arm64, with a `checksums.txt`, on the [releases page][releases].

1. Extract `jocasta` from the archive for your system.
2. Put a `jocasta.yaml` next to it.
3. From that directory, run `jocasta serve`. It runs without root.

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

A device you have not labelled, grouped, typed, noted or ignored is deleted
once no scan has seen it for 90 days. Phones leave one of these each time they
change their privacy address. Anything you set on a device keeps it. Change the
window, or set it to `0` to keep every device:

```yaml
retention:
  devices: "2160h"
```

Do not commit a `jocasta.yaml` that holds real addresses or credentials.
`jocasta.yaml` and `*.db` are already in `.gitignore`.

## Name devices

A device can be named by more than one source. The name shown is the one from
the source ranked highest here:

1. Reverse DNS, looked up for each address a sweep finds.
2. A static DHCP lease on your router.
3. mDNS, asked of each address the sweep finds that has no reverse DNS name.
4. A dynamic DHCP lease on your router.

When two sources of the same rank disagree, the one heard from last wins. The
device page lists the name each source gives.

mDNS names phones, TVs, printers and computers that reverse DNS does not
know, such as `living-room-tv.local`. Jocasta sends one query to each such
device, on UDP port 5353, and needs no host networking or multicast for it.
The two settings are independent. With `scan.devices.resolve_names: false`, no
device has a reverse DNS name, so every device that answers the sweep is asked
over mDNS.
Most devices answer only a query from their own segment, as the mDNS standard
asks, so devices on other segments rarely get an mDNS name. To turn it off,
set `scan.devices.resolve_mdns: false`.

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

## Show what is plugged in where

The Topology page draws the network from the internet down: the router, each
switch and access point on the port it hangs from, and each device on its port
or Wi-Fi network. It is built from the bridge and Wi-Fi tables of every
MikroTik Jocasta reads, so the router from
[Read devices from your router](#read-devices-from-your-router) already gives
a first picture. A switch or access point that announces itself to the router
appears there with the devices behind it.

To see which port of a switch or access point each device is on, add it as
another RouterOS instance with `topology_only: true`:

```yaml
plugins:
  routeros:
    gateway:
      enabled: true
      host: "192.0.2.1"
      # ...
    switch_core:
      enabled: true
      host: "192.0.2.2"
      user: "jocasta"
      password: "change-me"
      topology_only: true    # read what is plugged into it; the router lists the devices
```

A `topology_only` source is left out of device discovery, so its management
address cannot rename a network. Every RouterOS source is read for its
topology on `scan.devices.interval`.

What each table adds:

- The bridge host table says which port each hardware address is behind, and
  in which VLAN when the bridge has VLAN filtering on.
- Neighbour discovery (MNDP or LLDP) names the switch or access point on each
  port. RouterOS runs it on the `LAN` interface list by default; see
  `/ip neighbor discovery-settings`.
- The Wi-Fi registration table marks Wi-Fi clients and names their network
  and band, from the wifi or the wireless package, with the rate each way and
  the signal. A router managing access points with CAPsMAN lists their
  clients too.
- The Ethernet monitor gives the rate each Ethernet and SFP port's link runs
  at, and the fastest rate both ends offer.

Check a source with `jocasta plugin run switch_core`, which prints its ports,
the addresses learned on each, and its neighbours.

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
name the country yourself, by its two-letter code. A country named here is
used whatever the outside address says:

```yaml
location:
  country: "AU"
```

Only the country is used, to start the lines from its middle; nothing finer
is asked for or stored.

## Send changes to your phone

Jocasta can send what each scan changed to an [ntfy](https://ntfy.sh) topic,
as a signed JSON POST to a URL of your own, or to any service that takes an
HTTP request, such as Discord, Slack or Telegram. See
[Send to another service](#send-to-another-service).

1. Under `notify` in the config file, add each destination by name, with one
   service block, then restart Jocasta:

   ```yaml
   notify:
     phone:
       ntfy:
         url: "https://ntfy.example.com/jocasta"   # the topic's address, as ntfy shows it
         token: ""                                 # or JOCASTA_NOTIFY__PHONE__NTFY__TOKEN
     automation:
       webhook:
         url: "https://hooks.example.com/jocasta"
         secret: "change-me"                       # required; signs each request
   ```

   A destination is on unless it says `enabled: false`.
2. In **Settings → Notifications**, tick the kinds of change each destination
   is sent, then select **Save**.
3. Optional: select **Send test**. A message titled "Jocasta test" arrives at
   the destination.

Each scan that changes something is sent as one message, such as
"2 new devices on 192.0.2.0/24". The first scan of a network is sent as a
count ("Found 42 devices"). Changes to devices you ignore, and your own edits,
are left out. A message that cannot be delivered is logged and shown on the
settings page, and is not sent again.

### Check a webhook's signature

A webhook request is a POST whose JSON body has `title`, `message`,
`scan_id` and `events`. Two headers come with it:

- `X-Jocasta-Signature-256`: `sha256=` and the hex HMAC-SHA256 of the raw
  body, keyed with the destination's `secret`.
- `X-Jocasta-Delivery`: a random id for this request. Log it, and drop a
  delivery you have already handled.

To check a request, compute the HMAC over the body exactly as received, before
parsing it, and compare it with the header in constant time. In Go:

```go
mac := hmac.New(sha256.New, []byte(secret))
mac.Write(body)
want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
ok := hmac.Equal([]byte(want), []byte(r.Header.Get("X-Jocasta-Signature-256")))
```

Reject the request when `ok` is false.

### Send to another service

The `http` service posts each message with a body you write as a template,
so it reaches any service that takes an HTTP request. Copy the recipe for
yours below, under `notify`.

The template is a [Go template](https://pkg.go.dev/text/template). It is
given:

- `.Title` and `.Body`: the message's title, such as "2 new devices on
  192.0.2.0/24", and its text, one change per line.
- `.ScanID`: the scan the changes came from, 0 for a test.
- `.Events`: each change, with `.Kind`, `.DeviceID`, `.Device` and `.Change`.
- `json`: writes a value as JSON, quoted and escaped. Write every value in a
  JSON body with it, as `{{ json .Title }}`: device names can hold quotes and
  the body holds line breaks.
- `truncate`: cuts a string to a number of characters and ends it with `…`,
  as `{{ .Body | truncate 2000 | json }}`. A message lists up to 20 changes,
  and long device names can take it past a service's limit, which the
  service answers with an error.

The body is sent as `application/json` unless `headers` names another
`Content-Type`. Jocasta renders the template with two sample messages when it
starts: one with a change, and the test message, which has none. If the
template does not parse, or its output does not parse as JSON, Jocasta
reports it and does not start.

A device names itself, so its name can hold text a chat service reads as a
mention, such as `@everyone`, or as formatting. The Slack, Telegram, Gotify,
Pushover and Apprise recipes send plain text. Discord has no plain-text
mode, so its recipe turns mentions off and a device name can still show
formatted.

Discord, with the channel's webhook URL from its Integrations settings:

```yaml
discord:
  http:
    url: "https://discord.com/api/webhooks/<id>/<token>"
    body: '{"content": {{ printf "%s\n%s" .Title .Body | truncate 2000 | json }}, "allowed_mentions": {"parse": []}}'
```

Slack, with an incoming webhook URL. The message goes in a `plain_text`
block, which Slack shows as written. The title is also sent as `text`, which
Slack shows in its notifications:

```yaml
slack:
  http:
    url: "https://hooks.slack.com/services/<path>"
    body: '{"text": {{ json .Title }}, "blocks": [{"type": "section", "text": {"type": "plain_text", "text": {{ printf "%s\n%s" .Title .Body | truncate 3000 | json }}}}]}'
```

Telegram, with a bot's token and the chat to post in:

```yaml
telegram:
  http:
    url: "https://api.telegram.org/bot<token>/sendMessage"
    body: '{"chat_id": "<chat id>", "text": {{ printf "%s\n%s" .Title .Body | truncate 4096 | json }}}'
```

[Gotify](https://gotify.net), with an application's token:

```yaml
gotify:
  http:
    url: "https://gotify.example.com/message"
    headers:
      X-Gotify-Key: "<app token>"
    body: '{"title": {{ json .Title }}, "message": {{ json .Body }}, "priority": 5}'
```

[Pushover](https://pushover.net), with an application's token and your user
key:

```yaml
pushover:
  http:
    url: "https://api.pushover.net/1/messages.json"
    body: '{"token": "<app token>", "user": "<user key>", "title": {{ json .Title }}, "message": {{ .Body | truncate 1024 | json }}}'
```

For email, SMS, Microsoft Teams, Signal and
[many more](https://github.com/caronc/apprise/wiki), run an
[Apprise API](https://github.com/caronc/apprise-api) server beside Jocasta.
Save the services' Apprise URLs under a key in Apprise, then post to that key:

```yaml
apprise:
  http:
    url: "http://apprise.example.com:8000/notify/jocasta"
    body: '{"title": {{ json .Title }}, "body": {{ json .Body }}}'
```

Home Assistant, n8n and Node-RED take any JSON, so they use the `webhook`
service. For Home Assistant, add an automation with a webhook
trigger and point a webhook destination at it. Home Assistant does not check
the signature, but a secret is still required. The automation reads the
message as `trigger.json.title` and `trigger.json.message`, and each change
under `trigger.json.events`.

```yaml
home_assistant:
  webhook:
    url: "http://homeassistant.local:8123/api/webhook/<webhook id>"
    secret: "change-me"
```

## Optional features

- **Port scanning**: set `scan.ports.enabled: true` to probe every known
  address for open TCP ports on a timer. See [CLI](cli.md#ports) for one-off
  scans.
- **Traffic**: who each device talks to, from your router's flow exports,
  with a Traffic page and a live Map. See
  [Record who devices talk to](#record-who-devices-talk-to) and
  [the web UI](ui.md#traffic-page).
- **Notifications**: new devices, opened ports and other changes, sent to
  ntfy, a signed webhook, or any service that takes an HTTP request. See
  [Send changes to your phone](#send-changes-to-your-phone).
- **MCP server**: lets AI agents query the inventory. See [MCP](mcp.md).
- **JSON API**: under `/api`, for scripts and dashboards. It takes the same API
  tokens as MCP, sent as `Authorization: Bearer <token>`.

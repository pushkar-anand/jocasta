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

Until the admin account exists, whoever opens `/setup` first creates it. Finish
setup straight after the first start, before anyone else on the network can
reach the page.

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
5. NetBIOS, asked of each address the sweep finds that has no name from
   reverse DNS or mDNS.
6. DNS-SD, the name each address still without a name gives the services it
   advertises.
7. SSDP, the label in the UPnP description of each address still without a
   name.

When two sources of the same rank disagree, the one heard from last wins. The
device page lists the name each source gives. Once a sweep has a name for a
device, a lower-ranked lookup does not replace it. The device keeps that name
until the lookup that gave it, or a higher-ranked one, answers with another.

mDNS gives a name to phones, TVs, printers and computers that reverse DNS
does not know, such as `living-room-tv.local`. Jocasta sends one query to each
such device, on UDP port 5353, and needs no host networking or multicast for
it. Most devices answer only a query from their own segment, as the mDNS
standard asks, so devices on other segments rarely get an mDNS name.

To turn it off, set `scan.devices.resolve_mdns: false`. It does not depend on
`scan.devices.resolve_names`: with reverse DNS off, no device has a reverse DNS
name, so every device that answers the sweep is asked over mDNS.

NetBIOS gives a name to Windows computers and to devices running Samba, such
as a NAS. A NetBIOS name, such as `DESKTOP-4F2K`, is 15 characters at most.
Jocasta sends one query to each device still without a name, on UDP port 137.
Windows answers only with file and printer sharing turned on, and by default
only a query from its own segment. To turn it off, set
`scan.devices.resolve_netbios: false`.

DNS-SD names TVs, speakers, printers and other devices that advertise their
services over mDNS but do not answer an mDNS query for their own name. After
each sweep, Jocasta asks the multicast group `224.0.0.251:5353` which service
types are on the segment, then which devices offer each one. A Google Cast
device is named after the name its owner gave it in the Home app, such as
`Living Room TV`. Any other device is named after the name most of its
services share, leaving out names that are only a serial number or other ID.
Jocasta binds UDP port 5353 to hear the answers, sharing it with any mDNS
responder on the same machine, such as Avahi. Like SSDP, it reaches only
Jocasta's own segment, and only with host networking. To turn it off, set
`scan.devices.resolve_dnssd: false`.

Jocasta also records the services each device advertises, named or not, and
lists them in the device page's Ports table. The port scan probes each
advertised TCP port, so the table says whether it answers. The scan also
probes every port recorded open, so when a service moves to a new port, its
old port closes. The classifier reads the services: a print service marks a
printer, and the Android TV remote service a TV. A service no sweep has heard
within `retention.history` drops off.

SSDP names TVs, speakers, media players, printers and routers that nothing
else names. The name is the label the device shows its owner, such as
`Living Room TV`. After a sweep that leaves a device without a name, Jocasta
sends one SSDP search to the multicast group `239.255.255.250:1900`. For each
device still without a name that answers, it fetches the UPnP description the
answer points to and takes the `friendlyName` from it. It fetches only from
the address that answered, over plain HTTP, at most 64 KB and for at most 2
seconds. The search reaches only Jocasta's own segment, and only with host
networking, since a bridge network does not pass multicast. To turn it off,
set `scan.devices.resolve_ssdp: false`.

## Read devices from your router

A sweep from one machine only sees hardware addresses on its own segment. On a
network split into VLANs, reading the router's ARP and DHCP tables identifies
the rest. MikroTik RouterOS and OpenWrt are supported.

### MikroTik RouterOS

Jocasta reads RouterOS over its REST API:

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

### OpenWrt

Jocasta reads OpenWrt over ubus, the JSON-RPC endpoint LuCI uses, at
`http://<router>/ubus`. Any router with LuCI installed serves it, so there is
nothing to install. Jocasta reads the neighbour table, the DHCP leases and the
static leases, and the interfaces with their VLANs. For the Topology page it
also reads the ports, the bridge table and the Wi-Fi clients. It changes
nothing on the router.

Give Jocasta a login of its own that can only read. On the router:

1. Copy [`openwrt/jocasta.json`](openwrt/jocasta.json) to
   `/usr/share/rpcd/acl.d/jocasta.json`. It grants the reads Jocasta makes and
   nothing else. LuCI's own groups do not grant the bridge table, so root
   cannot read it without this file either.
2. Hash a password for the login:

   ```sh
   uhttpd -m 'change-me'
   ```

3. Add the login to `/etc/config/rpcd`, with the hash the last step printed:

   ```
   config login
   	option username 'jocasta'
   	option password '$1$...'
   	list read 'jocasta'
   ```

4. Reload rpcd:

   ```sh
   /etc/init.d/rpcd reload
   ```

Then add the router to Jocasta:

```yaml
plugins:
  openwrt:
    gateway:                 # instance name, shown as the source
      enabled: true
      host: "192.0.2.1"
      user: "jocasta"
      password: "change-me"  # or JOCASTA_PLUGINS__OPENWRT__GATEWAY__PASSWORD
      ssl: false             # true once luci-ssl is installed
      insecure: false        # true for uhttpd's self-signed cert
```

With `ssl: false` the password crosses the network unencrypted, so Jocasta
logs a warning at startup for each RouterOS or OpenWrt source set that way.

Check it with `jocasta plugin run gateway`. A login rpcd refuses, or one
missing the ACL file, reads as credentials rejected.

A name you set under Network → DHCP and DNS → Static Leases ranks above the
name a device asks for, as a static lease does on RouterOS. A network is named
after its OpenWrt interface, such as `lan` or `iot`, and an interface on a VLAN
device carries its tag.

## Show what is plugged in where

The Topology page draws the network from the internet down: the router, each
switch and access point on the port it hangs from, and each device on its port
or Wi-Fi network. It is built from the bridge and Wi-Fi tables of every
MikroTik and OpenWrt router Jocasta reads, so the router from
[Read devices from your router](#read-devices-from-your-router) already gives
a first picture. A switch or access point that announces itself to the router
appears there with the devices behind it.

To see which port of a switch or access point each device is on, add it as
another instance with `topology_only: true`, under `routeros` or `openwrt`:

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
address cannot rename a network. Every RouterOS and OpenWrt source is read
for its topology on `scan.devices.interval`.

What each RouterOS table adds:

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

What each OpenWrt table adds:

- The kernel's bridge table says which port each hardware address is behind.
  On a router with a DSA switch, as most current ones have, each switch port
  is a device of its own, so the table names the port. On an older router
  with a swconfig switch, every switch port is one device, so the table names
  only that device. rpcd reads at most 256 entries of the table. A bridge that
  holds more shows the first 256, and the read logs a warning saying so.
- The bridge-vlan sections of the network configuration say which VLANs each
  port carries.
- iwinfo's station list marks Wi-Fi clients and names their network and
  band, with the rate each way and the signal.
- LuCI's device list gives the rate each port's link runs at.

OpenWrt runs no neighbour discovery by default, so an OpenWrt source names no
neighbours. The router still finds an OpenWrt access point among the
addresses on its ports when the access point is read too.

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

`exporters` is required. Only datagrams from the listed addresses are read.
A device on the same network can forge that address, so bind `listen` to an
address only the router reaches, or allow the port only from the router in
your firewall.

With host networking the listener is reachable as it is. On a bridge network,
publish the port as UDP: `-p 2055:2055/udp`.

On MikroTik RouterOS, point Traffic Flow at the Jocasta host (here
`192.0.2.10`):

```
/ip traffic-flow set enabled=yes interfaces=all
/ip traffic-flow target add dst-address=192.0.2.10 port=2055 version=ipfix
```

On OpenWrt, install softflowd and point it at the Jocasta host:

```sh
opkg update && opkg install softflowd    # OpenWrt 24.10 and older
apk update && apk add softflowd          # OpenWrt 25.12 and newer
uci set softflowd.@softflowd[0].enabled='1'
uci set softflowd.@softflowd[0].host_port='192.0.2.10:2055'
uci set softflowd.@softflowd[0].export_version='9'
uci set softflowd.@softflowd[0].track_ipv6='1'
uci set softflowd.@softflowd[0].timeout='maxlife=300'
uci delete softflowd.@softflowd[0].sampling_rate
uci commit softflowd
/etc/init.d/softflowd enable
/etc/init.d/softflowd start
```

Each line changes a default that would cost you traffic:

- `export_version` 9 carries IPv6, which the default, 5, cannot. IPFIX (`10`)
  works too.
- `track_ipv6` records IPv6 conversations as well as IPv4.
- `maxlife=300` reports a long download every five minutes. Without it, a
  conversation is reported only when it ends, so a stream that runs all
  evening lands in the hour it stopped.
- Deleting `sampling_rate` counts every packet. The default, `100`, counts one
  in 100. Jocasta scales sampled counts back up, but a conversation of a few
  packets may not be counted at all.

softflowd watches `br-lan`, the default LAN bridge. To record another
segment, such as a VLAN on `br-lan.20`, add a softflowd section for its
interface, with a `pid_file` and `control_socket` of its own. List the
router's LAN address in `exporters`: that is the address the exports come
from.

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
"2 new devices on 192.0.2.0/24". The first scan of a network, when every
device it finds is new, is sent as a count ("Found 42 devices"). Changes to
devices you ignore, and your own edits, are left out. A message that cannot be delivered is logged and shown on the
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

## Get told when a device goes quiet

Watch the devices whose absence you would want to hear about, such as a server,
a NAS or a camera. A watched device goes quiet (offline) when no scan has seen
it for longer than the online window, 15 minutes by default, and comes back
when one sees it again. Phones and laptops do both all day, so devices you do
not watch never log either.

1. On the device's page, select **Watch**.
2. In **Settings → Notifications**, tick **Watched device went quiet** and
   **Watched device came back** for a destination, then select **Save**.

A message reads "1 device went quiet on 192.0.2.0/24", with a line such as
"NAS went quiet after 18 minutes without an answer". The change log records
both kinds whether or not a destination is sent them. To list the watched
devices that are down now, filter the device list to **Watched only** and
**Quiet**.

Only a scan that could have seen a device decides it went quiet: a sweep of a
network it has an address on, or a read of a router that lists it. A device on
a network nothing scans stays as it was last seen. A router read that fails
part way decides nothing.

To change how long a device may go unseen, set the online window. It also
decides what the device list counts as seen recently:

```yaml
inventory:
  online_window: "30m"
```

## Optional features

- **Port scanning**: set `scan.ports.enabled: true` to probe every known
  address on your networks for open TCP ports on a timer. Each address is also probed on the
  TCP ports its device advertises a service on. See [CLI](cli.md#ports) for
  one-off scans.
- **Traffic**: who each device talks to, from your router's flow exports,
  with a Traffic page and a live Map. See
  [Record who devices talk to](#record-who-devices-talk-to) and
  [the web UI](ui.md#traffic-page).
- **Notifications**: new devices, opened ports and other changes, sent to
  ntfy, a signed webhook, or any service that takes an HTTP request. See
  [Send changes to your phone](#send-changes-to-your-phone).
- **MCP server**: lets AI agents query the inventory. See [MCP](mcp.md).
- **JSON API**: under `/api`, for scripts and dashboards. It takes the same API
  tokens as MCP, sent as `Authorization: Bearer <token>`. A token can expire
  after 30 days, 90 days or 1 year. After that it is refused like a revoked
  one and stays listed until you revoke it.

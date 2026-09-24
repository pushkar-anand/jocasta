# jocasta

Jocasta keeps an inventory of the devices on your network. It recognises each
device by its hardware address, so a device keeps its label and notes when its
IP changes, and it logs every change it sees. Connect your router and Jocasta
also identifies devices on other VLANs and shows who each device talks to. It
runs as a single binary with a web interface.

> **Pre-1.0 and under active development.** A release can change the
> configuration format, the database schema or the HTTP API with no upgrade
> path. Pin to a version and check the release notes before moving to the next.

![Overview](docs/img/overview.png)

## The problem

The usual ways of listing what is connected have these gaps:

- A device that changes its IP address, or picks a new random Wi-Fi hardware
  address, looks like a new device.
- An ARP scan, the router's lease list and a port scanner each show one slice
  of the network at one moment. None of them remembers.
- A scan from one machine fully identifies devices only on its own segment. On
  a network split into VLANs, most devices show up as an address and nothing
  else.

Jocasta keeps one record of what is on the network and what changed.

## How it works

Jocasta identifies a device by its hardware (MAC) address. An IP address is
treated as a lease the device holds for now, so the label, group and notes you
give a device stay with it when the address changes.

On its own, Jocasta sweeps the networks you list. Connect your router and it
also reads the router's ARP and DHCP tables. The router sees every segment, so
devices a single machine cannot reach still get a vendor and a name.

New devices, addresses gained or dropped, and hostname changes go into a
change log. Sweeps run on a timer, every five minutes by default, or when you
run one by hand.

If your router exports flow records (NetFlow or IPFIX), Jocasta also records
who each device talks to: other devices, and organisations and countries on
the internet. It stores only hourly totals per device.

## What you get

Some features need a source connected first. The **Needs** column says which.

| Feature | What it does | Needs |
|---|---|---|
| Device inventory | Every device with its addresses, segment, vendor, name, and when it was last seen. Search and filter by group, network or presence. | Nothing. Without the router, only devices on Jocasta's own segment get a hardware address, vendor and name. |
| Your own labels | Give a device a label, a group and notes, or mark it ignored. Scans never overwrite them. | Nothing |
| Network view | Each segment as its own page with the devices on it. | Nothing. Segment names and VLAN tags come from the router. |
| Change log | What appeared, moved or was renamed, and when, for one device or the whole network. | Nothing |
| Open ports | Which TCP ports each device listens on, and when that changed. | Port scanning turned on |
| Traffic | Who each device talks to, on your network and on the internet, over the last day, week or month. Shows devices probing the network, and which of your services the internet reached or tried to reach. | Router flow exports |
| Map | The last hour's traffic as a live tree, from the router out to each network, device and organisation, and a world map of the countries the network talked to. | Router flow exports |
| API | A JSON API over the same data, for scripts. | Nothing |
| MCP server | An [MCP](https://modelcontextprotocol.io) endpoint, so AI agents such as Claude Code can look up devices, label them, and report what changed. | Turned on in config |
| One binary | An embedded database, no other services to run. | |

Sources supported today:

- **Router tables:** MikroTik RouterOS, read over its REST API. See
  [Read devices from your router](docs/setup.md#read-devices-from-your-router).
- **Router flow exports:** any router that sends NetFlow v5, v9 or IPFIX. See
  [Record who devices talk to](docs/setup.md#record-who-devices-talk-to).

More screenshots: [docs/ui.md](docs/ui.md).

## Quick start

Write a minimal `jocasta.yaml` naming the networks to sweep:

```yaml
networks:
  - "192.0.2.0/24"

server:
  auth:
    cookie_secure: false   # only while you reach it over plain HTTP, not HTTPS
```

Run the container on the host network, so it can read hardware addresses:

```bash
docker run -d --name jocasta --network host \
  -v jocasta-data:/data \
  -v ./jocasta.yaml:/data/jocasta.yaml:ro \
  ghcr.io/pushkar-anand/jocasta:latest
```

Open `http://<host>:8080` and create the admin account. The first sweep runs
straight away and then every five minutes.

Every other setting, with its default, is in
[`jocasta.example.yaml`](jocasta.example.yaml). For binaries, building from
source and reading your router, see [setup](docs/setup.md).

## Documentation

- [Setup](docs/setup.md): other ways to install, configuration, reading
  devices from your router, and recording traffic from its flow exports.
- [CLI](docs/cli.md): one-off sweeps, port scans and source reads from the
  command line.
- [MCP server](docs/mcp.md): connecting AI agents to the inventory.
- [The web UI](docs/ui.md): a tour of the interface.
- [Development](docs/development.md): building and working on jocasta.

## Help and feedback

Report bugs and ask questions in
[GitHub issues](https://github.com/pushkar-anand/jocasta/issues).

## License

Jocasta is released under the [GNU AGPL-3.0](LICENSE).

Jocasta embeds third-party data:

- MAC vendor names from the [IEEE registries](https://standards.ieee.org/products-programs/regauth/)
  and [Wireshark's manufacturer table](https://www.wireshark.org/).
- [IP to ASN data](https://db-ip.com/db/download/ip-to-asn-lite) and
  [IP to Country data](https://db-ip.com/db/download/ip-to-country-lite) by
  [DB-IP](https://db-ip.com), licensed under
  [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/).
- Country outlines from [Natural Earth](https://www.naturalearthdata.com/),
  in the public domain.

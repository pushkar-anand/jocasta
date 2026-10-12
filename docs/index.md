# Jocasta

Jocasta keeps an inventory of the devices on your network. It recognises each
device by its hardware address, so a device keeps its label and notes when its
IP changes, and it logs every change it sees. It runs as a single binary with
a web interface, a JSON API and an MCP server.

!!! warning "Pre-1.0 and under active development"

    A release can change the configuration format, the database schema or the
    HTTP API with no upgrade path. Pin to a version and check the release notes
    before moving to the next. The version menu at the top of the page shows
    the docs for each release.

![The overview page](img/overview.png)

## How it works

1. Jocasta sweeps the networks you list, every 5 minutes by default, and asks
   each device that answers for its name.
2. It files each device under its hardware address, and treats an IP address
   as a lease the device holds for now.
3. It logs what changed: new devices, new addresses, new names, opened ports.
   Connect your router, flow exports and DNS resolver for devices on every
   VLAN, who each device talks to and what it looks up.

## Start here

<div class="grid cards" markdown>

-   **[Your first inventory](tutorial.md)**

    Run Jocasta, sweep your network, name a device and get told when it goes
    quiet. About 10 minutes.

-   **[How Jocasta works](concepts.md)**

    Devices, sweeps, sources and presence: the ideas the rest of the docs use.

-   **[Plugins](plugins/index.md)**

    Read your router, its flow exports and your DNS resolver.

-   **[Notifications](notifications/index.md)**

    Send changes to ntfy, a signed webhook, or a chat service.

-   **[MCP server](integrations/mcp.md) and [JSON API](integrations/api.md)**

    Let AI agents and scripts read and label the inventory.

-   **[Configuration reference](reference/configuration.md)**

    Every setting, with its type, default and allowed values.

</div>

## Get help

Report bugs and ask questions in
[GitHub issues](https://github.com/pushkar-anand/jocasta/issues). For
common problems and their fixes, see
[Fix common problems](operate/troubleshooting.md).

These docs are also published for language models: [`llms.txt`](llms.txt)
lists every page as Markdown, and [`llms-full.txt`](llms-full.txt) holds every
page in one file.

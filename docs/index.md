# Jocasta

Jocasta keeps an inventory of the devices on your network. It logs every
change. It is one binary, with a web UI, a JSON API and an MCP server.

!!! warning "Pre-1.0"

    A release can change the config format, the database schema or the API
    with no upgrade path. Pin a version. Before you move to a new version,
    read the [upgrade notes](operate/upgrading.md). To read the docs for your
    release, select it in the version menu at the top.

![The overview page](img/overview.png)

## How it works

1. Jocasta sweeps the networks you list, every 5 minutes.
2. It records each device under its hardware address, because an IP address
   is only a lease.
3. It logs what changed.

To see other VLANs, add your router. To see traffic, add flow exports. To see
lookups, add your DNS resolver.

## Start here

<div class="grid cards" markdown>

-   **[Your first inventory](tutorial.md)**

    Run Jocasta, sweep your network, name a device, and get a message when
    it goes quiet. 10 minutes.

-   **[How Jocasta works](concepts.md)**

    Devices, scans, sources and presence.

-   **[Plugins](plugins/index.md)**

    Routers, flow exports, DNS resolvers.

-   **[Notifications](notifications/index.md)**

    ntfy, signed webhooks, chat services.

-   **[MCP server](integrations/mcp.md) and [JSON API](integrations/api.md)**

    For agents and scripts.

-   **[Configuration reference](reference/configuration.md)**

    Every setting, its type and default.

</div>

## Get help

[GitHub issues](https://github.com/pushkar-anand/jocasta/issues), or
[Fix common problems](operate/troubleshooting.md).

For language models: [`llms.txt`](llms.txt) lists every page as Markdown, and
[`llms-full.txt`](llms-full.txt) holds them all in one file.

# Jocasta

Jocasta keeps an inventory of the devices on your network. It logs every change. It is one binary, with a web UI, a JSON API and an MCP server.

> [!WARNING]
>
> **Pre-1.0**
>
> A release can change the config format, the database schema or the API with no upgrade path. Pin a version. Before you move to a new version, read the [upgrade notes](<https://pushkar-anand.github.io/jocasta/0.15/operate/upgrading/index.md>). To read the docs for your release, select it in the version menu at the top.

## How it works

1. Jocasta sweeps the networks you list, or the network of the host, every 5 minutes.
2. It records each device under its hardware address, because an IP address is only a lease.
3. It logs what changed.

To see other VLANs, add your router. To see traffic, add flow exports. To see lookups, add your DNS resolver.

## Start here

- **[Your first inventory](<https://pushkar-anand.github.io/jocasta/0.15/tutorial/index.md>)**

  Run Jocasta, sweep your network, name a device, and get a message when it goes quiet. 10 minutes.
- **[How Jocasta works](<https://pushkar-anand.github.io/jocasta/0.15/concepts/index.md>)**

  Devices, scans, sources and presence.
- **[Plugins](<https://pushkar-anand.github.io/jocasta/0.15/plugins/index.md>)**

  Routers, flow exports, DNS resolvers.
- **[Notifications](<https://pushkar-anand.github.io/jocasta/0.15/notifications/index.md>)**

  ntfy, signed webhooks, chat services.
- **[MCP server](<https://pushkar-anand.github.io/jocasta/0.15/integrations/mcp/index.md>) and [JSON API](<https://pushkar-anand.github.io/jocasta/0.15/integrations/api/index.md>)**

  For agents and scripts.
- **[Configuration reference](<https://pushkar-anand.github.io/jocasta/0.15/reference/configuration/index.md>)**

  Every setting, its type and default.

## Get help

[GitHub issues](<https://github.com/pushkar-anand/jocasta/issues>), or [Fix common problems](<https://pushkar-anand.github.io/jocasta/0.15/operate/troubleshooting/index.md>).

For language models: [`llms.txt`](<https://pushkar-anand.github.io/jocasta/0.15/llms.txt>) lists every page as Markdown, and [`llms-full.txt`](<https://pushkar-anand.github.io/jocasta/0.15/llms-full.txt>) holds them all in one file.

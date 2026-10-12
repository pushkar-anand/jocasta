# Jocasta

Jocasta keeps an inventory of the devices on your network. It recognises each
device by its hardware address, so a device keeps its label and notes when its
IP changes, and it logs every change it sees. Connect your router and Jocasta
also identifies devices on other VLANs and shows who each device talks to. It
runs as a single binary with a web interface.

> **Pre-1.0 and under active development.** A release can change the
> configuration format, the database schema or the HTTP API with no upgrade
> path. Pin to a version and check the release notes before moving to the next.
> The version menu at the top of the page shows the docs for each release.

![The overview page](img/overview.png)

## Where to start

- [Install](install.md) Jocasta, then set the networks to sweep in
  [Configuration](configuration.md).
- Connect your router, flow exports and DNS resolver with
  [Plugins](plugins/index.md).
- Get changes sent to your phone or chat with
  [Notifications](notifications/index.md).
- Connect scripts and AI agents with the [JSON API](integrations/api.md) and
  the [MCP server](integrations/mcp.md).
- Look up any setting in the
  [configuration reference](reference/configuration.md).

## For AI agents

These docs are also published as plain text for language models:
[`llms.txt`](llms.txt) lists every page as Markdown, and
[`llms-full.txt`](llms-full.txt) holds every page in one file.

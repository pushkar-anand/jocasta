# Jocasta

Jocasta keeps an inventory of the devices on your network. It recognises each device by its hardware address, so a device keeps its label and notes when its IP changes, and it logs every change it sees. Connect your router and Jocasta also identifies devices on other VLANs and shows who each device talks to. It runs as a single binary with a web interface.

> **Pre-1.0 and under active development.** A release can change the configuration format, the database schema or the HTTP API with no upgrade path. Pin to a version and check the release notes before moving to the next. The version menu at the top of the page shows the docs for each release.

## Where to start

- [Install](<https://pushkar-anand.github.io/jocasta/dev/install/index.md>) Jocasta, then set the networks to sweep in [Configuration](<https://pushkar-anand.github.io/jocasta/dev/configuration/index.md>).
- Connect your router, flow exports and DNS resolver with [Plugins](<https://pushkar-anand.github.io/jocasta/dev/plugins/index.md>).
- Get changes sent to your phone or chat with [Notifications](<https://pushkar-anand.github.io/jocasta/dev/notifications/index.md>).
- Connect scripts and AI agents with the [JSON API](<https://pushkar-anand.github.io/jocasta/dev/integrations/api/index.md>) and the [MCP server](<https://pushkar-anand.github.io/jocasta/dev/integrations/mcp/index.md>).
- Look up any setting in the [configuration reference](<https://pushkar-anand.github.io/jocasta/dev/reference/configuration/index.md>).

## For AI agents

These docs are also published as plain text for language models: [`llms.txt`](<https://pushkar-anand.github.io/jocasta/dev/llms.txt>) lists every page as Markdown, and [`llms-full.txt`](<https://pushkar-anand.github.io/jocasta/dev/llms-full.txt>) holds every page in one file.

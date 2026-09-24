# MCP server

Jocasta can serve the inventory to AI agents such as Claude Code over the
[Model Context Protocol](https://modelcontextprotocol.io). It is off by
default.

An agent connected this way can answer questions such as:

- What joined the network this week?
- Which devices have SSH open?
- What is 192.0.2.47, and when was it first seen?
- Which devices talked to a new organisation today?

With a `read_write` token it can also label and group devices. It reads what
past scans recorded and never starts a scan.

## Turn it on

1. Set `server.mcp.enabled: true` in the config file, or
   `JOCASTA_SERVER__MCP__ENABLED=true`.
   While it is off, `/mcp` answers 404.
2. Create an API token under Settings, API tokens. A `read` token is offered
   only the tools that read the inventory. A `read_write` token is also offered
   the tools that change it.

The endpoint is `/mcp`, using the Streamable HTTP transport, with the token
sent as `Authorization: Bearer <token>`. Clients discover the available tools
and [prompts](#prompts) when they connect.

Device hostnames and vendor names are reported by the devices themselves, so
anything on your network can choose what an agent reads there. Give an agent
a `read` token unless it needs to change something.

## Connect a client

The examples use `https://jocasta.example.test/mcp`; replace it with your
server's address.

**Claude Code**

```bash
claude mcp add --transport http jocasta https://jocasta.example.test/mcp \
  --header "Authorization: Bearer <token>"
```

**Codex**

Codex reads the token from an environment variable, which keeps it out of
its config file:

```bash
export JOCASTA_TOKEN=<token>
codex mcp add jocasta --url https://jocasta.example.test/mcp \
  --bearer-token-env-var JOCASTA_TOKEN
```

**Hermes Agent**

Put `JOCASTA_TOKEN=<token>` in `~/.hermes/.env`, add the server to
`~/.hermes/config.yaml`, then run `/reload-mcp` in a running session:

```yaml
mcp_servers:
  jocasta:
    url: "https://jocasta.example.test/mcp"
    headers:
      Authorization: "Bearer ${JOCASTA_TOKEN}"
```

**Other clients**

Any client that supports Streamable HTTP and lets you set a request header
works. For clients that can only launch a local server over stdio, a bridge
such as [`mcp-remote`](https://www.npmjs.com/package/mcp-remote) can forward to
the endpoint.

## Tools

| Tool | What it answers |
|---|---|
| `list_devices` | Which devices match a search, group, network, type or online status. |
| `get_device` | Everything about one device: addresses, ports, sources, your labels. |
| `list_events` | What changed, across the network or for one device. |
| `list_networks`, `get_network` | The network segments, and how many devices are on each. |
| `list_groups` | The groups you filed devices under. |
| `get_stats` | How many devices, online, offline, ignored and new in the last 24 hours. |
| `get_port_overview` | Open ports across the network, and the commonest services. |
| `list_scans` | When scans ran, and what they found. |
| `list_traffic` | Who devices exchanged data with. Needs traffic collection. |
| `update_device_curation` | Sets a device's label, group, type, notes and ignored flag. `read_write` tokens only. |

## Prompts

Prompts are saved procedures you start yourself; the agent then carries them
out with the tools. In Claude Code they appear as slash commands, such as
`/mcp__jocasta__weekly_report`.

| Prompt | What it does |
|---|---|
| `triage_devices` | Finds the devices that need attention: unlabelled, doubtfully classified, or likely duplicates left by a randomised hardware address. Proposes a label, group and type for each. With a `read_write` token the agent applies the proposals you confirm; with a `read` token it only proposes. |
| `weekly_report` | Summarises what changed over the last 7 days: new devices, devices gone quiet, organisations a device reached for the first time (when traffic collection is set up), port changes, identity changes, and what may need a look. Takes an optional `days` argument, from 1 to 90. It can reach back only as far as `retention.history` keeps the change log. |

## Errors

Errors are [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem
documents, as in the HTTP API. A failed tool call returns one as the text of an
error result, with the tool name as `instance`. The cause of a 500 stays in
the server log.

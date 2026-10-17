# MCP server

Jocasta can serve the inventory to AI agents, such as Claude Code, over the [Model Context Protocol](<https://modelcontextprotocol.io>). By default, the MCP server is off.

An agent connected this way can answer questions such as:

- What joined the network this week?
- Which devices have SSH open?
- What is 192.0.2.47, and when was it first seen?
- Which devices talked to a new organisation today?

With a `read_write` token, the agent can also label and group devices. It reads what past scans recorded. It never starts a scan.

## Turn it on

1. Set `server.mcp.enabled: true` in the config file, or `JOCASTA_SERVER__MCP__ENABLED=true`. While the MCP server is off, `/mcp` answers 404.
2. Create an API token. See [API tokens](<https://pushkar-anand.github.io/jocasta/dev/operate/users/#api-tokens>). A `read` token gets only the tools that read the inventory. A `read_write` token also gets the tools that change it.

The endpoint is `/mcp`. It uses the Streamable HTTP transport. Send the token as `Authorization: Bearer <token>`. When a client connects, it finds the available tools and [prompts](<#prompts>).

The devices themselves report their hostnames and vendor names. So any device on your network can choose what an agent reads in these fields. Give an agent a `read` token, unless it must change something.

## Connect a client

The examples use `https://jocasta.example.test/mcp`. Replace it with the address of your server.

**Claude Code**

```
claude mcp add --transport http jocasta https://jocasta.example.test/mcp \
  --header "Authorization: Bearer <token>"
```

**Codex**

Codex reads the token from an environment variable. So the token is not in its config file:

```
export JOCASTA_TOKEN=<token>
codex mcp add jocasta --url https://jocasta.example.test/mcp \
  --bearer-token-env-var JOCASTA_TOKEN
```

**Hermes Agent**

1. Put `JOCASTA_TOKEN=<token>` in `~/.hermes/.env`.
2. Add the server to `~/.hermes/config.yaml`:

   ```
   mcp_servers:
     jocasta:
       url: "https://jocasta.example.test/mcp"
       headers:
         Authorization: "Bearer ${JOCASTA_TOKEN}"
   ```
3. In a running session, run `/reload-mcp`.

**Other clients**

Any client works if it supports Streamable HTTP and lets you set a request header. Some clients can only start a local server over stdio. For these clients, a bridge such as [`mcp-remote`](<https://www.npmjs.com/package/mcp-remote>) can forward to the endpoint.

## Tools

| Tool | What it answers |
| --- | --- |
| `list_devices` | Which devices match a search, group, network, type, presence (seen recently or quiet) or whether you watch them. |
| `get_device` | Everything about one device: addresses, ports, advertised services, sources, your labels, and where it is plugged in and how fast. |
| `explain_device` | Why a device has the class it has: the facts the classifier used, the rule that decided and every other rule that matched. |
| `list_events` | What changed, across the network or for one device. |
| `list_networks`, `get_network` | The network segments, and how many devices are on each. |
| `list_groups` | The groups you filed devices under. |
| `get_stats` | How many devices were seen recently, are quiet, are ignored or were discovered in the last 24 hours. |
| `get_port_overview` | Open ports across the network, and the commonest services. |
| `list_scans` | When scans ran, and what they found. |
| `list_traffic` | Who devices exchanged data with. Needs traffic collection. |
| `list_dns` | The names a device looked up, and which were blocked. Needs [lookups recorded](<https://pushkar-anand.github.io/jocasta/dev/plugins/dns/index.md>). |
| `get_topology` | What is plugged in where, and how fast each link runs. Needs [topology set up](<https://pushkar-anand.github.io/jocasta/dev/plugins/topology/index.md>). |
| `update_device_curation` | Sets a device's label, group, type, notes and ignored flag. `read_write` tokens only. |
| `watch_device` | Watches a device, or stops, so you are told when it goes quiet or comes back. `read_write` tokens only. |

## Prompts

A prompt is a saved procedure that you start yourself. The agent then does the procedure with the tools. In Claude Code, prompts show as slash commands, such as `/mcp__jocasta__weekly_report`.

| Prompt | What it does |
| --- | --- |
| `triage_devices` | Finds the devices that need attention: unlabelled, doubtfully classified, or likely duplicates left by a randomised hardware address. Proposes a label, group and type for each. With a `read_write` token the agent applies the proposals you confirm; with a `read` token it only proposes. |
| `weekly_report` | Summarises what changed over the last 7 days: new devices, devices gone quiet, organisations a device reached for the first time (when traffic collection is set up), port changes, identity changes, and what may need a look. Takes an optional `days` argument, from 1 to 90. It can reach back only as far as `retention.history` keeps the change log. |

## Errors

An error is an [RFC 9457](<https://www.rfc-editor.org/rfc/rfc9457>) problem document, as in the HTTP API. A failed tool call returns this document as the text of an error result. The `instance` field has the tool name. Jocasta writes the cause of a 500 only to the server log.

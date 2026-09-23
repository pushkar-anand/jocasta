# MCP server

Jocasta can serve the inventory to AI agents such as Claude Code over the
[Model Context Protocol](https://modelcontextprotocol.io). It is off by
default.

## Turn it on

1. Set `mcp.enabled: true` in the config file, or `JOCASTA_MCP__ENABLED=true`.
   While it is off, `/mcp` answers 404.
2. Create an API token under Settings, API tokens. A `read` token is offered
   only the tools that read the inventory. A `read_write` token is also offered
   the tools that change it.

The endpoint is `/mcp`, using the Streamable HTTP transport, with the token
sent as `Authorization: Bearer <token>`. Clients discover the available tools
when they connect.

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

Codex reads the token from an environment variable rather than storing it in
its config:

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

## Errors

Errors are [RFC 9457](https://www.rfc-editor.org/rfc/rfc9457) problem
documents, as in the HTTP API. A failed tool call returns one as the text of an
error result, with the tool name as `instance`. The cause of a 500 is logged on
the server, not sent to the agent.

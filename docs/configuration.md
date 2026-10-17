# Configure

You must set only `networks`, the prefixes to sweep. All other settings have
a default.
[`jocasta.example.yaml`](https://github.com/pushkar-anand/jocasta/blob/main/jocasta.example.yaml)
lists every setting, with its default and a line about what it does. The
[configuration reference](reference/configuration.md) gives the type,
allowed values and environment variable of each setting.

Jocasta reads settings in this order. Each one overrides the one before it:

1. built-in defaults
2. a YAML file, `jocasta.yaml` in the working directory, or the path given to
   `--config`
3. environment variables prefixed `JOCASTA_`

In an environment variable, a double underscore separates levels. A single
underscore stays part of the key:

```bash
JOCASTA_SCAN__DEVICES__INTERVAL=10m      # scan.devices.interval
JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD=change-me
```

Jocasta shows times in the time zone of the server. In a container, this
time zone is UTC. To see local times, set your time zone. This setting
changes only how Jocasta shows times. It always stores them in UTC.

```yaml
location:
  timezone: "Australia/Sydney"   # an IANA zone name
```

Jocasta deletes a device when no scan has seen it for 90 days. It does not
delete a device that you labelled, grouped, typed, noted, ignored or watched.
Each time a phone changes its privacy address, it leaves one such device. To
change the window, set `retention.devices`. To keep every device, set it to
`0`:

```yaml
retention:
  devices: "2160h"
```

Do not commit a `jocasta.yaml` that holds real addresses or credentials to a
repository.

## What to turn on next

- **Port scanning**: to probe every known address on your networks for open
  TCP ports at a set interval, set `scan.ports.enabled: true`. Jocasta also
  probes each address on the TCP ports on which its device advertises a
  service. For a single scan, see [CLI](cli.md#ports).
- **Routers**: devices on every VLAN, and what is connected where. See
  [Plugins](plugins/index.md).
- **Traffic**: who each device talks to, from the flow exports of your
  router. It adds a Traffic page and a live Map. See
  [NetFlow](plugins/netflow.md) and [Tour the web UI](ui.md#traffic-page).
- **DNS lookups**: what each device looks up, from the query log of your
  resolver. See [Record what devices look up](plugins/dns.md).
- **Notifications**: new devices, opened ports and other changes. Jocasta
  sends them to ntfy, a signed webhook, or any service that accepts an HTTP
  request. See [Notifications](notifications/index.md).
- **MCP server**: AI agents can query the inventory. See
  [MCP server](integrations/mcp.md).
- **JSON API**: under `/api`, for scripts and dashboards. See
  [JSON API](integrations/api.md).

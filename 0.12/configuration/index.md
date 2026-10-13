# Configure

The only setting you need is `networks`, the prefixes to sweep. Everything else has a default. [`jocasta.example.yaml`](<https://github.com/pushkar-anand/jocasta/blob/main/jocasta.example.yaml>) lists every setting with its default and a line on what it does, and the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.12/reference/configuration/index.md>) gives each one's type, allowed values and environment variable.

Settings are read in this order, each overriding the last:

1. built-in defaults
2. a YAML file, `jocasta.yaml` in the working directory, or the path given to `--config`
3. environment variables prefixed `JOCASTA_`

In an environment variable, a double underscore separates levels and a single underscore stays part of the key:

```
JOCASTA_SCAN__DEVICES__INTERVAL=10m      # scan.devices.interval
JOCASTA_PLUGINS__ROUTEROS__GATEWAY__PASSWORD=change-me
```

Times are shown in the server's time zone, which in a container is UTC. Name yours to see them in local time. This changes only how times are shown; they are stored in UTC.

```
location:
  timezone: "Australia/Sydney"   # an IANA zone name
```

A device you have not labelled, grouped, typed, noted, ignored or watched is deleted once no scan has seen it for 90 days. Phones leave one of these each time they change their privacy address. Anything you set on a device keeps it. Change the window, or set it to `0` to keep every device:

```
retention:
  devices: "2160h"
```

Do not commit a `jocasta.yaml` that holds real addresses or credentials to a repository.

## What to turn on next

- **Port scanning**: set `scan.ports.enabled: true` to probe every known address on your networks for open TCP ports on a timer. Each address is also probed on the TCP ports its device advertises a service on. See [CLI](<https://pushkar-anand.github.io/jocasta/0.12/cli/#ports>) for one-off scans.
- **Routers**: devices on every VLAN, and what is plugged in where. See [Plugins](<https://pushkar-anand.github.io/jocasta/0.12/plugins/index.md>).
- **Traffic**: who each device talks to, from your router's flow exports, with a Traffic page and a live Map. See [NetFlow](<https://pushkar-anand.github.io/jocasta/0.12/plugins/netflow/index.md>) and [Tour the web UI](<https://pushkar-anand.github.io/jocasta/0.12/ui/#traffic-page>).
- **DNS lookups**: what each device looks up, from your resolver's query log. See [Record what devices look up](<https://pushkar-anand.github.io/jocasta/0.12/plugins/dns/index.md>).
- **Notifications**: new devices, opened ports and other changes, sent to ntfy, a signed webhook, or any service that takes an HTTP request. See [Notifications](<https://pushkar-anand.github.io/jocasta/0.12/notifications/index.md>).
- **MCP server**: lets AI agents query the inventory. See [MCP server](<https://pushkar-anand.github.io/jocasta/0.12/integrations/mcp/index.md>).
- **JSON API**: under `/api`, for scripts and dashboards. See [JSON API](<https://pushkar-anand.github.io/jocasta/0.12/integrations/api/index.md>).

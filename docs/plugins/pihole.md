# Pi-hole

Jocasta reads the query log of a [Pi-hole](https://pi-hole.net) v6 over its
API. [Record what devices look up](dns.md) tells what Jocasta records. It
also tells how to make each device ask Pi-hole directly. Pi-hole does not log
answer addresses, so its lookups do not name traffic.

```yaml
plugins:
  pihole:
    resolver:
      enabled: true
      host: "192.0.2.53"
      port: 443
      ssl: true
      insecure: true         # for Pi-hole's self-signed certificate
      password: "change-me"
```

To prepare the server:

1. In Pi-hole's web interface, create an app password.
2. Put it in `password`, or in `JOCASTA_PLUGINS__PIHOLE__RESOLVER__PASSWORD`.

Pi-hole has no read-only login, so the app password can also change
settings. A new app password signs out every session. This includes the session of
Jocasta. Jocasta signs in again on its next read. If the Pi-hole has no
password, leave `password` empty.

Keep the privacy level of Pi-hole at the level that shows everything. A level
that hides domains or clients also hides them from Jocasta.

Each read takes at most 20,000 lookups. If more lookups collect while Jocasta
is stopped, it keeps the newest. It logs that it skipped the rest.

## All settings

The [configuration reference](../reference/configuration.md#pluginspihole)
lists every `plugins.pihole` setting, with its default and allowed values.

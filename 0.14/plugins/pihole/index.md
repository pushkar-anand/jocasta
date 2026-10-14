# Pi-hole

Jocasta reads the query log of a [Pi-hole](<https://pi-hole.net>) v6 over its API. What is recorded, and how to route lookups so each device asks Pi-hole directly, is in [Record what devices look up](<https://pushkar-anand.github.io/jocasta/0.14/plugins/dns/index.md>). Pi-hole logs no answer addresses, so its lookups name no traffic.

```
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

Pi-hole has no read-only login, so the app password can change settings too. Making a new app password signs out every session, Jocasta's included, and Jocasta signs in again on its next read. On a Pi-hole with no password set, leave `password` empty.

Keep Pi-hole's privacy level at the one that shows everything. A level that hides domains or clients hides them from Jocasta too.

Each read takes at most 20,000 lookups. If more pile up while Jocasta is stopped, it keeps the newest and logs that it skipped the rest.

## All settings

Every `plugins.pihole` setting, with its default and allowed values, is in the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.14/reference/configuration/#pluginspihole>).

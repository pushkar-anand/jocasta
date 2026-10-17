# AdGuard Home

Jocasta reads the query log of an [AdGuard Home](<https://adguard.com/adguard-home/overview.html>) over its API. [Record what devices look up](<https://pushkar-anand.github.io/jocasta/dev/plugins/dns/index.md>) tells what Jocasta records. It also tells how to make each device ask AdGuard Home directly.

```
plugins:
  adguard:
    resolver:
      enabled: true
      host: "192.0.2.53"
      port: 443
      ssl: true
      user: "jocasta"
      password: "change-me"
```

Put the web interface login in `user` and `password`. You can also put the password in `JOCASTA_PLUGINS__ADGUARD__RESOLVER__PASSWORD`. AdGuard Home has no read-only users, so the login can also change settings. If the server has no login, leave `user` empty.

In the settings of AdGuard Home, keep the query log on. Keep client address anonymisation off. An anonymised address names no device, so Jocasta counts its lookups against no device.

Each read takes at most 20,000 lookups. If more lookups collect while Jocasta is stopped, it keeps the newest. It logs that it skipped the rest.

## All settings

The [configuration reference](<https://pushkar-anand.github.io/jocasta/dev/reference/configuration/#pluginsadguard>) lists every `plugins.adguard` setting, with its default and allowed values.

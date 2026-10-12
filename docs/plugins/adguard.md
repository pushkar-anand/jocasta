# AdGuard Home

Jocasta reads the query log of an
[AdGuard Home](https://adguard.com/adguard-home/overview.html) over its API.
What is recorded, and how to route lookups so each device asks AdGuard Home
directly, is in [DNS resolvers](index.md#dns-resolvers).

```yaml
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

Put the web interface login in `user` and `password`, or the password in
`JOCASTA_PLUGINS__ADGUARD__RESOLVER__PASSWORD`. AdGuard Home has no read-only
users, so the login can change settings too. On a server with no login set,
leave `user` empty.

In AdGuard Home's settings, keep the query log on and client address
anonymisation off. An anonymised address names no device, so its lookups
count against none.

Each read takes at most 20,000 lookups. If more pile up while Jocasta is
stopped, it keeps the newest and logs that it skipped the rest.


## All settings

Every `plugins.adguard` setting, with its default and allowed values, is in the
[configuration reference](../reference/configuration.md#pluginsadguard).

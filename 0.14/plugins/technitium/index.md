# Technitium

Jocasta reads the query log of a [Technitium DNS Server](<https://technitium.com/dns/>) over its HTTP API. [Record what devices look up](<https://pushkar-anand.github.io/jocasta/0.14/plugins/dns/index.md>) tells what Jocasta records. It also tells how to make each device ask the server directly.

```
plugins:
  technitium:
    resolver:
      enabled: true
      host: "192.0.2.53"
      port: 53443
      ssl: true
      insecure: true         # for the server's self-signed certificate
      token: "change-me"
```

To prepare the server:

1. In Apps, App Store, install Query Logs (Sqlite). Technitium keeps a query log only through an app like this one.
2. In Administration, Users, add a user for Jocasta.
3. In Administration, Permissions, give that user View on the Logs section and nothing else.
4. Sign in as that user. In the user menu, select Create API Token.
5. Put the token in `token`, or in `JOCASTA_PLUGINS__TECHNITIUM__RESOLVER__TOKEN`.

If more than one app logs queries, set `log_app` to the app to read. Use the name that the Apps page shows.

In a cluster, each node logs the lookups that it answered to its own disk. Add one entry for each node, at the node's own address. Do not use a virtual address that the nodes share, such as a VRRP address. It reaches only one node at a time, so Jocasta does not read the lookups of the other node. The primary node copies the token and the log app to every node.

## All settings

The [configuration reference](<https://pushkar-anand.github.io/jocasta/0.14/reference/configuration/#pluginstechnitium>) lists every `plugins.technitium` setting, with its default and allowed values.

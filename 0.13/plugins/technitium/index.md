# Technitium

Jocasta reads the query log of a [Technitium DNS Server](<https://technitium.com/dns/>) over its HTTP API. What is recorded, and how to route lookups so each device asks the server directly, is in [Record what devices look up](<https://pushkar-anand.github.io/jocasta/0.13/plugins/dns/index.md>).

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
2. In Administration, Users, add a user for Jocasta. In Administration, Permissions, give that user View on the Logs section and nothing else.
3. Sign in as that user, and in the user menu select Create API Token. Put the token in `token`, or in `JOCASTA_PLUGINS__TECHNITIUM__RESOLVER__TOKEN`.

With more than one app logging queries, name the one to read in `log_app`, as the Apps page lists it.

In a cluster, each node logs the lookups it answered to its own disk. Add one entry per node, each at that node's own address. A virtual address shared between nodes, such as one VRRP keeps, reaches one node at a time, and the other node's lookups would go unread. The token and the log app reach every node from the primary.

## All settings

Every `plugins.technitium` setting, with its default and allowed values, is in the [configuration reference](<https://pushkar-anand.github.io/jocasta/0.13/reference/configuration/#pluginstechnitium>).

# CLI

```
jocasta <command> [flags]

  serve              Start the web server and the sweep poller.  (default)
  scan <cidr>        Sweep a prefix once and print the result.
  ports [target]     Probe TCP ports on an address, a prefix, or the inventory.
  plugin run <name>  Read one configured source and print what it claims.
  version            Print build information.

  -c, --config       Path to the config file (default "jocasta.yaml")
      --log-level    debug | info | warn | error
      --log-format   text | json
```

Every command takes `--help` for its own flags.

## `serve`

```bash
jocasta serve                 # host and port from config
jocasta serve -p 9000         # override the port
```

Starts the HTTP server. When `scan.devices.enabled` is set, it also starts a
poller that sweeps every network in `networks` on `scan.devices.interval`. When
`scan.ports.enabled` is set, a second poller port-scans every address the
inventory holds on `scan.ports.interval`. Each enabled `netflow` instance
starts a listener for its router's flow exports, and an hourly prune deletes
records older than their `retention` window.

## `scan`

Runs one sweep and prints a table, or JSON with `--json`. With `--save` it also
records the sweep in the inventory, under the name from `--source`, then
`scan.source`, then this host's name.

```
$ jocasta scan 192.0.2.0/24
IP           MAC                VENDOR   HOSTNAME              RTT     DETAILS
192.0.2.1    00:00:5e:00:53:01  -        gateway.example      1.2ms
192.0.2.10   00:00:5e:00:53:02  -        nas.example          0.8ms
192.0.2.20   00:00:5e:00:53:04  -        -                    3.1ms   self (eth0)

$ jocasta scan 192.0.2.0/24 --save --rate 500 --no-resolve-names
```

The sweep uses a raw ICMP socket where it can and an unprivileged datagram
socket otherwise, so it runs without root.

## `ports`

Probes TCP ports with a plain `connect()`, so it needs no privileges and cannot
change the target. With no argument it scans every current address in the
inventory; give an address or a prefix to scan only that. `--ports` takes a
spec like `22,80,443,8000-8100`; the default is a curated preset of about a
hundred ports a homelab commonly runs. `--concurrency` caps how many
connections are open at once (64 by default). `--save` records what it finds
against the matching devices.

```
$ jocasta ports 192.0.2.10
ADDRESS      PORT   SERVICE
192.0.2.10   22     ssh
192.0.2.10   443    https

$ jocasta ports --save                      # every known address, recorded
$ jocasta ports 192.0.2.0/24 --ports 1-1024 --json
```

## `plugin run`

Reads one configured source without starting the server. Use it to check a
credential or a firewall rule against the real router. It reads the instance
even when it is disabled in config.

```
$ jocasta plugin run gateway
NETWORK           VLAN  NAME
192.0.2.0/24      -     Trusted
198.51.100.0/24   20    IoT

ADDRESS          MAC                VENDOR  HOSTNAME    STANDING     PRESENT
192.0.2.1        00:00:5e:00:53:01  -       gateway     DHCP_STATIC  true
198.51.100.10    00:00:5e:00:53:09  -       thermostat  DHCP_LEASE   true

$ jocasta plugin run gateway --json --save
```

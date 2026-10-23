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

To see the flags of a command, run it with `--help`.

## serve

```
jocasta serve                 # host and port from config
jocasta serve -p 9000         # override the port
```

Starts the HTTP server. It also starts these tasks:

- If `scan.devices.enabled` is set, a poller sweeps every network in `networks` at each `scan.devices.interval`.
- If `scan.ports.enabled` is set, a second poller port-scans every address that the inventory has on your networks, at each `scan.ports.interval`. It also port-scans each new device when a sweep or a router read finds it.
- At each `scan.devices.interval`, Jocasta reads each enabled `routeros` and `openwrt` instance for what is connected to it.
- Each enabled `netflow` instance starts a listener for the flow exports of its router.
- Each hour, a prune deletes records that are older than their `retention` window. It also deletes devices that you never edited and that no scan saw within `retention.devices`.

## scan

Runs one sweep and prints a table, or JSON with `--json`. With `--save`, it also records the sweep in the inventory. The source name comes from `--source`. If that is not set, it comes from `scan.source`, and then from the name of this host.

```
$ jocasta scan 192.0.2.0/24
IP           MAC                VENDOR   HOSTNAME              RTT     DETAILS
192.0.2.1    00:00:5e:00:53:01  -        gateway.example      1.2ms
192.0.2.10   00:00:5e:00:53:02  -        nas.example          0.8ms
192.0.2.20   00:00:5e:00:53:04  -        -                    3.1ms   self (eth0)

$ jocasta scan 192.0.2.0/24 --save --rate 500 --no-resolve-names
```

The sweep uses a raw ICMP socket if it can. If not, it uses an unprivileged datagram socket. So it runs without root. If a host has no reverse DNS name, the sweep asks for its name over mDNS, then NetBIOS, then DNS-SD, then SSDP. It stops when one of them gives a name. To turn each one off, use `--no-resolve-mdns`, `--no-resolve-netbios`, `--no-resolve-dnssd` or `--no-resolve-ssdp`. With `--json`, each host also lists the services that it advertises over DNS-SD.

## ports

Probes TCP ports with a plain `connect()`. So it needs no privileges and cannot change the target.

With no argument, it scans every current address that the inventory has on your networks. Your networks are those in `networks` and the segments that a router serves. It does not scan a neighbour outside them, such as the gateway of your ISP. It also scans each address on these TCP ports:

- the ports on which its device advertises a service
- the ports recorded as open on that device

To scan only one address or prefix, give it as the argument.

`--ports` takes a list such as `22,80,443,8000-8100`. By default, it scans a preset of about a hundred ports that homelabs often use. `--concurrency` sets the maximum number of open connections at one time (64 by default). `--save` records the results on the matching devices.

```
$ jocasta ports 192.0.2.10
ADDRESS      PORT   SERVICE
192.0.2.10   22     ssh
192.0.2.10   443    https

$ jocasta ports --save                      # every known address, recorded
$ jocasta ports 192.0.2.0/24 --ports 1-1024 --json
```

## plugin run

Reads one configured source without starting the server. Use it to test a credential or a firewall rule against the real router. It reads the instance even when the config disables it.

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

After the devices, it prints the topology of the source:

- its ports, and the VLANs on each port
- the hardware addresses that it learned on each port
- its neighbours

A `topology_only` instance prints only the topology. `--save` records both.

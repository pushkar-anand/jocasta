# Get told when a device goes quiet

Watch the devices whose absence you would want to hear about, such as a server,
a NAS or a camera. A watched device goes quiet (offline) when no scan has seen
it for longer than the online window, 15 minutes by default, and comes back
when one sees it again. Phones and laptops do both all day, so devices you do
not watch never log either.

1. On the device's page, select **Watch**.
2. In **Settings → Notifications**, tick **Watched device went quiet** and
   **Watched device came back** for a destination, then select **Save**.

A message reads "1 device went quiet on 192.0.2.0/24", with a line such as
"NAS went quiet after 18 minutes without an answer". The change log records
both kinds whether or not a destination is sent them. To list the watched
devices that are down now, filter the device list to **Watched only** and
**Quiet**.

Only a scan that could have seen a device decides it went quiet: a sweep of a
network it has an address on, or a read of a router that lists it. A device on
a network nothing scans stays as it was last seen. A router read that fails
part way decides nothing.

To change how long a device may go unseen, set the online window. It also
decides what the device list counts as seen recently:

```yaml
inventory:
  online_window: "30m"
```


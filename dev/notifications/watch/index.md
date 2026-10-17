# Get told when a device goes quiet

Watch the devices that you want to hear about when they go away, such as a server, a NAS or a camera. A watched device goes quiet when no scan has seen it for longer than the online window. By default, the online window is 15 minutes. The device comes back when a scan sees it again. Phones and laptops go quiet and come back all day. So a device that you do not watch never logs these events.

1. On the device's page, select **Watch**.
2. Open the account menu at the top right, and select **Notifications**.
3. For a destination, tick **Watched device went quiet** and **Watched device came back**.
4. Select **Save**.

A message has a title such as "1 device went quiet on 192.0.2.0/24". It has a line such as "NAS went quiet after 18 minutes without an answer". The change log records both kinds of event, even when no destination gets them. To list the watched devices that are quiet now, filter the device list to **Watched only** and **Quiet**.

Only a scan that could see a device can mark it quiet. That scan is a sweep of a network where the device has an address, or a read of a router that lists the device. If no scan covers the network of a device, the device stays as it was last seen. If a router read fails before it ends, it marks no device quiet.

To change how long a device can stay unseen, set the online window. The online window also sets which devices the device list counts as seen recently:

```
inventory:
  online_window: "30m"
```

# How names are chosen

More than one source can name a device. Jocasta shows the name from the source with the highest rank in this list. If you give a device a label, Jocasta shows the label instead of all of these names. See [Label, group and ignore devices](<https://pushkar-anand.github.io/jocasta/0.15/devices/index.md>).

1. Reverse DNS. Jocasta looks it up for each address that a sweep finds.
2. A static DHCP lease on your router.
3. mDNS. Jocasta asks each address that the sweep finds and that has no reverse DNS name.
4. A dynamic DHCP lease on your router.
5. NetBIOS. Jocasta asks each address that the sweep finds and that has no name from reverse DNS or mDNS.
6. DNS-SD. If an address still has no name, Jocasta uses the name that it gives the services it advertises.
7. SSDP. If an address still has no name, Jocasta uses the label in its UPnP description.

If two names of the same rank are different, the device keeps its current name while a source still gives that name. If no source gives it, the device takes the name that Jocasta heard last.

A device can have several addresses, such as a router with an address on each VLAN. Each address can give a name. The device keeps one of these names. It does not change to the name of the address that Jocasta read last. The device page lists the name that each source gives for each address.

When a device leaves an address, Jocasta stops using the names at that address. The device page stops listing them. The address stays in the history of the device.

Jocasta does not use the reverse DNS names that systemd-resolved makes up, such as `_gateway`.

When a sweep has a name for a device, a lookup with a lower rank does not replace it. The device keeps that name until a lookup with the same or a higher rank gives a different name.

## mDNS

mDNS names phones, TVs, printers and computers that reverse DNS does not know, such as `living-room-tv.local`. Jocasta sends one query to each such device, on UDP port 5353. This query does not need host networking or multicast. As the mDNS standard asks, most devices answer only a query from their own segment. So devices on other segments rarely get an mDNS name.

To turn mDNS off, set `scan.devices.resolve_mdns: false`. It does not depend on `scan.devices.resolve_names`. If reverse DNS is off, no device has a reverse DNS name. Then Jocasta asks every device that answers the sweep over mDNS.

## NetBIOS

NetBIOS names Windows computers and devices that run Samba, such as a NAS. A NetBIOS name, such as `DESKTOP-4F2K`, has at most 15 characters. Jocasta sends one query, on UDP port 137, to each device that still has no name. Windows answers only when file and printer sharing is on. By default, it answers only a query from its own segment. To turn NetBIOS off, set `scan.devices.resolve_netbios: false`.

## DNS-SD

DNS-SD names TVs, speakers, printers and other devices that advertise their services over mDNS but do not answer an mDNS query for their own name.

After each sweep, Jocasta asks the multicast group `224.0.0.251:5353` which service types are on the segment. Then it asks which devices offer each type. A Google Cast device gets the name that its owner gave it in the Home app, such as `Living Room TV`. Any other device gets the name that most of its services share. Jocasta does not use a name that is only a serial number or another ID.

Jocasta binds UDP port 5353 to hear the answers. It shares this port with any mDNS responder on the same machine, such as Avahi. Like SSDP, DNS-SD reaches only Jocasta's own segment, and only with host networking. To turn it off, set `scan.devices.resolve_dnssd: false`.

Jocasta also records the services that each device advertises, with or without a name. The Ports table on the device page lists the services that the latest sweep heard.

The port scan probes each advertised TCP port, so the table shows whether the port answers. The scan also probes every port that it recorded as open. So when a service moves to a new port, its old port closes. A port that the scan probes only because a device advertised it opens and closes without an event. This is because a phone advertises on a new random port each time.

The classifier reads every service that a sweep heard within `retention.history`. So if one sweep misses an answer, the device's type does not change. For example, a print service marks a printer, and the Android TV remote service marks a TV. If no sweep heard a service within `retention.history`, Jocasta deletes it.

## SSDP

SSDP names TVs, speakers, media players, printers and routers that no other source names. The name is the label that the device shows its owner, such as `Living Room TV`.

If a sweep leaves a device without a name, Jocasta sends one SSDP search to the multicast group `239.255.255.250:1900`. A device that answers points to its UPnP description. For each device that still has no name, Jocasta gets that description and takes the `friendlyName` from it. It gets the description only from the address that answered, over plain HTTP. It reads at most 64 KB and waits at most 2 seconds.

The search reaches only Jocasta's own segment, and only with host networking, because a bridge network does not pass multicast. To turn SSDP off, set `scan.devices.resolve_ssdp: false`.

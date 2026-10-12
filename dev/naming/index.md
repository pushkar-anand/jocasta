# Name devices

A device can be named by more than one source. The name shown is the one from the source ranked highest here:

1. Reverse DNS, looked up for each address a sweep finds.
2. A static DHCP lease on your router.
3. mDNS, asked of each address the sweep finds that has no reverse DNS name.
4. A dynamic DHCP lease on your router.
5. NetBIOS, asked of each address the sweep finds that has no name from reverse DNS or mDNS.
6. DNS-SD, the name each address still without a name gives the services it advertises.
7. SSDP, the label in the UPnP description of each address still without a name.

When two sources of the same rank disagree, the one heard from last wins. The device page lists the name each source gives. Once a sweep has a name for a device, a lower-ranked lookup does not replace it. The device keeps that name until the lookup that gave it, or a higher-ranked one, answers with another.

mDNS gives a name to phones, TVs, printers and computers that reverse DNS does not know, such as `living-room-tv.local`. Jocasta sends one query to each such device, on UDP port 5353, and needs no host networking or multicast for it. Most devices answer only a query from their own segment, as the mDNS standard asks, so devices on other segments rarely get an mDNS name.

To turn it off, set `scan.devices.resolve_mdns: false`. It does not depend on `scan.devices.resolve_names`: with reverse DNS off, no device has a reverse DNS name, so every device that answers the sweep is asked over mDNS.

NetBIOS gives a name to Windows computers and to devices running Samba, such as a NAS. A NetBIOS name, such as `DESKTOP-4F2K`, is 15 characters at most. Jocasta sends one query to each device still without a name, on UDP port 137. Windows answers only with file and printer sharing turned on, and by default only a query from its own segment. To turn it off, set `scan.devices.resolve_netbios: false`.

DNS-SD names TVs, speakers, printers and other devices that advertise their services over mDNS but do not answer an mDNS query for their own name. After each sweep, Jocasta asks the multicast group `224.0.0.251:5353` which service types are on the segment, then which devices offer each one. A Google Cast device is named after the name its owner gave it in the Home app, such as `Living Room TV`. Any other device is named after the name most of its services share, leaving out names that are only a serial number or other ID. Jocasta binds UDP port 5353 to hear the answers, sharing it with any mDNS responder on the same machine, such as Avahi. Like SSDP, it reaches only Jocasta's own segment, and only with host networking. To turn it off, set `scan.devices.resolve_dnssd: false`.

Jocasta also records the services each device advertises, named or not, and lists them in the device page's Ports table. The port scan probes each advertised TCP port, so the table says whether it answers. The scan also probes every port recorded open, so when a service moves to a new port, its old port closes. The classifier reads the services: a print service marks a printer, and the Android TV remote service a TV. A service no sweep has heard within `retention.history` drops off.

SSDP names TVs, speakers, media players, printers and routers that nothing else names. The name is the label the device shows its owner, such as `Living Room TV`. After a sweep that leaves a device without a name, Jocasta sends one SSDP search to the multicast group `239.255.255.250:1900`. For each device still without a name that answers, it fetches the UPnP description the answer points to and takes the `friendlyName` from it. It fetches only from the address that answered, over plain HTTP, at most 64 KB and for at most 2 seconds. The search reaches only Jocasta's own segment, and only with host networking, since a bridge network does not pass multicast. To turn it off, set `scan.devices.resolve_ssdp: false`.

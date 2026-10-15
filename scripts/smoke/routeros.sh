#!/bin/sh
# Boots RouterOS CHR in the smoke test's RouterOS container, in place of the
# image's own entrypoint.
#
# ether1 is QEMU's user network: RouterOS's default DHCP client takes an
# address on it, as on a WAN, and the container's port 80 is forwarded to
# it, which is how the REST API is reached before the router has any other
# address. ether2 is bridged onto the smoke network, where smoke.sh gives it
# ROUTEROS_IP. Booting straight from disk skips the network boot QEMU's
# firmware would otherwise try on each interface first.
set -e

# The container holds ROUTEROS_IP until it is handed to the router: find the
# interface it is on, and bridge that one.
lan=$(ip -o -4 addr show | awk -v ip="$ROUTEROS_IP" 'index($4, ip "/") == 1 { split($2, a, "@"); print a[1] }')

if [ -z "$lan" ]; then
	echo "no interface holds $ROUTEROS_IP" >&2
	exit 1
fi

ip addr flush dev "$lan"
ip link add br0 type bridge
ip link set "$lan" master br0
ip tuntap add tap0 mode tap
ip link set tap0 master br0
ip link set "$lan" up
ip link set tap0 up
ip link set br0 up

exec qemu-system-x86_64 -nographic -serial mon:stdio -m 256 -smp 1 -boot c \
	-drive "file=/routeros_source/$ROUTEROS_IMAGE,if=ide" \
	-nic user,hostfwd=tcp::80-:80 \
	-nic "tap,ifname=tap0,script=no,downscript=no,mac=$ROUTEROS_MAC"

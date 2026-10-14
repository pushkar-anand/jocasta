#!/usr/bin/env bash
# Smoke-test the OpenWrt, Pi-hole and AdGuard Home plugins against the real
# services in containers.
#
#   scripts/smoke/smoke.sh up      start the services and seed them with data
#   scripts/smoke/smoke.sh check   read each one with jocasta and check what it says
#   scripts/smoke/smoke.sh down    remove the containers and the network
#
# Every address, hardware address and name the services hold is a
# documentation value. The containers sit on SMOKE_SUBNET, and each web
# interface is published on 127.0.0.1, which jocasta reads.
set -euo pipefail

REGISTRY="${SMOKE_REGISTRY:-docker.io}"
PIHOLE_IMAGE="${SMOKE_PIHOLE_IMAGE:-$REGISTRY/pihole/pihole:2026.09.0}"
ADGUARD_IMAGE="${SMOKE_ADGUARD_IMAGE:-$REGISTRY/adguard/adguardhome:v0.107.79}"
OPENWRT_IMAGE="${SMOKE_OPENWRT_IMAGE:-$REGISTRY/openwrt/rootfs:x86-64-23.05.5}"

NET=jocasta-smoke
PREFIX="${SMOKE_PREFIX:-198.51.100}"
PIHOLE_IP="$PREFIX.10"
ADGUARD_IP="$PREFIX.11"
OPENWRT_IP="$PREFIX.12"

PIHOLE_PORT="${SMOKE_PIHOLE_PORT:-18081}"
ADGUARD_PORT="${SMOKE_ADGUARD_PORT:-18082}"
ADGUARD_SETUP_PORT="${SMOKE_ADGUARD_SETUP_PORT:-18083}"
OPENWRT_PORT="${SMOKE_OPENWRT_PORT:-18084}"

PIHOLE_PASSWORD=smoke-pihole
ADGUARD_USER=smoke
ADGUARD_PASSWORD=smoke-adguard
OPENWRT_USER=jocasta
OPENWRT_PASSWORD=smoke-openwrt

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="${SMOKE_DIR:-${TMPDIR:-/tmp}/jocasta-smoke}"

log() { printf '==> %s\n' "$*" >&2; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# hcurl calls a published port on this host, bypassing any proxy the shell
# routes outbound traffic through.
hcurl() { curl -sS -m 5 --noproxy '*' "$@"; }

# wait_for runs its arguments until they succeed, for up to a minute.
wait_for() {
	local what=$1
	shift

	for _ in $(seq 60); do
		if "$@" >/dev/null 2>&1; then
			return 0
		fi

		sleep 1
	done

	fail "$what did not come up within a minute"
}

up() {
	down 2>/dev/null || true

	log "creating network $NET on $PREFIX.0/24"
	docker network create --subnet "$PREFIX.0/24" "$NET" >/dev/null

	log "starting Pi-hole, AdGuard Home and OpenWrt"
	docker run -d --name jocasta-smoke-pihole --network "$NET" --ip "$PIHOLE_IP" \
		-p "127.0.0.1:$PIHOLE_PORT:80" \
		-e TZ=UTC \
		-e FTLCONF_webserver_api_password="$PIHOLE_PASSWORD" \
		-e FTLCONF_dns_listeningMode=all \
		"$PIHOLE_IMAGE" >/dev/null

	docker run -d --name jocasta-smoke-adguard --network "$NET" --ip "$ADGUARD_IP" \
		-p "127.0.0.1:$ADGUARD_PORT:80" -p "127.0.0.1:$ADGUARD_SETUP_PORT:3000" \
		"$ADGUARD_IMAGE" >/dev/null

	# procd has to be PID 1 for netifd, rpcd and uhttpd to start, and netifd
	# needs to manage the container's interface.
	docker run -d --name jocasta-smoke-openwrt --network "$NET" --ip "$OPENWRT_IP" \
		-p "127.0.0.1:$OPENWRT_PORT:80" \
		--cap-add NET_ADMIN \
		"$OPENWRT_IMAGE" /sbin/init >/dev/null

	seed_openwrt
	seed_pihole
	seed_adguard
	lookups

	log "ready; run '$0 check'"
}

seed_openwrt() {
	log "setting up OpenWrt as docs/plugins/openwrt.md describes"
	# First boot writes the network configuration and starts rpcd, which
	# serves the session object every login needs.
	wait_for "OpenWrt's first boot" docker exec jocasta-smoke-openwrt \
		sh -c 'uci -q get network.lan && ubus list session && ubus list network.interface.lan'

	docker cp "$ROOT/docs/openwrt/jocasta.json" jocasta-smoke-openwrt:/usr/share/rpcd/acl.d/jocasta.json

	docker exec -i jocasta-smoke-openwrt sh -s -- "$OPENWRT_USER" "$OPENWRT_PASSWORD" "$OPENWRT_IP" "$PREFIX" <<'EOF'
set -e
user=$1 password=$2 ip=$3 prefix=$4

uci add rpcd login >/dev/null
uci set rpcd.@login[-1].username="$user"
uci set rpcd.@login[-1].password="$(uhttpd -m "$password")"
uci add_list rpcd.@login[-1].read=jocasta
uci commit rpcd
/etc/init.d/rpcd reload

# netifd cannot create br-lan inside a container, so lan sits on eth0 at
# the address Docker gave it.
uci set network.lan.device=eth0
uci set network.lan.ipaddr="$ip"
uci commit network
/etc/init.d/network restart

uci add dhcp host >/dev/null
uci set dhcp.@host[-1].name=printer
uci set dhcp.@host[-1].mac=00:00:5e:00:53:04
uci set dhcp.@host[-1].ip="$prefix.60"
uci add dhcp host >/dev/null
uci set dhcp.@host[-1].name=laptop-static
uci set dhcp.@host[-1].mac=00:00:5e:00:53:01
uci set dhcp.@host[-1].ip="$prefix.50"
uci commit dhcp

expires=$(( $(date +%s) + 43200 ))
cat > /tmp/dhcp.leases <<LEASES
$expires 00:00:5e:00:53:01 $prefix.50 laptop-a 01:00:00:5e:00:53:01
$expires 00:00:5e:00:53:02 $prefix.51 phone-b *
$expires 00:00:5e:00:53:03 $prefix.52 * *
LEASES
EOF

	wait_for "OpenWrt's ubus endpoint" hcurl -f -d '{}' "http://127.0.0.1:$OPENWRT_PORT/ubus"
}

seed_pihole() {
	log "setting up Pi-hole"
	wait_for "Pi-hole's API" hcurl -f -d "{\"password\":\"$PIHOLE_PASSWORD\"}" "http://127.0.0.1:$PIHOLE_PORT/api/auth"
	docker exec jocasta-smoke-pihole pihole deny ads.example.com >/dev/null
}

seed_adguard() {
	log "setting up AdGuard Home"

	local setup="http://127.0.0.1:$ADGUARD_SETUP_PORT" web="http://127.0.0.1:$ADGUARD_PORT"
	local json=(-H 'Content-Type: application/json')

	wait_for "AdGuard Home's setup" hcurl -f "$setup/control/install/get_addresses"

	hcurl -f "${json[@]}" -d "{\"web\":{\"ip\":\"0.0.0.0\",\"port\":80},\"dns\":{\"ip\":\"0.0.0.0\",\"port\":53},\"username\":\"$ADGUARD_USER\",\"password\":\"$ADGUARD_PASSWORD\"}" \
		"$setup/control/install/configure" >/dev/null

	wait_for "AdGuard Home's API" hcurl -f -u "$ADGUARD_USER:$ADGUARD_PASSWORD" "$web/control/status"

	local auth=(-u "$ADGUARD_USER:$ADGUARD_PASSWORD")

	# Pi-hole answers as upstream, so no lookup leaves the network.
	hcurl -f "${auth[@]}" "${json[@]}" -d "{\"upstream_dns\":[\"$PIHOLE_IP\"]}" "$web/control/dns_config" >/dev/null
	hcurl -f "${auth[@]}" "${json[@]}" -d '{"rules":["||ads.example.com^"]}' "$web/control/filtering/set_rules" >/dev/null
	hcurl -f "${auth[@]}" "${json[@]}" -d '{"domain":"host-a.example.com","answer":"192.0.2.10"}' "$web/control/rewrite/add" >/dev/null
}

# lookups sends each name to both resolvers from the OpenWrt container. Its
# nslookup asks for A and AAAA, so each name is two lookups.
lookups() {
	log "sending lookups"

	for server in "$PIHOLE_IP" "$ADGUARD_IP"; do
		for name in ads.example.com host-a.example.com nosuchname.invalid; do
			docker exec jocasta-smoke-openwrt nslookup "$name" "$server" >/dev/null 2>&1 || true
		done
	done
}

write_config() {
	mkdir -p "$WORK"

	cat > "$WORK/jocasta.yaml" <<EOF
db:
  path: "$WORK"
plugins:
  openwrt:
    gateway:
      enabled: true
      host: "127.0.0.1"
      port: $OPENWRT_PORT
      user: "$OPENWRT_USER"
      password: "$OPENWRT_PASSWORD"
  pihole:
    resolver:
      enabled: true
      host: "127.0.0.1"
      port: $PIHOLE_PORT
      password: "$PIHOLE_PASSWORD"
  adguard:
    filter:
      enabled: true
      host: "127.0.0.1"
      port: $ADGUARD_PORT
      user: "$ADGUARD_USER"
      password: "$ADGUARD_PASSWORD"
EOF
}

# expect fails unless a line of out matches the extended regular expression.
expect() {
	local what=$1 out=$2 pattern=$3

	if ! grep -Eq "$pattern" <<<"$out"; then
		printf '%s\n' "$out" >&2
		fail "$what: no line matches /$pattern/"
	fi

	printf 'ok    %s\n' "$what"
}

check() {
	write_config

	log "building jocasta"
	(cd "$ROOT" && go build -o "$WORK/jocasta" ./cmd/jocasta)

	local bin=("$WORK/jocasta" -c "$WORK/jocasta.yaml" --log-level error plugin run)
	local out ip="${PREFIX//./\\.}"

	out=$("${bin[@]}" gateway)
	expect "openwrt: the static name outranks the lease's" "$out" \
		"^$ip\.50 +00:00:5e:00:53:01 .* laptop-static +DHCP_STATIC +true"
	expect "openwrt: a lease names its device" "$out" \
		"^$ip\.51 +00:00:5e:00:53:02 .* phone-b +DHCP_LEASE +true"
	expect "openwrt: a static host with no lease is not present" "$out" \
		"^$ip\.60 +00:00:5e:00:53:04 .* printer +DHCP_STATIC +false"
	expect "openwrt: the segment is named after its interface" "$out" \
		"^$ip\.0/24 +- +lan"

	out=$("${bin[@]}" resolver)
	expect "pihole: deny-listed lookups count as blocked" "$out" \
		"^$ip\.12 +ads\.example\.com +2 +2 +0"

	out=$("${bin[@]}" filter)
	expect "adguard: filtered lookups count as blocked" "$out" \
		"^$ip\.12 +ads\.example\.com +2 +2 +0"
	expect "adguard: a rewritten lookup neither blocks nor fails" "$out" \
		"^$ip\.12 +host-a\.example\.com +2 +0 +0"
	expect "adguard: a name that does not exist counts as failed" "$out" \
		"^$ip\.12 +nosuchname\.invalid +2 +0 +2"

	log "reading OpenWrt with the live test in pkg/openwrt"
	(cd "$ROOT" && \
		JOCASTA_PLUGINS__OPENWRT__GATEWAY__HOST=127.0.0.1 \
		JOCASTA_PLUGINS__OPENWRT__GATEWAY__PORT="$OPENWRT_PORT" \
		JOCASTA_PLUGINS__OPENWRT__GATEWAY__USER="$OPENWRT_USER" \
		JOCASTA_PLUGINS__OPENWRT__GATEWAY__PASSWORD="$OPENWRT_PASSWORD" \
		go test ./pkg/openwrt -run TestReadLive -count=1)

	log "every check passed"
}

down() {
	docker rm -f jocasta-smoke-pihole jocasta-smoke-adguard jocasta-smoke-openwrt >/dev/null 2>&1 || true
	docker network rm "$NET" >/dev/null 2>&1 || true
}

case "${1:-}" in
up) up ;;
check) check ;;
down) down ;;
*)
	echo "usage: $0 up|check|down" >&2
	exit 2
	;;
esac

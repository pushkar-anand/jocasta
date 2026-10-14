#!/usr/bin/env bash
# Smoke-test the OpenWrt, RouterOS, Pi-hole, AdGuard Home, Technitium and
# NetFlow plugins against the real services in containers.
#
#   scripts/smoke/smoke.sh up      start the services and seed them with data
#   scripts/smoke/smoke.sh check   read each one with jocasta and check what it says
#   scripts/smoke/smoke.sh down    remove the containers and the network
#
# Every address, hardware address and name the services hold is a
# documentation value. The containers sit on SMOKE_PREFIX.0/24, and each web
# interface is published on 127.0.0.1, which jocasta reads.
#
# SMOKE_SKIP leaves out any of "routeros", which runs RouterOS in QEMU,
# "technitium", whose setup needs internet from the container, and
# "netflow", which needs Docker on Linux.
set -euo pipefail

REGISTRY="${SMOKE_REGISTRY:-docker.io}"
PIHOLE_IMAGE="${SMOKE_PIHOLE_IMAGE:-$REGISTRY/pihole/pihole:2026.09.0}"
ADGUARD_IMAGE="${SMOKE_ADGUARD_IMAGE:-$REGISTRY/adguard/adguardhome:v0.107.79}"
OPENWRT_IMAGE="${SMOKE_OPENWRT_IMAGE:-$REGISTRY/openwrt/rootfs:x86-64-23.05.5}"
TECHNITIUM_IMAGE="${SMOKE_TECHNITIUM_IMAGE:-$REGISTRY/technitium/dns-server:15.6.1}"
PMACCT_IMAGE="${SMOKE_PMACCT_IMAGE:-$REGISTRY/pmacct/pmacctd:v1.7.9}"

# MikroTik publishes no container image. This one carries RouterOS CHR and
# QEMU; routeros.sh boots it.
ROUTEROS_IMAGE="${SMOKE_ROUTEROS_IMAGE:-$REGISTRY/evilfreelancer/docker-routeros:7.23.8}"

NET=jocasta-smoke
WAN_NET=jocasta-smoke-wan
PREFIX="${SMOKE_PREFIX:-198.51.100}"
GATEWAY_IP="$PREFIX.1"
PIHOLE_IP="$PREFIX.10"
ADGUARD_IP="$PREFIX.11"
OPENWRT_IP="$PREFIX.12"
TECHNITIUM_IP="$PREFIX.13"
ROUTEROS_IP="$PREFIX.14"

PIHOLE_PORT="${SMOKE_PIHOLE_PORT:-18081}"
ADGUARD_PORT="${SMOKE_ADGUARD_PORT:-18082}"
ADGUARD_SETUP_PORT="${SMOKE_ADGUARD_SETUP_PORT:-18083}"
OPENWRT_PORT="${SMOKE_OPENWRT_PORT:-18084}"
TECHNITIUM_PORT="${SMOKE_TECHNITIUM_PORT:-18085}"
ROUTEROS_PORT="${SMOKE_ROUTEROS_PORT:-18086}"
FLOW_PORT="${SMOKE_FLOW_PORT:-2055}"
ROUTEROS_FLOW_PORT="${SMOKE_ROUTEROS_FLOW_PORT:-2056}"

PIHOLE_PASSWORD=smoke-pihole
ADGUARD_USER=smoke
ADGUARD_PASSWORD=smoke-adguard
OPENWRT_USER=jocasta
OPENWRT_PASSWORD=smoke-openwrt
TECHNITIUM_ADMIN_PASSWORD=smoke-technitium-admin
TECHNITIUM_USER=jocasta
TECHNITIUM_PASSWORD=smoke-technitium
ROUTEROS_USER=jocasta
ROUTEROS_PASSWORD=smoke-routeros

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="${SMOKE_DIR:-${TMPDIR:-/tmp}/jocasta-smoke}"
CONTAINERS=(jocasta-smoke-flows jocasta-smoke-pihole jocasta-smoke-adguard jocasta-smoke-technitium jocasta-smoke-routeros jocasta-smoke-openwrt)

log() { printf '==> %s\n' "$*" >&2; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

# skipped reports whether SMOKE_SKIP names the service.
skipped() { [[ " ${SMOKE_SKIP:-} " == *" $1 "* ]]; }

# hcurl calls a published port on this host, bypassing any proxy the shell
# routes outbound traffic through.
hcurl() { curl -sS -m 5 --noproxy '*' "$@"; }

# json_field prints the first string value of a key in a flat JSON object.
json_field() { sed -n "s/.*\"$1\":\"\([^\"]*\)\".*/\1/p" | head -n 1; }

# wait_for runs its arguments until they succeed, for up to WAIT_SECONDS,
# a minute unless set.
wait_for() {
	local what=$1
	shift

	for _ in $(seq "${WAIT_SECONDS:-60}"); do
		if "$@" >/dev/null 2>&1; then
			return 0
		fi

		sleep 1
	done

	fail "$what did not come up within ${WAIT_SECONDS:-60} seconds"
}

up() {
	down
	mkdir -p "$WORK"

	# NetFlow reaches this host at the network's gateway address, which only
	# Linux puts on the host.
	if ! skipped netflow && [[ "$(uname -s)" != Linux ]]; then
		fail "NetFlow needs Docker on Linux; set SMOKE_SKIP=netflow to leave it out"
	fi

	log "creating network $NET on $PREFIX.0/24"
	docker network create --subnet "$PREFIX.0/24" --gateway "$GATEWAY_IP" "$NET" >/dev/null

	log "starting the services"

	# procd has to be PID 1 for netifd, rpcd and uhttpd to start, and netifd
	# needs to manage the container's interface.
	docker run -d --name jocasta-smoke-openwrt --network "$NET" --ip "$OPENWRT_IP" \
		-p "127.0.0.1:$OPENWRT_PORT:80" \
		--cap-add NET_ADMIN \
		"$OPENWRT_IMAGE" /sbin/init >/dev/null

	docker run -d --name jocasta-smoke-pihole --network "$NET" --ip "$PIHOLE_IP" \
		-p "127.0.0.1:$PIHOLE_PORT:80" \
		-e TZ=UTC \
		-e FTLCONF_webserver_api_password="$PIHOLE_PASSWORD" \
		-e FTLCONF_dns_listeningMode=all \
		"$PIHOLE_IMAGE" >/dev/null

	docker run -d --name jocasta-smoke-adguard --network "$NET" --ip "$ADGUARD_IP" \
		-p "127.0.0.1:$ADGUARD_PORT:80" -p "127.0.0.1:$ADGUARD_SETUP_PORT:3000" \
		"$ADGUARD_IMAGE" >/dev/null

	if ! skipped technitium; then
		docker run -d --name jocasta-smoke-technitium --network "$NET" --ip "$TECHNITIUM_IP" \
			-p "127.0.0.1:$TECHNITIUM_PORT:5380" \
			-e DNS_SERVER_ADMIN_PASSWORD="$TECHNITIUM_ADMIN_PASSWORD" \
			"$TECHNITIUM_IMAGE" >/dev/null
	fi

	if ! skipped routeros; then
		# The published port has to reach QEMU's forward on the WAN network,
		# so that network carries the default route: Docker publishes on it.
		# The smoke network's address moves to the router once it boots.
		docker network create "$WAN_NET" >/dev/null
		docker create --name jocasta-smoke-routeros \
			--network "name=$WAN_NET,gw-priority=1" \
			-p "127.0.0.1:$ROUTEROS_PORT:80" \
			--cap-add NET_ADMIN --device /dev/net/tun \
			-e ROUTEROS_IP="$ROUTEROS_IP" -e ROUTEROS_MAC=00:00:5e:00:53:20 \
			-v "$ROOT/scripts/smoke/routeros.sh:/routeros.sh:ro" \
			--entrypoint sh \
			"$ROUTEROS_IMAGE" /routeros.sh >/dev/null
		docker network connect --ip "$ROUTEROS_IP" "$NET" jocasta-smoke-routeros
		docker start jocasta-smoke-routeros >/dev/null
	fi

	seed_openwrt
	seed_pihole
	seed_adguard
	skipped technitium || seed_technitium
	skipped routeros || seed_routeros
	skipped netflow || start_exporter
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

seed_technitium() {
	log "setting up Technitium as docs/plugins/technitium.md describes"

	local api="http://127.0.0.1:$TECHNITIUM_PORT/api" admin

	wait_for "Technitium's API" hcurl -f "$api/user/login?user=admin&pass=$TECHNITIUM_ADMIN_PASSWORD"
	admin=$(hcurl "$api/user/login?user=admin&pass=$TECHNITIUM_ADMIN_PASSWORD" | json_field token)

	# Technitium keeps a query log only through an app. The server downloads
	# it from Technitium's app store, so this step needs the container to
	# reach the internet.
	local url
	url=$(hcurl -m 60 "$api/apps/listStoreApps?token=$admin" | tr '{}' '\n\n' |
		grep '"name":"Query Logs (Sqlite)"' | json_field url || true)

	if [[ -z "$url" ]]; then
		fail "Technitium could not list its app store, which needs internet from the container; set SMOKE_SKIP=technitium to leave it out"
	fi

	hcurl -f -m 120 -G "$api/apps/downloadAndInstall" --data-urlencode "token=$admin" \
		--data-urlencode "name=Query Logs (Sqlite)" --data-urlencode "url=$url" | grep -q '"status":"ok"' ||
		fail "Technitium could not install Query Logs (Sqlite) from $url"

	# Lookups from a documentation range are not from a private network,
	# which Technitium recurses for by default. Pi-hole answers as upstream.
	hcurl -f "$api/settings/set?token=$admin&recursion=Allow&forwarders=$PIHOLE_IP&forwarderProtocol=Udp" >/dev/null
	hcurl -f "$api/blocked/add?token=$admin&domain=ads.example.com" >/dev/null

	hcurl -f "$api/admin/users/create?token=$admin&user=$TECHNITIUM_USER&pass=$TECHNITIUM_PASSWORD" >/dev/null
	hcurl -f "$api/admin/permissions/set?token=$admin&section=Logs&userPermissions=$TECHNITIUM_USER|true|false|false" >/dev/null

	hcurl -f "$api/user/createToken?user=$TECHNITIUM_USER&pass=$TECHNITIUM_PASSWORD&tokenName=jocasta" |
		json_field token > "$WORK/technitium.token"
}

seed_routeros() {
	log "setting up RouterOS"

	local api="http://127.0.0.1:$ROUTEROS_PORT/rest"

	# CHR starts with an admin login and no password.
	ros() {
		local method=$1 path=$2
		shift 2
		hcurl -f -u admin: -X "$method" -H 'Content-Type: application/json' "$api$path" "$@" >/dev/null
	}

	WAIT_SECONDS=300 wait_for "RouterOS's REST API" hcurl -f -u admin: "$api/system/resource"

	ros PUT /ip/address -d "{\"address\":\"$ROUTEROS_IP/24\",\"interface\":\"ether2\"}"
	ros PUT /user -d "{\"name\":\"$ROUTEROS_USER\",\"group\":\"read\",\"password\":\"$ROUTEROS_PASSWORD\"}"

	# ether1 takes its address over DHCP, which is not a segment the router
	# serves; ether2 is one. So is the bridge, with a DHCP server holding a
	# static lease, and a static ARP entry.
	ros PUT /interface/bridge -d '{"name":"bridge-lan"}'
	ros PUT /ip/address -d '{"address":"203.0.113.1/24","interface":"bridge-lan"}'
	ros PUT /ip/pool -d '{"name":"lan","ranges":"203.0.113.100-203.0.113.200"}'
	ros PUT /ip/dhcp-server -d '{"name":"lan","interface":"bridge-lan","address-pool":"lan"}'
	ros PUT /ip/dhcp-server/lease -d '{"address":"203.0.113.50","mac-address":"00:00:5E:00:53:01","server":"lan","comment":"laptop - wired"}'
	ros PUT /ip/arp -d '{"address":"203.0.113.60","mac-address":"00:00:5E:00:53:04","interface":"bridge-lan"}'

	# A ping puts the OpenWrt container in the ARP table. The first can go
	# unanswered while ether2's address settles, so it is retried.
	wait_for "RouterOS's ping to OpenWrt" sh -c "curl -sS -m 10 --noproxy '*' -u admin: -H 'Content-Type: application/json' \
		-d '{\"address\":\"$OPENWRT_IP\",\"count\":\"1\"}' '$api/ping' | grep -q '\"received\":\"1\"'"

	if ! skipped netflow; then
		# As docs/plugins/routeros.md sets it up, with short timeouts so a
		# flow is exported seconds after it ends. RouterOS sends its template
		# every 20 packets by default, and the first went out before anything
		# listened, so it is sent with every packet here.
		ros POST /ip/traffic-flow/set -d '{"enabled":"yes","interfaces":"all","active-flow-timeout":"10s","inactive-flow-timeout":"5s"}'
		ros PUT /ip/traffic-flow/target -d "{\"dst-address\":\"$GATEWAY_IP\",\"port\":\"$ROUTEROS_FLOW_PORT\",\"version\":\"ipfix\",\"v9-template-refresh\":\"1\"}"
	fi
}

# start_exporter runs pmacct in the OpenWrt container's network namespace,
# exporting NetFlow v9 to this host as the router would. The timeouts are
# short so a flow is exported seconds after it ends.
start_exporter() {
	log "starting the NetFlow exporter"

	cat > "$WORK/pmacctd.conf" <<EOF
daemonize: false
pcap_interface: eth0
aggregate: src_host, dst_host, src_port, dst_port, proto, tcpflags
plugins: nfprobe
nfprobe_receiver: $GATEWAY_IP:$FLOW_PORT
nfprobe_version: 9
nfprobe_timeouts: general=5:udp=5:tcp=5:icmp=5:maxlife=10:expint=5
EOF

	docker run -d --name jocasta-smoke-flows --network container:jocasta-smoke-openwrt \
		--cap-add NET_RAW --cap-add NET_ADMIN \
		-v "$WORK/pmacctd.conf:/etc/pmacct/pmacctd.conf:ro" \
		"$PMACCT_IMAGE" >/dev/null
}

# lookups sends each name to every resolver from the OpenWrt container. Its
# nslookup asks for A and AAAA, so each name is two lookups.
lookups() {
	log "sending lookups"

	local servers=("$PIHOLE_IP" "$ADGUARD_IP")
	skipped technitium || servers+=("$TECHNITIUM_IP")

	for server in "${servers[@]}"; do
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

	# A skipped service is left out: its instance would fail validation, and
	# with it every read.
	skipped routeros || cat >> "$WORK/jocasta.yaml" <<EOF
  routeros:
    core:
      enabled: true
      host: "127.0.0.1"
      port: $ROUTEROS_PORT
      user: "$ROUTEROS_USER"
      password: "$ROUTEROS_PASSWORD"
EOF

	skipped technitium || cat >> "$WORK/jocasta.yaml" <<EOF
  technitium:
    recursor:
      enabled: true
      host: "127.0.0.1"
      port: $TECHNITIUM_PORT
      token: "$(cat "$WORK/technitium.token")"
EOF

	skipped netflow || cat >> "$WORK/jocasta.yaml" <<EOF
  netflow:
    gateway:
      enabled: true
      listen: "$GATEWAY_IP:$FLOW_PORT"
      exporters:
        - "$OPENWRT_IP"
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

	if ! skipped technitium; then
		# The app writes its log in batches, a few seconds behind the lookups.
		for _ in $(seq 30); do
			out=$("${bin[@]}" recursor 2>&1 || true)
			grep -Eq "^$ip\.12 +nosuchname\.invalid" <<<"$out" && break
			sleep 1
		done

		expect "technitium: blocked lookups count as blocked" "$out" \
			"^$ip\.12 +ads\.example\.com +2 +2 +0"
		expect "technitium: a name that does not exist counts as failed" "$out" \
			"^$ip\.12 +nosuchname\.invalid +2 +0 +2"
	fi

	if ! skipped routeros; then
		check_routeros
	fi

	if ! skipped netflow; then
		check_netflow
	fi

	log "reading OpenWrt with the live test in pkg/openwrt"
	(cd "$ROOT" && \
		JOCASTA_PLUGINS__OPENWRT__GATEWAY__HOST=127.0.0.1 \
		JOCASTA_PLUGINS__OPENWRT__GATEWAY__PORT="$OPENWRT_PORT" \
		JOCASTA_PLUGINS__OPENWRT__GATEWAY__USER="$OPENWRT_USER" \
		JOCASTA_PLUGINS__OPENWRT__GATEWAY__PASSWORD="$OPENWRT_PASSWORD" \
		go test ./pkg/openwrt -run TestReadLive -count=1)

	log "every check passed"
}

check_routeros() {
	local out ip="${PREFIX//./\\.}"

	out=$("$WORK/jocasta" -c "$WORK/jocasta.yaml" --log-level error plugin run core)
	expect "routeros: the router's own address on the bridge is present" "$out" \
		"^203\.0\.113\.1 +([0-9a-f]{2}:){5}[0-9a-f]{2} .* true$"
	expect "routeros: the router's own address on ether2 is present" "$out" \
		"^$ip\.14 +00:00:5e:00:53:20 .* true$"
	expect "routeros: a static lease names its hardware address" "$out" \
		"^203\.0\.113\.50 +00:00:5e:00:53:01 .* false$"
	expect "routeros: a static ARP entry names its hardware address" "$out" \
		"^203\.0\.113\.60 +00:00:5e:00:53:04 .* false$"
	expect "routeros: a neighbour is learned from the ARP table" "$out" \
		"^$ip\.12 +([0-9a-f]{2}:){5}[0-9a-f]{2} "
	expect "routeros: the bridge is a segment" "$out" \
		"^203\.0\.113\.0/24 +- +bridge-lan"
	expect "routeros: ether2 is a segment" "$out" \
		"^$ip\.0/24 +- +ether2"

	if grep -Eq "^10\.0\.2\.0/24 " <<<"$out"; then
		printf '%s\n' "$out" >&2
		fail "routeros: the DHCP uplink's segment is listed"
	fi

	printf 'ok    %s\n' "routeros: the DHCP uplink is not a segment"

	log "reading RouterOS with the live tests in pkg/routeros and internal/plugin"
	(cd "$ROOT" && \
		JOCASTA_PLUGINS__ROUTEROS__HOST=127.0.0.1 \
		JOCASTA_PLUGINS__ROUTEROS__PORT="$ROUTEROS_PORT" \
		JOCASTA_PLUGINS__ROUTEROS__USER="$ROUTEROS_USER" \
		JOCASTA_PLUGINS__ROUTEROS__PASSWORD="$ROUTEROS_PASSWORD" \
		go test ./pkg/routeros ./internal/plugin -run 'TestReadLive|TestDiscoverLive' -count=1)
}

# check_netflow listens with the live test in internal/plugin while the
# OpenWrt container makes a lookup, which pmacct exports as two flows.
check_netflow() {
	log "listening for NetFlow from the exporter"

	(cd "$ROOT" && go test -c -o "$WORK/plugin.test" ./internal/plugin)

	local server="$PIHOLE_IP"

	# pmacct resends its template only every so many packets, and data it
	# sends before the listener has one cannot be decoded. Restarting it once
	# the listener is up makes its first packet carry the template.
	(
		sleep 2
		docker restart jocasta-smoke-flows >/dev/null
		sleep 2
		docker exec jocasta-smoke-openwrt nslookup flow.example.com "$server" >/dev/null 2>&1 || true
	) &

	local out
	out=$(JOCASTA_PLUGINS__NETFLOW__GATEWAY__LISTEN="$GATEWAY_IP:$FLOW_PORT" \
		JOCASTA_PLUGINS__NETFLOW__GATEWAY__EXPORTERS="$OPENWRT_IP" \
		JOCASTA_NETFLOW_LIVE_WAIT=60s \
		"$WORK/plugin.test" -test.run TestNetFlowLive -test.v 2>&1) || {
		printf '%s\n' "$out" >&2
		fail "netflow: no flow arrived from the exporter"
	}

	local server_re="${server//./\\.}"

	expect "netflow: a lookup is decoded as a flow to the resolver" "$out" \
		"flow $ip\.12:[0-9]+ -> $server_re:53 proto=17 .* exporter=$ip\.12"
	expect "netflow: its answer is decoded as a flow back" "$out" \
		"flow $server_re:53 -> $ip\.12:[0-9]+ proto=17 .* exporter=$ip\.12"

	if skipped routeros; then
		return
	fi

	log "listening for IPFIX from RouterOS"

	# A request from this host to the router is a flow RouterOS exports. The
	# first batch can instead hold the REST reads above, which reached it on
	# ether1, so any flow it exports will do.
	(
		sleep 2
		hcurl -o /dev/null "http://$ROUTEROS_IP/" || true
	) &

	out=$(JOCASTA_PLUGINS__NETFLOW__GATEWAY__LISTEN="$GATEWAY_IP:$ROUTEROS_FLOW_PORT" \
		JOCASTA_PLUGINS__NETFLOW__GATEWAY__EXPORTERS="$ROUTEROS_IP" \
		JOCASTA_NETFLOW_LIVE_WAIT=60s \
		"$WORK/plugin.test" -test.run TestNetFlowLive -test.v 2>&1) || {
		printf '%s\n' "$out" >&2
		fail "routeros: no IPFIX flow arrived from Traffic Flow"
	}

	expect "routeros: a flow it exported is decoded from IPFIX" "$out" \
		"flow [0-9.]+:[0-9]+ -> [0-9.]+:[0-9]+ proto=[0-9]+ .* exporter=$ip\.14"
}

down() {
	docker rm -f "${CONTAINERS[@]}" >/dev/null 2>&1 || true
	docker network rm "$NET" "$WAN_NET" >/dev/null 2>&1 || true
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

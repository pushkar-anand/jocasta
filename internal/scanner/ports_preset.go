package scanner

import (
	"maps"
	"slices"
)

// presetServices is the default set of the port scan. It has the TCP ports on
// which a homelab really runs something, each with its usual service. It is
// chosen by hand. So it stays easy to check in review. Also, a scan of the
// whole inventory is about a hundred connects for each host. This is a tenth
// of the top 1000 of nmap.
//
// The map is the whole list. presetPorts is its keys, sorted. So the set and
// the names cannot become different. A name is a guess for display. A service
// on a non-standard port gets the wrong label. So the scan never fingerprints
// to confirm a name.
var presetServices = map[uint16]string{
	21:    "ftp",
	22:    "ssh",
	23:    "telnet",
	25:    "smtp",
	53:    "dns",
	80:    "http",
	81:    "http-alt",
	88:    "kerberos",
	110:   "pop3",
	111:   "rpcbind",
	135:   "msrpc",
	139:   "netbios-ssn",
	143:   "imap",
	389:   "ldap",
	443:   "https",
	445:   "smb",
	465:   "smtps",
	500:   "isakmp",
	515:   "printer",
	548:   "afp",
	554:   "rtsp",
	587:   "submission",
	631:   "ipp",
	636:   "ldaps",
	873:   "rsync",
	902:   "vmware",
	990:   "ftps",
	993:   "imaps",
	995:   "pop3s",
	1080:  "socks",
	1194:  "openvpn",
	1433:  "mssql",
	1521:  "oracle",
	1723:  "pptp",
	1883:  "mqtt",
	2049:  "nfs",
	2181:  "zookeeper",
	2222:  "ssh",
	2375:  "docker",
	2376:  "docker-tls",
	2379:  "etcd",
	2380:  "etcd-peer",
	3000:  "grafana",
	3128:  "squid",
	3260:  "iscsi",
	3306:  "mysql",
	3389:  "rdp",
	4444:  "krb524",
	5000:  "upnp",
	5001:  "synology",
	5060:  "sip",
	5201:  "iperf3",
	5222:  "xmpp",
	5353:  "mdns",
	5432:  "postgresql",
	5555:  "adb",
	5601:  "kibana",
	5672:  "amqp",
	5900:  "vnc",
	5901:  "vnc",
	5984:  "couchdb",
	6000:  "x11",
	6379:  "redis",
	6443:  "kubernetes-api",
	6789:  "unifi",
	7000:  "airplay",
	7878:  "radarr",
	8006:  "proxmox",
	8008:  "chromecast",
	8009:  "chromecast-tls",
	8080:  "http-proxy",
	8081:  "http-alt",
	8086:  "influxdb",
	8096:  "jellyfin",
	8123:  "home-assistant",
	8200:  "vault",
	8443:  "https-alt",
	8500:  "consul",
	8581:  "homebridge",
	8883:  "mqtts",
	8888:  "http-alt",
	8989:  "sonarr",
	9000:  "portainer",
	9090:  "prometheus",
	9091:  "transmission",
	9092:  "kafka",
	9093:  "alertmanager",
	9100:  "node-exporter",
	9200:  "elasticsearch",
	9300:  "elasticsearch",
	9443:  "portainer-https",
	10000: "webmin",
	11211: "memcached",
	15672: "rabbitmq-management",
	19999: "netdata",
	25565: "minecraft",
	27017: "mongodb",
	32400: "plex",
	51820: "wireguard",
	64738: "mumble",
}

// presetPorts is the keys of presetServices in ascending order. It is resolved
// once, so the scanner can use it without a normalising pass.
var presetPorts = slices.Sorted(maps.Keys(presetServices))

// ServiceName returns the usual service on a port. It returns the empty string
// for every port outside the preset. The name is a guess for display. No
// probe confirms it.
func ServiceName(port uint16) string {
	return presetServices[port]
}

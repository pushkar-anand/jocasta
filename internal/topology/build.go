package topology

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/pushkar-anand/jocasta/internal/classify"
)

// Build assembles the tree from what sources say and places devices in it.
//
// A device goes to the deepest node that saw it on a port leading to no other
// node, from the latest reads where there are any and the last sighting
// otherwise. Devices no source has seen are left out, except online ones,
// which are listed in [Tree.Unplaced]. A device whose hardware address no
// source reported, or that is itself a node, is not a leaf.
func Build(sources []Source, devices []Device) *Tree {
	b := newBuilder(sources, devices)

	if root := b.pickRoot(); root != nil {
		b.tree.Root = root.node
		b.depth[root.node] = 0

		b.findLinks()
		b.place(root, "", b.others(root))
		b.placeStrangers()
		b.attach()
	}

	b.finish()

	return b.tree
}

// reader is one source while the tree is assembled.
type reader struct {
	src  *Source
	node *Node

	ports map[string]Port

	// toward[b] is the port of this source that b is behind, as its tables
	// say.
	toward map[*reader]string

	// up is the port the source hangs from. Everything it learns there is
	// above it or on another branch, so a sighting on it places nothing.
	up string

	// links is the ports leading to another read source, and onPort the node
	// on a port: a read source, an announced neighbour, an inferred node or a
	// hypervisor.
	links  map[string]bool
	onPort map[string]*Node
}

// current reports whether s came from the source's latest read.
func (r *reader) current(s Sighting) bool {
	return !s.LastSeen.Before(r.src.ReadAt)
}

type builder struct {
	tree *Tree

	readers    []*reader
	byOwn      map[string]*reader
	byIdentity map[string]*reader
	devByMAC   map[string]*Device

	root  *reader
	depth map[*Node]int

	// seen is the nodes made from neighbours no source reads, by key, and
	// nodeMACs every hardware address that is a node rather than a leaf.
	seen     map[string]*Node
	nodeMACs map[string]bool
}

// newBuilder prepares source and device identities for tree construction.
// Sources sharing a hardware address are resolved in source-name order.
func newBuilder(sources []Source, devices []Device) *builder {
	b := &builder{
		tree: &Tree{
			nodeByDevice: map[int64]*Node{},
			leafByDevice: map[int64]*Leaf{},
		},
		byOwn:      map[string]*reader{},
		byIdentity: map[string]*reader{},
		devByMAC:   map[string]*Device{},
		depth:      map[*Node]int{},
		seen:       map[string]*Node{},
		nodeMACs:   map[string]bool{},
	}

	for i := range devices {
		if devices[i].MAC != "" {
			b.devByMAC[devices[i].MAC] = &devices[i]
		}
	}

	sorted := slices.Clone(sources)
	slices.SortFunc(sorted, func(a, c Source) int { return strings.Compare(a.Name, c.Name) })

	for i := range sorted {
		src := &sorted[i]

		r := &reader{
			src:    src,
			node:   &Node{Kind: NodeRead, Key: fmt.Sprintf("s%d", src.ID), Name: cmp.Or(src.Identity, src.Name), Online: true},
			ports:  map[string]Port{},
			toward: map[*reader]string{},
			links:  map[string]bool{},
			onPort: map[string]*Node{},
		}

		for _, p := range src.Ports {
			r.ports[p.Name] = p
		}

		for _, mac := range src.Own {
			if _, taken := b.byOwn[mac]; !taken {
				b.byOwn[mac] = r
			}

			b.claim(r.node, mac)
		}

		if src.Identity != "" {
			b.byIdentity[src.Identity] = r
		}

		b.readers = append(b.readers, r)
	}

	return b
}

// claim marks mac as a node's rather than a leaf's, and makes n the inventory
// device with that address when n is none yet.
func (b *builder) claim(n *Node, mac string) {
	if mac == "" {
		return
	}

	b.nodeMACs[mac] = true

	if d, ok := b.devByMAC[mac]; ok && n.DeviceID == 0 {
		n.DeviceID = d.ID
		n.Online = d.Online
	}
}

// pickRoot returns the gateway, the first in name order when several say they
// are, or the first source when none does.
func (b *builder) pickRoot() *reader {
	for _, r := range b.readers {
		if r.src.Gateway {
			b.root = r

			return r
		}
	}

	if len(b.readers) > 0 {
		b.root = b.readers[0]
	}

	return b.root
}

func (b *builder) others(root *reader) []*reader {
	var out []*reader

	for _, r := range b.readers {
		if r != root {
			out = append(out, r)
		}
	}

	return out
}

// match returns the read source a neighbour is, or nil.
func (b *builder) match(n Neighbour) *reader {
	if r, ok := b.byOwn[n.MAC]; ok {
		return r
	}

	return b.byIdentity[n.Identity]
}

// findLinks fills toward from each source's current sightings and neighbours.
func (b *builder) findLinks() {
	for _, a := range b.readers {
		for _, s := range a.src.Seen {
			if other, ok := b.byOwn[s.MAC]; ok && other != a && a.current(s) {
				b.setToward(a, other, s.Port)
			}
		}

		for _, n := range a.src.Neighbours {
			other := b.match(n)
			if other == nil || other == a {
				continue
			}

			b.setToward(a, other, n.Port)

			other.node.Board = cmp.Or(other.node.Board, n.Board)
		}
	}
}

// setToward records that other is behind port of a. A wired port or radio
// replaces a VLAN or bridge interface, since the port is where it plugs in.
func (b *builder) setToward(a, other *reader, port string) {
	have := a.toward[other]
	if have == "" || (a.ports[have].Kind == PortVirtual && a.ports[port].Kind != PortVirtual) {
		a.toward[other] = port
	}
}

// upPort returns the port of z that x is behind, or failing that the root.
func (b *builder) upPort(z, x *reader) string {
	return cmp.Or(z.toward[x], z.toward[b.root])
}

// below reports whether z sees y on a port other than the one leading back
// towards x, which puts y in z's subtree.
func (b *builder) below(z, y, x *reader) bool {
	p := z.toward[y]

	return p != "" && p != b.upPort(z, x)
}

// place hangs the sources in set from x, x having been reached through its
// port up. Each source in set ends up in the tree exactly once.
func (b *builder) place(x *reader, up string, set []*reader) {
	// The sources x sees behind one port are all in that branch. The ones no
	// other source in the branch has below it hang directly from the port;
	// the rest go below whichever of those has them below it. More than one
	// hanging directly from a port means something between them the tree
	// does not read.
	byPort := map[string][]*reader{}

	var unseen []*reader

	for _, y := range set {
		p := x.toward[y]
		if p == "" || p == up {
			unseen = append(unseen, y)

			continue
		}

		byPort[p] = append(byPort[p], y)
	}

	for _, p := range sortedKeys(byPort) {
		branch := byPort[p]
		x.links[p] = true

		var direct []*reader

		for _, y := range branch {
			if !slices.ContainsFunc(branch, func(z *reader) bool { return z != y && b.below(z, y, x) }) {
				direct = append(direct, y)
			}
		}

		// Tables that contradict each other put everything below something
		// else; hang the branch here rather than lose it.
		if len(direct) == 0 {
			direct = branch
		}

		parent, parentPort := x.node, p
		if len(direct) > 1 {
			parent, parentPort = b.intermediate(x, p, false), ""
		}

		taken := map[*reader]bool{}
		for _, c := range direct {
			taken[c] = true
		}

		for _, c := range direct {
			var sub []*reader

			for _, z := range branch {
				if !taken[z] && b.below(c, z, x) {
					sub = append(sub, z)
					taken[z] = true
				}
			}

			b.hangReader(c, parent, x, parentPort)
			b.place(c, c.toward[x], sub)
		}

		for _, z := range branch {
			if !taken[z] {
				b.hangReader(z, parent, x, parentPort)
				b.place(z, z.toward[x], nil)
			}
		}
	}

	for _, y := range unseen {
		b.hangReader(y, x.node, x, "")
		b.place(y, y.toward[x], nil)
	}
}

// hangReader hangs c from parent, which is x's node or a node on x's port.
func (b *builder) hangReader(c *reader, parent *Node, x *reader, parentPort string) {
	up := c.toward[x]
	c.up = up

	link := &Link{ParentPort: parentPort, Port: up}

	switch {
	case parentPort != "":
		link.VLANs, link.Trunk = carried(x.ports[parentPort])
		link.Speed = x.ports[parentPort].Speed
		x.onPort[parentPort] = c.node
	case up != "":
		link.VLANs, link.Trunk = carried(c.ports[up])
		link.Speed = c.ports[up].Speed
	}

	b.hang(c.node, parent, link)
}

// hang adds child below parent.
func (b *builder) hang(child, parent *Node, link *Link) {
	child.Parent = parent
	child.Uplink = link
	parent.Children = append(parent.Children, child)
	b.depth[child] = b.depth[parent] + 1
}

// carried returns the VLANs a port carries, and whether any are tagged.
func carried(p Port) (vlans []int, trunk bool) {
	vlans = append(slices.Clone(p.Tagged), p.Untagged...)
	if len(vlans) == 0 && p.PVID > 0 {
		vlans = []int{p.PVID}
	}

	slices.Sort(vlans)

	return slices.Compact(vlans), len(p.Tagged) > 0
}

// strangers returns the neighbours on port p of x that no source reads.
func (b *builder) strangers(x *reader, p string) []Neighbour {
	var out []Neighbour

	for _, n := range x.src.Neighbours {
		if n.Port == p && b.match(n) == nil {
			out = append(out, n)
		}
	}

	return out
}

// intermediate returns the node on port p of x, making it on first use: the
// neighbour that announced itself there when exactly one did, and an unnamed
// node otherwise.
func (b *builder) intermediate(x *reader, p string, wifi bool) *Node {
	if n, ok := x.onPort[p]; ok {
		return n
	}

	var n *Node

	if s := b.strangers(x, p); len(s) == 1 {
		n = b.seenNode(s[0])
	} else {
		n = &Node{Kind: NodeUnnamed, Key: fmt.Sprintf("u%d:%s", x.src.ID, p), Online: true, WiFi: wifi}
	}

	b.hangOnPort(x, p, n)

	return n
}

// hangOnPort hangs n from port p of x as the node on that port.
func (b *builder) hangOnPort(x *reader, p string, n *Node) {
	link := &Link{ParentPort: p, Speed: x.ports[p].Speed}
	link.VLANs, link.Trunk = carried(x.ports[p])

	b.hang(n, x.node, link)
	x.onPort[p] = n
}

// seenKey is the key of the node made from neighbour n.
func seenKey(n Neighbour) string { return "n" + cmp.Or(n.MAC, n.Identity) }

// seenNode returns the node for a neighbour no source reads.
func (b *builder) seenNode(n Neighbour) *Node {
	key := seenKey(n)

	if node, ok := b.seen[key]; ok {
		return node
	}

	node := &Node{Kind: NodeSeen, Key: key, Name: cmp.Or(n.Identity, n.MAC), Board: n.Board, Online: true}
	b.claim(node, n.MAC)

	b.seen[key] = node

	return node
}

// placeStrangers makes a node of each neighbour no source reads that has other
// devices behind it, on the deepest source that hears it on a port leading to
// no other source.
func (b *builder) placeStrangers() {
	readers := slices.Clone(b.readers)
	slices.SortStableFunc(readers, func(a, c *reader) int { return cmp.Compare(b.depth[c.node], b.depth[a.node]) })

	for _, x := range readers {
		for _, p := range portsWithStrangers(x) {
			if x.links[p] || p == x.up {
				continue
			}

			s := b.strangers(x, p)
			if len(s) != 1 || b.seen[seenKey(s[0])] != nil {
				continue
			}

			if b.othersOnPort(x, p, s[0].MAC) {
				b.intermediate(x, p, false)
			}
		}
	}
}

func portsWithStrangers(x *reader) []string {
	var out []string

	for _, n := range x.src.Neighbours {
		out = append(out, n.Port)
	}

	slices.Sort(out)

	return slices.Compact(out)
}

// othersOnPort reports whether x's latest read learned any address on port p
// besides mac and the sources' own.
func (b *builder) othersOnPort(x *reader, p, mac string) bool {
	for _, s := range x.src.Seen {
		if s.Port == p && s.MAC != mac && x.current(s) && b.byOwn[s.MAC] == nil {
			return true
		}
	}

	return false
}

// spot is one place a device was seen.
type spot struct {
	r    *reader
	s    Sighting
	cur  bool
	link bool
}

// better reports whether a is a better place for a device than c: from a
// latest read, on a port leading to no other source, deeper, and more recent,
// in that order.
func (b *builder) better(a, c spot) bool {
	switch {
	case a.cur != c.cur:
		return a.cur
	case a.link != c.link:
		return !a.link
	case b.depth[a.r.node] != b.depth[c.r.node]:
		return b.depth[a.r.node] > b.depth[c.r.node]
	case !a.s.LastSeen.Equal(c.s.LastSeen):
		return a.s.LastSeen.After(c.s.LastSeen)
	}

	return cmp.Or(strings.Compare(a.r.src.Name, c.r.src.Name), strings.Compare(a.s.Port, c.s.Port)) < 0
}

// betterWiFi reports whether a says more about a Wi-Fi client than c: it names
// the network, it is from a latest read, or it is more recent, in that order.
// A router managing access points names the network of a client the access
// point's own bridge only marks as Wi-Fi.
func betterWiFi(a, c spot) bool {
	switch {
	case (a.s.SSID != "") != (c.s.SSID != ""):
		return a.s.SSID != ""
	case a.cur != c.cur:
		return a.cur
	}

	return a.s.LastSeen.After(c.s.LastSeen)
}

// attach places every device a source has seen.
func (b *builder) attach() {
	best := map[string]spot{}
	wifi := map[string]spot{}
	vlans := map[string][]int{}

	for _, r := range b.readers {
		for _, s := range r.src.Seen {
			if b.nodeMACs[s.MAC] || b.devByMAC[s.MAC] == nil || (r.up != "" && s.Port == r.up) {
				continue
			}

			sp := spot{r: r, s: s, cur: r.current(s), link: r.links[s.Port]}

			if have, ok := best[s.MAC]; !ok || b.better(sp, have) {
				best[s.MAC] = sp
			}

			if have, ok := wifi[s.MAC]; s.WiFi && (!ok || betterWiFi(sp, have)) {
				wifi[s.MAC] = sp
			}
		}
	}

	// Every VLAN the device was seen in on the port it is placed on.
	for _, r := range b.readers {
		for _, s := range r.src.Seen {
			sp, ok := best[s.MAC]
			if ok && sp.r == r && sp.s.Port == s.Port && r.current(s) == sp.cur && s.VLAN > 0 {
				vlans[s.MAC] = append(vlans[s.MAC], s.VLAN)
			}
		}
	}

	leaves := make([]*Leaf, 0, len(best))

	for _, mac := range sortedKeys(best) {
		sp := best[mac]
		d := b.devByMAC[mac]

		l := &Leaf{
			DeviceID: d.ID, MAC: mac, Name: d.Name, Class: d.Class, Online: d.Online,
			Port: sp.s.Port, Owner: sp.r.node, Current: sp.cur,
		}

		if w, ok := wifi[mac]; ok {
			l.WiFi, l.SSID, l.Band = true, w.s.SSID, w.s.Band

			if w.cur {
				l.Radio = w.s.Radio
			}
		} else if sp.cur {
			l.Speed = sp.r.ports[sp.s.Port].Speed
		}

		l.VLANs = slices.Compact(slices.Sorted(slices.Values(vlans[mac])))

		switch {
		case len(l.VLANs) > 0:
			l.VLAN = l.VLANs[0]
		case d.VLAN > 0:
			l.VLAN = d.VLAN
		default:
			l.VLAN = sp.r.ports[sp.s.Port].PVID
		}

		// A device seen on a port with a node on it is somewhere behind that
		// node. On a port leading to another source, that source did not see
		// it, so it is somewhere on that branch.
		if n := sp.r.onPort[sp.s.Port]; n != nil {
			behind(l, n)
		}

		leaves = append(leaves, l)
	}

	leaves = b.inferSwitches(leaves)

	for _, l := range leaves {
		b.tree.leafByDevice[l.DeviceID] = l
		b.group(l)
	}
}

// behind moves l under node n on its port. The port's link is n's, so l no
// longer has a port or its speed.
func behind(l *Leaf, n *Node) {
	l.Owner, l.Port, l.Speed = n, "", Speed{}
}

// inferSwitches puts the devices sharing one wired port of a source under a
// node on that port: something the tree does not read is plugged in there,
// with all of them behind it. When exactly one of them is a hypervisor, the
// rest are its virtual machines and it is that node. It returns the leaves
// left once any hypervisor has become a node.
func (b *builder) inferSwitches(leaves []*Leaf) []*Leaf {
	type portKey struct {
		r    *reader
		port string
	}

	owners := map[*Node]*reader{}
	for _, r := range b.readers {
		owners[r.node] = r
	}

	shared := map[portKey][]*Leaf{}

	for _, l := range leaves {
		r, ok := owners[l.Owner]
		if !ok || l.Port == "" || r.ports[l.Port].Kind != PortWired {
			continue
		}

		k := portKey{r, l.Port}
		shared[k] = append(shared[k], l)
	}

	hosts := map[*Leaf]bool{}

	for k, ls := range shared {
		if len(ls) < 2 {
			continue
		}

		var n *Node

		if h := hypervisor(ls); h != nil && k.r.onPort[k.port] == nil {
			n = b.host(k.r, k.port, h)
			hosts[h] = true
		} else {
			allWiFi := !slices.ContainsFunc(ls, func(l *Leaf) bool { return !l.WiFi })
			n = b.intermediate(k.r, k.port, allWiFi)
			n.VMs = n.Kind == NodeUnnamed && !allWiFi && mostlyVMs(ls)
		}

		for _, l := range ls {
			behind(l, n)
		}
	}

	return slices.DeleteFunc(leaves, func(l *Leaf) bool { return hosts[l] })
}

// hypervisor returns the one leaf in ls classed as a hypervisor, by the
// classifier or its owner, and nil when there is none or more than one.
func hypervisor(ls []*Leaf) *Leaf {
	var found *Leaf

	for _, l := range ls {
		if l.Class != string(classify.Hypervisor) {
			continue
		}

		if found != nil {
			return nil
		}

		found = l
	}

	return found
}

// host makes hypervisor h the node on port p of x.
func (b *builder) host(x *reader, p string, h *Leaf) *Node {
	n := &Node{Kind: NodeHost, Key: fmt.Sprintf("h%d", h.DeviceID), Name: h.Name, Online: true}
	b.claim(n, h.MAC)
	b.hangOnPort(x, p, n)

	return n
}

// vmPrefixes are the hardware address blocks hypervisors assign to virtual
// machines: VirtualBox, QEMU and KVM, Proxmox, VMware, Hyper-V, Xen and
// Parallels.
var vmPrefixes = []string{
	"08:00:27", "52:54:00", "bc:24:11",
	"00:05:69", "00:0c:29", "00:1c:14", "00:50:56",
	"00:15:5d", "00:16:3e", "00:1c:42",
}

// mostlyVMs reports whether more than half of ls have a virtual machine's
// hardware address.
func mostlyVMs(ls []*Leaf) bool {
	vms := 0

	for _, l := range ls {
		mac := strings.ToLower(l.MAC)

		if slices.ContainsFunc(vmPrefixes, func(p string) bool { return strings.HasPrefix(mac, p) }) {
			vms++
		}
	}

	return vms*2 > len(ls)
}

// group files l in its owner's group for its VLAN, or for its Wi-Fi network
// and VLAN.
func (b *builder) group(l *Leaf) {
	ssid := ""
	if l.WiFi {
		ssid = l.SSID
	}

	for _, g := range l.Owner.Groups {
		if g.WiFi == l.WiFi && g.SSID == ssid && g.VLAN == l.VLAN {
			g.Devices = append(g.Devices, l)

			return
		}
	}

	l.Owner.Groups = append(l.Owner.Groups, &Group{WiFi: l.WiFi, SSID: ssid, VLAN: l.VLAN, Devices: []*Leaf{l}})
}

// finish orders the tree, lists the VLANs and the unplaced devices, and
// indexes the nodes by device.
func (b *builder) finish() {
	vlans := map[int]bool{}

	var walk func(n *Node)

	walk = func(n *Node) {
		if n.DeviceID != 0 {
			b.tree.nodeByDevice[n.DeviceID] = n
		}

		slices.SortStableFunc(n.Children, func(a, c *Node) int {
			return cmp.Or(ComparePorts(a.Uplink.ParentPort, c.Uplink.ParentPort), strings.Compare(a.Name, c.Name))
		})

		slices.SortStableFunc(n.Groups, func(a, c *Group) int {
			return cmp.Or(
				compareBool(a.WiFi, c.WiFi),
				strings.Compare(a.SSID, c.SSID),
				cmp.Compare(a.VLAN, c.VLAN),
			)
		})

		for _, g := range n.Groups {
			if g.VLAN > 0 {
				vlans[g.VLAN] = true
			}

			slices.SortStableFunc(g.Devices, func(a, c *Leaf) int {
				return cmp.Or(ComparePorts(a.Port, c.Port), strings.Compare(a.Name, c.Name), cmp.Compare(a.DeviceID, c.DeviceID))
			})
		}

		for _, c := range n.Children {
			walk(c)
		}
	}

	if b.tree.Root != nil {
		walk(b.tree.Root)
	}

	b.tree.VLANs = slices.Sorted(maps.Keys(vlans))

	for _, d := range b.devByMAC {
		_, placed := b.tree.leafByDevice[d.ID]
		_, isNode := b.tree.nodeByDevice[d.ID]

		if d.Online && !placed && !isNode {
			b.tree.Unplaced = append(b.tree.Unplaced, &Leaf{
				DeviceID: d.ID, MAC: d.MAC, Name: d.Name, Class: d.Class, Online: d.Online, VLAN: d.VLAN,
			})
		}
	}

	slices.SortFunc(b.tree.Unplaced, func(a, c *Leaf) int {
		return cmp.Or(strings.Compare(a.Name, c.Name), cmp.Compare(a.DeviceID, c.DeviceID))
	})
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.SortedFunc(maps.Keys(m), ComparePorts)
}

// compareBool orders false before true.
func compareBool(a, c bool) int {
	switch {
	case a == c:
		return 0
	case !a:
		return -1
	default:
		return 1
	}
}

// ComparePorts orders port names the way a person reads them, so ether2 comes
// before ether10.
func ComparePorts(a, c string) int {
	for a != "" && c != "" {
		da, ra := leadingDigits(a)
		dc, rc := leadingDigits(c)

		if da != "" && dc != "" {
			if n := cmp.Or(cmp.Compare(len(strings.TrimLeft(da, "0")), len(strings.TrimLeft(dc, "0"))),
				strings.Compare(strings.TrimLeft(da, "0"), strings.TrimLeft(dc, "0"))); n != 0 {
				return n
			}

			a, c = ra, rc

			continue
		}

		if a[0] != c[0] {
			return cmp.Compare(a[0], c[0])
		}

		a, c = a[1:], c[1:]
	}

	return cmp.Compare(len(a), len(c))
}

func leadingDigits(s string) (digits, rest string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}

	return s[:i], s[i:]
}

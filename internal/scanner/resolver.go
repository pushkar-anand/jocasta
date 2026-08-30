package scanner

import "net"

// resolver is the system resolver. It is a package variable so tests can swap in
// one that answers from a stub instead of the network.
var resolver = net.DefaultResolver

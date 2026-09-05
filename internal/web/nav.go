package web

// crumb is the way back out of a page that is about one thing. Only such a page
// sets one; every other page is reached from the rail, which is already there.
type crumb struct {
	Label string
	Href  string
}

// view is what the layout needs from every page. The sidebar's sections are
// static markup in partial/nav, not built from this; Section only says which
// of them to mark current.
type view struct {
	Title   string
	Section string

	// Crumb is the way back, shown before the title. Nil leaves it out.
	Crumb *crumb

	// Live marks a page that refreshes itself, which is the only thing the
	// indicator in the topbar claims.
	Live bool

	// IsAdmin is whether the signed-in account may reach the admin-only pages,
	// so the topbar shows the link to them only to someone it would let in.
	IsAdmin bool

	// Note is the ambient line at the foot of the rail. Empty leaves it out.
	Note string
}

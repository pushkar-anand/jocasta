package web

import "html/template"

// nav is one entry of the sidebar navigation. The entries live in Go so a page
// that does not exist yet cannot be linked to from the layout, and so a section
// cannot be added without the glyph that names it in the rail.
type nav struct {
	Label   string
	Href    string
	Current bool

	// Icon is the glyph's paths, on a 24px grid and stroked in currentColor.
	// It is markup this package owns rather than anything a request supplies,
	// which is what makes carrying it as HTML safe.
	Icon template.HTML
}

var sections = []nav{
	{
		Label: "Overview", Href: "/",
		Icon: `<rect x="3" y="3" width="8" height="8" rx="2"/><rect x="13" y="3" width="8" height="8" rx="2"/>` +
			`<rect x="3" y="13" width="8" height="8" rx="2"/><rect x="13" y="13" width="8" height="8" rx="2"/>`,
	},
	{
		Label: "Devices", Href: "/devices",
		Icon: `<rect x="3" y="4" width="18" height="6" rx="1.5"/><rect x="3" y="14" width="18" height="6" rx="1.5"/>` +
			`<circle cx="7" cy="7" r="0.7"/><circle cx="7" cy="17" r="0.7"/>`,
	},
	{
		Label: "Events", Href: "/events",
		Icon: `<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.5 2"/>`,
	},
	{
		Label: "Scans", Href: "/scans",
		Icon: `<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="4.2"/><circle cx="12" cy="12" r="1"/>` +
			`<path d="M12 2.3v2.2M21.7 12h-2.2M12 21.7v-2.2M2.3 12h2.2"/>`,
	},
}

// crumb is the way back out of a page that is about one thing. Only such a page
// sets one; every other page is reached from the rail, which is already there.
type crumb struct {
	Label string
	Href  string
}

// view is what the layout needs from every page.
type view struct {
	Title   string
	Section string

	// Crumb is the way back, shown before the title. Nil leaves it out.
	Crumb *crumb

	// Live marks a page that refreshes itself, which is the only thing the
	// indicator in the topbar claims.
	Live bool

	// Note is the ambient line at the foot of the rail. Empty leaves it out.
	Note string
}

// Nav returns the masthead entries with the current one marked.
func (v view) Nav() []nav {
	entries := make([]nav, 0, len(sections))

	for _, s := range sections {
		s.Current = s.Label == v.Section
		entries = append(entries, s)
	}

	return entries
}

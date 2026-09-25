package web

import "github.com/pushkar-anand/jocasta/internal/db/dbtype"

// crumb is the way back out of a page that is about one thing. Only such a page
// sets one; every other page is reached from the rail, which is already there.
type crumb struct {
	Label string
	Href  string
}

// liveEvery is a view's Live: every when the page refreshes itself, and empty
// when it does not.
func liveEvery(live bool, every string) string {
	if !live {
		return ""
	}

	return every
}

// view is what the layout needs from every page. The sidebar's sections are
// static markup in partial/nav; Section only says which of them to mark
// current.
type view struct {
	Title   string
	Section string

	// Crumb is the way back, shown before the title. Nil leaves it out.
	Crumb *crumb

	// Live is how often a page that refreshes itself does so, as the
	// indicator in the topbar says it; empty on a page that does not.
	Live string

	// Role is the signed-in account's role.
	Role dbtype.UserRole

	// SignedInAs is the signed-in account's name, shown in the topbar account
	// menu. Empty on the pages that render outside the signed-in shell.
	SignedInAs string

	// Note is the ambient line at the foot of the rail. Empty leaves it out.
	Note string

	// Window is the configured online window, said in words ("15 minutes"), for
	// the pages that explain what "seen recently" counts. Empty leaves it out.
	Window string
}

// roleDisplay maps a stored role value to its UI label. Forms submit the stored
// values (read, read_write).
func roleDisplay(role dbtype.UserRole) string {
	switch role {
	case dbtype.RoleAdmin:
		return "Admin"
	case dbtype.RoleReadWrite:
		return "Editor"
	case dbtype.RoleRead:
		return "Viewer"
	default:
		return string(role)
	}
}

// permOption is one radio in a permission-choice fieldset.
type permOption struct {
	Value   string
	Label   string
	Hint    string
	Checked bool
}

// permChoiceView backs partial/permission-choice, the Viewer/Editor picker
// shared by the create-user and create-token forms. Field is the form field
// name; the write option is omitted when allowWrite is false.
type permChoiceView struct {
	Field   string
	Legend  string
	Options []permOption
}

func permChoice(field, legend, selected string, allowWrite bool) permChoiceView {
	v := permChoiceView{
		Field:  field,
		Legend: legend,
		Options: []permOption{{
			Value:   string(dbtype.RoleRead),
			Label:   roleDisplay(dbtype.RoleRead),
			Hint:    "Reads the inventory. Can create read tokens.",
			Checked: selected != string(dbtype.RoleReadWrite),
		}},
	}

	if allowWrite {
		v.Options = append(v.Options, permOption{
			Value:   string(dbtype.RoleReadWrite),
			Label:   roleDisplay(dbtype.RoleReadWrite),
			Hint:    "Also edits device labels and groups. Can create read/write tokens.",
			Checked: selected == string(dbtype.RoleReadWrite),
		})
	}

	return v
}

// scopeChoice is permChoice for the create-token form. Wire values match the
// user roles (read, read_write); the hints describe what the token reaches
// through the API.
func scopeChoice(selected string, allowWrite bool) permChoiceView {
	v := permChoiceView{
		Field:  "scope",
		Legend: "Permission",
		Options: []permOption{{
			Value:   string(dbtype.TokenRead),
			Label:   roleDisplay(dbtype.RoleRead),
			Hint:    "Reads the inventory through the API.",
			Checked: selected != string(dbtype.TokenReadWrite),
		}},
	}

	if allowWrite {
		v.Options = append(v.Options, permOption{
			Value:   string(dbtype.TokenReadWrite),
			Label:   roleDisplay(dbtype.RoleReadWrite),
			Hint:    "Also edits device labels and groups through the API.",
			Checked: selected == string(dbtype.TokenReadWrite),
		})
	}

	return v
}

// scopeDisplay is roleDisplay for an API token's scope string.
func scopeDisplay(scope string) string {
	return roleDisplay(dbtype.UserRole(scope))
}

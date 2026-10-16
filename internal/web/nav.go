package web

import "github.com/pushkar-anand/jocasta/internal/db/dbtype"

// crumb is the way back from a page that is about one thing. Only such a page
// sets one. A person reaches every other page from the rail, which is already
// there.
type crumb struct {
	Label string
	Href  string
}

// liveEvery is the Live of a view. It is every when the page refreshes itself,
// and empty when it does not.
func liveEvery(live bool, every string) string {
	if !live {
		return ""
	}

	return every
}

// view is what the layout needs from every page. The sections of the sidebar
// are static markup in partial/nav. Section only tells which of them to mark
// as current.
type view struct {
	Title   string
	Section string

	// Crumb is the way back, shown before the title. Nil leaves it out.
	Crumb *crumb

	// Live is how often a page refreshes itself, as the indicator in the
	// topbar says it. It is empty on a page with no refresh.
	Live string

	// Role is the role of the signed-in account.
	Role dbtype.UserRole

	// SignedInAs is the name of the signed-in account, in the account menu of
	// the topbar. It is empty on the pages outside the signed-in shell.
	SignedInAs string

	// Note is the line at the bottom of the rail. Empty leaves it out.
	Note string

	// Window is the configured online window in words ("15 minutes"), for the
	// pages that explain what "seen recently" counts. Empty leaves it out.
	Window string
}

// roleDisplay maps a stored role value to its UI label. Forms send the stored
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

// permChoiceView is the data of partial/permission-choice. This is the
// Viewer/Editor picker of the create-user and create-token forms. Field is the
// form field name. The write option is left out when allowWrite is false.
type permChoiceView struct {
	Field   string
	Legend  string
	Options []permOption
}

// permChoice builds the role picker. It leaves out Editor when writes are
// forbidden. An unknown selection becomes Viewer.
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

// scopeChoice is permChoice for the create-token form. The sent values match
// the user roles (read, read_write). The hints describe what the token reaches
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

// scopeDisplay is roleDisplay for the scope string of an API token.
func scopeDisplay(scope string) string {
	return roleDisplay(dbtype.UserRole(scope))
}

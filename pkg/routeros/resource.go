package routeros

import "context"

// Resource is /system/resource: what the router is, and what it runs.
//
// Log the Version field. It explains a table that arrived in an unexpected
// shape. Only RouterOS 7 and later have the REST API. So any router that
// answers runs 7 or later.
type Resource struct {
	Version          string `json:"version"`
	BoardName        string `json:"board-name"`
	Platform         string `json:"platform"`
	ArchitectureName string `json:"architecture-name"`
	CPU              string `json:"cpu"`
	Uptime           string `json:"uptime"`

	// Memory and disk figures are decimal byte counts, as strings.
	FreeMemory  string `json:"free-memory"`
	TotalMemory string `json:"total-memory"`
}

// Resource returns the identity and version of the router.
func (r *RouterOS) Resource(ctx context.Context) (*Resource, error) {
	return r.get[Resource](ctx, resourceAPI)
}

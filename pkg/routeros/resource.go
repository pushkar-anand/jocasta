package routeros

import "context"

// Resource is /system/resource: what the router is and what it is running.
//
// Version is the field worth logging: it explains a table that arrived in an
// unexpected shape. The REST API exists only from RouterOS 7, so any router
// that answers runs 7 or later.
type Resource struct {
	Version          string `json:"version"`
	BoardName        string `json:"board-name"`
	Platform         string `json:"platform"`
	ArchitectureName string `json:"architecture-name"`
	CPU              string `json:"cpu"`
	Uptime           string `json:"uptime"`

	// Memory and disk figures arrive as decimal byte counts rendered as
	// strings.
	FreeMemory  string `json:"free-memory"`
	TotalMemory string `json:"total-memory"`
}

// Resource returns the router's identity and version.
func (r *RouterOS) Resource(ctx context.Context) (*Resource, error) {
	return r.get[Resource](ctx, resourceAPI)
}

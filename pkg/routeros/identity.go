package routeros

import "context"

// Identity is /system/identity: the name an operator gave the router, which is
// also what it announces to its neighbours.
type Identity struct {
	Name string `json:"name"`
}

// Identity returns the router's own name.
func (r *RouterOS) Identity(ctx context.Context) (*Identity, error) {
	return r.get[Identity](ctx, identityAPI)
}

package routeros

import "context"

// Identity is /system/identity: the name that an operator gave the router. The
// router also announces this name to its neighbours.
type Identity struct {
	Name string `json:"name"`
}

// Identity returns the name of the router.
func (r *RouterOS) Identity(ctx context.Context) (*Identity, error) {
	return r.get[Identity](ctx, identityAPI)
}

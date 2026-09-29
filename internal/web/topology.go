package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/topology"
	"github.com/pushkar-anand/jocasta/internal/web/topomap"
)

// topologyPage is the network as a tree: what is plugged into what.
type topologyPage struct {
	view

	Tree   *topology.Tree
	Layout *topomap.Layout

	// VLANs is the legend: every VLAN a placed device is in, in order.
	VLANs []vlanKey
}

// vlanKey is one VLAN in the legend.
type vlanKey struct {
	VLAN    int
	Name    string
	Tone    int
	Devices int
}

// Label is how the legend names the VLAN, as the map names a network: the
// network's name when it has one, then the VLAN.
func (k vlanKey) Label() string {
	vlan := "VLAN\u00a0" + strconv.Itoa(k.VLAN)
	if k.Name == "" {
		return vlan
	}

	return k.Name + " · " + vlan
}

// topologyView serves the topology page.
func (h *Handler) topologyView(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		data, err := h.buildTopology(ctx)
		if err != nil {
			return err
		}

		data.view = view{
			Title: "Topology", Section: "Topology", Live: liveEvery(data.Layout != nil, "minute"),
			Role: sm.CurrentRole(ctx), SignedInAs: sm.CurrentUsername(ctx),
		}

		h.htmlWriter.Success(w, r, templatePageTopology, data)

		return nil
	}
}

// topologyLive serves the tree alone, which is what the page polls for.
func (h *Handler) topologyLive() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		data, err := h.buildTopology(r.Context())
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePartialTopologyBody, data)

		return nil
	}
}

func (h *Handler) buildTopology(ctx context.Context) (*topologyPage, error) {
	tree, err := h.store.Topology(ctx)
	if err != nil {
		return nil, err
	}

	data := &topologyPage{Tree: tree, Layout: topomap.Place(tree)}

	if data.Layout == nil {
		return data, nil
	}

	nets, err := h.store.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}

	names := make(map[int]string, len(nets))

	for _, n := range nets {
		if n.VLAN > 0 && names[n.VLAN] == "" {
			names[n.VLAN] = n.Name
		}
	}

	counts := make(map[int]int, len(tree.VLANs))

	for _, g := range data.Layout.Groups {
		for _, c := range g.Chips {
			counts[c.VLAN]++
		}
	}

	for i, v := range tree.VLANs {
		data.VLANs = append(data.VLANs, vlanKey{VLAN: v, Name: names[v], Tone: i, Devices: counts[v]})
	}

	return data, nil
}

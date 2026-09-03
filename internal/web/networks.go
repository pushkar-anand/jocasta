package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// networkData is one network and the devices currently on it. The device list
// is the same rows partial the Devices page renders, so .Devices and .Groups
// are named to match what that partial reads.
type networkData struct {
	view
	Network *inventory.Network
	Devices []*inventory.Device
	Groups  []string
}

func (h *Handler) network(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		// The pattern admits any segment, so a non-id lands here rather than
		// at a handler that would try to look it up.
		h.notFound(w, r)

		return
	}

	network, err := h.store.Network(ctx, id)
	if err != nil {
		if errors.Is(err, inventory.ErrNotFound) {
			h.notFound(w, r)

			return
		}

		h.fail(w, r, err)

		return
	}

	devices, err := h.store.ListDevices(ctx, inventory.DeviceFilter{Network: id})
	if err != nil {
		h.fail(w, r, err)

		return
	}

	groups, err := h.store.Groups(ctx)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	data := &networkData{
		view: view{
			Title:   network.CIDR,
			Section: "Overview",
			Crumb:   &crumb{Label: "Overview", Href: "/"},
		},
		Network: network,
		Devices: devices,
		Groups:  groups,
	}

	if note, err := h.sweepNote(ctx); err == nil {
		data.Note = note
	}

	h.renderer.Render(w, r, "page/network", data)
}

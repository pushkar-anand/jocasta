package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// network serves one prefix and the devices on it. The device list is the same
// filtered, paged list the Devices page draws, scoped to this network: the
// filter form and pager address this page, and the network select is left out
// since the path already names it.
func (h *Handler) network(w http.ResponseWriter, r *http.Request) {
	net, ok := h.networkFromPath(w, r)
	if !ok {
		return
	}

	data, err := h.deviceListData(r, view{
		Title:   net.CIDR,
		Section: "Devices",
		Crumb:   &crumb{Label: "Devices", Href: "/devices"},
	}, "/networks/"+strconv.FormatInt(net.ID, 10), net)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	h.renderer.Render(w, r, "page/network", data)
}

// networkRows serves the device table on its own, which is what the filter form
// on a network's page fetches as it is filled in.
func (h *Handler) networkRows(w http.ResponseWriter, r *http.Request) {
	net, ok := h.networkFromPath(w, r)
	if !ok {
		return
	}

	data, err := h.deviceListData(r, view{}, "/networks/"+strconv.FormatInt(net.ID, 10), net)
	if err != nil {
		h.fail(w, r, err)

		return
	}

	w.Header().Set("HX-Push-Url", data.canonical())

	h.renderer.Render(w, r, "partial/device-rows", data)
}

// networkFromPath reads the prefix the route names, answering the request
// itself if the segment is not an id or names nothing.
func (h *Handler) networkFromPath(w http.ResponseWriter, r *http.Request) (*inventory.Network, bool) {
	id, ok := pathID(r)
	if !ok {
		// The pattern admits any segment, so a non-id lands here rather than
		// at a handler that would try to look it up.
		h.notFound(w, r)

		return nil, false
	}

	net, err := h.store.Network(r.Context(), id)
	if err != nil {
		if errors.Is(err, inventory.ErrNotFound) {
			h.notFound(w, r)

			return nil, false
		}

		h.fail(w, r, err)

		return nil, false
	}

	return net, true
}

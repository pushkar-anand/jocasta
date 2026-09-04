package web

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// networkPath is where one network's own page lives.
func networkPath(id int64) string {
	return (&url.URL{Path: "/networks"}).JoinPath(strconv.FormatInt(id, 10)).String()
}

// network serves one prefix and the devices on it. The device list is the same
// filtered, paged list the Devices page draws, scoped to this network: the
// filter form and pager address this page, and the network select is left out
// since the path already names it.
func (h *Handler) network() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		net, err := h.store.Network(r.Context(), id)
		if err != nil {
			return err
		}

		q, err := h.reader.ReadAndValidateQueryParams[deviceQuery](r)
		if err != nil {
			return err
		}

		data, err := buildDeviceListData(r.Context(), h.store, *q, view{
			Title:   net.CIDR,
			Section: "Devices",
			Crumb:   &crumb{Label: "Devices", Href: "/devices"},
		}, networkPath(net.ID), net)
		if err != nil {
			return err
		}

		h.htmlWriter.Success(w, r, templatePageNetwork, data)
		return nil
	}
}

// networkRows serves the device table on its own, which is what the filter form
// on a network's page fetches as it is filled in.
func (h *Handler) networkRows() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		net, err := h.store.Network(r.Context(), id)
		if err != nil {
			return err
		}

		q, err := h.reader.ReadAndValidateQueryParams[deviceQuery](r)
		if err != nil {
			return err
		}

		data, err := buildDeviceListData(
			r.Context(), h.store, *q,
			view{}, networkPath(net.ID), net,
		)
		if err != nil {
			return err
		}

		w.Header().Set("HX-Push-Url", data.canonical())

		h.htmlWriter.Success(w, r, templatePartialDeviceRows, data)
		return nil
	}
}

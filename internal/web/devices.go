package web

import (
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// deviceQuery is every value that the filter form puts in the query string. The
// validate tags refuse a query string with the wrong shape before it reaches
// the store. Examples are a term that is too long, or a page that is not a
// positive number. Schema decoding fails on a key that is not listed here. So
// there is a field for every control of the form, also with no constraint.
type deviceQuery struct {
	Query          string `schema:"q" validate:"omitempty,max=255"`
	Group          string `schema:"group" validate:"omitempty,max=100"`
	Network        string `schema:"network" validate:"omitempty,max=20"`
	Type           string `schema:"type" validate:"omitempty,max=40"`
	Status         string `schema:"status" validate:"omitempty,max=40"`
	Sort           string `schema:"sort" validate:"omitempty,max=40"`
	IncludeIgnored bool   `schema:"ignored" validate:"omitempty"`
	Watched        bool   `schema:"watched" validate:"omitempty"`
	Page           int    `schema:"page" validate:"omitempty,min=1"`
}

// listDevices serves the Devices page. With rows, it serves only the table,
// which the filter form fetches while a person fills it.
func (h *Handler) listDevices(sm *auth.Session, rows bool) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		q, err := h.reader.ReadAndValidateQueryParams[deviceQuery](r)
		if err != nil {
			return err
		}

		data, err := buildDevicesData(r.Context(), h.store, *q)
		if err != nil {
			h.log.ErrorContext(r.Context(), "failed to build device list", logger.Err(err))

			return err
		}

		// The rows also have the Edit button. So they still need to know
		// whether this account can use it.
		data.Role = sm.CurrentRole(r.Context())

		if rows {
			w.Header().Set("HX-Push-Url", data.canonical())
			h.htmlWriter.Success(w, r, templatePartialDeviceRows, data)

			return nil
		}

		data.SignedInAs = sm.CurrentUsername(r.Context())
		h.htmlWriter.Success(w, r, templatePageDevices, data)

		return nil
	}
}

// device serves the details and history of one device. A malformed or missing
// id gives a not-found page.
func (h *Handler) device(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		// A missing device is a missing page: a 404.
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		device, err := h.store.Device(r.Context(), id)
		if err != nil {
			return err
		}

		data, err := buildDevicePageData(r.Context(), h.store, device, r.URL.Query())
		if err != nil {
			h.log.ErrorContext(r.Context(), "failed to build device page", logger.Err(err))

			return err
		}

		data.Role = sm.CurrentRole(r.Context())
		data.SignedInAs = sm.CurrentUsername(r.Context())

		h.htmlWriter.Success(w, r, templatePageDevice, data)

		return nil
	}
}

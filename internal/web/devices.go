package web

import (
	"net/http"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// deviceQuery is every value the filter form puts in the query string. The
// validate tags turn away a query string that is the wrong shape -- an
// over-long term, a page that is not a positive number -- before it reaches the
// store. schema decoding fails on a key that is not listed here, so a field is
// added for every control the form carries even where nothing constrains it.
type deviceQuery struct {
	Query          string `schema:"q" validate:"omitempty,max=255"`
	Group          string `schema:"group" validate:"omitempty,max=100"`
	Network        string `schema:"network" validate:"omitempty,max=20"`
	Type           string `schema:"type" validate:"omitempty,max=40"`
	Status         string `schema:"status" validate:"omitempty,max=40"`
	Sort           string `schema:"sort" validate:"omitempty,max=40"`
	IncludeIgnored bool   `schema:"ignored" validate:"omitempty"`
	Page           int    `schema:"page" validate:"omitempty,min=1"`
}

func (h *Handler) listDevices(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	type query struct {
		deviceQuery
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		q, err := h.reader.ReadAndValidateQueryParams[query](r)
		if err != nil {
			return err
		}

		data, err := buildDevicesData(r.Context(), h.store, q.deviceQuery)
		if err != nil {
			h.log.ErrorContext(r.Context(), "failed to build device list", logger.Err(err))

			return err
		}

		data.IsAdmin = isAdmin(sm, a, r)

		h.htmlWriter.Success(w, r, templatePageDevices, data)
		return nil
	}
}

// deviceRows serves the table on its own, which is what the form fetches as it
// is filled in.
func (h *Handler) deviceRows() response.HandlerFunc {
	type query struct {
		deviceQuery
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		q, err := h.reader.ReadAndValidateQueryParams[query](r)
		if err != nil {
			return err
		}

		data, err := buildDevicesData(r.Context(), h.store, q.deviceQuery)
		if err != nil {
			h.log.ErrorContext(r.Context(), "failed to build device list", logger.Err(err))

			return err
		}

		w.Header().Set("HX-Push-Url", data.canonical())

		h.htmlWriter.Success(w, r, templatePartialDeviceRows, data)
		return nil
	}
}

func (h *Handler) device(sm *auth.Session, a *auth.Auth) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		// A device that is not there is a page that is not there, not a fault.
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		device, err := h.store.Device(r.Context(), id)
		if err != nil {
			return err
		}

		data, err := buildDevicePageData(r.Context(), h.store, device)
		if err != nil {
			h.log.ErrorContext(r.Context(), "failed to build device page", logger.Err(err))

			return err
		}

		data.IsAdmin = isAdmin(sm, a, r)

		h.htmlWriter.Success(w, r, templatePageDevice, data)
		return nil
	}
}

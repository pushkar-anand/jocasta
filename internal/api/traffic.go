package api

import (
	"cmp"
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// deviceTraffic answers who one device exchanged data with, the same shape
// list_traffic gives an agent for one device.
func (h *Handler) deviceTraffic(store *inventory.Store) response.HandlerFunc {
	type (
		// An unset days gets the default.
		trafficRequest struct {
			Days int `schema:"days" validate:"omitempty,min=1,max=90"`
		}

		trafficResponse struct {
			// Recorded is false when no traffic has ever been recorded,
			// meaning nothing is collecting. A quiet device reports true.
			Recorded bool `json:"recorded"`

			*inventory.DeviceTraffic
		}
	)

	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := deviceID(r)
		if err != nil {
			return err
		}

		q, err := h.reader.ReadAndValidateQueryParams[trafficRequest](r)
		if err != nil {
			return err
		}

		// A device that does not exist is a 404.
		if _, err := store.Device(r.Context(), id); err != nil {
			return err
		}

		recorded, err := store.TrafficRecorded(r.Context())
		if err != nil {
			return err
		}

		days := cmp.Or(q.Days, 1)

		traffic, err := store.DeviceTraffic(r.Context(), id, time.Now().Add(-time.Duration(days)*24*time.Hour))
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, trafficResponse{Recorded: recorded, DeviceTraffic: traffic})

		return nil
	}
}

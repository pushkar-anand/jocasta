package api

import (
	"cmp"
	"net/http"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// deviceTraffic answers who one device exchanged data with. It has the same
// shape that list_traffic gives an agent for one device.
func (h *Handler) deviceTraffic(store *inventory.Store) response.HandlerFunc {
	type (
		// An unset days gets the default.
		trafficRequest struct {
			Days int `schema:"days" validate:"omitempty,min=1,max=90"`
		}

		trafficResponse struct {
			// Recorded is false when Jocasta never recorded traffic. This
			// means that no source collects traffic. A quiet device reports
			// true.
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
		if err := store.RequireDevice(r.Context(), id); err != nil {
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

// deviceDNS answers the names that one device looked up. It has the same shape
// that list_dns gives an agent.
func (h *Handler) deviceDNS(store *inventory.Store) response.HandlerFunc {
	type (
		// An unset days gets the default.
		dnsRequest struct {
			Days int `schema:"days" validate:"omitempty,min=1,max=90"`
		}

		dnsResponse struct {
			// Recorded is false when Jocasta never read the log of a
			// resolver. This means that no source collects lookups. A quiet
			// device reports true.
			Recorded bool                 `json:"recorded"`
			Names    []*inventory.DNSName `json:"names"`
		}
	)

	return func(w http.ResponseWriter, r *http.Request) error {
		id, err := deviceID(r)
		if err != nil {
			return err
		}

		q, err := h.reader.ReadAndValidateQueryParams[dnsRequest](r)
		if err != nil {
			return err
		}

		// A device that does not exist is a 404.
		if err := store.RequireDevice(r.Context(), id); err != nil {
			return err
		}

		recorded, err := store.DNSRead(r.Context())
		if err != nil {
			return err
		}

		days := cmp.Or(q.Days, 1)

		names, err := store.DeviceDNS(r.Context(), id, time.Now().Add(-time.Duration(days)*24*time.Hour))
		if err != nil {
			return err
		}

		h.jsonWriter.Ok(w, r, dnsResponse{Recorded: recorded, Names: names})

		return nil
	}
}

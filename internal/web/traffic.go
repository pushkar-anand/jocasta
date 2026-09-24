package web

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// trafficWindow is one period the traffic view can cover.
type trafficWindow struct {
	Key   string
	Label string
	span  time.Duration
}

// trafficWindows are the periods offered, shortest first. The first is the
// default: "today, roughly" is the question most visits ask.
var trafficWindows = []trafficWindow{
	{Key: "24h", Label: "24 hours", span: 24 * time.Hour},
	{Key: "7d", Label: "7 days", span: 7 * 24 * time.Hour},
	{Key: "30d", Label: "30 days", span: 30 * 24 * time.Hour},
}

// windowFor returns the window key names, falling back to the default for
// anything else rather than refusing: it only ever arrives from a link.
func windowFor(key string) trafficWindow {
	for _, w := range trafficWindows {
		if w.Key == key {
			return w
		}
	}

	return trafficWindows[0]
}

// trafficSection is a device's "Talks to" section, on the page and as the
// fragment its filter form fetches.
type trafficSection struct {
	DeviceID int64
	Window   trafficWindow
	Windows  []trafficWindow
	Filter   trafficFilter

	// Services are the services the device used over the window, for the
	// filter's choices. Taken before filtering, so picking one never empties
	// the list it was picked from.
	Services []serviceChoice

	// Recorded is whether any traffic has been recorded at all, which is how
	// the section tells "this device exchanged nothing" from "nothing is
	// collecting".
	Recorded bool
	Traffic  *inventory.DeviceTraffic

	// Local and Internet are Traffic after the filter, grouped for reading.
	Local    []*localEntry
	Internet []*orgEntry
}

// Matched reports whether anything is left once the filter is applied.
func (t *trafficSection) Matched() bool {
	return len(t.Local) > 0 || len(t.Internet) > 0
}

// ClearPath is the device page with the period kept and every filter dropped.
func (t *trafficSection) ClearPath() string {
	return devicePath(t.DeviceID, t.Window, trafficFilter{})
}

// buildTrafficSection reads one device's traffic over the window key names,
// narrowed by f.
func buildTrafficSection(
	ctx context.Context, store *inventory.Store, id int64, key string, f trafficFilter, now time.Time,
) (*trafficSection, error) {
	w := windowFor(key)

	recorded, err := store.TrafficRecorded(ctx)
	if err != nil {
		return nil, err
	}

	traffic, err := store.DeviceTraffic(ctx, id, now.Add(-w.span))
	if err != nil {
		return nil, err
	}

	sec := &trafficSection{
		DeviceID: id,
		Window:   w,
		Windows:  trafficWindows,
		Filter:   f,
		Services: serviceChoices(traffic),
		Recorded: recorded,
		Traffic:  traffic,
	}

	sec.Local, sec.Internet = groupTraffic(traffic, f)

	return sec, nil
}

// devicePath is a device's page with the traffic period and filter when they
// are not the defaults, so the address bar can be reloaded or shared.
func devicePath(id int64, w trafficWindow, f trafficFilter) string {
	q := url.Values{}
	if w.Key != trafficWindows[0].Key {
		q.Set("traffic", w.Key)
	}

	f.encode(q)

	p := "/devices/" + strconv.FormatInt(id, 10)
	if len(q) > 0 {
		p += "?" + q.Encode()
	}

	return p
}

// trafficWindowKey is the period a request asked for. The form and the page
// call it traffic; window is what the section's links sent before it had a
// form, and still works.
func trafficWindowKey(q url.Values) string {
	return cmp.Or(q.Get("traffic"), q.Get("window"))
}

// deviceTraffic serves the "Talks to" section alone, for its filter form.
func (h *Handler) deviceTraffic() response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		id, ok := pathID(r)
		if !ok {
			return inventory.ErrNotFound
		}

		// A device that is not there is a 404 here too, not an empty section.
		if _, err := h.store.Device(r.Context(), id); err != nil {
			return err
		}

		q := r.URL.Query()

		data, err := buildTrafficSection(r.Context(), h.store, id, trafficWindowKey(q), trafficFilterFrom(q), time.Now())
		if err != nil {
			return fmt.Errorf("device %d traffic: %w", id, err)
		}

		w.Header().Set("HX-Push-Url", devicePath(id, data.Window, data.Filter))

		h.htmlWriter.Success(w, r, templatePartialDeviceTraffic, data)

		return nil
	}
}

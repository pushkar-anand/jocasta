package web

import (
	"context"
	"fmt"
	"net/http"
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
// fragment its period switch fetches.
type trafficSection struct {
	DeviceID int64
	Window   trafficWindow
	Windows  []trafficWindow

	// Recorded is whether any traffic has been recorded at all, which is how
	// the section tells "this device exchanged nothing" from "nothing is
	// collecting".
	Recorded bool
	Traffic  *inventory.DeviceTraffic
}

// buildTrafficSection reads one device's traffic over the window key names.
func buildTrafficSection(
	ctx context.Context, store *inventory.Store, id int64, key string, now time.Time,
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

	return &trafficSection{
		DeviceID: id,
		Window:   w,
		Windows:  trafficWindows,
		Recorded: recorded,
		Traffic:  traffic,
	}, nil
}

// devicePath is a device's page, with the traffic period when it is not the
// default, so the address bar can be reloaded or shared.
func devicePath(id int64, w trafficWindow) string {
	p := "/devices/" + strconv.FormatInt(id, 10)
	if w.Key != trafficWindows[0].Key {
		p += "?traffic=" + w.Key
	}

	return p
}

// deviceTraffic serves the "Talks to" section alone, for its period switch.
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

		data, err := buildTrafficSection(r.Context(), h.store, id, r.URL.Query().Get("window"), time.Now())
		if err != nil {
			return fmt.Errorf("device %d traffic: %w", id, err)
		}

		w.Header().Set("HX-Push-Url", devicePath(id, data.Window))

		h.htmlWriter.Success(w, r, templatePartialDeviceTraffic, data)

		return nil
	}
}

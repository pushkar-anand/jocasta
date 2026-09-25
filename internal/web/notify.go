package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/pushkar-anand/build-with-go/http/response"
	"github.com/pushkar-anand/build-with-go/logger"
	"github.com/pushkar-anand/jocasta/internal/auth"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
	"github.com/pushkar-anand/jocasta/internal/notify"
)

// WithNotifier gives the notifications page the notifier and the destinations
// it sends to.
func WithNotifier(n *notify.Notifier) Option {
	return func(h *Handler) { h.notifier = n }
}

// kindChoice is one checkbox on a destination's card.
type kindChoice struct {
	Kind    dbtype.EventKind
	Label   string
	Checked bool
}

// destinationCard is one destination as the page shows it.
type destinationCard struct {
	Name  string
	Kind  string
	Host  string
	Kinds []kindChoice

	// LastAt is when the last message was sent since the server started, and
	// zero when none was. LastErr says why it failed, and is empty when it
	// arrived.
	LastAt  time.Time
	LastErr string
}

// notificationsData is the notifications settings page.
type notificationsData struct {
	view
	Destinations []destinationCard

	// Saved, Sent and Error are one-shot flashes from the POSTs, read by the
	// GET they redirect to.
	Saved string
	Sent  string
	Error string
}

const (
	flashNotifySaved = "flash.notify_saved"
	flashNotifySent  = "flash.notify_sent"
	flashNotifyError = "flash.notify_error"
)

// notifiable returns the kinds of change a scan can record, in the order
// dbtype.EventKinds lists them. An edit is the owner's own doing and is never
// sent, so it is not offered.
func notifiable() []dbtype.EventKind {
	return slices.DeleteFunc(slices.Clone(dbtype.EventKinds()), func(k dbtype.EventKind) bool {
		return k == dbtype.EventDeviceEdited
	})
}

// notifications serves the notifications settings page. The route is gated to
// an admin, so the page always renders in the admin view.
func (h *Handler) notifications(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		var cards []destinationCard

		for _, d := range h.destinations() {
			chosen, err := h.notifier.Rules(ctx, d.Name())
			if err != nil {
				return err
			}

			card := destinationCard{Name: d.Name(), Kind: string(d.Kind()), Host: d.Host()}

			for _, k := range notifiable() {
				card.Kinds = append(card.Kinds, kindChoice{
					Kind:    k,
					Label:   inventory.Label(k),
					Checked: slices.Contains(chosen, k),
				})
			}

			if last, ok := h.notifier.Last(d.Name()); ok {
				card.LastAt = last.At
				if last.Err != nil {
					card.LastErr = last.Err.Error()
				}
			}

			cards = append(cards, card)
		}

		h.htmlWriter.Success(w, r, templatePageNotifications, notificationsData{
			view: view{
				Title: "Notifications", Section: "Notifications", Role: dbtype.RoleAdmin,
				SignedInAs: sm.CurrentUsername(ctx),
			},
			Destinations: cards,
			Saved:        sm.PopFlash(ctx, flashNotifySaved),
			Sent:         sm.PopFlash(ctx, flashNotifySent),
			Error:        sm.PopFlash(ctx, flashNotifyError),
		})

		return nil
	}
}

// saveNotifications replaces the kinds of change one destination is sent.
func (h *Handler) saveNotifications(sm *auth.Session) response.HandlerFunc {
	type rulesForm struct {
		Kinds []string `schema:"kind"`
	}

	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		d, ok := h.destination(r.PathValue("name"))
		if !ok {
			return inventory.ErrNotFound
		}

		input, err := h.reader.ReadAndValidateForm[rulesForm](r)
		if err != nil {
			return err
		}

		// Only the kinds the page offers are kept. Its checkboxes submit
		// nothing else, so anything more came from a hand-made request and
		// dropping it loses no choice.
		var kinds []dbtype.EventKind

		for _, k := range notifiable() {
			if slices.Contains(input.Kinds, string(k)) {
				kinds = append(kinds, k)
			}
		}

		if err := h.notifier.SetRules(ctx, d.Name(), kinds); err != nil {
			return err
		}

		sm.Flash(ctx, flashNotifySaved, savedMessage(d.Name(), len(kinds)))
		http.Redirect(w, r, "/settings/notifications", http.StatusSeeOther)

		return nil
	}
}

// testNotification sends one destination a test message and says how it went.
func (h *Handler) testNotification(sm *auth.Session) response.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		ctx := r.Context()

		d, ok := h.destination(r.PathValue("name"))
		if !ok {
			return inventory.ErrNotFound
		}

		if err := h.notifier.SendTest(ctx, d); err != nil {
			h.log.WarnContext(ctx, "test notification failed", slog.String("destination", d.Name()), logger.Err(err))
			sm.Flash(ctx, flashNotifyError, failedTestMessage(d, err))
		} else {
			sm.Flash(ctx, flashNotifySent, "Sent a test to "+d.Name()+".")
		}

		http.Redirect(w, r, "/settings/notifications", http.StatusSeeOther)

		return nil
	}
}

// destinations returns the configured destinations, none when no notifier
// was given.
func (h *Handler) destinations() []*notify.Destination {
	if h.notifier == nil {
		return nil
	}

	return h.notifier.Destinations()
}

func (h *Handler) destination(name string) (*notify.Destination, bool) {
	if h.notifier == nil {
		return nil, false
	}

	return h.notifier.Destination(name)
}

// savedMessage names the destination saved and what it is now sent.
func savedMessage(name string, kinds int) string {
	switch kinds {
	case 0:
		return "Saved: " + name + " is sent nothing."
	case 1:
		return "Saved: " + name + " is sent 1 kind of change."
	}

	return fmt.Sprintf("Saved: %s is sent %d kinds of change.", name, kinds)
}

// failedTestMessage says what the service answered and which settings to
// check, which differ by the kind of destination.
func failedTestMessage(d *notify.Destination, err error) string {
	check := "url and secret"
	if d.Kind() == notify.KindNtfy {
		check = "url and token"
	}

	return fmt.Sprintf("The test did not reach %s (%s). Check its %s under notify in the config file.",
		d.Name(), err, check)
}

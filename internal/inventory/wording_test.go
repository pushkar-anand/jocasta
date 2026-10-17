package inventory

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

func TestPhrase(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "was discovered", Phrase(dbtype.EventDeviceDiscovered))
	assert.Equal(t, "was identified", Phrase(dbtype.EventDeviceIdentified))
	assert.Equal(t, "changed its hostname", Phrase(dbtype.EventHostnameChanged))
	assert.Equal(t, "was edited", Phrase(dbtype.EventDeviceEdited))
	assert.Equal(t, "got a new address", Phrase(dbtype.EventAddressAdded))
	assert.Equal(t, "dropped an address", Phrase(dbtype.EventAddressReleased))
	assert.Equal(t, "started listening on", Phrase(dbtype.EventPortOpened))
	assert.Equal(t, "stopped listening on", Phrase(dbtype.EventPortClosed))

	// events.kind has no CHECK. So a kind added in Go without a phrase here
	// must still render. Its own name is the most true fallback.
	assert.Equal(t, "group assigned", Phrase(dbtype.EventKind("GROUP_ASSIGNED")))
}

func TestChange(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "old → new", (&Event{OldValue: "old", NewValue: "new"}).Change())
	assert.Equal(t, "192.0.2.10", (&Event{NewValue: "192.0.2.10"}).Change())
	assert.Equal(t, "device 2 folded into 1", (&Event{Detail: "device 2 folded into 1"}).Change())

	// A discovery changed nothing. It is the event itself.
	assert.Empty(t, (&Event{}).Change())

	// A port event names the port. If the port is a known port, it also names
	// the service, from the value that the change wrote.
	assert.Equal(t, "port 22 (ssh)",
		(&Event{Kind: dbtype.EventPortOpened, NewValue: "22", Detail: "ssh"}).Change())
	assert.Equal(t, "port 44321",
		(&Event{Kind: dbtype.EventPortClosed, OldValue: "44321"}).Change())

	// An edit names the field, because the user owns several fields.
	edit := &Event{Kind: dbtype.EventDeviceEdited, Detail: "label"}

	edit.NewValue = "Office printer"
	assert.Equal(t, "label: Office printer", edit.Change())

	edit.OldValue = "Printer"
	assert.Equal(t, "label: Printer → Office printer", edit.Change())

	// An emptied field shows as a change to nothing.
	edit.NewValue = ""
	assert.Equal(t, "label: Printer → cleared", edit.Change())

	// A type that the user set shows the name of the class, as a new
	// classification does.
	typed := &Event{Kind: dbtype.EventDeviceEdited, Detail: "type", NewValue: "game_console"}
	assert.Equal(t, "type: Game console", typed.Change())

	typed.OldValue, typed.NewValue = "game_console", ""
	assert.Equal(t, "type: Game console → cleared", typed.Change())

	// A released address has its own name.
	assert.Equal(t, "192.0.2.55",
		(&Event{Kind: dbtype.EventAddressReleased, OldValue: "192.0.2.55", Detail: "unanswered"}).Change())

	// A new classification shows the names from the device page.
	assert.Equal(t, "Smart-home hub → Camera",
		(&Event{Kind: dbtype.EventDeviceClassified, OldValue: "iot_hub", NewValue: "camera"}).Change())
}

// Every kind that the schema knows has text for the log and for the settings
// page. The fallbacks are for kinds added in Go with no text yet. No release
// must use them.
func TestEveryKindIsWorded(t *testing.T) {
	t.Parallel()

	for _, k := range dbtype.EventKinds() {
		fallback := strings.ToLower(strings.ReplaceAll(string(k), "_", " "))

		assert.NotEqualf(t, fallback, Phrase(k), "kind %s has no phrase", k)
		assert.NotEqualf(t, Phrase(k), Label(k), "kind %s has no label", k)
	}

	assert.Equal(t, "group assigned", Label(dbtype.EventKind("GROUP_ASSIGNED")))
}

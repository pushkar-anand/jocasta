package notify

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// KindWebhook is any URL that takes a signed JSON POST.
const KindWebhook Kind = "webhook"

// The headers every webhook request carries.
const (
	// HeaderSignature is "sha256=" and the hex HMAC-SHA256 of the request
	// body, keyed with the destination's secret.
	HeaderSignature = "X-Jocasta-Signature-256"

	// HeaderDelivery is a random id per request, for a receiver to log and
	// to drop a delivery it has already handled.
	HeaderDelivery = "X-Jocasta-Delivery"
)

// Webhook posts each message as JSON with a title, a message and the changes
// it lists, signed with a shared secret so the receiver can tell it came from
// this server.
type Webhook struct {
	URL string `koanf:"url"`

	// Secret keys the signature. It is required: an unsigned request could
	// have come from anyone.
	Secret string `koanf:"secret"`
}

// Validate reports a URL that is not an absolute http or https address, and a
// missing secret.
func (w *Webhook) Validate() error {
	if _, err := parseURL(w.URL); err != nil {
		return err
	}

	if w.Secret == "" {
		return errors.New("a secret is required, to sign each request")
	}

	return nil
}

// Host is the webhook's host.
func (w *Webhook) Host() string {
	u, err := parseURL(w.URL)
	if err != nil {
		return ""
	}

	return u.Host
}

// Send posts m, signed.
func (w *Webhook) Send(ctx context.Context, m Message) error {
	raw, err := json.Marshal(webhookBody(m))
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	header := http.Header{}
	header.Set(HeaderSignature, Sign(w.Secret, raw))
	header.Set(HeaderDelivery, rand.Text())

	return post(ctx, w.URL, header, raw)
}

// Sign returns the signature header's value for body: "sha256=" and the hex
// HMAC-SHA256 of body keyed with secret. A receiver recomputes it over the raw
// body it was sent and compares the two in constant time.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)

	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// webhookBody is the message, and the changes as data for a receiver that
// acts on one device.
func webhookBody(m Message) any {
	type change struct {
		Kind     dbtype.EventKind `json:"kind"`
		DeviceID int64            `json:"device_id,omitempty"`
		Device   string           `json:"device,omitempty"`
		Change   string           `json:"change,omitempty"`
	}

	changes := make([]change, 0, len(m.Events))
	for _, e := range m.Events {
		changes = append(changes, change{Kind: e.Kind, DeviceID: e.DeviceID, Device: e.DeviceName, Change: e.Change()})
	}

	return struct {
		Title   string   `json:"title"`
		Message string   `json:"message"`
		ScanID  int64    `json:"scan_id,omitempty"`
		Events  []change `json:"events"`
	}{m.Title, m.Body, m.ScanID, changes}
}

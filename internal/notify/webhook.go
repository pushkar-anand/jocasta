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

// KindWebhook is any URL that accepts a signed JSON POST.
const KindWebhook Kind = "webhook"

// The headers of every webhook request.
const (
	// HeaderSignature is "sha256=", then the hex HMAC-SHA256 of the request
	// body. The key is the secret of the destination.
	HeaderSignature = "X-Jocasta-Signature-256"

	// HeaderDelivery is a random id for each request. A receiver logs it. It
	// also uses it to discard a delivery that it already handled.
	HeaderDelivery = "X-Jocasta-Delivery"
)

// Webhook posts each message as JSON, with a title, a message and its changes.
// It signs the message with a shared secret. So the receiver can tell that it
// came from this server.
type Webhook struct {
	URL string `koanf:"url"`

	// Secret is the key of the signature. It is required, because a request
	// without a signature could come from anyone.
	Secret string `koanf:"secret"`
}

// Validate reports a URL that is not an absolute http or https address. It
// also reports a missing secret.
func (w *Webhook) Validate() error {
	if _, err := parseURL(w.URL); err != nil {
		return err
	}

	if w.Secret == "" {
		return errors.New("a secret is required, to sign each request")
	}

	return nil
}

// Host is the host of the webhook.
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

// Sign returns the value of the signature header for body. This is "sha256=",
// then the hex HMAC-SHA256 of body with secret as key. A receiver computes it
// again over the raw body that it got. It compares the two in constant time.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)

	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// change is one change as data, for a receiver that acts on one device. The
// webhook sends it as JSON. The template of the http provider reads it.
type change struct {
	Kind     dbtype.EventKind `json:"kind"`
	DeviceID int64            `json:"device_id,omitempty"`
	Device   string           `json:"device,omitempty"`
	Change   string           `json:"change,omitempty"`
}

// changes returns the events of m as data.
func changes(m Message) []change {
	out := make([]change, 0, len(m.Events))
	for _, e := range m.Events {
		out = append(out, change{Kind: e.Kind, DeviceID: e.DeviceID, Device: e.DeviceName, Change: e.Change()})
	}

	return out
}

// webhookBody is the message, with the changes as data.
func webhookBody(m Message) any {
	return struct {
		Title   string   `json:"title"`
		Message string   `json:"message"`
		ScanID  int64    `json:"scan_id,omitempty"`
		Events  []change `json:"events"`
	}{m.Title, m.Body, m.ScanID, changes(m)}
}

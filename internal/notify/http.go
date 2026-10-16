package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"text/template"

	"golang.org/x/net/http/httpguts"

	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
	"github.com/pushkar-anand/jocasta/internal/inventory"
)

// KindHTTP is any service that accepts a POST with a body from a template,
// such as Discord, Slack, Telegram, Gotify or an Apprise API server.
const KindHTTP Kind = "http"

// HTTP posts each message with a body that the owner writes as a template. So
// one provider reaches every service that has its own request shape. Its
// requests have no signature. A receiver that checks the origin of a request
// uses the [Webhook], which signs each request.
type HTTP struct {
	// URL is where the request goes. It can contain the token of the
	// service, as for Discord and Telegram. Jocasta never logs or shows it.
	URL string `koanf:"url"`

	// Headers go with every request, such as an Authorization header. A
	// Content-Type in them replaces the default, application/json.
	Headers map[string]string `koanf:"headers"`

	// Body is a text/template of the request body. It gets .Title, .Body,
	// .ScanID and .Events. Each event has .Kind, .DeviceID, .Device and
	// .Change. Its json function writes a value as JSON. So
	// {{ json .Title }} is a string with quotes and escapes. truncate n cuts
	// a string to at most n characters, for a service with a length limit.
	Body string `koanf:"body"`

	// tmpl is Body, after Validate parsed it. Send executes only tmpl. So a
	// destination sends only a template that passed validation.
	tmpl *template.Template
}

// templateData is the data of the body template.
type templateData struct {
	Title  string
	Body   string
	ScanID int64
	Events []change
}

// samples render when Jocasta reads the config. So Jocasta reports a template
// that fails or writes broken JSON at startup, before a scan has anything to
// send. The first sample has the quotes and line breaks of real messages. The
// second is the test message of the settings page, which has no events. So a
// template that indexes .Events fails here, not on the first test send.
var samples = []Message{
	{
		Title:  `2 changes on "lab"`,
		Body:   "host-a \\ 192.0.2.10\nhost-b <new>",
		ScanID: 1,
		Events: []*inventory.Event{
			{Kind: dbtype.EventDeviceDiscovered, DeviceID: 1, DeviceName: `host-a "tv"`, NewValue: "192.0.2.10"},
		},
	},
	Test(),
}

// Validate reports these problems:
//   - a URL that is not an absolute http or https address
//   - a header that cannot be sent
//   - a missing body, or a body that does not parse or render
//   - a JSON body that does not render as JSON
//
// On success, it keeps the parsed body, which Send executes.
func (h *HTTP) Validate() error {
	if _, err := parseURL(h.URL); err != nil {
		return err
	}

	for k, v := range h.Headers {
		if !httpguts.ValidHeaderFieldName(k) {
			return fmt.Errorf("header %q has a character HTTP does not allow in a name; use letters, digits and hyphens", k)
		}

		if !httpguts.ValidHeaderFieldValue(v) {
			return fmt.Errorf("header %q has a line break or control character in its value; remove it", k)
		}
	}

	if strings.TrimSpace(h.Body) == "" {
		return errors.New("a body is required; see the recipes in docs/notifications/other-services.md")
	}

	t, err := template.New("body").
		Funcs(template.FuncMap{"json": toJSON, "truncate": truncate}).
		Parse(h.Body)
	if err != nil {
		return fmt.Errorf("the body does not parse: %w", err)
	}

	for _, m := range samples {
		raw, err := render(t, m)
		if err != nil {
			return err
		}

		if h.sendsJSON() && !json.Valid(raw) {
			return errors.New("the body does not render as JSON; write each value with the json function, as {{ json .Title }}")
		}
	}

	h.tmpl = t

	return nil
}

// Host is the host of the service.
func (h *HTTP) Host() string {
	u, err := parseURL(h.URL)
	if err != nil {
		return ""
	}

	return u.Host
}

// Send posts m, with the body from the template. It returns an error if
// Validate did not pass.
func (h *HTTP) Send(ctx context.Context, m Message) error {
	if h.tmpl == nil {
		return errors.New("the destination was not validated")
	}

	raw, err := render(h.tmpl, m)
	if err != nil {
		return err
	}

	header := http.Header{}
	for k, v := range h.Headers {
		header.Set(k, v)
	}

	return post(ctx, h.URL, header, raw)
}

// render executes the body template t over m. An error names the problem with
// the template, and never the values that it got.
func render(t *template.Template, m Message) ([]byte, error) {
	var buf bytes.Buffer

	data := templateData{Title: m.Title, Body: m.Body, ScanID: m.ScanID, Events: changes(m)}
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("the body does not render: %w", err)
	}

	return buf.Bytes(), nil
}

// sendsJSON reports whether the body goes as JSON. This is true when no header
// names a different Content-Type.
func (h *HTTP) sendsJSON() bool {
	for k, v := range h.Headers {
		if http.CanonicalHeaderKey(k) != "Content-Type" {
			continue
		}

		mt, _, err := mime.ParseMediaType(v)

		return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
	}

	return true
}

// toJSON writes v as JSON, for the json function of the template.
func toJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	return string(raw), nil
}

// truncate cuts s to at most n characters, for the truncate function of the
// template. If it cuts s, it adds an ellipsis at the end.
func truncate(n int, s string) string {
	r := []rune(s)
	if n < 1 || len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}

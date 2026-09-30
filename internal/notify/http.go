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

// KindHTTP is any service that takes a POST whose body a template shapes,
// such as Discord, Slack, Telegram, Gotify or an Apprise API server.
const KindHTTP Kind = "http"

// HTTP posts each message with a body the owner writes as a template, so one
// provider reaches every service that takes a request in a shape of its own.
// Its requests are unsigned. A receiver that checks where a request came from
// uses the [Webhook], which signs each one.
type HTTP struct {
	// URL is where the request goes. It can hold the service's token, as
	// Discord's and Telegram's do; it is never logged or shown.
	URL string `koanf:"url"`

	// Headers are sent with every request, such as an Authorization header.
	// A Content-Type among them replaces the default, application/json.
	Headers map[string]string `koanf:"headers"`

	// Body is a text/template of the request body. It is given .Title,
	// .Body, .ScanID and .Events, each event with .Kind, .DeviceID, .Device
	// and .Change. Its json function writes a value as JSON, so
	// {{ json .Title }} is a quoted, escaped string, and truncate n cuts a
	// string to at most n characters, for a service that caps its length.
	Body string `koanf:"body"`

	// tmpl is Body, parsed by Validate. Send executes it and nothing else, so
	// a destination sends only a template that passed validation.
	tmpl *template.Template
}

// templateData is what the body template is given.
type templateData struct {
	Title  string
	Body   string
	ScanID int64
	Events []change
}

// samples are rendered when the config is read, so a template that fails or
// writes broken JSON is reported at startup, before a scan has anything to
// send. The first carries the quotes and line breaks real messages carry. The
// second is the settings page's test message, which has no events, so a
// template that indexes .Events fails here and not on the first test send.
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

// Validate reports a URL that is not an absolute http or https address, a
// header that cannot be sent, a missing body, a body that does not parse or
// render, and a JSON body that renders as something other than JSON. On
// success it keeps the parsed body, which Send executes.
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
		return errors.New("a body is required; see the recipes in docs/setup.md")
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

// Host is the service's host.
func (h *HTTP) Host() string {
	u, err := parseURL(h.URL)
	if err != nil {
		return ""
	}

	return u.Host
}

// Send posts m, shaped by the body template. It returns an error when
// Validate has not passed.
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

// render executes the body template t over m. An error names what is wrong
// with the template and never the values it was given.
func render(t *template.Template, m Message) ([]byte, error) {
	var buf bytes.Buffer

	data := templateData{Title: m.Title, Body: m.Body, ScanID: m.ScanID, Events: changes(m)}
	if err := t.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("the body does not render: %w", err)
	}

	return buf.Bytes(), nil
}

// sendsJSON reports whether the body is sent as JSON: when no header names
// another Content-Type.
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

// toJSON writes v as JSON, for the template's json function.
func toJSON(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}

	return string(raw), nil
}

// truncate cuts s to at most n characters, ending it with an ellipsis when it
// is cut, for the template's truncate function.
func truncate(n int, s string) string {
	r := []rune(s)
	if n < 1 || len(r) <= n {
		return s
	}

	return string(r[:n-1]) + "…"
}

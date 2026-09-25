package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// KindNtfy is an ntfy topic.
const KindNtfy Kind = "ntfy"

// Ntfy publishes to one topic on an ntfy server.
type Ntfy struct {
	// URL is the topic's address as ntfy shows it, such as
	// https://ntfy.sh/jocasta: the server, then the topic.
	URL string `koanf:"url"`

	// Token is an access token, for a server that requires one.
	Token string `koanf:"token"`

	// Priority is ntfy's 1 (min) to 5 (max). Zero leaves it to the server.
	Priority int `koanf:"priority"`
}

// Validate reports a URL that is not an absolute http or https address or
// names no topic, and a priority outside 0 to 5.
func (n *Ntfy) Validate() error {
	if _, _, err := n.split(); err != nil {
		return err
	}

	if n.Priority < 0 || n.Priority > 5 {
		return fmt.Errorf("priority %d is outside 1 to 5", n.Priority)
	}

	return nil
}

// Host is the ntfy server's host.
func (n *Ntfy) Host() string {
	server, _, _ := n.split()
	if server == nil {
		return ""
	}

	return server.Host
}

// Send publishes m to the topic.
func (n *Ntfy) Send(ctx context.Context, m Message) error {
	server, topic, err := n.split()
	if err != nil {
		return err
	}

	body := map[string]any{"topic": topic, "title": m.Title, "message": m.Body}
	if n.Priority != 0 {
		body["priority"] = n.Priority
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode message: %w", err)
	}

	header := http.Header{}
	if n.Token != "" {
		header.Set("Authorization", "Bearer "+n.Token)
	}

	// ntfy takes a title alongside the message only when the message is
	// published as JSON, and JSON is published to the server's root with the
	// topic in the body.
	return post(ctx, server.String(), header, raw)
}

// split parts the topic URL into the server's root and the topic.
func (n *Ntfy) split() (*url.URL, string, error) {
	u, err := parseURL(n.URL)
	if err != nil {
		return nil, "", err
	}

	path := strings.Trim(u.Path, "/")

	i := strings.LastIndex(path, "/")
	topic := path[i+1:]

	if topic == "" {
		return nil, "", errors.New("the url names no topic; add it after the server, as ntfy shows it")
	}

	server := *u
	server.Path = "/" + path[:i+1]
	server.RawQuery = ""

	return &server, topic, nil
}

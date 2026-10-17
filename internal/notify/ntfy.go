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
	// URL is the address of the topic as ntfy shows it, such as
	// https://ntfy.sh/jocasta. It is the server, then the topic.
	URL string `koanf:"url"`

	// Token is an access token, for a server that requires one.
	Token string `koanf:"token"`

	// Priority is the ntfy priority, from 1 (min) to 5 (max). With zero, the
	// server sets it.
	Priority int `koanf:"priority"`
}

// Validate reports a URL that is not an absolute http or https address, or
// that names no topic. It also reports a priority outside 0 to 5.
func (n *Ntfy) Validate() error {
	if _, _, err := n.split(); err != nil {
		return err
	}

	if n.Priority < 0 || n.Priority > 5 {
		return fmt.Errorf("priority %d is outside 0 to 5. Use 0 to leave it to the server", n.Priority)
	}

	return nil
}

// Host is the host of the ntfy server.
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

	// ntfy accepts a title with the message only when the message is JSON.
	// JSON goes to the root of the server, with the topic in the body.
	return post(ctx, server.String(), header, raw)
}

// split separates the topic URL into the root of the server and the topic.
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

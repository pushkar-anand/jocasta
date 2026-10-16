// Package notify sends the changes of each finished scan to where the owner
// reads them.
//
// Each service is a [Provider] in its own file. A new service needs that file,
// a field on [Config], and a line in its provider method. Most services accept
// a JSON POST. The [HTTP] provider reaches them with a template. A service
// gets its own provider only when [HTTP] cannot reach it.
package notify

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultTimeout limits one send when a destination names no timeout. So a
// service that stops answering delays only one message.
const DefaultTimeout = 10 * time.Second

// Kind names the service of a destination, such as "ntfy". It is the key of
// the block of the service in the config file.
type Kind string

// Provider delivers messages to one service.
type Provider interface {
	// Validate reports the problem with the config of the service. It returns
	// nil when the config is usable.
	Validate() error

	// Host names where messages go. It is safe to show, because it has no
	// path, query or credentials.
	Host() string

	// Send delivers m before the deadline of ctx. An error must not contain a
	// credential, because Jocasta logs and shows it.
	Send(ctx context.Context, m Message) error
}

// Config is the block of one destination in the config file. It has the
// options of every destination, and a block for its one service.
type Config struct {
	// Enabled turns the destination off when false. If the field is not
	// set, the destination is on.
	Enabled *bool `koanf:"enabled"`

	// Timeout limits one send. Zero means DefaultTimeout.
	Timeout time.Duration `koanf:"timeout"`

	Ntfy    *Ntfy    `koanf:"ntfy"`
	Webhook *Webhook `koanf:"webhook"`
	HTTP    *HTTP    `koanf:"http"`
}

// On reports whether the destination is enabled.
func (c Config) On() bool {
	return c.Enabled == nil || *c.Enabled
}

// provider returns the one service that the block names. It returns an error
// when the block names none, or more than one.
func (c Config) provider() (Kind, Provider, error) {
	var (
		kinds     []Kind
		providers []Provider
	)

	add := func(k Kind, p Provider) {
		kinds = append(kinds, k)
		providers = append(providers, p)
	}

	if c.Ntfy != nil {
		add(KindNtfy, c.Ntfy)
	}

	if c.Webhook != nil {
		add(KindWebhook, c.Webhook)
	}

	if c.HTTP != nil {
		add(KindHTTP, c.HTTP)
	}

	switch len(providers) {
	case 0:
		return "", nil, errors.New("names no service; add an ntfy, webhook or http block")
	case 1:
		return kinds[0], providers[0], nil
	}

	return "", nil, fmt.Errorf("names more than one service: %v", kinds)
}

// Destination is one configured place to send messages.
type Destination struct {
	name     string
	kind     Kind
	provider Provider
	timeout  time.Duration
}

// NewDestination returns the destination with the name name, built from its
// config block c. It returns an error when c does not name exactly one
// service, or the config of the service is not usable.
func NewDestination(name string, c Config) (*Destination, error) {
	kind, p, err := c.provider()
	if err != nil {
		return nil, fmt.Errorf("notify %q: %w", name, err)
	}

	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("notify %q: %s: %w", name, kind, err)
	}

	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	return &Destination{name: name, kind: kind, provider: p, timeout: timeout}, nil
}

// Name is the key of the destination in the config file. It identifies the
// destination.
func (d *Destination) Name() string { return d.name }

// Kind is the service of the destination.
func (d *Destination) Kind() Kind { return d.kind }

// Host is where the destination delivers. It is safe to show.
func (d *Destination) Host() string { return d.provider.Host() }

// Send delivers m within the timeout of the destination.
func (d *Destination) Send(ctx context.Context, m Message) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	return d.provider.Send(ctx, m)
}

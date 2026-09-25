// Package notify sends what each finished scan changed to where the owner
// reads it.
//
// Each service is a [Provider] in a file of its own. Adding one takes that
// file, a field on [Config] and a line in its provider method.
package notify

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultTimeout bounds one send when a destination names no timeout of its
// own, so a service that stops answering holds up one message and no more.
const DefaultTimeout = 10 * time.Second

// Kind names the service a destination delivers to, such as "ntfy". It is
// the key of the service's block in the config file.
type Kind string

// Provider delivers messages to one service.
type Provider interface {
	// Validate reports what is wrong with the service's config, and nil when
	// it can be used.
	Validate() error

	// Host names where messages go, safe to show: no path, query or
	// credentials.
	Host() string

	// Send delivers m before ctx's deadline. An error must not carry a
	// credential, since it is logged and shown.
	Send(ctx context.Context, m Message) error
}

// Config is one destination's block in the config file: the options every
// destination has, and a block for the one service it delivers to.
type Config struct {
	// Enabled turns the destination off when false. A destination is on when
	// the field is left out.
	Enabled *bool `koanf:"enabled"`

	// Timeout bounds one send. Zero means DefaultTimeout.
	Timeout time.Duration `koanf:"timeout"`

	Ntfy    *Ntfy    `koanf:"ntfy"`
	Webhook *Webhook `koanf:"webhook"`
}

// On reports whether the destination is enabled.
func (c Config) On() bool {
	return c.Enabled == nil || *c.Enabled
}

// provider returns the one service the block names. It returns an error when
// the block names none, or more than one.
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

	switch len(providers) {
	case 0:
		return "", nil, errors.New("names no service; add an ntfy or a webhook block")
	case 1:
		return kinds[0], providers[0], nil
	}

	return "", nil, fmt.Errorf("names more than one service: %v", kinds)
}

// Destination is one configured place messages are sent.
type Destination struct {
	name     string
	kind     Kind
	provider Provider
	timeout  time.Duration
}

// NewDestination returns the destination called name, built from its config
// block c. It returns an error when c does not name exactly one service, or
// the service's config cannot be used.
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

// Name is the destination's key in the config file, which identifies it.
func (d *Destination) Name() string { return d.name }

// Kind is the service the destination delivers to.
func (d *Destination) Kind() Kind { return d.kind }

// Host is where the destination delivers to, safe to show.
func (d *Destination) Host() string { return d.provider.Host() }

// Send delivers m within the destination's timeout.
func (d *Destination) Send(ctx context.Context, m Message) error {
	ctx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	return d.provider.Send(ctx, m)
}

package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"

	"github.com/pushkar-anand/jocasta/internal/classify"
	"github.com/pushkar-anand/jocasta/internal/db/dbtype"
)

// ClassExplanation is the case behind a device's class: the facts the
// classifier was given, the rule it chose, and every other rule that matched.
type ClassExplanation struct {
	// Class is the device's effective class: Type when the owner set one, and
	// Guess otherwise.
	Class classify.Class `json:"class,omitempty"`

	// Type is the class the owner set, and Overridden reports whether they set
	// one. Guess and the rules are what the classifier says either way.
	Type       classify.Class `json:"type,omitempty"`
	Overridden bool           `json:"overridden"`

	// Guess and Confidence are the classifier's answer, both empty when no
	// rule matched.
	Guess      classify.Class      `json:"guess,omitempty"`
	Confidence classify.Confidence `json:"confidence,omitempty"`

	Facts ClassFacts `json:"facts"`

	// Rule is the rule that decided Guess, or nil when no rule matched.
	Rule *ClassRule `json:"rule,omitempty"`

	// OtherRules are the rules that matched and lost, the most specific first.
	// One naming the class Rule names is what raises the confidence.
	OtherRules []ClassRule `json:"other_rules"`
}

// ClassFacts are the facts the classifier reasons over, as the inventory
// records them. An empty field is a fact the inventory does not have.
type ClassFacts struct {
	Vendor         string                `json:"vendor,omitempty"`
	Hostname       string                `json:"hostname,omitempty"`
	HostnameSource dbtype.HostnameSource `json:"hostname_source,omitempty"`
	Randomised     bool                  `json:"randomised"`

	// Network is the name of one network the device holds an address on.
	Network string `json:"network,omitempty"`

	Addresses []netip.Addr `json:"addresses"`  // held now
	OpenPorts []uint16     `json:"open_ports"` // open now, in number order

	// Services are the DNS-SD service types the device advertised within the
	// history window, lowercased and sorted, and Models the models they give.
	Services []string `json:"services"`
	Models   []string `json:"models,omitempty"`
}

// ClassRule is one classification rule that matched a device.
type ClassRule struct {
	Class  classify.Class `json:"class"`
	Reason string         `json:"reason"`

	// Conditions is how many facts the rule tested. The rule testing the most
	// wins, and a tie goes to the rule listed first.
	Conditions int `json:"conditions"`

	// Weak marks a loose fallback, such as a vendor that makes many kinds of
	// device. A weak rule that matches alone gives a low-confidence guess.
	Weak bool `json:"weak"`
}

// ExplainClass returns the case behind the class of device id. It runs the
// classifier over what the inventory records now, so the guess matches the
// device's recorded one unless something changed since a scan last touched it.
//
// A device that does not exist is an error wrapping [ErrNotFound].
func (s *Store) ExplainClass(ctx context.Context, id int64) (*ClassExplanation, error) {
	row, err := s.q.GetDevice(ctx, id)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("device %d: %w", id, ErrNotFound)
	case err != nil:
		return nil, fmt.Errorf("device %d: %w", id, err)
	}

	in, err := classifyInput(ctx, s.q, row)
	if err != nil {
		return nil, err
	}

	got := classify.Explain(in)

	out := &ClassExplanation{
		Guess:      got.Result.Class,
		Confidence: got.Result.Confidence,
		Facts: ClassFacts{
			Vendor:         in.Vendor,
			Hostname:       in.Hostname,
			HostnameSource: row.HostnameSource,
			Randomised:     in.Randomised,
			Network:        in.NetworkName,
			Addresses:      in.Addresses,
			OpenPorts:      got.Facts.Ports,
			Services:       got.Facts.Services,
			Models:         got.Facts.Models,
		},
		OtherRules: []ClassRule{},
	}

	// A type this build does not know is no override, as newDevice reads it.
	out.Class = out.Guess
	if override := classify.Class(row.DeviceType.String); override.Known() {
		out.Class, out.Type, out.Overridden = override, override, true
	}

	for _, m := range got.Matches {
		r := ClassRule{Class: m.Class, Reason: m.Reason, Conditions: m.Conds, Weak: m.Weak}

		if m.Winner {
			out.Rule = &r
			continue
		}

		out.OtherRules = append(out.OtherRules, r)
	}

	return out, nil
}

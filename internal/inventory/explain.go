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

// ClassExplanation is the case of the class of a device: the facts of the
// classifier, the rule that it chose, and every other rule that matched.
type ClassExplanation struct {
	// Class is the effective class of the device. It is Type when the owner set
	// one. Else, it is Guess.
	Class classify.Class `json:"class,omitempty"`

	// Type is the class that the owner set. Overridden reports whether the
	// owner set one. Guess and the rules are what the classifier says in both
	// cases.
	Type       classify.Class `json:"type,omitempty"`
	Overridden bool           `json:"overridden"`

	// Guess and Confidence are the answer of the classifier. Both are empty
	// when no rule matched.
	Guess      classify.Class      `json:"guess,omitempty"`
	Confidence classify.Confidence `json:"confidence,omitempty"`

	Facts ClassFacts `json:"facts"`

	// Rule is the rule that decided Guess. It is nil when no rule matched.
	Rule *ClassRule `json:"rule,omitempty"`

	// OtherRules are the rules that matched and lost, the most specific first.
	// A rule for the same class as Rule raises the confidence.
	OtherRules []ClassRule `json:"other_rules"`
}

// ClassFacts are the facts of the classifier, as the inventory records them. An
// empty field is a fact that the inventory does not have.
type ClassFacts struct {
	VendorShort    string                `json:"vendor_short,omitempty"`
	Hostname       string                `json:"hostname,omitempty"`
	HostnameSource dbtype.HostnameSource `json:"hostname_source,omitempty"`
	Randomised     bool                  `json:"randomised"`

	// Network is the name of one network where the device has an address.
	Network string `json:"network,omitempty"`

	Addresses []netip.Addr `json:"addresses"`  // held now
	OpenPorts []uint16     `json:"open_ports"` // open now, in number order

	// Services are the DNS-SD service types that the device advertised within
	// the history window, lowercase and sorted. Models are their models.
	Services []string `json:"services"`
	Models   []string `json:"models,omitempty"`
}

// ClassRule is one classification rule that matched a device.
type ClassRule struct {
	Class  classify.Class `json:"class"`
	Reason string         `json:"reason"`

	// Conditions is how many facts the rule tested. The rule that tests the
	// most wins. A tie goes to the rule listed first.
	Conditions int `json:"conditions"`

	// Weak marks a loose fallback, such as a vendor that makes many kinds of
	// device. A weak rule that matches alone gives a guess with low
	// confidence.
	Weak bool `json:"weak"`
}

// ExplainClass returns the case of the class of device id. It runs the
// classifier over what the inventory records now. So the guess matches the
// recorded guess, unless something changed after the last scan of the device.
//
// A device that does not exist is an error that wraps [ErrNotFound].
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
			VendorShort:    in.Vendor,
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

	// A type that this build does not know is no override, as for newDevice.
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

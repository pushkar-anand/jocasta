package classify

// This file is a debugging aid for tuning the rules against real data. It
// reports every rule that matched an input and which one [Device] picked.
// Nothing in production depends on it.

import "sort"

// RuleMatch is one rule that fired for an input.
type RuleMatch struct {
	Class  Class
	Reason string
	Conds  int // how many conditions it tested (its specificity)
	Weak   bool
	Winner bool
}

// Explanation is the full case behind a [Device] call.
type Explanation struct {
	Facts   Facts
	Result  Result
	Matches []RuleMatch // every rule that fired, most specific first
}

// Explain runs the same pipeline as [Device] and returns everything that
// matched, not just the winner.
func Explain(in Input) Explanation {
	f := facts(in)
	res := Device(in)

	winIdx := -1
	winConds := -1

	for i := range ruleset {
		if conds, ok := match(ruleset[i].Cond, f); ok && conds > winConds {
			winIdx, winConds = i, conds
		}
	}

	var matches []RuleMatch

	for i := range ruleset {
		conds, ok := match(ruleset[i].Cond, f)
		if !ok {
			continue
		}

		matches = append(matches, RuleMatch{
			Class:  ruleset[i].Class,
			Reason: ruleset[i].reason(f),
			Conds:  conds,
			Weak:   ruleset[i].Weak,
			Winner: i == winIdx,
		})
	}

	sort.SliceStable(matches, func(a, b int) bool { return matches[a].Conds > matches[b].Conds })

	return Explanation{Facts: f, Result: res, Matches: matches}
}

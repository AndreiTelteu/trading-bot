package backtest

import (
	"fmt"
	"strings"
)

// ReplayMembershipPolicy is the immutable Stage 04-to-replay eligibility
// contract. Active members are always tradable. Shortlist members are tradable
// only when the strategy explicitly opts into the shortlist.
type ReplayMembershipPolicy struct {
	IncludeShortlist bool `json:"include_shortlist"`
}

func replayMemberEligible(stage string, shortlisted bool, rejected bool, policy ReplayMembershipPolicy) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(stage)) {
	case "active":
		return !rejected, nil
	case "shortlist":
		return policy.IncludeShortlist && shortlisted && !rejected, nil
	case "ranked", "rejected":
		return false, nil
	default:
		return false, fmt.Errorf("unknown persisted universe stage %q", stage)
	}
}

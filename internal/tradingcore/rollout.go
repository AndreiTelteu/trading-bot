package tradingcore

import "strings"

type ModelAuthority struct {
	UseModel, Observe, Blocked bool
	Reason                     string
}

// ResolveModelAuthority is deliberately mode-aware. Artifact presence never
// grants authority, and research/shadow contexts can only observe.
func ResolveModelAuthority(mode ExecutionMode, rolloutState, fallback string, modelAvailable bool) ModelAuthority {
	state := strings.ToLower(strings.TrimSpace(rolloutState))
	fallback = strings.ToLower(strings.TrimSpace(fallback))
	result := ModelAuthority{Observe: modelAvailable}
	authorized := false
	switch mode {
	case ExecutionResearch, ExecutionShadow:
	case ExecutionPaper:
		authorized = state == "paper" || state == "limited_live" || state == "full_live"
	case ExecutionLimitedLive:
		authorized = state == "limited_live" || state == "full_live"
	case ExecutionFullLive:
		authorized = state == "full_live"
	}
	if authorized && modelAvailable {
		result.UseModel = true
		return result
	}
	if authorized && !modelAvailable && fallback != "rule_based" {
		result.Blocked = true
		result.Reason = "model_unavailable_no_fallback"
		return result
	}
	if !authorized && fallback != "rule_based" {
		result.Blocked = true
		result.Reason = "rollout_not_authorized_no_fallback"
	}
	return result
}

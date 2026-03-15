package services

import "testing"

func TestParseProposalsFromResponseWithTypeBacktest(t *testing.T) {
	settings := map[string]string{
		"stop_mult":                "1.5",
		"ai_max_proposals":         "5",
		"ai_change_budget_pct":     "50",
		"ai_max_keys_per_category": "2",
	}
	weights := map[string]float64{"rsi": 1.0}
	allowedKeys := map[string]bool{"stop_mult": true, "weight:rsi": true}
	categories := map[string]string{"stop_mult": "trading"}
	response := `[{"proposal_type":"backtest_parameter_adjustment","parameter_key":"stop_mult","old_value":"1.5","new_value":"1.8","reasoning":"Use a slightly wider ATR stop based on the backtest result."}]`

	proposals, err := parseProposalsFromResponseWithType(response, settings, weights, allowedKeys, categories, "backtest_parameter_adjustment")
	if err != nil {
		t.Fatalf("parseProposalsFromResponseWithType() error = %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("Expected 1 proposal, got %d", len(proposals))
	}
	if proposals[0].ProposalType != "backtest_parameter_adjustment" {
		t.Fatalf("Unexpected proposal type: %s", proposals[0].ProposalType)
	}
	if proposals[0].ParameterKey == nil || *proposals[0].ParameterKey != "stop_mult" {
		t.Fatalf("Unexpected parameter key: %v", proposals[0].ParameterKey)
	}
	if proposals[0].NewValue == nil || *proposals[0].NewValue != "1.8" {
		t.Fatalf("Unexpected new value: %v", proposals[0].NewValue)
	}
}

func TestParseProposalsFromResponseWithTypeDetailedReportsBudgetRejection(t *testing.T) {
	settings := map[string]string{
		"stop_mult":                "1.5",
		"ai_max_proposals":         "5",
		"ai_change_budget_pct":     "10",
		"ai_max_keys_per_category": "2",
	}
	weights := map[string]float64{"rsi": 1.0}
	allowedKeys := map[string]bool{"stop_mult": true, "weight:rsi": true}
	categories := map[string]string{"stop_mult": "trading"}
	response := `[{"proposal_type":"backtest_parameter_adjustment","parameter_key":"stop_mult","old_value":"1.5","new_value":"1.8","reasoning":"Too large for the configured budget."}]`

	result, err := parseProposalsFromResponseWithTypeDetailed(response, settings, weights, allowedKeys, categories, "backtest_parameter_adjustment")
	if err != nil {
		t.Fatalf("parseProposalsFromResponseWithTypeDetailed() error = %v", err)
	}
	if len(result.Proposals) != 0 {
		t.Fatalf("Expected 0 proposals, got %d", len(result.Proposals))
	}
	if !result.Diagnostics.FoundJSONArray {
		t.Fatal("Expected diagnostics to detect JSON array")
	}
	if result.Diagnostics.RawProposalCount != 1 {
		t.Fatalf("Expected 1 raw proposal, got %d", result.Diagnostics.RawProposalCount)
	}
	if result.Diagnostics.RejectedCounts["change_over_budget"] != 1 {
		t.Fatalf("Expected change_over_budget rejection, got %v", result.Diagnostics.RejectedCounts)
	}
}

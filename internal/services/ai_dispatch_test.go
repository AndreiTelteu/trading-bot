package services

import "testing"

func TestParseExperimentDraftResponse(t *testing.T) {
	drafts, err := parseExperimentDraftResponse(`prefix [{"name":"fast","hypothesis":"shorter lookback wins after costs","rationale":"isolates horizon","parameters":{"lookback_bars":"20"}},{"name":"slow","hypothesis":"slower lookback lowers turnover","rationale":"isolates horizon","parameters":{"lookback_bars":"60"}}] suffix`, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 2 || drafts[0].Parameters["lookback_bars"] != "20" || drafts[1].Name != "slow" {
		t.Fatalf("unexpected drafts: %#v", drafts)
	}
}

func TestParseExperimentDraftResponseRejectsWrongCountAndMissingArray(t *testing.T) {
	if _, err := parseExperimentDraftResponse(`[{"name":"one","parameters":{}}]`, 2); err == nil {
		t.Fatal("expected wrong-count response to fail")
	}
	if _, err := parseExperimentDraftResponse(`not json`, 1); err == nil {
		t.Fatal("expected missing array to fail")
	}
}

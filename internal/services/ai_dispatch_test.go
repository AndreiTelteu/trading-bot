package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"trading-go/internal/database"
)

func TestParseExperimentDraftResponse(t *testing.T) {
	drafts, err := parseExperimentDraftResponse(`prefix [{"name":"fast","hypothesis":"shorter lookback wins after costs","rationale":"isolates horizon","parameters":{"lookback_bars":"20"}},{"name":"slow","hypothesis":"slower lookback lowers turnover","rationale":"isolates horizon","parameters":{"lookback_bars":"60"}}] suffix`, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 2 || drafts[0].Parameters["lookback_bars"] != "20" || drafts[1].Name != "slow" {
		t.Fatalf("unexpected drafts: %#v", drafts)
	}
}

func TestCallLLMWithJSONOutputRequestsJSONObject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		format, _ := payload["response_format"].(map[string]any)
		if format["type"] != "json_object" {
			t.Errorf("response_format=%#v", payload["response_format"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"experiments\":[]}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	key := "test-only"
	result, err := callLLMWithOptions(&database.LLMConfig{BaseURL: server.URL, APIKey: &key, Model: "fixture"}, "return json", llmCallOptions{JSONOutput: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != `{"experiments":[]}` || result.FinishReason != "stop" {
		t.Fatalf("result=%#v", result)
	}
}

func TestParseExperimentDraftResponseAcceptsJSONObjectAndScalarParameters(t *testing.T) {
	drafts, err := parseExperimentDraftResponse("```json\n"+`{"experiments":[{"name":"bounded risk","hypothesis":"lower exposure reduces drawdown","rationale":"isolates exposure","parameters":{"max_positions":2,"vol_normalization":true,"max_gross":0.75}}]}`+"\n```", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].Parameters["max_positions"] != "2" || drafts[0].Parameters["vol_normalization"] != "true" || drafts[0].Parameters["max_gross"] != "0.75" {
		t.Fatalf("unexpected normalized draft: %#v", drafts)
	}
}

func TestParseExperimentDraftResponseRejectsWrongCountAndMissingArray(t *testing.T) {
	if _, err := parseExperimentDraftResponse(`[{"name":"one","parameters":{}}]`, 2); err == nil {
		t.Fatal("expected wrong-count response to fail")
	}
	if _, err := parseExperimentDraftResponse(`not json`, 1); err == nil {
		t.Fatal("expected missing array to fail")
	}
	if _, err := parseExperimentDraftResponse(``, 1); err == nil {
		t.Fatal("expected empty response to fail")
	}
	if _, err := parseExperimentDraftResponse(`{"experiments":[{"name":"x","hypothesis":"y","rationale":"z","parameters":{"lookback":[1,2]}}]}`, 1); err == nil {
		t.Fatal("expected non-scalar parameter to fail")
	}
}

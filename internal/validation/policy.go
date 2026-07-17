package validation

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

const AuthorityPolicySchemaVersion = "authority-policy-envelope-v1"

var RequiredConfirmatoryMetrics = []string{"after_cost_expectancy", "after_cost_return", "benchmark_relative_return", "coverage", "gross_exposure", "max_drawdown", "net_exposure", "turnover"}

func NewAuthorityPolicyEnvelope(payload map[string]string) (AuthorityPolicyEnvelope, error) {
	if len(payload) == 0 || len(payload) > 256 {
		return AuthorityPolicyEnvelope{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "authority_policy", Details: "complete bounded policy payload is required"}
	}
	normalized := make(map[string]string, len(payload))
	for key, value := range payload {
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "" || value == "" {
			return AuthorityPolicyEnvelope{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "authority_policy", Details: "empty policy key/value"}
		}
		normalized[key] = canonicalPolicyScalar(value)
	}
	encoded, _ := json.Marshal(struct {
		SchemaVersion string            `json:"schema_version"`
		Payload       map[string]string `json:"payload"`
	}{AuthorityPolicySchemaVersion, normalized})
	return AuthorityPolicyEnvelope{SchemaVersion: AuthorityPolicySchemaVersion, Payload: normalized, Digest: digest(encoded)}, nil
}

func (e AuthorityPolicyEnvelope) Verify() error {
	expected, err := NewAuthorityPolicyEnvelope(e.Payload)
	if err != nil {
		return err
	}
	if e.SchemaVersion != expected.SchemaVersion || e.Digest != expected.Digest {
		return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "authority_policy", Details: "policy envelope digest mismatch"}
	}
	return nil
}
func (e AuthorityPolicyEnvelope) WithRolloutState(state string) (AuthorityPolicyEnvelope, error) {
	payload := make(map[string]string, len(e.Payload))
	for k, v := range e.Payload {
		payload[k] = v
	}
	payload["rollout_state"] = strings.TrimSpace(state)
	return NewAuthorityPolicyEnvelope(payload)
}
func canonicalPolicyScalar(value string) string {
	if parsed, err := strconv.ParseFloat(value, 64); err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0) {
		if parsed == 0 {
			return "0"
		}
		return strconv.FormatFloat(parsed, 'g', -1, 64)
	}
	switch strings.ToLower(value) {
	case "true":
		return "true"
	case "false":
		return "false"
	}
	return value
}
func sortedUnique(values []string) ([]string, error) {
	result := append([]string(nil), values...)
	sort.Strings(result)
	for i, value := range result {
		if strings.TrimSpace(value) == "" || (i > 0 && value == result[i-1]) {
			return nil, fmt.Errorf("values must be non-empty and unique")
		}
	}
	return result, nil
}

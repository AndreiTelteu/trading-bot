package operations

import (
	"encoding/json"
	"testing"
)

func TestCanonicalRowsDigestDetectsIdentityChangeWithEqualAggregates(t *testing.T) {
	left, err := CanonicalRowsDigest(map[string][]json.RawMessage{"fills": {json.RawMessage(`{"id":"fill-a","quantity":"1","fee":"0.1"}`), json.RawMessage(`{"id":"fill-b","quantity":"1","fee":"0.1"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalRowsDigest(map[string][]json.RawMessage{"fills": {json.RawMessage(`{"id":"fill-x","quantity":"1","fee":"0.1"}`), json.RawMessage(`{"id":"fill-y","quantity":"1","fee":"0.1"}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if left.Tables["fills"].Count != right.Tables["fills"].Count {
		t.Fatal("fixture aggregate changed")
	}
	if left.Digest == right.Digest {
		t.Fatal("identity substitution with equal aggregates was not detected")
	}
}

func TestCanonicalRowsDigestIgnoresDatabaseReturnOrder(t *testing.T) {
	a := json.RawMessage(`{"id":"a","occurred_at":"2026-01-01T00:00:00Z"}`)
	b := json.RawMessage(`{"id":"b","occurred_at":"2026-01-02T00:00:00Z"}`)
	left, _ := CanonicalRowsDigest(map[string][]json.RawMessage{"ledger_events": {a, b}})
	right, _ := CanonicalRowsDigest(map[string][]json.RawMessage{"ledger_events": {b, a}})
	if left.Digest != right.Digest {
		t.Fatal("canonical digest depends on query return order")
	}
}

func TestCanonicalRowsDigestSurvivesJSONBNormalization(t *testing.T) {
	left, err := CanonicalRowsDigest(map[string][]json.RawMessage{
		"ledger_events": {json.RawMessage(`{"id":"event","stage08_context_json":{"versions":{"strategy":"v1","policy":"p1"},"active_path":"paper"}}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := CanonicalRowsDigest(map[string][]json.RawMessage{
		"ledger_events": {json.RawMessage(`{ "stage08_context_json": { "active_path": "paper", "versions": { "policy": "p1", "strategy": "v1" } }, "id": "event" }`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if left.Digest != right.Digest {
		t.Fatalf("semantic JSON changed canonical digest: %s != %s", left.Digest, right.Digest)
	}
}

func TestCanonicalRowsDigestPreservesExactNumericTokens(t *testing.T) {
	for name, rows := range map[string][2]string{
		"integer_above_float64_precision": {`{"value":9007199254740992}`, `{"value":9007199254740993}`},
		"high_precision_decimal":          {`{"value":0.1234567890123456789012345678901}`, `{"value":0.1234567890123456789012345678902}`},
	} {
		t.Run(name, func(t *testing.T) {
			leftRow, rightRow := rows[0], rows[1]
			left, err := CanonicalRowsDigest(map[string][]json.RawMessage{"economic": {json.RawMessage(leftRow)}})
			if err != nil {
				t.Fatal(err)
			}
			right, err := CanonicalRowsDigest(map[string][]json.RawMessage{"economic": {json.RawMessage(rightRow)}})
			if err != nil {
				t.Fatal(err)
			}
			if left.Digest == right.Digest {
				t.Fatal("distinct numeric values produced the same fingerprint")
			}
		})
	}
}

func TestCanonicalConstraintDefinitionNormalizesRestoreOnlyRedundantCasts(t *testing.T) {
	source := "CHECK (status::text = ANY (ARRAY['planned'::character varying, 'approved'::character varying, 'applied'::character varying]::text[]))"
	restored := "CHECK (status::text = ANY (ARRAY['planned'::character varying::text, 'approved'::character varying::text, 'applied'::character varying::text]))"
	if canonicalConstraintDefinition(source) != canonicalConstraintDefinition(restored) {
		t.Fatalf("equivalent pg_dump/pg_restore constraint definitions did not canonicalize: %q != %q", canonicalConstraintDefinition(source), canonicalConstraintDefinition(restored))
	}
	for name, changed := range map[string]string{
		"value":    "CHECK (status::text = ANY (ARRAY['planned'::character varying, 'approved'::character varying]::text[]))",
		"operator": "CHECK (status::text = ALL (ARRAY['planned'::character varying, 'approved'::character varying, 'applied'::character varying]::text[]))",
		"type":     "CHECK (status::text = ANY (ARRAY['planned'::character varying, 'approved'::character varying, 'applied'::character varying]::character varying[]))",
	} {
		t.Run(name, func(t *testing.T) {
			if canonicalConstraintDefinition(source) == canonicalConstraintDefinition(changed) {
				t.Fatal("meaningful constraint definition change was hidden")
			}
		})
	}
}

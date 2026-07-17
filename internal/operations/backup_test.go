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

package validation

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"trading-go/internal/database"
)

func TestCompactEvidenceLargeFoldRoundTrip(t *testing.T) {
	manifest := manifestFixture(t)
	result := passingResult(t, manifest)
	for i := range result.Folds {
		p := &result.Folds[i].Primitives
		first, last := p.Curve[0], p.Curve[len(p.Curve)-1]
		p.Curve = make([]CurvePrimitive, 0, 47000)
		for j := 0; j < 46999; j++ {
			point := first
			point.At = first.At.Add(time.Duration(j) * time.Microsecond)
			p.Curve = append(p.Curve, point)
		}
		p.Curve = append(p.Curve, last)
		var err error
		result.Folds[i].Metrics, err = DeriveFoldMetrics(*p)
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	result.Aggregate, err = Evaluate(result.Folds, manifest.Spec)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(struct {
		Result WalkForwardResult `json:"result"`
	}{result})
	if err != nil || len(legacy) <= MaxEvidenceBytes {
		t.Fatalf("legacy size=%d err=%v", len(legacy), err)
	}
	prepared, err := prepareEvidenceForPersistence(manifest, &result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.RootBytes) > MaxEvidenceBytes || len(prepared.Folds) != len(result.Folds) {
		t.Fatalf("compact size=%d folds=%d", len(prepared.RootBytes), len(prepared.Folds))
	}
	rows := make([]database.ValidationFoldEvidence, len(prepared.Folds))
	for i, fold := range prepared.Folds {
		if len(fold.Bytes) > MaxFoldEvidenceBytes {
			t.Fatalf("fold %d size=%d", i, len(fold.Bytes))
		}
		rows[i] = database.ValidationFoldEvidence{ExperimentID: manifest.ID, FoldIndex: fold.Ref.FoldIndex, SchemaVersion: EvidenceSchemaVersion, Status: "passed", FrozenDigest: fold.Ref.FrozenDigest, EvidenceJSON: string(fold.Bytes), EvidenceDigest: fold.Ref.EvidenceDigest}
	}
	got, err := hydratePreparedEvidence(manifest, prepared.RootBytes, rows)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(result)
	actual, _ := json.Marshal(got.Result)
	if string(want) != string(actual) {
		t.Fatal("hydration changed complete result")
	}
	rows[0].EvidenceDigest = strings.Repeat("0", 64)
	if _, err := hydratePreparedEvidence(manifest, prepared.RootBytes, rows); err == nil {
		t.Fatal("corrupt fold accepted")
	}
}

func TestCompactEvidencePreflightClassificationAndFailure(t *testing.T) {
	manifest := manifestFixture(t)
	result := passingResult(t, manifest)
	result.Aggregate.Passed = !result.Aggregate.Passed
	_, err := prepareEvidenceForPersistence(manifest, &result, nil)
	var prewrite *prewriteResultValidationError
	if !errors.As(err, &prewrite) {
		t.Fatalf("supplied result error is not prewrite: %v", err)
	}
	if _, err := prepareEvidenceForPersistence(manifest, nil, nil); err == nil {
		t.Fatal("nil/nil accepted")
	}
	if _, err := prepareEvidenceForPersistence(manifest, &result, errors.New("failure")); err == nil {
		t.Fatal("result/failure accepted")
	}
	prepared, err := prepareEvidenceForPersistence(manifest, nil, &DiagnosticError{Code: DiagnosticMissingBenchmark})
	if err != nil {
		t.Fatal(err)
	}
	got, err := hydratePreparedEvidence(manifest, prepared.RootBytes, nil)
	if err != nil || got.Status != "failed" || got.Failure == nil || got.Result != nil || len(prepared.Folds) != 0 {
		t.Fatalf("failure hydration=%+v err=%v", got, err)
	}
}

func TestCompactEvidenceRejectsMissingExtraAndOversizeFolds(t *testing.T) {
	manifest := manifestFixture(t)
	result := passingResult(t, manifest)
	prepared, err := prepareEvidenceForPersistence(manifest, &result, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]database.ValidationFoldEvidence, len(prepared.Folds))
	for i, fold := range prepared.Folds {
		rows[i] = database.ValidationFoldEvidence{ExperimentID: manifest.ID, FoldIndex: fold.Ref.FoldIndex, SchemaVersion: EvidenceSchemaVersion, Status: "passed", FrozenDigest: fold.Ref.FrozenDigest, EvidenceJSON: string(fold.Bytes), EvidenceDigest: fold.Ref.EvidenceDigest}
	}
	if _, err := hydratePreparedEvidence(manifest, prepared.RootBytes, rows[:len(rows)-1]); err == nil {
		t.Fatal("missing fold accepted")
	}
	if _, err := hydratePreparedEvidence(manifest, prepared.RootBytes, append(rows, rows[0])); err == nil {
		t.Fatal("extra fold accepted")
	}
	for _, mutate := range []func(*database.ValidationFoldEvidence){
		func(r *database.ValidationFoldEvidence) { r.FoldIndex++ },
		func(r *database.ValidationFoldEvidence) { r.SchemaVersion = "unknown" },
		func(r *database.ValidationFoldEvidence) { r.FrozenDigest = strings.Repeat("1", 64) },
		func(r *database.ValidationFoldEvidence) { r.EvidenceDigest = strings.Repeat("2", 64) },
		func(r *database.ValidationFoldEvidence) { r.Status = "failed" },
	} {
		badRows := append([]database.ValidationFoldEvidence(nil), rows...)
		mutate(&badRows[0])
		if _, err := hydratePreparedEvidence(manifest, prepared.RootBytes, badRows); err == nil {
			t.Fatal("malformed fold row accepted")
		}
	}
	var root compactEvidenceRoot
	if err := json.Unmarshal(prepared.RootBytes, &root); err != nil {
		t.Fatal(err)
	}
	root.Result.Folds[1] = root.Result.Folds[0]
	duplicateRefs, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hydratePreparedEvidence(manifest, duplicateRefs, rows); err == nil {
		t.Fatal("duplicate root reference accepted")
	}
	if err := checkFoldEvidenceSize(MaxFoldEvidenceBytes+1, 0); err == nil {
		t.Fatal("oversize fold accepted")
	}
	if err := checkFoldEvidenceSize(1, MaxTotalFoldEvidenceBytes); err == nil {
		t.Fatal("oversize total accepted")
	}
	if err := checkRootEvidenceSize(MaxEvidenceBytes + 1); err == nil {
		t.Fatal("oversize root accepted")
	}
}

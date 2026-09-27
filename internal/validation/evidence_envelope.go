package validation

import (
	"encoding/json"
	"errors"
	"fmt"
	"trading-go/internal/database"
)

// The root is intentionally small; complete economic primitives remain in
// immutable per-fold rows. WalkForwardResult and fold schemas remain v2.
const CompactEvidenceRootSchemaVersion = "validation-evidence-root-v3"

// A synthetic three-fold fixture with 45–47k curve points per fold serialized
// to 6.1–6.4 million bytes per fold (18.7 million together). These independent finite bounds
// leave room for denser primitives without weakening the 2 MiB root bound.
const MaxFoldEvidenceBytes = 16 << 20
const MaxTotalFoldEvidenceBytes = 64 << 20

type foldEvidenceRef struct {
	FoldIndex      int    `json:"fold_index"`
	EvidenceDigest string `json:"evidence_digest"`
	FrozenDigest   string `json:"frozen_digest"`
}
type compactEvidenceResult struct {
	SchemaVersion string            `json:"schema_version"`
	ExperimentID  string            `json:"experiment_id"`
	Folds         []foldEvidenceRef `json:"folds"`
	Aggregate     Evaluation        `json:"aggregate"`
}
type compactEvidenceRoot struct {
	SchemaVersion string                 `json:"schema_version"`
	ExperimentID  string                 `json:"experiment_id"`
	Status        string                 `json:"status"`
	Result        *compactEvidenceResult `json:"result,omitempty"`
	Failure       *DiagnosticError       `json:"failure,omitempty"`
}
type preparedFoldEvidence struct {
	Ref   foldEvidenceRef
	Bytes []byte
}
type preparedEvidence struct {
	RootBytes  []byte
	RootDigest string
	ID         string
	Status     string
	Folds      []preparedFoldEvidence
}

func checkFoldEvidenceSize(size, preceding int) error {
	if size > MaxFoldEvidenceBytes || size < 0 {
		return fmt.Errorf("validation fold evidence exceeds 16 MiB limit")
	}
	if preceding > MaxTotalFoldEvidenceBytes-size {
		return fmt.Errorf("validation fold evidence total exceeds 64 MiB limit")
	}
	return nil
}
func checkRootEvidenceSize(size int) error {
	if size > MaxEvidenceBytes || size < 0 {
		return fmt.Errorf("validation evidence exceeds 2 MiB limit")
	}
	return nil
}

// prepareEvidenceForPersistence is the exact pure preflight used by persistence
// and by non-persisting diagnostics. It performs semantic validation, all
// serialization, size checks, and content-ID derivation before a transaction.
func prepareEvidenceForPersistence(manifest ExperimentManifest, result *WalkForwardResult, failure error) (preparedEvidence, error) {
	if err := manifest.Verify(); err != nil {
		return preparedEvidence{}, err
	}
	if (result == nil) == (failure == nil) {
		return preparedEvidence{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Details: "exactly one of result or failure is required"}
	}
	root := compactEvidenceRoot{SchemaVersion: CompactEvidenceRootSchemaVersion, ExperimentID: manifest.ID}
	prepared := preparedEvidence{}
	if result != nil {
		if err := validateResultForPersistence(manifest, result); err != nil {
			return preparedEvidence{}, err
		}
		root.Status = "passed"
		root.Result = &compactEvidenceResult{SchemaVersion: result.SchemaVersion, ExperimentID: result.ExperimentID, Aggregate: result.Aggregate, Folds: make([]foldEvidenceRef, 0, len(result.Folds))}
		total := 0
		for _, fold := range result.Folds {
			b, err := json.Marshal(fold)
			if err != nil {
				return preparedEvidence{}, resultValidationError(err)
			}
			if err := checkFoldEvidenceSize(len(b), total); err != nil {
				return preparedEvidence{}, resultValidationError(err)
			}
			total += len(b)
			frozen, err := fold.Frozen.Digest()
			if err != nil {
				return preparedEvidence{}, resultValidationError(err)
			}
			ref := foldEvidenceRef{FoldIndex: fold.Fold.Index, EvidenceDigest: digest(b), FrozenDigest: frozen}
			root.Result.Folds = append(root.Result.Folds, ref)
			prepared.Folds = append(prepared.Folds, preparedFoldEvidence{Ref: ref, Bytes: b})
		}
	} else {
		root.Status = "failed"
		var diagnostic *DiagnosticError
		if !errors.As(failure, &diagnostic) {
			diagnostic = &DiagnosticError{Code: DiagnosticInvalidManifest, Details: failure.Error()}
		}
		root.Failure = diagnostic
	}
	b, err := json.Marshal(root)
	if err != nil {
		if result != nil {
			return preparedEvidence{}, resultValidationError(err)
		}
		return preparedEvidence{}, err
	}
	if err := checkRootEvidenceSize(len(b)); err != nil {
		if result != nil {
			return preparedEvidence{}, resultValidationError(err)
		}
		return preparedEvidence{}, err
	}
	prepared.RootBytes, prepared.RootDigest, prepared.Status = b, digest(b), root.Status
	prepared.ID = digest([]byte(manifest.ID + "\n" + prepared.RootDigest))
	return prepared, nil
}

// hydratePreparedEvidence verifies the exact ordered fold set and every row's
// schema, identity, digest, frozen decision, and reproduced economic metrics.
func hydratePreparedEvidence(manifest ExperimentManifest, rootBytes []byte, rows []database.ValidationFoldEvidence) (PersistedEvidence, error) {
	bad := func(detail string) (PersistedEvidence, error) {
		return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: detail}
	}
	if err := checkRootEvidenceSize(len(rootBytes)); err != nil {
		return bad(err.Error())
	}
	var root compactEvidenceRoot
	if err := json.Unmarshal(rootBytes, &root); err != nil {
		return bad(err.Error())
	}
	if _, err := json.Marshal(root); err != nil {
		return bad("invalid compact evidence root")
	}
	if root.SchemaVersion != CompactEvidenceRootSchemaVersion || root.ExperimentID != manifest.ID {
		return bad("compact evidence root identity mismatch")
	}
	if root.Status == "failed" {
		if root.Result != nil || root.Failure == nil || len(rows) != 0 {
			return bad("failed evidence has result or fold rows")
		}
		return PersistedEvidence{ExperimentID: manifest.ID, Status: root.Status, Failure: root.Failure}, nil
	}
	if root.Status != "passed" || root.Result == nil || root.Failure != nil {
		return bad("invalid compact evidence status")
	}
	r := root.Result
	if r.SchemaVersion != EvidenceSchemaVersion || r.ExperimentID != manifest.ID || len(r.Folds) != len(manifest.Spec.Folds) || len(rows) != len(r.Folds) {
		return bad("compact evidence fold set mismatch")
	}
	result := WalkForwardResult{SchemaVersion: r.SchemaVersion, ExperimentID: r.ExperimentID, Aggregate: r.Aggregate, Folds: make([]FoldResult, 0, len(r.Folds))}
	total := 0
	for i, ref := range r.Folds {
		row := rows[i]
		if ref.FoldIndex != manifest.Spec.Folds[i].Index || row.FoldIndex != ref.FoldIndex || row.ExperimentID != manifest.ID || row.SchemaVersion != EvidenceSchemaVersion || row.Status != "passed" || row.EvidenceDigest != ref.EvidenceDigest || row.FrozenDigest != ref.FrozenDigest {
			return bad("compact evidence fold reference mismatch")
		}
		if len(row.EvidenceJSON) > 2*MaxFoldEvidenceBytes {
			return bad("compact fold row exceeds bounded load size")
		}
		var fold FoldResult
		if err := json.Unmarshal([]byte(row.EvidenceJSON), &fold); err != nil {
			return bad(err.Error())
		}
		foldBytes, err := json.Marshal(fold)
		if err != nil || digest(foldBytes) != ref.EvidenceDigest {
			return bad("compact evidence fold digest mismatch")
		}
		if err := checkFoldEvidenceSize(len(foldBytes), total); err != nil {
			return bad(err.Error())
		}
		total += len(foldBytes)
		frozen, err := fold.Frozen.Digest()
		if err != nil || frozen != ref.FrozenDigest || fold.Fold != manifest.Spec.Folds[i] || fold.Frozen.FoldIndex != ref.FoldIndex {
			return bad("compact evidence frozen or fold identity mismatch")
		}
		result.Folds = append(result.Folds, fold)
	}
	if err := validateResultForPersistence(manifest, &result); err != nil {
		return bad(err.Error())
	}
	return PersistedEvidence{ExperimentID: manifest.ID, Status: root.Status, Result: &result}, nil
}

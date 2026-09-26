package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"time"

	"trading-go/internal/backtest"
	"trading-go/internal/database"
	"trading-go/internal/validation"

	"gorm.io/gorm"
)

const (
	stage07SourceSHA               = "ad775fdb6af118d6f8a55db09aaeb22a0b20f9a6"
	stage07OldSpecSHA              = "a3b0d140308a1c383882012c92a4937df2da96908e5e4b6215bb037ae02e95c7"
	stage07NoFillRule              = "selected_zero_base_volume_cancel_at_bar_close_v1"
	stage07PlanVersion             = "researchlab-stage07-plan-v1"
	stage07CandidateImplementation = "c71a6af5020f45c4b6e4107e6bd4994c5a43c9a252041dcb8b278eadad4216ec"
	stage07BaselineImplementation  = "d1d6f449093afdac3b38be1a78f660dec6c322c85827127e88a9cb6dd792ba3e"
)

type stage07Options struct {
	Mode, OldManifestFile, PlanFile, PlanSHA256, SourceJobIDs  string
	Creator, IdempotencyKey, LedgerFile, MarkerFile, DriverSHA string
}

type stage07SourceReference struct {
	Comparison               backtest.Stage07ComparisonReference `json:"comparison"`
	ValidationArtifactDigest string                              `json:"validation_artifact_digest"`
	SourceCodeRevision       string                              `json:"source_code_revision"`
}

// Matches the persisted Stage 07 source envelope's field order and types so
// json.Marshal reconstructs its canonical digest after PostgreSQL jsonb load.
type stage07CanonicalSourceArtifact struct {
	SchemaVersion, ComparisonDigest, DatasetManifestID string
	ReplaySettings                                     map[string]string                         `json:"replay_settings"`
	ReplaySettingsDigest                               string                                    `json:"replay_settings_digest"`
	Results                                            map[string]backtest.Stage05StrategyResult `json:"results"`
}

func stage07ExactDigest(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}

func verifyStage07CanonicalSourceArtifact(raw []byte, sourceDigest, comparisonDigest, datasetID string) (stage07CanonicalSourceArtifact, error) {
	var artifact stage07CanonicalSourceArtifact
	if len(raw) == 0 || len(raw) > 16<<20 || !stage07ExactDigest(sourceDigest) || !stage07ExactDigest(comparisonDigest) || !stage07ExactDigest(datasetID) {
		return artifact, fmt.Errorf("Stage 07 source artifact or digest is missing or unbounded")
	}
	if err := decodeStage07JSON(raw, &artifact); err != nil {
		return artifact, err
	}
	canonical, err := json.Marshal(artifact)
	if err != nil {
		return artifact, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(canonical)) != sourceDigest || artifact.SchemaVersion != "stage07-source-artifact-v2" || artifact.ComparisonDigest != comparisonDigest || artifact.DatasetManifestID != datasetID || len(artifact.ReplaySettings) == 0 || len(artifact.Results) == 0 {
		return artifact, fmt.Errorf("Stage 07 source artifact canonical digest or envelope differs")
	}
	settings, err := json.Marshal(artifact.ReplaySettings)
	if err != nil {
		return artifact, err
	}
	if fmt.Sprintf("%x", sha256.Sum256(settings)) != artifact.ReplaySettingsDigest {
		return artifact, fmt.Errorf("Stage 07 source replay settings digest differs")
	}
	return artifact, nil
}

type stage07Plan struct {
	SchemaVersion      string                   `json:"schema_version"`
	OldManifestFile    string                   `json:"old_manifest_file"`
	OldManifestSHA256  string                   `json:"old_manifest_sha256"`
	SourceCodeRevision string                   `json:"source_code_revision"`
	DriverCodeRevision string                   `json:"driver_code_revision"`
	PriorFamilyID      string                   `json:"prior_family_id"`
	Creator            string                   `json:"creator"`
	IdempotencyKey     string                   `json:"idempotency_key"`
	SourceReferences   []stage07SourceReference `json:"source_references"`
	ComparisonDigest   string                   `json:"comparison_digest"`
	Spec               validation.ManifestSpec  `json:"spec"`
}

func validateStage07CodeLineage(driverSHA string) error {
	if err := exec.Command("git", "merge-base", "--is-ancestor", stage07SourceSHA, driverSHA).Run(); err != nil {
		return fmt.Errorf("reviewed driver is not descended from source revision: %w", err)
	}
	output, err := exec.Command("git", "diff", "--name-only", "--no-renames", "-z", stage07SourceSHA, driverSHA, "--").Output()
	if err != nil {
		return err
	}
	paths := make([]string, 0)
	for _, path := range bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0}) {
		if len(path) != 0 {
			paths = append(paths, string(path))
		}
	}
	return validateStage07DiffPaths(paths)
}

func validateStage07DiffPaths(paths []string) error {
	for _, path := range paths {
		if !strings.HasPrefix(path, "cmd/researchlab/") && !strings.HasPrefix(path, "docs/") {
			return fmt.Errorf("source-to-driver diff changes forbidden path %q", path)
		}
	}
	return nil
}

func verifyStage07PlanSHA(raw []byte, expected string) error {
	if len(expected) != 64 || strings.Trim(expected, "0123456789abcdef") != "" || fmt.Sprintf("%x", sha256.Sum256(raw)) != expected {
		return fmt.Errorf("Stage 07 plan differs from reviewed SHA-256")
	}
	return nil
}

func decodeStage07JSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("JSON must contain one exact object")
	}
	return nil
}

func readStage07PrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, fmt.Errorf("Stage 07 file must be regular and mode 0600")
	}
	return os.ReadFile(path)
}

func stage07OldSpec(path string) (validation.ManifestSpec, error) {
	raw, err := readStage07PrivateFile(path)
	if err != nil {
		return validation.ManifestSpec{}, err
	}
	if err := verifyStage07PlanSHA(raw, stage07OldSpecSHA); err != nil {
		return validation.ManifestSpec{}, fmt.Errorf("prior manifest: %w", err)
	}
	var spec validation.ManifestSpec
	if err := decodeStage07JSON(raw, &spec); err != nil {
		return spec, err
	}
	if _, err := validation.NewManifest(spec, time.Now().UTC()); err != nil {
		return spec, err
	}
	if spec.StudyType != "exploratory" || !spec.Exploratory || spec.ConfirmatoryHoldout != nil || !reflect.DeepEqual(spec.FoldSourceJobIDs, []uint{63, 64, 65}) || spec.Policies.Execution != "backtest-execution-v1" {
		return spec, fmt.Errorf("pinned prior exploratory spec differs from declared lineage")
	}
	return spec, nil
}

func parseStage07SourceIDs(raw string) ([]uint, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 3 {
		return nil, fmt.Errorf("exactly three new source job IDs are required")
	}
	ids := make([]uint, 0, 3)
	seen := map[uint]bool{}
	for _, part := range parts {
		id, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32)
		if err != nil || id <= 65 || seen[uint(id)] {
			return nil, fmt.Errorf("source IDs must be distinct new jobs")
		}
		seen[uint(id)] = true
		ids = append(ids, uint(id))
	}
	return ids, nil
}

func loadStage07References(ids []uint, old validation.ManifestSpec) ([]stage07SourceReference, string, error) {
	refs := make([]stage07SourceReference, 0, len(ids))
	comparisons := make([]backtest.Stage07ComparisonReference, 0, len(ids))
	for _, id := range ids {
		ref, err := backtest.LoadStage07ComparisonReference(database.DB, id)
		if err != nil {
			return nil, "", err
		}
		candidate, cOK := ref.Strategies[old.Candidate.ID]
		baseline, bOK := ref.Strategies[old.Baseline.ID]
		if !cOK || !bOK || ref.Candidate != old.Candidate.ID+"@"+old.Candidate.Version || ref.DatasetDigest != old.DatasetDigest || candidate.ConfigDigest != old.Candidate.ConfigDigest || baseline.ConfigDigest != old.Baseline.ConfigDigest {
			return nil, "", fmt.Errorf("source job %d differs from pinned strategy/dataset identity", id)
		}
		var job database.BacktestJob
		if err := database.DB.First(&job, id).Error; err != nil {
			return nil, "", err
		}
		if job.ValidationArtifactJSON == nil || job.ValidationArtifactDigest == nil {
			return nil, "", fmt.Errorf("source job %d lacks validation artifact", id)
		}
		artifact, err := verifyStage07CanonicalSourceArtifact([]byte(*job.ValidationArtifactJSON), *job.ValidationArtifactDigest, ref.ArtifactDigest, old.DatasetManifestID)
		if err != nil {
			return nil, "", err
		}
		c, cOK := artifact.Results[old.Candidate.ID]
		b, bOK := artifact.Results[old.Baseline.ID]
		if !cOK || !bOK || c.Manifest.CodeRevision != stage07SourceSHA || b.Manifest.CodeRevision != stage07SourceSHA {
			return nil, "", fmt.Errorf("source job %d code revision differs from reviewed source SHA", id)
		}
		refs = append(refs, stage07SourceReference{ref, *job.ValidationArtifactDigest, stage07SourceSHA})
		comparisons = append(comparisons, ref)
	}
	if err := validateStage07ReferenceSet(refs, old); err != nil {
		return nil, "", err
	}
	encoded, err := json.Marshal(comparisons)
	if err != nil {
		return nil, "", err
	}
	return refs, fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func validateStage07ReferenceSet(refs []stage07SourceReference, old validation.ManifestSpec) error {
	if len(refs) != 3 || refs[0].Comparison.JobID != 66 {
		return fmt.Errorf("audited source #66 and two exact repeats are required")
	}
	seen := map[uint]bool{}
	comparisonDigest, sourceDigest := refs[0].Comparison.ArtifactDigest, refs[0].ValidationArtifactDigest
	if !stage07ExactDigest(comparisonDigest) || !stage07ExactDigest(sourceDigest) {
		return fmt.Errorf("source canonical digests are missing")
	}
	for _, ref := range refs {
		if ref.Comparison.JobID <= 65 || seen[ref.Comparison.JobID] {
			return fmt.Errorf("source job IDs must be distinct new jobs")
		}
		seen[ref.Comparison.JobID] = true
		if ref.Comparison.ArtifactDigest != comparisonDigest || ref.ValidationArtifactDigest != sourceDigest {
			return fmt.Errorf("exact source repetitions have different canonical comparison or Stage 07 artifact digests")
		}
		candidate, cOK := ref.Comparison.Strategies[old.Candidate.ID]
		baseline, bOK := ref.Comparison.Strategies[old.Baseline.ID]
		if !cOK || !bOK || ref.Comparison.Candidate != old.Candidate.ID+"@"+old.Candidate.Version || candidate.ImplementationDigest != stage07CandidateImplementation || baseline.ImplementationDigest != stage07BaselineImplementation || candidate.ConfigDigest != old.Candidate.ConfigDigest || baseline.ConfigDigest != old.Baseline.ConfigDigest || ref.Comparison.DatasetDigest != old.DatasetDigest || ref.SourceCodeRevision != stage07SourceSHA || ref.ValidationArtifactDigest == "" {
			return fmt.Errorf("Stage 07 source reference differs from audited #66 implementation or pinned configuration/dataset")
		}
	}
	return nil
}

func buildStage07Spec(old validation.ManifestSpec, refs []stage07SourceReference, driverSHA, comparisonDigest string) (validation.ManifestSpec, error) {
	if err := validateStage07ReferenceSet(refs, old); err != nil {
		return old, err
	}
	if driverSHA == "" || comparisonDigest == "" {
		return old, fmt.Errorf("incomplete Stage 07 identity")
	}
	// Copy through JSON so all pinned slices and maps remain independent.
	raw, err := json.Marshal(old)
	if err != nil {
		return old, err
	}
	var next validation.ManifestSpec
	if err := json.Unmarshal(raw, &next); err != nil {
		return old, err
	}
	next.CodeRevision = driverSHA
	next.FoldSourceJobIDs = []uint{refs[0].Comparison.JobID, refs[1].Comparison.JobID, refs[2].Comparison.JobID}
	next.Candidate.ImplementationDigest = refs[0].Comparison.Strategies[old.Candidate.ID].ImplementationDigest
	next.Baseline.ImplementationDigest = refs[0].Comparison.Strategies[old.Baseline.ID].ImplementationDigest
	next.FamilyID = "" // New implementation and policy require a new immutable family scope.
	next.Policies.Execution = "backtest-execution-v3"
	policy := next.Policies
	policy.Composite = ""
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return next, err
	}
	next.Policies.Composite = fmt.Sprintf("policy_bundle_v3_%x", sha256.Sum256(policyJSON))[:29]
	next.AuthorityPolicy.Payload["execution_policy"] = "backtest-execution-v3"
	next.AuthorityPolicy, err = validation.NewAuthorityPolicyEnvelope(next.AuthorityPolicy.Payload)
	if err != nil {
		return next, err
	}
	next.ExecutionSemantics["execution_policy_version"] = "backtest-execution-v3"
	next.ExecutionSemantics["no_fill_rule"] = stage07NoFillRule
	next.ExecutionSemantics["source_code_revision"] = stage07SourceSHA
	next.Artifacts.Comparison = "stage05-comparison-references-sha256:" + comparisonDigest
	next.Reproduce = validation.ReproductionInvocation{Command: "go", Args: []string{"run", "./cmd/researchlab", "-stage07-mode", "run", "-clone-marker-file", "<clone-marker-file>", "-ledger-file", "<private-attempt-ledger>", "-reviewed-code-sha", driverSHA, "-stage07-plan-file", "<reviewed-plan-file>", "-stage07-plan-sha256", "<reviewed-plan-sha256>"}, Env: map[string]string{"BACKTEST_CODE_REVISION": driverSHA, "DATABASE_URL_FILE": "<clone-runtime-dsn-file>", "STAGE08_NEW_BACKTEST": "research"}}
	manifest, err := validation.NewManifest(next, time.Now().UTC())
	if err != nil {
		return next, err
	}
	return manifest.Spec, nil
}

func validateStage07Plan(plan stage07Plan, opts stage07Options) error {
	if plan.SchemaVersion != stage07PlanVersion || plan.SourceCodeRevision != stage07SourceSHA || plan.DriverCodeRevision != opts.DriverSHA || plan.OldManifestSHA256 != stage07OldSpecSHA || plan.Creator == "" || len(plan.IdempotencyKey) < 8 || len(plan.IdempotencyKey) > 120 || len(plan.SourceReferences) != 3 {
		return fmt.Errorf("Stage 07 plan identity or attempt metadata differs")
	}
	old, err := stage07OldSpec(plan.OldManifestFile)
	if err != nil {
		return err
	}
	if plan.PriorFamilyID != old.FamilyID {
		return fmt.Errorf("prior research family lineage differs from pinned spec")
	}
	ids := make([]uint, 0, 3)
	for _, ref := range plan.SourceReferences {
		ids = append(ids, ref.Comparison.JobID)
	}
	parsed, err := parseStage07SourceIDs(fmt.Sprintf("%d,%d,%d", ids[0], ids[1], ids[2]))
	if err != nil {
		return err
	}
	live, digest, err := loadStage07References(parsed, old)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(live, plan.SourceReferences) || digest != plan.ComparisonDigest {
		return fmt.Errorf("reviewed source references changed")
	}
	want, err := buildStage07Spec(old, live, opts.DriverSHA, digest)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(want, plan.Spec) {
		return fmt.Errorf("reviewed Stage 07 spec differs from pinned derivation")
	}
	return nil
}

func runStage07Mode(opts stage07Options) error {
	if opts.Mode != "prepare" && opts.Mode != "run" {
		return fmt.Errorf("Stage 07 mode must be prepare or run")
	}
	ledger, err := openLedger(opts.LedgerFile)
	if err != nil {
		return err
	}
	defer ledger.Close()
	if opts.Mode == "prepare" {
		if opts.OldManifestFile == "" || opts.SourceJobIDs == "" || opts.Creator == "" || len(opts.IdempotencyKey) < 8 || len(opts.IdempotencyKey) > 120 || opts.PlanSHA256 != "" {
			return fmt.Errorf("prepare requires prior manifest, three sources, creator, fresh key, and no plan SHA")
		}
		old, err := stage07OldSpec(opts.OldManifestFile)
		if err != nil {
			return err
		}
		ids, err := parseStage07SourceIDs(opts.SourceJobIDs)
		if err != nil {
			return err
		}
		refs, digest, err := loadStage07References(ids, old)
		if err != nil {
			return err
		}
		spec, err := buildStage07Spec(old, refs, opts.DriverSHA, digest)
		if err != nil {
			return err
		}
		plan := stage07Plan{SchemaVersion: stage07PlanVersion, OldManifestFile: opts.OldManifestFile, OldManifestSHA256: stage07OldSpecSHA, SourceCodeRevision: stage07SourceSHA, DriverCodeRevision: opts.DriverSHA, PriorFamilyID: old.FamilyID, Creator: opts.Creator, IdempotencyKey: opts.IdempotencyKey, SourceReferences: refs, ComparisonDigest: digest, Spec: spec}
		if err := validateStage07Plan(plan, opts); err != nil {
			return err
		}
		manifest, err := validation.NewManifest(spec, time.Now().UTC())
		if err != nil {
			return err
		}
		if _, _, err := (backtest.Stage07ExperimentSource{DB: database.DB}).Load(manifest); err != nil {
			return fmt.Errorf("Stage 07 source preflight: %w", err)
		}
		raw, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		file, err := os.OpenFile(opts.PlanFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(raw)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		if closeErr := file.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			return writeErr
		}
		planSHA := fmt.Sprintf("%x", sha256.Sum256(raw))
		if err := appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "status": "stage07_plan_prepared", "plan_sha256": planSHA, "source_job_ids": ids, "comparison_digest": digest, "prior_family_id": old.FamilyID, "family_id": spec.FamilyID, "source_code_sha": stage07SourceSHA, "driver_code_sha": opts.DriverSHA}); err != nil {
			return err
		}
		fmt.Printf("{\"status\":\"stage07_plan_prepared\",\"plan_sha256\":%q,\"comparison_digest\":%q}\n", planSHA, digest)
		return nil
	}
	if opts.OldManifestFile != "" || opts.SourceJobIDs != "" || opts.Creator != "" || opts.IdempotencyKey != "" {
		return fmt.Errorf("run accepts only the exact reviewed plan")
	}
	raw, err := readStage07PrivateFile(opts.PlanFile)
	if err != nil {
		return err
	}
	if err := verifyStage07PlanSHA(raw, opts.PlanSHA256); err != nil {
		return err
	}
	var plan stage07Plan
	if err := decodeStage07JSON(raw, &plan); err != nil {
		return err
	}
	if err := validateStage07Plan(plan, opts); err != nil {
		return err
	}
	manifest, err := validation.NewManifest(plan.Spec, time.Now().UTC())
	if err != nil {
		return err
	}
	if _, _, err := (backtest.Stage07ExperimentSource{DB: database.DB}).Load(manifest); err != nil {
		return fmt.Errorf("Stage 07 source preflight: %w", err)
	}
	if err := appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "status": "stage07_execution_intent", "plan_sha256": opts.PlanSHA256, "content_id": manifest.ContentID, "source_job_ids": plan.Spec.FoldSourceJobIDs, "comparison_digest": plan.ComparisonDigest, "prior_family_id": plan.PriorFamilyID, "family_id": plan.Spec.FamilyID, "source_code_sha": stage07SourceSHA, "driver_code_sha": opts.DriverSHA}); err != nil {
		return err
	}
	first := plan.Spec.FoldSourceJobIDs[0]
	repo := validation.Repository{DB: database.DB}
	var existing database.ValidationExperiment
	if err := database.DB.Where("idempotency_key=?", plan.IdempotencyKey).First(&existing).Error; err == nil {
		return fmt.Errorf("Stage 07 idempotency key already registered; refusing another execution")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	created, err := repo.CreateManifestAuthenticated(manifest, &first, &plan.ComparisonDigest, plan.Creator, plan.IdempotencyKey)
	if err != nil {
		_ = appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "status": "stage07_registration_failed", "error": err.Error()})
		return err
	}
	if created.ID != manifest.ID {
		return fmt.Errorf("Stage 07 idempotency key resolved to an existing manifest; refusing another execution")
	}
	if err := appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "status": "stage07_registered", "experiment_id": created.ID, "content_id": created.ContentID}); err != nil {
		return err
	}
	evidence, runErr := (validation.JobService{Repository: repo, Source: backtest.Stage07ExperimentSource{DB: database.DB}}).Run(created.ID)
	status := "stage07_completed"
	if runErr != nil {
		status = "stage07_failed"
	}
	if err := appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "status": status, "experiment_id": created.ID, "evidence_id": evidence.ID, "evidence_status": evidence.Status, "failure": evidence.Failure}); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	fmt.Printf("{\"experiment_id\":%q,\"evidence_id\":%q,\"status\":%q}\n", created.ID, evidence.ID, evidence.Status)
	return nil
}

package validation

import (
	"fmt"
	"time"
)

const (
	ManifestSchemaVersion  = "validation-experiment-manifest-v1"
	EvidenceSchemaVersion  = "validation-evidence-v1"
	MLSchemaVersion        = "ml-evaluation-v1"
	MaxFolds               = 64
	MaxBootstrapIterations = 10000
	MaxManifestBytes       = 1 << 20
)

type DiagnosticCode string

const (
	DiagnosticInvalidManifest          DiagnosticCode = "invalid_manifest"
	DiagnosticManifestIntegrity        DiagnosticCode = "manifest_integrity_failure"
	DiagnosticInsufficientWindows      DiagnosticCode = "insufficient_independent_windows"
	DiagnosticInvalidWindowOrder       DiagnosticCode = "invalid_window_order"
	DiagnosticInsufficientObservations DiagnosticCode = "insufficient_observations"
	DiagnosticInsufficientTrades       DiagnosticCode = "insufficient_trades"
	DiagnosticInsufficientRegimes      DiagnosticCode = "insufficient_regimes"
	DiagnosticMissingBenchmark         DiagnosticCode = "missing_benchmark"
	DiagnosticIncompleteCoverage       DiagnosticCode = "incomplete_coverage"
	DiagnosticZeroTrades               DiagnosticCode = "zero_trades"
	DiagnosticNonFinite                DiagnosticCode = "non_finite_metric"
	DiagnosticUnsupportedUnit          DiagnosticCode = "unsupported_statistical_unit"
	DiagnosticOneClass                 DiagnosticCode = "one_class_labels"
	DiagnosticTestLeakage              DiagnosticCode = "test_data_leakage"
	DiagnosticDominated                DiagnosticCode = "performance_dominated"
	DiagnosticBaselineMismatch         DiagnosticCode = "baseline_candidate_or_exposure_mismatch"
	DiagnosticInvalidProbability       DiagnosticCode = "invalid_probability"
)

type DiagnosticError struct {
	Code    DiagnosticCode `json:"code"`
	Field   string         `json:"field,omitempty"`
	Details string         `json:"details,omitempty"`
}

func (e *DiagnosticError) Error() string {
	if e == nil {
		return "validation failed"
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Details)
}

type Interval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func (v Interval) Valid() bool { return !v.Start.IsZero() && v.End.After(v.Start) }

type Fold struct {
	Index      int      `json:"index"`
	Train      Interval `json:"train"`
	Validation Interval `json:"validation"`
	Test       Interval `json:"test"`
}

type VersionRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest,omitempty"`
}

type PolicyBundle struct {
	Composite      string `json:"composite"`
	Execution      string `json:"execution"`
	Universe       string `json:"universe"`
	ModelSelection string `json:"model_selection"`
	EntrySelection string `json:"entry_selection"`
	PortfolioRisk  string `json:"portfolio_risk"`
	Rollout        string `json:"rollout"`
	Cost           string `json:"cost"`
}

type SampleRequirements struct {
	MinFolds               int `json:"min_folds"`
	MinIndependentUnits    int `json:"min_independent_units"`
	MinObservationsPerFold int `json:"min_observations_per_fold"`
	MinTradesPerFold       int `json:"min_trades_per_fold"`
	MinRegimes             int `json:"min_regimes"`
}

type Threshold struct {
	Metric string  `json:"metric"`
	Op     string  `json:"op"`
	Value  float64 `json:"value"`
}

type ArtifactLinks struct {
	Metrics    string `json:"metrics"`
	Trades     string `json:"trades"`
	Curves     string `json:"curves"`
	Cohorts    string `json:"cohorts"`
	Factors    string `json:"factors"`
	Coverage   string `json:"coverage"`
	Comparison string `json:"comparison"`
}

type ReproductionInvocation struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

type ManifestSpec struct {
	SchemaVersion       string                   `json:"schema_version"`
	StudyType           string                   `json:"study_type"`
	Exploratory         bool                     `json:"exploratory"`
	CodeRevision        string                   `json:"code_revision"`
	Candidate           VersionRef               `json:"candidate"`
	Baseline            VersionRef               `json:"baseline"`
	Model               *ModelAuthority          `json:"model,omitempty"`
	Policies            PolicyBundle             `json:"policies"`
	DatasetManifestID   string                   `json:"dataset_manifest_id"`
	DatasetManifestHash string                   `json:"dataset_manifest_hash"`
	UniversePolicy      string                   `json:"universe_policy"`
	Interval            Interval                 `json:"interval"`
	DecisionClock       string                   `json:"decision_clock"`
	ExecutionClock      string                   `json:"execution_clock"`
	Seed                int64                    `json:"seed"`
	ExecutionSemantics  map[string]string        `json:"execution_semantics"`
	Folds               []Fold                   `json:"folds"`
	FeatureHorizon      time.Duration            `json:"feature_horizon"`
	LabelHorizon        time.Duration            `json:"label_horizon"`
	Purge               time.Duration            `json:"purge"`
	Embargo             time.Duration            `json:"embargo"`
	AllowedTuning       map[string][]string      `json:"allowed_tuning"`
	Metrics             []string                 `json:"metrics"`
	StatisticalUnit     string                   `json:"statistical_unit"`
	BootstrapIterations int                      `json:"bootstrap_iterations"`
	Samples             SampleRequirements       `json:"sample_requirements"`
	PromotionThresholds []Threshold              `json:"promotion_thresholds"`
	RollbackThresholds  []Threshold              `json:"rollback_thresholds"`
	RequiredElapsed     map[string]time.Duration `json:"required_elapsed"`
	Artifacts           ArtifactLinks            `json:"artifacts"`
	Reproduce           ReproductionInvocation   `json:"reproduce"`
	ResearchOverride    *ResearchOverride        `json:"research_override,omitempty"`
}

type ResearchOverride struct {
	Mode      string   `json:"mode"`
	BoundedTo Interval `json:"bounded_to"`
	Reason    string   `json:"reason"`
}

type ExperimentManifest struct {
	ID            string       `json:"id"`
	ContentID     string       `json:"content_id"`
	ContentDigest string       `json:"content_digest"`
	CreatedAt     time.Time    `json:"created_at"`
	Spec          ManifestSpec `json:"spec"`
}

type ArtifactClass string

const (
	ArtifactBootstrap           ArtifactClass = "bootstrap"
	ArtifactContractFixture     ArtifactClass = "contract_fixture"
	ArtifactResearch            ArtifactClass = "research"
	ArtifactShadowCandidate     ArtifactClass = "shadow_candidate"
	ArtifactPromotableCandidate ArtifactClass = "promotable_candidate"
)

type FeatureField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type ModelAuthority struct {
	Version          string         `json:"version"`
	Class            ArtifactClass  `json:"class"`
	ModelDigest      string         `json:"model_digest"`
	FeatureSpec      string         `json:"feature_spec"`
	Features         []FeatureField `json:"features"`
	LabelSpec        string         `json:"label_spec"`
	LabelHorizon     time.Duration  `json:"label_horizon"`
	CodeRevision     string         `json:"code_revision"`
	DatasetManifest  string         `json:"dataset_manifest"`
	TrainingManifest string         `json:"training_manifest"`
	PolicyVersion    string         `json:"policy_version"`
	Seed             int64          `json:"seed"`
}

func (m ModelAuthority) CanHoldAuthority() bool {
	return m.Class == ArtifactPromotableCandidate
}

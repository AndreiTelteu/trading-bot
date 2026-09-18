// Package research exports the only supported hand-off to offline model
// experimentation. It deliberately does not evaluate a portfolio, fit a
// model, register an artifact, or persist promotion evidence.
package research

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/services"

	"gorm.io/gorm"
)

const (
	ProposalDatasetSchemaVersion          = "research-proposal-dataset-v1"
	ProposalDatasetRowSchemaVersion       = "research-proposal-row-v1"
	FixedHorizonAfterCostLabelSpecVersion = "fixed_horizon_after_cost_return_v1"
	ProposalDatasetRowsFilename           = "dataset.jsonl"
	ProposalDatasetManifestFilename       = "dataset.manifest.json"
)

type ProposalDatasetRequest struct {
	DatasetManifestID  string
	Start              time.Time
	End                time.Time
	LabelHorizon       time.Duration
	FeatureSpecVersion string
	LabelSpecVersion   string
	PolicyVersion      string
}

type ProposalDatasetRow struct {
	SchemaVersion      string             `json:"schema_version"`
	DecisionID         string             `json:"decision_id"`
	DecisionTime       time.Time          `json:"decision_time"`
	Symbol             string             `json:"symbol"`
	FeatureSnapshotID  uint               `json:"feature_snapshot_id"`
	UniverseSnapshotID uint               `json:"universe_snapshot_id"`
	PolicyVersion      string             `json:"policy_version"`
	FeatureSpecVersion string             `json:"feature_spec_version"`
	LabelSpecVersion   string             `json:"label_spec_version"`
	LabelHorizonSecs   int64              `json:"label_horizon_seconds"`
	CostModelVersion   string             `json:"cost_model_version"`
	OutcomeReturn      float64            `json:"outcome_return"`
	Profitable         bool               `json:"profitable"`
	Values             map[string]float64 `json:"values"`
}

type ProposalDatasetManifest struct {
	SchemaVersion       string    `json:"schema_version"`
	DatasetManifestID   string    `json:"dataset_manifest_id"`
	DatasetManifestHash string    `json:"dataset_manifest_hash"`
	FeatureSpecVersion  string    `json:"feature_spec_version"`
	LabelSpecVersion    string    `json:"label_spec_version"`
	LabelHorizonSeconds int64     `json:"label_horizon_seconds"`
	PolicyVersion       string    `json:"policy_version"`
	Start               time.Time `json:"start"`
	End                 time.Time `json:"end"`
	FeatureNames        []string  `json:"feature_names"`
	RowsFile            string    `json:"rows_file"`
	RowsSHA256          string    `json:"rows_sha256"`
	RowCount            int       `json:"row_count"`
	ContentDigest       string    `json:"content_digest"`
}

type ProposalDataset struct {
	Manifest ProposalDatasetManifest
	Rows     []ProposalDatasetRow
}

func BuildProposalDataset(db *gorm.DB, request ProposalDatasetRequest) (ProposalDataset, error) {
	if db == nil {
		return ProposalDataset{}, fmt.Errorf("research proposal dataset requires PostgreSQL")
	}
	request.DatasetManifestID = strings.TrimSpace(request.DatasetManifestID)
	request.FeatureSpecVersion = strings.TrimSpace(request.FeatureSpecVersion)
	request.LabelSpecVersion = strings.TrimSpace(request.LabelSpecVersion)
	request.PolicyVersion = strings.TrimSpace(request.PolicyVersion)
	request.Start, request.End = request.Start.UTC(), request.End.UTC()
	if len(request.DatasetManifestID) != 64 || request.Start.IsZero() || !request.End.After(request.Start) || request.LabelHorizon <= 0 || request.FeatureSpecVersion != services.ModelFeatureSpecVersion || request.LabelSpecVersion != FixedHorizonAfterCostLabelSpecVersion || request.PolicyVersion == "" {
		return ProposalDataset{}, fmt.Errorf("invalid immutable research proposal dataset request")
	}
	if request.LabelHorizon%time.Second != 0 {
		return ProposalDataset{}, fmt.Errorf("label horizon must be an integral number of seconds")
	}

	var source database.DatasetManifest
	if err := db.Where("id=?", request.DatasetManifestID).First(&source).Error; err != nil {
		return ProposalDataset{}, fmt.Errorf("load dataset manifest: %w", err)
	}
	if source.ContentHash != request.DatasetManifestID {
		return ProposalDataset{}, fmt.Errorf("dataset manifest %s has invalid content identity", request.DatasetManifestID)
	}
	if request.Start.Before(source.EffectiveStart.UTC()) || request.End.After(source.EffectiveEnd.UTC()) {
		return ProposalDataset{}, fmt.Errorf("requested interval is outside immutable dataset manifest coverage")
	}

	type selectedRow struct {
		DecisionID         string
		DecisionTime       time.Time
		Symbol             string
		FeatureSnapshotID  uint
		UniverseSnapshotID uint
		PolicyVersion      string
		CostModelVersion   string
		OutcomeReturn      float64
		OutcomeProfitable  bool
		FeaturesJSON       string
		QualityFlagsJSON   string
	}
	var selected []selectedRow
	err := db.Table("decision_cohorts dc").
		Select("dc.decision_id, dc.decision_time, dc.symbol, dc.feature_snapshot_id, dc.universe_snapshot_id, dc.policy_version, dc.cost_model_version, dc.outcome_return, dc.outcome_profitable, fs.features_json, fs.quality_flags_json").
		Joins("JOIN feature_snapshots fs ON fs.id = dc.feature_snapshot_id").
		Joins("JOIN universe_snapshots us ON us.id = fs.universe_snapshot_id").
		Where("dc.universe_snapshot_id = fs.universe_snapshot_id AND us.dataset_manifest_id = ? AND us.policy_version = ? AND dc.decision_time >= ? AND dc.decision_time < ? AND dc.horizon_seconds = ? AND dc.feature_spec_version = ? AND fs.feature_spec_version = ? AND fs.policy_version = ? AND dc.policy_version = ? AND dc.outcome_status = ? AND dc.outcome_return IS NOT NULL AND dc.outcome_profitable IS NOT NULL", request.DatasetManifestID, request.PolicyVersion, request.Start, request.End, int64(request.LabelHorizon/time.Second), request.FeatureSpecVersion, request.FeatureSpecVersion, request.PolicyVersion, request.PolicyVersion, "labeled").
		Order("dc.decision_time ASC, dc.symbol ASC, dc.decision_id ASC").
		Scan(&selected).Error
	if err != nil {
		return ProposalDataset{}, fmt.Errorf("select labeled decision cohorts: %w", err)
	}
	if len(selected) == 0 {
		return ProposalDataset{}, fmt.Errorf("research proposal dataset has no labeled cohorts")
	}

	rows := make([]ProposalDatasetRow, 0, len(selected))
	for _, row := range selected {
		values, err := decodeFeatureValues(row.FeaturesJSON, row.QualityFlagsJSON)
		if err != nil {
			return ProposalDataset{}, fmt.Errorf("decision cohort %s: %w", row.DecisionID, err)
		}
		if !finite(row.OutcomeReturn) {
			return ProposalDataset{}, fmt.Errorf("decision cohort %s has a non-finite outcome", row.DecisionID)
		}
		rows = append(rows, ProposalDatasetRow{SchemaVersion: ProposalDatasetRowSchemaVersion, DecisionID: row.DecisionID, DecisionTime: row.DecisionTime.UTC(), Symbol: row.Symbol, FeatureSnapshotID: row.FeatureSnapshotID, UniverseSnapshotID: row.UniverseSnapshotID, PolicyVersion: row.PolicyVersion, FeatureSpecVersion: request.FeatureSpecVersion, LabelSpecVersion: request.LabelSpecVersion, LabelHorizonSecs: int64(request.LabelHorizon / time.Second), CostModelVersion: row.CostModelVersion, OutcomeReturn: row.OutcomeReturn, Profitable: row.OutcomeProfitable, Values: values})
	}

	return ProposalDataset{Manifest: ProposalDatasetManifest{SchemaVersion: ProposalDatasetSchemaVersion, DatasetManifestID: source.ID, DatasetManifestHash: source.ContentHash, FeatureSpecVersion: request.FeatureSpecVersion, LabelSpecVersion: request.LabelSpecVersion, LabelHorizonSeconds: int64(request.LabelHorizon / time.Second), PolicyVersion: request.PolicyVersion, Start: request.Start, End: request.End, FeatureNames: services.ModelFeatureNames(), RowsFile: ProposalDatasetRowsFilename, RowCount: len(rows)}, Rows: rows}, nil
}

func WriteProposalDataset(outputDir string, dataset ProposalDataset) (ProposalDatasetManifest, error) {
	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" {
		return ProposalDatasetManifest{}, fmt.Errorf("output directory is required")
	}
	if err := validateDataset(dataset); err != nil {
		return ProposalDatasetManifest{}, err
	}
	if err := os.Mkdir(outputDir, 0o700); err != nil {
		return ProposalDatasetManifest{}, fmt.Errorf("create immutable output directory: %w", err)
	}
	rowsPath := filepath.Join(outputDir, ProposalDatasetRowsFilename)
	rowsFile, err := os.OpenFile(rowsPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ProposalDatasetManifest{}, err
	}
	hash := sha256.New()
	for _, row := range dataset.Rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			_ = rowsFile.Close()
			return ProposalDatasetManifest{}, err
		}
		encoded = append(encoded, '\n')
		if _, err := rowsFile.Write(encoded); err != nil {
			_ = rowsFile.Close()
			return ProposalDatasetManifest{}, err
		}
		_, _ = hash.Write(encoded)
	}
	if err := rowsFile.Close(); err != nil {
		return ProposalDatasetManifest{}, err
	}
	manifest := dataset.Manifest
	manifest.RowsSHA256 = hex.EncodeToString(hash.Sum(nil))
	manifest.ContentDigest = proposalManifestDigest(manifest)
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ProposalDatasetManifest{}, err
	}
	if err := os.WriteFile(filepath.Join(outputDir, ProposalDatasetManifestFilename), append(encoded, '\n'), 0o600); err != nil {
		return ProposalDatasetManifest{}, err
	}
	return manifest, nil
}

func decodeFeatureValues(featuresJSON, flagsJSON string) (map[string]float64, error) {
	var flags []string
	if err := json.Unmarshal([]byte(flagsJSON), &flags); err != nil {
		return nil, fmt.Errorf("invalid feature quality flags")
	}
	if len(flags) != 0 {
		return nil, fmt.Errorf("feature snapshot has quality flags")
	}
	values := map[string]float64{}
	if err := json.Unmarshal([]byte(featuresJSON), &values); err != nil {
		return nil, fmt.Errorf("invalid feature values")
	}
	expected := services.ModelFeatureNames()
	if len(values) != len(expected) {
		return nil, fmt.Errorf("feature snapshot schema has %d fields, want %d", len(values), len(expected))
	}
	for _, name := range expected {
		value, ok := values[name]
		if !ok || !finite(value) {
			return nil, fmt.Errorf("feature snapshot has invalid %s", name)
		}
	}
	return values, nil
}

func validateDataset(dataset ProposalDataset) error {
	manifest := dataset.Manifest
	if manifest.SchemaVersion != ProposalDatasetSchemaVersion || len(manifest.DatasetManifestID) != 64 || manifest.DatasetManifestHash != manifest.DatasetManifestID || manifest.FeatureSpecVersion != services.ModelFeatureSpecVersion || manifest.LabelSpecVersion != FixedHorizonAfterCostLabelSpecVersion || manifest.LabelHorizonSeconds <= 0 || manifest.PolicyVersion == "" || !manifest.End.After(manifest.Start) || manifest.RowsFile != ProposalDatasetRowsFilename || manifest.RowCount != len(dataset.Rows) || len(manifest.FeatureNames) != len(services.ModelFeatureNames()) {
		return fmt.Errorf("invalid research proposal dataset")
	}
	if strings.Join(manifest.FeatureNames, "\n") != strings.Join(services.ModelFeatureNames(), "\n") {
		return fmt.Errorf("research proposal feature schema mismatch")
	}
	if len(dataset.Rows) == 0 {
		return fmt.Errorf("research proposal dataset has no rows")
	}
	for index, row := range dataset.Rows {
		if row.SchemaVersion != ProposalDatasetRowSchemaVersion || row.DecisionID == "" || row.DecisionTime.IsZero() || row.Symbol == "" || row.FeatureSnapshotID == 0 || row.UniverseSnapshotID == 0 || row.PolicyVersion != manifest.PolicyVersion || row.FeatureSpecVersion != manifest.FeatureSpecVersion || row.LabelSpecVersion != manifest.LabelSpecVersion || row.LabelHorizonSecs != manifest.LabelHorizonSeconds || row.CostModelVersion == "" || !finite(row.OutcomeReturn) {
			return fmt.Errorf("invalid research proposal row %d", index)
		}
		if _, err := decodeFeatureValues(mustJSON(row.Values), "[]"); err != nil {
			return fmt.Errorf("invalid research proposal row %d: %w", index, err)
		}
	}
	return nil
}

func proposalManifestDigest(manifest ProposalDatasetManifest) string {
	manifest.ContentDigest = ""
	encoded, _ := json.Marshal(manifest)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func mustJSON(value map[string]float64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func finite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

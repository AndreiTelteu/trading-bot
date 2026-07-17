package services

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"trading-go/internal/database"
	"trading-go/internal/validation"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

//go:embed model_artifacts/*.json
var embeddedModelArtifacts embed.FS

var builtinArtifactPaths = map[string]string{
	DefaultActiveModelVersion: "model_artifacts/logistic_baseline_v1.json",
}

var modelArtifactCache = struct {
	sync.RWMutex
	artifacts map[string]LogisticModelArtifact
}{
	artifacts: make(map[string]LogisticModelArtifact),
}

func LoadConfiguredModel(settings map[string]string) (*LogisticModelArtifact, error) {
	policy := GetAuthorizedModelSelectionPolicy(settings)
	if !policy.Enabled() {
		return nil, nil
	}
	artifact, err := LoadModelArtifact(policy.ActiveModelVersion)
	if err != nil {
		return nil, err
	}
	if policy.UseForLiveEntries() {
		if err := verifyStage07ModelAuthority(policy, artifact); err != nil {
			return nil, err
		}
	}
	return artifact, nil
}

func LoadModelArtifact(version string) (*LogisticModelArtifact, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return nil, nil
	}

	modelArtifactCache.RLock()
	if cached, ok := modelArtifactCache.artifacts[version]; ok {
		modelArtifactCache.RUnlock()
		copyArtifact := cached
		return &copyArtifact, nil
	}
	modelArtifactCache.RUnlock()

	var (
		payload  []byte
		checksum string
		err      error
	)

	if builtinPath, ok := builtinArtifactPaths[version]; ok {
		payload, checksum, err = readEmbeddedArtifact(builtinPath)
		if err != nil {
			return nil, err
		}
		_ = ensureBuiltinModelArtifactRecord(version, builtinPath, checksum, payload)
	} else {
		payload, checksum, err = loadArtifactPayloadFromDatabase(version)
		if err != nil {
			return nil, err
		}
	}

	var artifact LogisticModelArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return nil, fmt.Errorf("failed to parse model artifact %s: %w", version, err)
	}
	if err := artifact.Validate(); err != nil {
		return nil, err
	}
	if checksum == "" {
		checksum = sha256Hex(payload)
	}

	modelArtifactCache.Lock()
	modelArtifactCache.artifacts[version] = artifact
	modelArtifactCache.Unlock()

	copyArtifact := artifact
	return &copyArtifact, nil
}

func readEmbeddedArtifact(path string) ([]byte, string, error) {
	payload, err := embeddedModelArtifacts.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read embedded model artifact %s: %w", path, err)
	}
	return payload, sha256Hex(payload), nil
}

func loadArtifactPayloadFromDatabase(version string) ([]byte, string, error) {
	if database.DB == nil {
		return nil, "", fmt.Errorf("model artifact %s requires database metadata", version)
	}

	var record database.ModelArtifact
	if err := database.DB.Where("version = ?", version).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", fmt.Errorf("model artifact %s not found", version)
		}
		return nil, "", err
	}

	path := strings.TrimSpace(record.ArtifactPath)
	if path == "" {
		return nil, "", fmt.Errorf("model artifact %s has no artifact path", version)
	}

	if strings.HasPrefix(path, "builtin://") {
		builtinPath := strings.TrimPrefix(path, "builtin://")
		payload, checksum, err := readEmbeddedArtifact(builtinPath)
		if err != nil {
			return nil, "", err
		}
		if record.ArtifactChecksum != "" && record.ArtifactChecksum != checksum {
			return nil, "", fmt.Errorf("checksum mismatch for model artifact %s", version)
		}
		return payload, checksum, nil
	}

	return nil, "", fmt.Errorf("unsupported artifact path for model %s: %s", version, path)
}

func ensureBuiltinModelArtifactRecord(version string, builtinPath string, checksum string, payload []byte) error {
	if database.DB == nil {
		return nil
	}

	var artifact LogisticModelArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		return err
	}
	metricsJSON, _ := json.Marshal(artifact.Metrics)
	featureSchema := make([]map[string]string, 0, len(artifact.Features))
	for _, feature := range artifact.Features {
		featureSchema = append(featureSchema, map[string]string{"name": feature.Name, "type": "float64"})
	}
	featureSchemaJSON, _ := json.Marshal(featureSchema)

	record := database.ModelArtifact{
		Version:            version,
		ModelFamily:        artifact.ModelFamily,
		FeatureSpecVersion: artifact.FeatureSpecVersion,
		LabelSpecVersion:   artifact.LabelSpecVersion,
		CalibrationMethod:  artifact.CalibrationMethod,
		TrainWindow:        artifact.TrainingWindow,
		ValidationWindow:   artifact.ValidationWindow,
		TestWindow:         artifact.TestWindow,
		MetricsSummaryJSON: string(metricsJSON),
		ArtifactPath:       "builtin://" + builtinPath,
		ArtifactChecksum:   checksum,
		ArtifactClass:      "contract_fixture",
		FeatureSchemaJSON:  string(featureSchemaJSON),
		ModelDigest:        checksum,
		RolloutState:       ModelRolloutShadow,
	}

	return database.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "version"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"model_family",
			"feature_spec_version",
			"label_spec_version",
			"calibration_method",
			"train_window",
			"validation_window",
			"test_window",
			"metrics_summary_json",
			"artifact_path",
			"artifact_checksum",
			"artifact_class",
			"feature_schema_json",
			"model_digest",
			"updated_at",
		}),
	}).Create(&record).Error
}

func verifyStage07ModelAuthority(policy ModelSelectionPolicy, loaded *LogisticModelArtifact) error {
	if database.DB == nil {
		return fmt.Errorf("Stage 07 model authority requires persistent governance evidence")
	}
	var artifact database.ModelArtifact
	if err := database.DB.Where("version = ?", policy.ActiveModelVersion).First(&artifact).Error; err != nil {
		return err
	}
	if loaded == nil || loaded.ArtifactClass != "promotable_candidate" || artifact.ArtifactClass != "promotable_candidate" {
		return fmt.Errorf("model artifact %s class %s is structurally quarantined", policy.ActiveModelVersion, artifact.ArtifactClass)
	}
	if artifact.FeatureSpecVersion == "" || artifact.LabelSpecVersion == "" || artifact.FeatureSchemaJSON == "" || artifact.TrainingManifestID == "" || artifact.CodeRevision == "" || artifact.DatasetManifestID == "" || artifact.ModelDigest == "" || artifact.ModelDigest != artifact.ArtifactChecksum {
		return fmt.Errorf("model artifact %s lacks exact Stage 07 training provenance", policy.ActiveModelVersion)
	}
	var storedFeatures []struct{ Name, Type string }
	if err := json.Unmarshal([]byte(artifact.FeatureSchemaJSON), &storedFeatures); err != nil || len(storedFeatures) != len(loaded.Features) {
		return fmt.Errorf("model artifact %s feature schema is invalid", policy.ActiveModelVersion)
	}
	for i, feature := range loaded.Features {
		if storedFeatures[i].Name != feature.Name || storedFeatures[i].Type != "float64" {
			return fmt.Errorf("model artifact %s feature order/type mismatch at index %d", policy.ActiveModelVersion, i)
		}
	}
	if loaded.FeatureSpecVersion != artifact.FeatureSpecVersion || loaded.LabelSpecVersion != artifact.LabelSpecVersion || loaded.Version != artifact.Version {
		return fmt.Errorf("model artifact %s schema/version metadata mismatch", policy.ActiveModelVersion)
	}
	var deployment database.GovernanceDeployment
	if err := database.DB.Where("context_key = ?", "model:"+policy.ActiveModelVersion).First(&deployment).Error; err != nil {
		return fmt.Errorf("model artifact %s has no Stage 07 authority: %w", policy.ActiveModelVersion, err)
	}
	if deployment.ArtifactVersion != policy.ActiveModelVersion || deployment.State != policy.RolloutState || deployment.PolicyVersion != policy.PolicyVersion {
		return fmt.Errorf("model artifact %s authority does not match exact rollout and policy", policy.ActiveModelVersion)
	}
	manifest, err := (validation.Repository{DB: database.DB}).LoadManifest(deployment.ExperimentID)
	if err != nil || manifest.Spec.Model == nil {
		return fmt.Errorf("model artifact %s authority manifest is unavailable or non-model", policy.ActiveModelVersion)
	}
	model := manifest.Spec.Model
	if model.Version != artifact.Version || model.Class != validation.ArtifactPromotableCandidate || model.ModelDigest != artifact.ModelDigest || model.FeatureSpec != artifact.FeatureSpecVersion || model.LabelSpec != artifact.LabelSpecVersion || model.CodeRevision != artifact.CodeRevision || model.DatasetManifest != artifact.DatasetManifestID || model.TrainingManifest != artifact.TrainingManifestID || model.Seed != artifact.TrainingSeed || model.PolicyVersion != deployment.PolicyVersion {
		return fmt.Errorf("model artifact %s provenance differs from immutable authority manifest", policy.ActiveModelVersion)
	}
	return nil
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

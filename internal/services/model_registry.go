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
	policy := GetModelSelectionPolicy(settings)
	if !policy.Enabled() {
		return nil, nil
	}
	return LoadModelArtifact(policy.ActiveModelVersion)
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
			"updated_at",
		}),
	}).Create(&record).Error
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

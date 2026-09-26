package services

import (
	"reflect"
	"testing"

	"trading-go/internal/database"
	"trading-go/internal/testutil"

	"gorm.io/gorm"
)

func TestBacktestBuiltinModelFirstLoadIsReadOnly(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	version := DefaultActiveModelVersion
	modelArtifactCache.Lock()
	previous, hadPrevious := modelArtifactCache.artifacts[version]
	previousPending := modelArtifactCache.readOnlyBuiltins[version]
	delete(modelArtifactCache.artifacts, version)
	delete(modelArtifactCache.readOnlyBuiltins, version)
	modelArtifactCache.Unlock()
	t.Cleanup(func() {
		modelArtifactCache.Lock()
		if hadPrevious {
			modelArtifactCache.artifacts[version] = previous
		} else {
			delete(modelArtifactCache.artifacts, version)
		}
		if previousPending {
			modelArtifactCache.readOnlyBuiltins[version] = true
		} else {
			delete(modelArtifactCache.readOnlyBuiltins, version)
		}
		modelArtifactCache.Unlock()
	})

	settings := map[string]string{"active_model_version": version, "model_rollout_state": ModelRolloutShadow}
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION READ ONLY").Error; err != nil {
			return err
		}
		database.DB = tx
		defer func() { database.DB = db }()

		first, err := LoadBacktestConfiguredModel(settings)
		if err != nil {
			return err
		}
		second, err := LoadBacktestConfiguredModel(settings)
		if err != nil {
			return err
		}
		if first == nil || second == nil || first.Version != version || !reflect.DeepEqual(first, second) {
			t.Errorf("builtin backtest model first/cache load mismatch: first=%+v second=%+v", first, second)
		}
		modelArtifactCache.RLock()
		_, cached := modelArtifactCache.artifacts[version]
		pending := modelArtifactCache.readOnlyBuiltins[version]
		modelArtifactCache.RUnlock()
		if !cached || !pending {
			t.Errorf("read-only builtin cache state: cached=%t pending registration=%t", cached, pending)
		}
		var count int64
		if err := tx.Model(&database.ModelArtifact{}).Where("version = ?", version).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("read-only backtest registered %d builtin model artifacts", count)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("first builtin backtest model load in read-only transaction: %v", err)
	}

	if _, err := LoadConfiguredModel(settings); err != nil {
		t.Fatalf("runtime model load after read-only cache population: %v", err)
	}
	var registered database.ModelArtifact
	if err := db.Where("version = ?", version).First(&registered).Error; err != nil {
		t.Fatalf("runtime did not register builtin model after backtest cache population: %v", err)
	}
	_, checksum, err := readEmbeddedArtifact(builtinArtifactPaths[version])
	if err != nil {
		t.Fatal(err)
	}
	if registered.ArtifactChecksum != checksum || registered.ArtifactClass != ModelArtifactContractFixture {
		t.Fatalf("runtime builtin registration has wrong checksum/class: %+v", registered)
	}
	modelArtifactCache.RLock()
	pending := modelArtifactCache.readOnlyBuiltins[version]
	modelArtifactCache.RUnlock()
	if pending {
		t.Fatal("runtime registration left builtin cache pending")
	}
}

package services

import (
	"testing"

	"trading-go/internal/database"
	"trading-go/internal/testutil"

	"gorm.io/gorm"
)

func TestResolveBacktestGovernanceContextReadsWithoutPolicyConfigWrites(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	settings := map[string]string{
		"backtest_fee_bps": "10", "backtest_slippage_bps": "5",
		"universe_top_k": "8", "max_positions": "3",
		"active_model_version": "read_only_backtest_model",
	}
	artifact := database.ModelArtifact{
		Version: "read_only_backtest_model", FeatureSpecVersion: "features-v1", LabelSpecVersion: "labels-v1",
	}
	if err := db.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET TRANSACTION READ ONLY").Error; err != nil {
			return err
		}
		database.DB = tx
		defer func() { database.DB = db }()

		context, err := ResolveBacktestGovernanceContext(settings, "dynamic")
		if err != nil {
			return err
		}
		if context.ModelVersion != artifact.Version || context.FeatureSpecVersion != artifact.FeatureSpecVersion || context.LabelSpecVersion != artifact.LabelSpecVersion {
			t.Errorf("read-only context did not retain model artifact DB reads: %+v", context)
		}
		if context.PolicyVersions.CompositeVersion == "" || context.PolicyVersions.ExecutionPolicyVersion == "" {
			t.Errorf("read-only context has empty policy identity: %+v", context.PolicyVersions)
		}
		var count int64
		if err := tx.Model(&database.PolicyConfig{}).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			t.Errorf("read-only backtest created %d policy configs", count)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("resolve backtest governance in read-only transaction: %v", err)
	}

	readOnly, err := ResolveBacktestGovernanceContext(settings, "dynamic")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := ResolveGovernanceContext(settings, "dynamic")
	if err != nil {
		t.Fatal(err)
	}
	if readOnly.PolicyVersions != runtime.PolicyVersions {
		t.Fatalf("backtest policy identity differs from runtime: backtest=%+v runtime=%+v", readOnly.PolicyVersions, runtime.PolicyVersions)
	}
	var count int64
	if err := db.Model(&database.PolicyConfig{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 6 {
		t.Fatalf("runtime policy synchronization created %d configs, want 6", count)
	}
}

package services

import (
	"testing"
	"trading-go/internal/database"
)

func TestStage07BootstrapModelCannotGainPaperAuthorityFromSettings(t *testing.T) {
	previous := database.DB
	database.DB = nil
	t.Cleanup(func() { database.DB = previous })
	settings := map[string]string{"active_model_version": DefaultActiveModelVersion, "model_rollout_state": ModelRolloutPaper, "model_fallback_mode": ModelFallbackRuleBased}
	artifact, err := LoadConfiguredModel(settings)
	if err != nil {
		t.Fatal(err)
	}
	if artifact == nil || artifact.ArtifactClass != "contract_fixture" {
		t.Fatalf("quarantined fixture=%+v", artifact)
	}
	if GetAuthorizedModelSelectionPolicy(settings).UseForLiveEntries() {
		t.Fatal("bootstrap contract fixture gained paper authority from settings")
	}
}

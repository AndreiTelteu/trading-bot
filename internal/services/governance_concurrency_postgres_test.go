package services

import (
	"sync"
	"testing"

	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestEnsurePolicyConfigsConcurrentWritersUseStableLockOrder(t *testing.T) {
	testutil.SetupPostgresDB(t)
	settings := map[string]string{
		"backtest_fee_bps": "10", "backtest_slippage_bps": "5",
		"universe_top_k": "8", "max_positions": "3",
		"active_model_version": "logistic_baseline_v1",
	}
	const workers = 8
	start := make(chan struct{})
	errors := make(chan error, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			_, err := EnsurePolicyConfigs(settings)
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent policy synchronization failed: %v", err)
		}
	}
	var active int64
	if err := database.DB.Model(&database.PolicyConfig{}).Where("is_active=true").Count(&active).Error; err != nil {
		t.Fatal(err)
	}
	if active != 6 {
		t.Fatalf("active policies=%d want=6", active)
	}
}

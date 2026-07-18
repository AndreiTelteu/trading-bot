package services

import (
	"strings"
	"testing"
	"time"

	"trading-go/internal/accounting"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
	"trading-go/internal/tradingcore"
)

func TestSharedPersistsAndApprovedShadowIsInert(t *testing.T) {
	for _, test := range []struct {
		name        string
		mode        tradingcore.ExecutionMode
		wantOpened  int
		wantFill    int64
		wantHistory int
		decision    string
	}{
		{name: "shared", mode: tradingcore.ExecutionPaper, wantOpened: 1, wantFill: 1, wantHistory: 1, decision: "buy"},
		{name: "shadow_compare", mode: tradingcore.ExecutionShadow, wantOpened: 0, wantFill: 0, wantHistory: 0, decision: "shadow_only"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := testutil.SetupPostgresDB(t)
			if err := database.SeedData(); err != nil {
				t.Fatal(err)
			}
			settings := map[string]string{"auto_trade_enabled": "true", "entry_percent": "5", "buy_only_strong": "false", "min_confidence_to_buy": "1", "max_positions": "5", "regime_gate_enabled": "false", "model_rollout_state": "shadow", "model_fallback_mode": "rule_based", "exchange_venue_id": "test-venue", "paper_fee_bps": "10", "paper_slippage_bps": "5"}
			analysis := AnalyzedCoin{Symbol: "BTCUSDT", Price: 100, Signal: "BUY", Rating: 5, Timeframe: "15m", CreatedAt: time.Now().UTC(), PolicyVersion: "risk-app-v1"}
			results, opened, err := executeShortlistTradesShared([]AnalyzedCoin{analysis}, nil, settings, test.mode)
			if err != nil {
				t.Fatal(err)
			}
			if opened != test.wantOpened || results[0].Decision != test.decision {
				t.Fatalf("mode result=%+v opened=%d", results, opened)
			}
			var histories []database.TrendAnalysisHistory
			if err := db.Find(&histories).Error; err != nil || len(histories) != test.wantHistory {
				t.Fatalf("decision history=%+v err=%v", histories, err)
			}
			if test.wantHistory > 0 && (!strings.Contains(histories[0].DecisionContextJSON, "BrokerCompleteness") || !strings.Contains(histories[0].DecisionContextJSON, "RiskTrace")) {
				t.Fatalf("history lacks canonical shared trace: %s", histories[0].DecisionContextJSON)
			}
			if test.mode == tradingcore.ExecutionShadow && (results[0].ShadowDecision != "buy" || results[0].ShadowReason != "approved_observation") {
				t.Fatalf("approved candidate intent was suppressed instead of observed: %+v", results[0])
			}
			var fillCount int64
			if err := db.Model(&database.Fill{}).Count(&fillCount).Error; err != nil || fillCount != test.wantFill {
				t.Fatalf("fill count=%d want=%d err=%v", fillCount, test.wantFill, err)
			}
		})
	}
}

func TestRuntimePendingOrderReconstructionUsesTrueRemainingQuantity(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	requested, executed, remaining := accounting.MustParse("3"), accounting.MustParse("2"), accounting.MustParse("1")
	zero := accounting.Zero()
	if err := db.Create(&database.Order{AccountID: "primary", OrderType: "buy", Symbol: "BTC", Status: string(tradingcore.BrokerPartiallyFilled), ExecutionMode: "paper", RequestedQuantityExact: &requested, ExecutedQuantityExact: &executed, RemainingQuantityExact: &remaining, AmountCryptoExact: &zero, AmountUsdtExact: &zero, FeeExact: &zero, ExecutedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
	identity, _, err := buildDeploymentStrategy(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := buildRuntimeDecisionContext(nil, nil, map[string]string{"exchange_venue_id": "test-venue", "regime_gate_enabled": "false"}, tradingcore.ExecutionPaper, time.Now().UTC(), identity)
	if err != nil {
		t.Fatal(err)
	}
	pending := snapshot.Portfolio().PendingOrders()
	if len(pending) != 1 || pending[0].Remaining.Decimal().String() != "1" {
		t.Fatalf("pending reconstruction = %+v", pending)
	}
}

package services

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"trading-go/internal/cutover"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
	"trading-go/internal/tradingcore"
	"trading-go/internal/validation"

	"gorm.io/gorm"
)

func TestRuntimeStrategyRegistryFailsClosedOnUnknownDigest(t *testing.T) {
	legacyLabelDigest := fmt.Sprintf("%x", sha256.Sum256([]byte("tradingcore.LegacyRuleStrategy:legacy-rule-v1")))
	if BaselineStrategyDigest == legacyLabelDigest || BaselineStrategyDigest != tradingcore.StrategyArtifactDigest("legacy") {
		t.Fatal("baseline governance digest is not bound to the embedded executable source artifact")
	}
	identity, strategy, err := instantiateRegisteredStrategy(TrendMomentumCandidateID, TrendMomentumCandidateVersion, TrendMomentumCandidateDigest)
	if err != nil {
		t.Fatal(err)
	}
	if identity.CodeIdentity != targetStrategyCodeIdentity {
		t.Fatalf("candidate code identity=%q", identity.CodeIdentity)
	}
	if _, ok := strategy.(tradingcore.TargetAllocationStrategy); !ok {
		t.Fatalf("candidate resolved to %T", strategy)
	}
	if _, _, err := instantiateRegisteredStrategy(TrendMomentumCandidateID, TrendMomentumCandidateVersion, strings.Repeat("f", 64)); err == nil {
		t.Fatal("unregistered candidate digest did not fail closed")
	}
}

func TestProductionSharedOrchestratorBindsBaselineCandidateAndLimitedLive(t *testing.T) {
	for _, test := range []struct {
		name, state  string
		baseline     bool
		wantFill     int64
		wantDecision string
	}{
		{name: "baseline paper", baseline: true, wantFill: 1, wantDecision: "buy"},
		{name: "candidate paper", state: "paper", wantFill: 1, wantDecision: "buy"},
		{name: "candidate limited-live dry-run", state: "limited_live", wantFill: 0, wantDecision: "skip"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := testutil.SetupPostgresDB(t)
			cutover.ResetForTest()
			t.Cleanup(cutover.ResetForTest)
			if err := database.SeedData(); err != nil {
				t.Fatal(err)
			}
			settings := GetAllSettings()
			for key, value := range map[string]string{
				"trading_engine_mode": "shared", "auto_trade_enabled": "true", "entry_percent": "5", "buy_only_strong": "false", "min_confidence_to_buy": "1", "max_positions": "5", "max_position_value": "1000", "max_turnover": "1000", "cash_reserve_percent": "0", "regime_gate_enabled": "false", "model_rollout_state": "shadow", "model_fallback_mode": "rule_based", "active_model_version": "fixture-model-v1", "model_feature_schema": "fixture-schema-v1", "selection_policy_top_k": "5", "selection_policy_min_prob": "0.53", "selection_policy_min_ev": "0.001", "exchange_venue_id": "test-venue", "decision_timeframe": "15m", "execution_timeframe": "15m", "paper_fee_bps": "10", "paper_slippage_bps": "5", "backtest_fee_bps": "10", "backtest_slippage_bps": "5",
			} {
				settings[key] = value
			}
			analysis := AnalyzedCoin{Symbol: "BTCUSDT", Price: 100, Signal: "BUY", Rating: 5, Timeframe: "15m", CreatedAt: time.Now().UTC(), PolicyVersion: "risk-app-v1"}
			wantIdentity := BaselineStrategyID + "@" + BaselineStrategyVersion + "#" + BaselineStrategyDigest
			if !test.baseline {
				settings["strategy_id"], settings["strategy_version"], settings["strategy_digest"] = TrendMomentumCandidateID, TrendMomentumCandidateVersion, TrendMomentumCandidateDigest
				settings["target_action.btc-usdt"], settings["target_quantity.btc-usdt"], settings["target_reason.btc-usdt"] = "buy", "1", "candidate_target"
				analysis.Signal = "SELL" // Candidate output must not be legacy rule behavior.
				installStrategyDeployment(t, db, settings, test.state)
				flags := cutover.SafeFlags()
				flags.LedgerAuthority, flags.SharedEngine, flags.CandidateStrategy = "authoritative", test.state, test.state
				if test.state == "paper" {
					flags.PointInTime = "research"
				} else {
					flags.PointInTime, flags.Stage07Context = "authoritative", "strategy:"+TrendMomentumCandidateID+"@"+TrendMomentumCandidateVersion
				}
				if err := cutover.Activate(flags); err != nil {
					t.Fatal(err)
				}
				wantIdentity = TrendMomentumCandidateID + "@" + TrendMomentumCandidateVersion + "#" + TrendMomentumCandidateDigest
			}
			results, opened := ExecuteShortlistTrades([]AnalyzedCoin{analysis}, nil, settings)
			if opened != int(test.wantFill) || results[0].Decision != test.wantDecision {
				t.Fatalf("result=%+v opened=%d", results[0], opened)
			}
			var fills []database.Fill
			if err := db.Find(&fills).Error; err != nil || int64(len(fills)) != test.wantFill {
				t.Fatalf("fills=%+v err=%v", fills, err)
			}
			if len(fills) > 0 && !strings.Contains(fills[0].StrategyVersion, wantIdentity) {
				t.Fatalf("executed strategy identity=%q want %q", fills[0].StrategyVersion, wantIdentity)
			}
			var histories []database.TrendAnalysisHistory
			if err := db.Find(&histories).Error; err != nil || len(histories) != 1 {
				t.Fatalf("histories=%+v err=%v", histories, err)
			}
			trace := histories[0].DecisionContextJSON
			for _, expected := range []string{wantIdentity, "paper-cost-v1", "RiskTrace"} {
				if !strings.Contains(trace, expected) {
					t.Fatalf("trace lacks %q: %s", expected, trace)
				}
			}
			if test.state == "limited_live" {
				var orders int64
				if err := db.Model(&database.Order{}).Count(&orders).Error; err != nil || orders != 0 {
					t.Fatalf("limited-live dry-run submitted an order: count=%d err=%v", orders, err)
				}
				if !strings.Contains(trace, string(tradingcore.RiskExecutionNotAuthorized)) {
					t.Fatalf("limited-live dry-run was not fenced pre-submit: %s", trace)
				}
			}
		})
	}
}

func installStrategyDeployment(t *testing.T, db *gorm.DB, settings map[string]string, state string) {
	t.Helper()
	envelope, err := BuildRuntimeAuthorityPolicy(settings, state)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	spec := validation.ManifestSpec{
		SchemaVersion: validation.ManifestSchemaVersion, StudyType: "exploratory", Exploratory: true, CodeRevision: targetStrategyCodeIdentity,
		Candidate: validation.VersionRef{ID: TrendMomentumCandidateID, Version: TrendMomentumCandidateVersion, Digest: TrendMomentumCandidateDigest}, Baseline: validation.VersionRef{ID: BaselineStrategyID, Version: BaselineStrategyVersion, Digest: BaselineStrategyDigest},
		Policies: validation.PolicyBundle{Composite: "policy-v1", Execution: "exec-v1", Universe: "universe-v1", ModelSelection: "model-v1", EntrySelection: "entry-v1", PortfolioRisk: "risk-v1", Rollout: "rollout-v1", Cost: "paper-cost-v1"}, GovernancePolicy: validation.GovernancePolicyVersion, AuthorityPolicy: envelope,
		DatasetManifestID: strings.Repeat("a", 64), DatasetManifestHash: strings.Repeat("a", 64), UniversePolicy: "universe-v1", Interval: validation.Interval{Start: base, End: base.Add(5 * time.Hour)}, DecisionClock: "15m-close", ExecutionClock: "dry-run", Seed: 1,
		ExecutionSemantics: map[string]string{"fee_bps": "10", "slippage_bps": "5", "timing": "decision", "liquidity": "bounded"}, Folds: []validation.Fold{
			{Index: 0, Train: validation.Interval{Start: base, End: base.Add(time.Hour)}, Validation: validation.Interval{Start: base.Add(time.Hour), End: base.Add(2 * time.Hour)}, Test: validation.Interval{Start: base.Add(2 * time.Hour), End: base.Add(3 * time.Hour)}},
			{Index: 1, Train: validation.Interval{Start: base, End: base.Add(2 * time.Hour)}, Validation: validation.Interval{Start: base.Add(2 * time.Hour), End: base.Add(3 * time.Hour)}, Test: validation.Interval{Start: base.Add(3 * time.Hour), End: base.Add(4 * time.Hour)}},
		}, FeatureHorizon: time.Minute, LabelHorizon: time.Minute,
		AllowedTuning: map[string][]string{"identity": {"fixed"}}, Metrics: []string{"coverage", "max_drawdown"}, StatisticalUnit: "chronological_test_window", BootstrapIterations: 1, Samples: validation.SampleRequirements{MinFolds: 2, MinIndependentUnits: 1, MinObservationsPerFold: 1}, PromotionThresholds: []validation.Threshold{{Metric: "coverage", Op: ">=", Value: 0.9}}, RollbackThresholds: []validation.Threshold{{Metric: "max_drawdown", Op: ">=", Value: 0.5}},
		Artifacts: validation.ArtifactLinks{Metrics: "metrics", Trades: "trades", Curves: "curves", Cohorts: "cohorts", Factors: "factors", Coverage: "coverage", Comparison: "comparison"}, Reproduce: validation.ReproductionInvocation{Command: "test", Args: []string{"strategy"}},
	}
	manifest, err := validation.NewManifest(spec, base.Add(6*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (validation.Repository{DB: database.DB}).CreateManifest(manifest, nil, nil); err != nil {
		t.Fatal(err)
	}
	evidenceID := strings.Repeat("e", 64)
	if err := db.Create(&database.ValidationEvidence{ID: evidenceID, ExperimentID: manifest.ID, SchemaVersion: "strategy-factory-test-v1", Status: "passed", EvidenceJSON: `{}`, EvidenceDigest: evidenceID, CreatedAt: base.Add(6 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	transitionID := strings.Repeat("d", 64)
	transition := database.GovernanceTransition{ID: transitionID, IdempotencyKey: "strategy-deploy-" + state, ContextKey: "strategy:" + TrendMomentumCandidateID + "@" + TrendMomentumCandidateVersion, ExperimentID: manifest.ID, EvidenceID: evidenceID, FromState: "shadow", ToState: state, ArtifactVersion: TrendMomentumCandidateVersion, Reason: "production seam test", ContentDigest: transitionID, CreatedAt: base.Add(7 * time.Hour), PolicyVersion: "policy-v1", AuthorityPolicyDigest: envelope.Digest, EvidenceDigest: evidenceID, Actor: "test"}
	if err := db.Create(&transition).Error; err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(envelope)
	deployment := database.GovernanceDeployment{ContextKey: transition.ContextKey, ExperimentID: manifest.ID, EvidenceID: transition.EvidenceID, State: state, ArtifactVersion: TrendMomentumCandidateVersion, PolicyVersion: "policy-v1", ActivatedAt: base.Add(7 * time.Hour), UpdatedAt: base.Add(7 * time.Hour), AuthorityPolicyJSON: string(payload), AuthorityPolicyDigest: envelope.Digest, TransitionID: transitionID}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT set_config('trading_bot.governance_transition_id', ?, true)", transitionID).Error; err != nil {
			return err
		}
		return tx.Create(&deployment).Error
	}); err != nil {
		t.Fatal(err)
	}
}

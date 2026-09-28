package main

import (
	"os"
	"path/filepath"
	"testing"

	"trading-go/internal/backtest"
	"trading-go/internal/config"
	"trading-go/internal/cutover"
)

func TestValidateConfigRejectsNonCloneAndElevatedPools(t *testing.T) {
	flags := cutover.SafeFlags()
	flags.NewBacktest = "research"
	base := config.Config{DatabaseURL: "postgresql://trading_bot_app_runtime:local@127.0.0.1:5544/trading_bot_research?sslmode=disable", Stage08Flags: flags}
	if err := validateConfig(&base); err != nil {
		t.Fatalf("isolated runtime rejected: %v", err)
	}
	for name, mutate := range map[string]func(*config.Config){
		"source database": func(c *config.Config) {
			c.DatabaseURL = "postgresql://trading_bot_app_runtime:local@127.0.0.1:5544/trading_bot?sslmode=disable"
		},
		"test port": func(c *config.Config) {
			c.DatabaseURL = "postgresql://trading_bot_app_runtime:local@127.0.0.1:5433/trading_bot_research?sslmode=disable"
		},
		"admin login": func(c *config.Config) {
			c.DatabaseURL = "postgresql://postgres:local@127.0.0.1:5544/trading_bot_research?sslmode=disable"
		},
		"missing login": func(c *config.Config) {
			c.DatabaseURL = "postgresql://127.0.0.1:5544/trading_bot_research?sslmode=disable"
		},
		"migration pool": func(c *config.Config) { c.MigrationDatabaseURL = "configured" },
		"ledger pool":    func(c *config.Config) { c.LedgerDatabaseURL = "configured" },
		"parity pool":    func(c *config.Config) { c.ParityDatabaseURL = "configured" },
		"wrong mode":     func(c *config.Config) { c.Stage08Flags.NewBacktest = "off" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base
			mutate(&cfg)
			if err := validateConfig(&cfg); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
}

func TestValidateRevisionRequiresReviewedIdentity(t *testing.T) {
	t.Setenv("BACKTEST_CODE_REVISION", "")
	if err := validateRevision("not-a-sha"); err == nil {
		t.Fatal("unreviewed revision accepted")
	}
}

func TestParseRequestRejectsUnknownAndTrailingFields(t *testing.T) {
	accepted, err := parseRequest([]byte(`{"strategy_id":"trend_momentum_candidate","execution_policy_version":"backtest-execution-v3"}`))
	if err != nil || accepted.ExecutionPolicyVersion != "backtest-execution-v3" {
		t.Fatalf("explicit v3 field not decoded: %v", err)
	}
	for _, payload := range []string{
		`{"strategy_id":"trend_momentum_candidate","execution_cadence":"1h"}`,
		`{"strategy_id":"trend_momentum_candidate","execution_policy_version":"backtest-execution-v2"}`,
		`{"strategy_id":"trend_momentum_candidate"} {"strategy_id":"trend_momentum_candidate"}`,
		`{"strategy_id":"trend_momentum_candidate","overrides":{"backtest_start":"2026-09-01T00:00:00Z"},"evaluation_end":"2026-10-01T00:00:00Z"}`,
	} {
		if _, err := parseRequest([]byte(payload)); err == nil {
			t.Fatalf("unsafe request accepted: %s", payload)
		}
	}
}

func TestOpenLedgerRequiresPrivateRegularFile(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "attempts.jsonl")
	f, err := openLedger(good)
	if err != nil {
		t.Fatal(err)
	}
	if err := appendRecord(f, map[string]string{"status": "submission_intent"}); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if info, err := os.Stat(good); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("attempt ledger was not mode 0600")
	}
	public := filepath.Join(root, "public.jsonl")
	if err := os.WriteFile(public, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := openLedger(public); err == nil {
		t.Fatal("public attempt ledger accepted")
	}
	link := filepath.Join(root, "link.jsonl")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openLedger(link); err == nil {
		t.Fatal("symlink attempt ledger accepted")
	}
}

func TestValidateRequestRejectsParameterAndOverrideDrift(t *testing.T) {
	req := input{Stage05RunRequest: backtest.Stage05RunRequest{
		StrategyID: "trend_momentum_candidate", StrategyVersion: "1.1.0", ExecutionPolicyVersion: "backtest-execution-v3", TargetGrossExposure: "0.75", MaxNetExposure: "0.75", FinalPolicy: "liquidate",
		Parameters: map[string]string{"variant": "combined", "vol_normalization": "true", "lookback_bars": "20", "trend_bars": "20", "regime_bars": "30", "rebalance": "48h", "top_n": "3", "max_positions": "3", "risk_on_gross": "0.75", "neutral_gross": "0.25", "risk_off_gross": "0", "regime_band": "0.02", "position_cap": "0.25", "max_gross": "0.75", "max_net": "0.75", "cash_reserve": "0.25", "vol_floor": "0.02", "turnover_budget": "0.10", "skip_delta": "0.015", "execution_gap_reserve": "0.1", "allocation_tolerance": "0.02", "hard_stop": "0.08", "include_shortlist": "true", "execution_intent": "backtest", "model_observation": "0"},
	}, Overrides: map[string]string{"backtest_dataset_manifest_id": manifestID, "backtest_start": "2024-12-01T00:00:00Z", "backtest_end": "2026-09-01T00:00:00Z", "backtest_symbols": "ETHUSDT,SOLUSDT,BNBUSDT,XRPUSDT,ADAUSDT,DOGEUSDT,AVAXUSDT,LINKUSDT", "backtest_universe_mode": "static", "backtest_fee_bps": "10", "backtest_slippage_bps": "5", "backtest_execution_1m": "true", "backtest_require_point_in_time": "true"}}
	if err := validateRequest(req); err != nil {
		t.Fatalf("frozen request rejected: %v", err)
	}
	req.ExecutionPolicyVersion = "backtest-execution-v4"
	if err := validateRequest(req); err != nil {
		t.Fatalf("fixed capacity-policy request rejected: %v", err)
	}
	req.FinalPolicy = "mark_to_market"
	if err := validateRequest(req); err != nil {
		t.Fatalf("matched capacity mark-to-market request rejected: %v", err)
	}
	req.ExecutionPolicyVersion = "backtest-execution-v3"
	if err := validateRequest(req); err != nil {
		t.Fatalf("matched v3 mark-to-market request rejected: %v", err)
	}
	req.FinalPolicy = "liquidate"
	req.ExecutionPolicyVersion = "backtest-execution-v3"
	req.Parameters["rebalance"] = "24h"
	if err := validateRequest(req); err == nil {
		t.Fatal("cadence drift accepted")
	}
	req.Parameters["rebalance"] = "48h"
	req.Overrides["backtest_end_date"] = "2026-10-01T00:00:00Z"
	if err := validateRequest(req); err == nil {
		t.Fatal("unknown end-date override accepted")
	}
	delete(req.Overrides, "backtest_end_date")
	req.StrategyVersion = "1.2.0"
	req.Parameters["entry_momentum_rule"] = "positive_new_targets_v1"
	if err := validateRequest(req); err != nil {
		t.Fatalf("one-rule exploratory request rejected: %v", err)
	}
	req.ExecutionPolicyVersion = "backtest-execution-v4"
	if err := validateRequest(req); err == nil {
		t.Fatal("failed positive-entry hypothesis accepted under new capacity policy")
	}
	req.ExecutionPolicyVersion = "backtest-execution-v3"
	delete(req.Parameters, "entry_momentum_rule")
	if err := validateRequest(req); err == nil {
		t.Fatal("new request omitted its frozen entry rule")
	}
}

func TestValidateRequestDecisionCouncilFrozenBoundary(t *testing.T) {
	req := input{Stage05RunRequest: backtest.Stage05RunRequest{
		StrategyID: "trend_momentum_candidate", StrategyVersion: "1.3.0", ExecutionPolicyVersion: "backtest-execution-v4", TargetGrossExposure: "0.75", MaxNetExposure: "0.75", FinalPolicy: "mark_to_market",
		Parameters: map[string]string{"variant": "combined", "vol_normalization": "true", "lookback_bars": "20", "trend_bars": "20", "regime_bars": "30", "rebalance": "48h", "top_n": "3", "max_positions": "3", "risk_on_gross": "0.75", "neutral_gross": "0.25", "risk_off_gross": "0", "regime_band": "0.02", "position_cap": "0.25", "max_gross": "0.75", "max_net": "0.75", "cash_reserve": "0.25", "vol_floor": "0.02", "turnover_budget": "0.10", "skip_delta": "0.015", "execution_gap_reserve": "0.1", "allocation_tolerance": "0.02", "hard_stop": "0.08", "include_shortlist": "true", "execution_intent": "backtest", "model_observation": "0", "decision_model": "aihubmix/decision-model-preview", "decision_council_policy": "observe_v1"},
	}, Overrides: map[string]string{"backtest_dataset_manifest_id": manifestID, "backtest_start": "2024-12-01T00:00:00Z", "backtest_end": "2026-09-01T00:00:00Z", "backtest_symbols": "ETHUSDT,SOLUSDT,BNBUSDT,XRPUSDT,ADAUSDT,DOGEUSDT,AVAXUSDT,LINKUSDT", "backtest_universe_mode": "static", "backtest_fee_bps": "10", "backtest_slippage_bps": "5", "backtest_execution_1m": "true", "backtest_require_point_in_time": "true"}}
	t.Setenv("AIHUBMIX_API_TOKEN", "")
	t.Setenv("AIHUBMIX_API_TOKEN_FILE", "")
	if err := validateRequest(req); err == nil {
		t.Fatal("missing provider token accepted")
	}
	t.Setenv("AIHUBMIX_API_TOKEN", "fixture-only")
	if err := validateRequest(req); err != nil {
		t.Fatalf("observe request rejected: %v", err)
	}
	req.Parameters["decision_council_policy"] = "veto_v1"
	if err := validateRequest(req); err != nil {
		t.Fatalf("veto request rejected: %v", err)
	}
	for name, change := range map[string]func(*input){
		"policy":    func(r *input) { r.Parameters["decision_council_policy"] = "autonomous" },
		"model":     func(r *input) { r.Parameters["decision_model"] = "unknown/model" },
		"execution": func(r *input) { r.ExecutionPolicyVersion = "backtest-execution-v3" },
		"final":     func(r *input) { r.FinalPolicy = "liquidate" },
		"override":  func(r *input) { r.Overrides["backtest_end"] = "2026-10-01T00:00:00Z" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := req
			copy.Parameters = make(map[string]string, len(req.Parameters))
			for k, v := range req.Parameters {
				copy.Parameters[k] = v
			}
			copy.Overrides = make(map[string]string, len(req.Overrides))
			for k, v := range req.Overrides {
				copy.Overrides[k] = v
			}
			change(&copy)
			if err := validateRequest(copy); err == nil {
				t.Fatal("altered council request accepted")
			}
		})
	}
}

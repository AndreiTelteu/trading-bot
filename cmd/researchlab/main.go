// Command researchlab runs explicit Stage 05 jobs against the isolated research clone.
// It never starts the HTTP server, schedulers, migrations, or ledger seeding.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"trading-go/internal/backtest"
	"trading-go/internal/config"
	"trading-go/internal/database"
	"trading-go/internal/operations"
	"trading-go/internal/services"
)

const manifestID = "32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc"

type input struct {
	backtest.Stage05RunRequest
	Overrides map[string]string `json:"overrides"`
}

func main() {
	markerFile := flag.String("clone-marker-file", "", "private clone marker file")
	requestFile := flag.String("request-file", "", "exact Stage 05 JSON request (omit for identity preflight)")
	ledgerFile := flag.String("ledger-file", "", "append-only local JSONL attempt ledger")
	expectedSHA := flag.String("reviewed-code-sha", "", "committed reviewed v3 code identity")
	repetitions := flag.Int("repetitions", 1, "exact sequential source jobs, 1 to 3")
	jobTimeout := flag.Duration("job-timeout", 4*time.Hour, "deadline per source job, at most 6h")
	flag.Parse()
	if *markerFile == "" {
		fatal(fmt.Errorf("clone-marker-file is required"))
	}
	cfg, err := config.LoadValidated()
	if err != nil {
		fatal(err)
	}
	if err := validateConfig(cfg); err != nil {
		fatal(err)
	}
	if err := database.OpenCommandPools(cfg, database.CommandPoolRequirements{ValidateRuntime: true}); err != nil {
		fatal(err)
	}
	if err := validateClone(*markerFile); err != nil {
		fatal(err)
	}
	stage08 := operations.New(database.DB, cfg.Stage08Flags)
	stage08.ReadOnly = true
	if _, err := stage08.Initialize(context.Background()); err != nil {
		fatal(err)
	}
	if *requestFile == "" {
		fmt.Println(`{"status":"clone_identity_verified"}`)
		return
	}
	if *ledgerFile == "" || *expectedSHA == "" || *repetitions < 1 || *repetitions > 3 || *jobTimeout < time.Minute || *jobTimeout > 6*time.Hour {
		fatal(fmt.Errorf("submission requires ledger-file, reviewed-code-sha, and 1 to 3 repetitions"))
	}
	if err := validateRevision(*expectedSHA); err != nil {
		fatal(err)
	}
	payload, err := os.ReadFile(*requestFile)
	if err != nil {
		fatal(err)
	}
	request, err := parseRequest(payload)
	if err != nil {
		fatal(err)
	}
	if err := validateRequest(request); err != nil {
		fatal(err)
	}
	ledger, err := openLedger(*ledgerFile)
	if err != nil {
		fatal(err)
	}
	defer ledger.Close()
	services.InitTradingService(cfg.BinanceAPIKey, cfg.BinanceSecret)
	requestDigest := fmt.Sprintf("%x", sha256.Sum256(payload))
	for i := 0; i < *repetitions; i++ {
		if err := appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "repetition": i + 1, "code_sha": *expectedSHA, "request_digest": requestDigest, "status": "submission_intent"}); err != nil {
			fatal(err)
		}
		job, err := backtest.StartStage05ComparisonJob(request.Stage05RunRequest, request.Overrides)
		if err != nil {
			_ = appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "repetition": i + 1, "status": "submission_failed", "error": err.Error()})
			fatal(err)
		}
		if err := appendRecord(ledger, map[string]any{"submitted_at": time.Now().UTC(), "job_id": job.ID, "repetition": i + 1, "code_sha": *expectedSHA, "request_digest": requestDigest, "status": "submitted"}); err != nil {
			fatal(err)
		}
		deadline := time.NewTimer(*jobTimeout)
		poll := time.NewTicker(5 * time.Second)
		for {
			select {
			case <-deadline.C:
				_ = appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "job_id": job.ID, "status": "poll_timeout"})
				fatal(fmt.Errorf("Stage 05 job %d exceeded polling deadline", job.ID))
			case <-poll.C:
			}
			result, err := backtest.GetBacktestJobResponse(job.ID)
			if err != nil {
				_ = appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "job_id": job.ID, "status": "poll_failed", "error": err.Error()})
				fatal(err)
			}
			if result.Status != "completed" && result.Status != "failed" {
				continue
			}
			poll.Stop()
			deadline.Stop()
			record := map[string]any{"finished_at": time.Now().UTC(), "job_id": job.ID, "status": result.Status, "artifact_digest": result.ArtifactDigest, "diagnostic": result.Diagnostic, "error": result.Error}
			if result.Comparison != nil {
				record["dataset_manifest_id"] = result.Comparison.ManifestID
				record["execution_policy_version"] = result.Comparison.Assumptions.ExecutionPolicy.Version
				record["rows"] = result.Comparison.Rows
			}
			if err := appendRecord(ledger, record); err != nil {
				fatal(err)
			}
			fmt.Printf("{\"job_id\":%d,\"status\":%q}\n", job.ID, result.Status)
			if result.Status != "completed" || result.Comparison == nil || result.ArtifactDigest == nil || result.Comparison.Assumptions.ExecutionPolicy.Version != "backtest-execution-v3" || result.Comparison.ManifestID != manifestID {
				fatal(fmt.Errorf("Stage 05 source job %d did not produce complete evidence; stopping repetitions", job.ID))
			}
			break
		}
	}
}

func parseRequest(payload []byte) (input, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return input{}, err
	}
	var policyVersion string
	if err := json.Unmarshal(envelope["execution_policy_version"], &policyVersion); err != nil || policyVersion != "backtest-execution-v3" {
		return input{}, fmt.Errorf("reviewed backtest-execution-v3 policy is required")
	}
	var request input
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return input{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return input{}, fmt.Errorf("request must contain exactly one JSON object")
	}
	return request, nil
}

func validateConfig(cfg *config.Config) error {
	if cfg.MigrationDatabaseURL != "" || cfg.LedgerDatabaseURL != "" || cfg.ParityDatabaseURL != "" {
		return fmt.Errorf("research runner accepts only a runtime DSN")
	}
	u, err := url.Parse(cfg.DatabaseURL)
	if err != nil || u.User == nil || u.Scheme != "postgresql" && u.Scheme != "postgres" || u.Hostname() != "127.0.0.1" || u.Port() != "5544" || u.Path != "/trading_bot_research" || u.User.Username() != "trading_bot_app_runtime" {
		return fmt.Errorf("runtime DSN must identify the isolated research clone")
	}
	if cfg.Stage08Flags.NewBacktest != "research" {
		return fmt.Errorf("Stage 08 research backtest mode is required")
	}
	return nil
}

func validateClone(markerFile string) error {
	want, err := os.ReadFile(markerFile)
	if err != nil {
		return err
	}
	var found struct {
		Database, Login, Marker string
		Port                    int
	}
	err = database.DB.Raw("SELECT current_database() AS database, current_user AS login, shobj_description((SELECT oid FROM pg_database WHERE datname=current_database()), 'pg_database') AS marker, inet_server_port() AS port").Scan(&found).Error
	if err != nil {
		return err
	}
	if found.Database != "trading_bot_research" || found.Login != "trading_bot_app_runtime" || found.Port != 5432 || found.Marker != strings.TrimSpace(string(want)) || !strings.HasPrefix(found.Marker, "bb-research-v3-") {
		return fmt.Errorf("clone identity marker or runtime login mismatch")
	}
	return nil
}

func validateRevision(expected string) error {
	if len(expected) != 40 || strings.Trim(expected, "0123456789abcdef") != "" || os.Getenv("BACKTEST_CODE_REVISION") != expected {
		return fmt.Errorf("BACKTEST_CODE_REVISION must equal the reviewed 40-character SHA")
	}
	head, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != expected {
		return fmt.Errorf("checked-out HEAD differs from reviewed code SHA")
	}
	status, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil || len(status) != 0 {
		return fmt.Errorf("research source tree must be clean")
	}
	return nil
}

func validateRequest(req input) error {
	if err := backtest.ValidateStage05RunRequest(req.Stage05RunRequest); err != nil {
		return err
	}
	expectedParameters := map[string]string{
		"variant": "combined", "vol_normalization": "true", "lookback_bars": "20", "trend_bars": "20", "regime_bars": "30", "rebalance": "48h", "top_n": "3", "max_positions": "3", "risk_on_gross": "0.75", "neutral_gross": "0.25", "risk_off_gross": "0", "regime_band": "0.02", "position_cap": "0.25", "max_gross": "0.75", "max_net": "0.75", "cash_reserve": "0.25", "vol_floor": "0.02", "turnover_budget": "0.10", "skip_delta": "0.015", "execution_gap_reserve": "0.1", "allocation_tolerance": "0.02", "hard_stop": "0.08", "include_shortlist": "true", "execution_intent": "backtest", "model_observation": "0",
	}
	expectedOverrides := map[string]string{
		"backtest_dataset_manifest_id":   manifestID,
		"backtest_start":                 "2024-12-01T00:00:00Z",
		"backtest_end":                   "2026-09-01T00:00:00Z",
		"backtest_symbols":               "ETHUSDT,SOLUSDT,BNBUSDT,XRPUSDT,ADAUSDT,DOGEUSDT,AVAXUSDT,LINKUSDT",
		"backtest_universe_mode":         "static",
		"backtest_fee_bps":               "10",
		"backtest_slippage_bps":          "5",
		"backtest_execution_1m":          "true",
		"backtest_require_point_in_time": "true",
	}
	if req.StrategyID != "trend_momentum_candidate" || req.StrategyVersion != "1.1.0" || req.ExecutionPolicyVersion != "backtest-execution-v3" || req.TargetGrossExposure != "0.75" || req.MaxNetExposure != "0.75" || req.FinalPolicy != "liquidate" || !maps.Equal(req.Parameters, expectedParameters) || !maps.Equal(req.Overrides, expectedOverrides) {
		return fmt.Errorf("request differs from frozen exploratory Stage 05 boundary")
	}
	return nil
}

func openLedger(path string) (*os.File, error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
			return nil, fmt.Errorf("attempt ledger must be a mode-0600 regular file")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		f.Close()
		return nil, fmt.Errorf("attempt ledger must be a mode-0600 regular file")
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

func appendRecord(f *os.File, record any) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(encoded, '\n')); err != nil {
		return err
	}
	return f.Sync()
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }

package backtest

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/pointintime"
	"trading-go/internal/services"
	"trading-go/internal/websocket"
)

var resolveBacktestRevision = func() (string, error) {
	if value := strings.TrimSpace(os.Getenv("BACKTEST_CODE_REVISION")); value != "" {
		return value, nil
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				return setting.Value, nil
			}
		}
	}
	return "", fmt.Errorf("backtest code revision unavailable; inject BACKTEST_CODE_REVISION or VCS build metadata")
}
var fetchBacktestBars = func(symbol, timeframe string, start, end time.Time) ([]services.OHLCV, error) {
	return services.GetExchange().FetchOHLCVRange(symbol, timeframe, start, end)
}
var loadBacktestConstraints = func(symbols []string) (map[string]SymbolConstraints, error) {
	info, err := services.GetExchange().FetchExchangeInfoCached(6 * time.Hour)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, symbol := range symbols {
		wanted[symbol] = true
	}
	result := map[string]SymbolConstraints{}
	for _, symbol := range info.Symbols {
		if !wanted[symbol.Symbol] {
			continue
		}
		constraint := SymbolConstraints{}
		for _, filter := range symbol.Filters {
			switch filter.FilterType {
			case "LOT_SIZE":
				constraint.QuantityStep, _ = strconv.ParseFloat(filter.StepSize, 64)
				constraint.MinQuantity, _ = strconv.ParseFloat(filter.MinQty, 64)
			case "PRICE_FILTER":
				constraint.PriceTick, _ = strconv.ParseFloat(filter.TickSize, 64)
			}
		}
		if constraint.QuantityStep > 0 && constraint.PriceTick > 0 {
			result[symbol.Symbol] = constraint
		}
	}
	return result, nil
}

type BacktestRunSummary struct {
	FailedLane        string                     `json:"failed_lane,omitempty"`
	JobID             uint                       `json:"job_id"`
	StartedAt         time.Time                  `json:"started_at"`
	FinishedAt        time.Time                  `json:"finished_at"`
	BacktestMode      BacktestMode               `json:"backtest_mode"`
	ModelVersion      string                     `json:"model_version,omitempty"`
	PolicyVersion     string                     `json:"policy_version,omitempty"`
	UniverseMode      UniverseMode               `json:"universe_mode"`
	PolicyContext     services.GovernanceContext `json:"policy_context"`
	ExperimentID      string                     `json:"experiment_id,omitempty"`
	CandidateSymbols  []string                   `json:"candidate_symbols,omitempty"`
	DatasetManifestID string                     `json:"dataset_manifest_id,omitempty"`
	SettingsSnapshot  map[string]string          `json:"settings_snapshot,omitempty"`
	Baseline          BacktestResult             `json:"baseline"`
	VolSizing         BacktestResult             `json:"vol_sizing"`
	Validation        ValidationSummary          `json:"validation"`
}

func StartBacktestJob() (*database.BacktestJob, error) {
	job := database.BacktestJob{
		Status:    "pending",
		Progress:  0,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := database.DB.Create(&job).Error; err != nil {
		return nil, err
	}

	go runBacktestJob(job.ID)
	return &job, nil
}

func GetBacktestJob(id uint) (*database.BacktestJob, error) {
	var job database.BacktestJob
	if err := database.DB.First(&job, id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func GetLatestBacktestJob() (*database.BacktestJob, error) {
	var job database.BacktestJob
	if err := database.DB.Order("created_at DESC").First(&job).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func runBacktestJob(jobID uint) {
	startedAt := time.Now()
	updateBacktestJob(jobID, "running", 0.02, "Loading settings")

	settingsSnapshot := services.GetAllSettings()
	config, series, err := prepareBacktestInputsWithSettings(settingsSnapshot)
	if err != nil {
		if pointintime.IsCoverageError(err) {
			coverage := CoverageReport{SchemaVersion: CoverageSchemaVersion, PolicyVersion: "point-in-time-manifest", Passed: false, Reasons: []CoverageReason{CoverageManifestIncompatible}, Diagnostics: []CoverageDiagnostic{{Dataset: "manifest", Status: "failed", Reason: CoverageManifestIncompatible}}}
			result := BacktestResult{Classification: RunCoverageFailed, Coverage: coverage, Manifest: buildManifest(config, coverage, RunCoverageFailed, config.DatasetManifestID)}
			failBacktestJobWithResults(jobID, config, settingsSnapshot, result, result, ValidationSummary{}, "preparation", err)
		} else {
			failBacktestJob(jobID, err)
		}
		return
	}
	database.DB.Model(&database.BacktestJob{}).Where("id=?", jobID).Update("dataset_manifest_id", config.DatasetManifestID)

	updateBacktestJob(jobID, "running", 0.35, "Running baseline + vol sizing backtests")

	var baselineResult, volResult BacktestResult
	var baselineErr, volErr error
	var btWg sync.WaitGroup
	btWg.Add(2)
	go func() {
		defer btWg.Done()
		baselineConfig := config
		baselineConfig.StrategyMode = StrategyBaseline
		baselineResult, baselineErr = RunBacktest(baselineConfig, series)
	}()
	go func() {
		defer btWg.Done()
		volConfig := config
		volConfig.StrategyMode = StrategyVolSizing
		volResult, volErr = RunBacktest(volConfig, series)
	}()
	btWg.Wait()
	if baselineErr != nil {
		failBacktestJobWithResults(jobID, config, settingsSnapshot, baselineResult, volResult, ValidationSummary{}, "baseline", baselineErr)
		return
	}
	if volErr != nil {
		failBacktestJobWithResults(jobID, config, settingsSnapshot, baselineResult, volResult, ValidationSummary{}, "vol_sizing", volErr)
		return
	}

	updateBacktestJob(jobID, "running", 0.7, "Running validation")
	validation, err := RunValidation(config, series,
		getSettingInt(settingsSnapshot, "validation_train_months", 12),
		getSettingInt(settingsSnapshot, "validation_test_months", 3),
		getSettingInt(settingsSnapshot, "validation_bootstrap_iterations", 500),
	)
	if err != nil {
		failBacktestJobWithResults(jobID, config, settingsSnapshot, baselineResult, volResult, validation, "validation", err)
		return
	}

	finishedAt := time.Now()
	summary := BacktestRunSummary{
		JobID:             jobID,
		StartedAt:         startedAt,
		FinishedAt:        finishedAt,
		BacktestMode:      config.BacktestMode,
		ModelVersion:      config.Governance.ModelVersion,
		PolicyVersion:     config.Governance.PolicyVersions.CompositeVersion,
		UniverseMode:      config.UniverseMode,
		PolicyContext:     config.Governance,
		CandidateSymbols:  append([]string(nil), config.Symbols...),
		DatasetManifestID: config.DatasetManifestID,
		SettingsSnapshot:  settingsSnapshot,
		Baseline:          baselineResult,
		VolSizing:         volResult,
		Validation:        validation,
	}
	if experimentID, err := RegisterExperimentRun(jobID, &summary); err == nil {
		summary.ExperimentID = experimentID
		summary.PolicyContext.ExperimentID = experimentID
	} else {
		failBacktestJob(jobID, err)
		return
	}
	outputDir, err := WriteBacktestOutputs(summary, "backtest_results")
	if err != nil {
		failBacktestJob(jobID, err)
		return
	}

	compactSummaryJSON, err := MarshalBacktestJobSummary(summary)
	if err != nil {
		failBacktestJob(jobID, err)
		return
	}
	finalMessage := fmt.Sprintf("Backtest completed (%s)", outputDir)
	updateBacktestJobWithSummary(jobID, "completed", 1.0, finalMessage, compactSummaryJSON, compactSummaryJSON)
	websocket.BroadcastBacktestComplete(jobID, "completed", BuildBacktestJobSummary(summary))
	logActivity("system", "Backtest completed", fmt.Sprintf("Job %d completed", jobID))
}

func RunBacktestSync() (BacktestRunSummary, error) {
	return RunBacktestSyncWithOverrides(nil)
}

func RunBacktestSyncWithOverrides(overrides map[string]string) (BacktestRunSummary, error) {
	settings := services.GetAllSettings()
	for key, value := range overrides {
		if strings.TrimSpace(value) == "" {
			continue
		}
		settings[key] = value
	}
	config, series, err := prepareBacktestInputsWithSettings(settings)
	if err != nil {
		return BacktestRunSummary{}, err
	}

	var baselineResult, volResult BacktestResult
	var baselineErr, volErr error
	var btWg sync.WaitGroup
	btWg.Add(2)
	go func() {
		defer btWg.Done()
		c := config
		c.StrategyMode = StrategyBaseline
		baselineResult, baselineErr = RunBacktest(c, series)
	}()
	go func() {
		defer btWg.Done()
		c := config
		c.StrategyMode = StrategyVolSizing
		volResult, volErr = RunBacktest(c, series)
	}()
	btWg.Wait()
	if baselineErr != nil {
		return BacktestRunSummary{}, baselineErr
	}
	if volErr != nil {
		return BacktestRunSummary{}, volErr
	}

	validation, err := RunValidation(config, series,
		getSettingInt(settings, "validation_train_months", 12),
		getSettingInt(settings, "validation_test_months", 3),
		getSettingInt(settings, "validation_bootstrap_iterations", 500),
	)
	if err != nil {
		return BacktestRunSummary{}, err
	}

	now := time.Now()
	summary := BacktestRunSummary{
		JobID:             0,
		StartedAt:         now,
		FinishedAt:        now,
		BacktestMode:      config.BacktestMode,
		ModelVersion:      config.Governance.ModelVersion,
		PolicyVersion:     config.Governance.PolicyVersions.CompositeVersion,
		UniverseMode:      config.UniverseMode,
		PolicyContext:     config.Governance,
		CandidateSymbols:  append([]string(nil), config.Symbols...),
		DatasetManifestID: config.DatasetManifestID,
		SettingsSnapshot:  settings,
		Baseline:          baselineResult,
		VolSizing:         volResult,
		Validation:        validation,
	}

	if experimentID, err := RegisterExperimentRun(0, &summary); err == nil {
		summary.ExperimentID = experimentID
		summary.PolicyContext.ExperimentID = experimentID
	} else {
		return BacktestRunSummary{}, err
	}

	if _, err := WriteBacktestOutputs(summary, "backtest_results"); err != nil {
		return BacktestRunSummary{}, err
	}

	return summary, nil
}

func WriteBacktestOutputs(summary BacktestRunSummary, outputBase string) (string, error) {
	if outputBase == "" {
		outputBase = "backtest_results"
	}
	label := fmt.Sprintf("run_%s", summary.StartedAt.Format("20060102_150405"))
	if summary.JobID > 0 {
		label = fmt.Sprintf("%s_job_%d", label, summary.JobID)
	}
	outputDir := filepath.Join(outputBase, label)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", err
	}

	summaryBytes, err := json.MarshalIndent(BuildBacktestJobSummary(summary), "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "summary.json"), summaryBytes, 0o644); err != nil {
		return "", err
	}

	metricsSummary := map[string]interface{}{
		"backtest_mode":  summary.BacktestMode,
		"model_version":  summary.ModelVersion,
		"policy_version": summary.PolicyVersion,
		"policy_context": summary.PolicyContext,
		"baseline":       summary.Baseline.Metrics,
		"vol_sizing":     summary.VolSizing.Metrics,
		"validation":     summary.Validation,
	}
	metricsBytes, err := json.MarshalIndent(metricsSummary, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "metrics_summary.json"), metricsBytes, 0o644); err != nil {
		return "", err
	}

	governanceBytes, err := json.MarshalIndent(map[string]interface{}{
		"backtest_mode":  summary.BacktestMode,
		"model_version":  summary.ModelVersion,
		"policy_version": summary.PolicyVersion,
		"policy_context": summary.PolicyContext,
		"experiment_id":  summary.ExperimentID,
	}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "governance_summary.json"), governanceBytes, 0o644); err != nil {
		return "", err
	}

	diagnosticsBytes, err := json.MarshalIndent(map[string]interface{}{
		"baseline": map[string]interface{}{
			"ranking_metrics": summary.Baseline.RankingMetrics,
			"diagnostics":     summary.Baseline.Diagnostics,
		},
		"vol_sizing": map[string]interface{}{
			"ranking_metrics": summary.VolSizing.RankingMetrics,
			"diagnostics":     summary.VolSizing.Diagnostics,
		},
		"validation": map[string]interface{}{
			"window_summaries":          summary.Validation.WindowSummaries,
			"promotion_readiness":       summary.Validation.PromotionReadiness,
			"baseline_regime_slices":    summary.Validation.BaselineRegimeSlices,
			"vol_regime_slices":         summary.Validation.VolSizingRegimeSlices,
			"baseline_symbol_cohorts":   summary.Validation.BaselineSymbolCohorts,
			"vol_symbol_cohorts":        summary.Validation.VolSizingSymbolCohorts,
			"baseline_decile_metrics":   summary.Validation.BaselineDecileMetrics,
			"vol_sizing_decile_metrics": summary.Validation.VolSizingDecileMetrics,
		},
	}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "strategy_diagnostics.json"), diagnosticsBytes, 0o644); err != nil {
		return "", err
	}

	if err := writeEquityCSV(filepath.Join(outputDir, "baseline_portfolio_equity.csv"), summary.Baseline.Equity); err != nil {
		return "", err
	}
	if err := writeEquityCSV(filepath.Join(outputDir, "vol_sizing_portfolio_equity.csv"), summary.VolSizing.Equity); err != nil {
		return "", err
	}

	if err := writeEquityBySymbol(outputDir, "baseline", summary.Baseline.EquityBySymbol); err != nil {
		return "", err
	}
	if err := writeEquityBySymbol(outputDir, "vol_sizing", summary.VolSizing.EquityBySymbol); err != nil {
		return "", err
	}
	if err := writeVersionedArtifacts(outputDir, "baseline", summary.Baseline); err != nil {
		return "", err
	}
	if err := writeVersionedArtifacts(outputDir, "vol_sizing", summary.VolSizing); err != nil {
		return "", err
	}

	return outputDir, nil
}

func writeVersionedArtifacts(outputDir, prefix string, result BacktestResult) error {
	if result.Manifest.SchemaVersion == "" {
		return nil
	}
	artifacts, err := MarshalArtifactBytes(result)
	if err != nil {
		return err
	}
	files := map[string][]byte{
		"manifest.json": artifacts.Manifest, "decisions.json": artifacts.Decisions,
		"orders.json": artifacts.Orders, "fills.json": artifacts.Fills,
		"trades.json": artifacts.Trades,
		"ledger.json": artifacts.Ledger, "exposure.json": artifacts.Exposure,
		"equity.json": artifacts.Equity, "metrics.json": artifacts.Metrics,
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	dir := filepath.Join(outputDir, prefix+"_artifacts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), files[name], 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeEquityBySymbol(outputDir string, prefix string, equityBySymbol map[string][]EquityPoint) error {
	for symbol, equity := range equityBySymbol {
		if len(equity) == 0 {
			continue
		}
		safeSymbol := strings.ReplaceAll(symbol, "/", "_")
		safeSymbol = strings.ReplaceAll(safeSymbol, ":", "_")
		fileName := fmt.Sprintf("%s_%s_equity.csv", prefix, safeSymbol)
		if err := writeEquityCSV(filepath.Join(outputDir, fileName), equity); err != nil {
			return err
		}
	}
	return nil
}

func writeEquityCSV(path string, equity []EquityPoint) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"timestamp", "equity"}); err != nil {
		return err
	}
	for _, point := range equity {
		row := []string{point.Time.Format(time.RFC3339), strconv.FormatFloat(point.Value, 'f', 6, 64)}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func updateBacktestJob(jobID uint, status string, progress float64, message string) {
	now := time.Now()
	job := database.BacktestJob{
		ID:        jobID,
		Status:    status,
		Progress:  progress,
		UpdatedAt: now,
	}
	if status == "running" {
		job.StartedAt = &now
	}
	if message != "" {
		job.Message = &message
	}
	database.DB.Model(&database.BacktestJob{}).Where("id = ?", jobID).Updates(&job)
	websocket.BroadcastBacktestProgress(jobID, status, progress, message)
}

func updateBacktestJobWithSummary(jobID uint, status string, progress float64, message string, summaryJSON string, summaryCompactJSON string) {
	now := time.Now()
	job := database.BacktestJob{
		ID:         jobID,
		Status:     status,
		Progress:   progress,
		UpdatedAt:  now,
		FinishedAt: &now,
	}
	if message != "" {
		job.Message = &message
	}
	if summaryJSON != "" {
		job.SummaryJSON = &summaryJSON
	}
	if summaryCompactJSON != "" {
		job.SummaryCompactJSON = &summaryCompactJSON
	}
	database.DB.Model(&database.BacktestJob{}).Where("id = ?", jobID).Updates(&job)
	websocket.BroadcastBacktestProgress(jobID, status, progress, message)
}

func failBacktestJob(jobID uint, err error) {
	msg := err.Error()
	now := time.Now()
	job := database.BacktestJob{
		ID:         jobID,
		Status:     "failed",
		Progress:   1.0,
		UpdatedAt:  now,
		FinishedAt: &now,
		Error:      &msg,
	}
	database.DB.Model(&database.BacktestJob{}).Where("id = ?", jobID).Updates(&job)
	websocket.BroadcastBacktestComplete(jobID, "failed", map[string]interface{}{
		"error": msg,
	})
	logActivity("error", "Backtest failed", msg)
}

func failBacktestJobWithResults(jobID uint, config BacktestConfig, settings map[string]string, baseline, vol BacktestResult, validation ValidationSummary, lane string, err error) {
	summary := BacktestRunSummary{JobID: jobID, FailedLane: lane, BacktestMode: config.BacktestMode, UniverseMode: config.UniverseMode, PolicyContext: config.Governance, CandidateSymbols: append([]string(nil), config.Symbols...), DatasetManifestID: config.DatasetManifestID, SettingsSnapshot: settings, Baseline: baseline, VolSizing: vol, Validation: validation}
	compact, _ := MarshalBacktestJobSummary(summary)
	msg := fmt.Sprintf("%s: %v", lane, err)
	now := time.Now()
	manifestID := config.DatasetManifestID
	var manifestPtr *string
	if manifestID != "" {
		var count int64
		if database.DB.Model(&database.DatasetManifest{}).Where("id=?", manifestID).Count(&count).Error == nil && count == 1 {
			manifestPtr = &manifestID
		}
	}
	database.DB.Model(&database.BacktestJob{}).Where("id = ?", jobID).Updates(&database.BacktestJob{ID: jobID, Status: "failed", Progress: 1, UpdatedAt: now, FinishedAt: &now, Error: &msg, SummaryJSON: &compact, SummaryCompactJSON: &compact, DatasetManifestID: manifestPtr})
	websocket.BroadcastBacktestComplete(jobID, "failed", BuildBacktestJobSummary(summary))
}

func prepareBacktestInputs() (BacktestConfig, map[string][]services.OHLCV, error) {
	settings := services.GetAllSettings()
	return prepareBacktestInputsWithSettings(settings)
}

func prepareBacktestInputsWithSettings(settings map[string]string) (BacktestConfig, map[string][]services.OHLCV, error) {
	if getSettingBool(settings, "backtest_require_point_in_time", false) || strings.TrimSpace(settings["backtest_dataset_manifest_id"]) != "" {
		return preparePointInTimeBacktestInputs(settings)
	}
	wallet := database.Wallet{}
	database.DB.First(&wallet)

	policy := services.GetUniversePolicy(settings)
	universeMode := resolveUniverseMode(settings)
	symbols := parseSymbols(settings["backtest_symbols"])
	if universeMode == UniverseDynamicRecompute && len(symbols) == 0 {
		discoveredSymbols, err := services.DiscoverEligibleUniverseSymbols()
		if err != nil {
			return BacktestConfig{}, nil, err
		}
		symbols = discoveredSymbols
	}
	if len(symbols) == 0 {
		return BacktestConfig{}, nil, fmt.Errorf("backtest_symbols is empty")
	}

	start, err := parseTime(settings["backtest_start"])
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	end, err := parseTime(settings["backtest_end"])
	if err != nil {
		return BacktestConfig{}, nil, err
	}

	timeframe := "15m"
	timeframeMinutes := 15

	series := map[string][]services.OHLCV{}
	executionSeries := map[string][]services.OHLCV{}
	fetchExecution := getSettingBool(settings, "backtest_execution_1m", false)

	type fetchResult struct {
		symbol  string
		candles []services.OHLCV
		exec1m  []services.OHLCV
		err     error
	}

	workers := runtime.NumCPU()
	if workers > len(symbols) {
		workers = len(symbols)
	}
	if workers < 1 {
		workers = 1
	}

	symbolCh := make(chan string, len(symbols))
	resultCh := make(chan fetchResult, len(symbols))
	for _, s := range symbols {
		symbolCh <- s
	}
	close(symbolCh)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sym := range symbolCh {
				candles, fetchErr := fetchBacktestBars(sym, timeframe, start, end)
				res := fetchResult{symbol: sym, candles: candles, err: fetchErr}
				if fetchErr == nil && fetchExecution {
					exec1m, execErr := fetchBacktestBars(sym, "1m", start, end)
					if execErr == nil && len(exec1m) > 0 {
						res.exec1m = exec1m
					}
				}
				resultCh <- res
			}
		}()
	}
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for res := range resultCh {
		if res.err != nil {
			return BacktestConfig{}, nil, fmt.Errorf("fetch %s: %w", res.symbol, res.err)
		}
		series[res.symbol] = res.candles
		if len(res.exec1m) > 0 {
			executionSeries[res.symbol] = res.exec1m
		}
	}
	engineMode, err := resolveBacktestEngine(settings)
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	benchmarkSymbol := strings.ToUpper(getSettingString(settings, "backtest_benchmark_symbol", "BTCUSDT"))
	benchmarkRequired := true
	if start.IsZero() || end.IsZero() {
		rangeStart, rangeEnd := seriesTimeRange(series)
		if start.IsZero() {
			start = rangeStart
		}
		if end.IsZero() {
			end = rangeEnd
		}
		// Discovery calls establish one immutable interval; all decision and
		// execution datasets are then reloaded against it.
		for _, symbol := range symbols {
			bounded, fetchErr := fetchBacktestBars(symbol, timeframe, start, end)
			if fetchErr != nil {
				return BacktestConfig{}, nil, fetchErr
			}
			series[symbol] = bounded
			if fetchExecution {
				execBars, execErr := fetchBacktestBars(symbol, "1m", start, end)
				if execErr == nil {
					executionSeries[symbol] = execBars
				}
			}
		}
	}
	benchmarkSeries, err := fetchBacktestBars(benchmarkSymbol, timeframe, start, end)
	if err != nil {
		return BacktestConfig{}, nil, fmt.Errorf("fetch independent benchmark %s: %w", benchmarkSymbol, err)
	}
	revision, err := resolveBacktestRevision()
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	constraints, err := loadBacktestConstraints(symbols)
	if err != nil {
		return BacktestConfig{}, nil, fmt.Errorf("load symbol constraints: %w", err)
	}
	constraintsAvailable := len(symbols) > 0
	for _, symbol := range symbols {
		constraint, ok := constraints[symbol]
		if !ok || constraint.QuantityStep <= 0 || constraint.PriceTick <= 0 || constraint.MinQuantity <= 0 {
			constraintsAvailable = false
			break
		}
	}

	modelPolicy := services.GetModelSelectionPolicy(settings)
	governance, err := services.ResolveGovernanceContext(settings, string(universeMode))
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	modelArtifact, err := services.LoadConfiguredModel(settings)
	if err != nil {
		return BacktestConfig{}, nil, err
	}

	config := BacktestConfig{
		EngineMode:      engineMode,
		CodeRevision:    revision,
		ConfigVersion:   getSettingString(settings, "backtest_config_version", "backtest-config-v1"),
		StrategyVersion: "legacy-rule-strategy-v1",
		Seed:            int64(getSettingInt(settings, "backtest_seed", 0)),
		AccountID:       "backtest", SettlementCurrency: getSettingString(settings, "backtest_settlement_currency", "USDT"), VenueID: getSettingString(settings, "backtest_venue_id", "binance"),
		BacktestMode:            resolveBacktestMode(universeMode, modelArtifact != nil),
		ExecutionSeries:         executionSeries,
		ExecutionSeriesRequired: fetchExecution,
		ExecutionTimeframe:      "1m",
		ExecutionTimeframeMins:  1,
		BenchmarkSymbol:         benchmarkSymbol,
		BenchmarkSeries:         benchmarkSeries,
		BenchmarkRequired:       benchmarkRequired,
		ConstraintsAvailable:    constraintsAvailable,
		Symbols:                 symbols,
		UniverseMode:            universeMode,
		UniversePolicy:          policy,
		Governance:              governance,
		Start:                   start,
		End:                     end,
		IndicatorConfig:         services.GetIndicatorSettings(),
		IndicatorWeights:        services.GetIndicatorWeights(),
		Timeframe:               timeframe,
		TimeframeMinutes:        timeframeMinutes,
		InitialBalance:          1000.0, // hardcoded starting balance for backtests
		FeeBps:                  getSettingFloat(settings, "backtest_fee_bps", 10),
		SlippageBps:             getSettingFloat(settings, "backtest_slippage_bps", 5),
		ModelArtifact:           modelArtifact,
		ModelPolicy:             modelPolicy,
		MaxPositions:            getSettingInt(settings, "max_positions", 5),
		TimeStopBars:            getSettingInt(settings, "time_stop_bars", 0),
		EntryPercent:            getSettingFloat(settings, "entry_percent", 5.0),
		StopLossPercent:         getSettingFloat(settings, "stop_loss_percent", 5.0),
		TakeProfitPercent:       getSettingFloat(settings, "take_profit_percent", 30.0),
		RiskPerTrade:            getSettingFloat(settings, "risk_per_trade", 0.5),
		StopMult:                getSettingFloat(settings, "stop_mult", 1.5),
		TpMult:                  getSettingFloat(settings, "tp_mult", 3.0),
		MaxPositionValue:        getSettingFloat(settings, "max_position_value", 0),
		AtrPeriod:               getSettingInt(settings, "atr_trailing_period", 14),
		AtrTrailingEnabled:      getSettingBool(settings, "atr_trailing_enabled", false),
		AtrTrailingMult:         getSettingFloat(settings, "atr_trailing_mult", 1.0),
		AtrAnnualizationEnabled: getSettingBool(settings, "atr_annualization_enabled", false),
		AtrAnnualizationDays:    getSettingInt(settings, "atr_annualization_days", 365),
		BuyOnlyStrong:           getSettingBool(settings, "buy_only_strong", true),
		MinConfidenceToBuy:      getSettingFloat(settings, "min_confidence_to_buy", 4.0),
		SellOnSignal:            getSettingBool(settings, "sell_on_signal", true),
		MinConfidenceToSell:     getSettingFloat(settings, "min_confidence_to_sell", 3.5),
		AllowSellAtLoss:         getSettingBool(settings, "allow_sell_at_loss", false),
		TrailingStopEnabled:     getSettingBool(settings, "trailing_stop_enabled", false),
		TrailingStopPercent:     getSettingFloat(settings, "trailing_stop_percent", 10.0),
		ExecutionPolicy:         ExecutionPolicy{Version: "backtest-execution-v1", Timing: ExecutionNextExecutable, Liquidity: LiquidityFullFillOHLCV, CostVersion: "backtest-cost-v1", Constraints: constraints},
	}
	if modelArtifact != nil {
		config.CoveragePolicy.RequiredModelFeatures = make([]string, 0, len(modelArtifact.Features))
		for _, feature := range modelArtifact.Features {
			config.CoveragePolicy.RequiredModelFeatures = append(config.CoveragePolicy.RequiredModelFeatures, feature.Name)
		}
		config.FeatureSeries = buildRuntimeFeatureCoverage(config.CoveragePolicy.RequiredModelFeatures, series, time.Duration(timeframeMinutes)*time.Minute)
	}

	return config, series, nil
}

func preparePointInTimeBacktestInputs(settings map[string]string) (BacktestConfig, map[string][]services.OHLCV, error) {
	manifestID := strings.TrimSpace(settings["backtest_dataset_manifest_id"])
	manifest, loadErr := pointintime.LoadManifest(database.DB, manifestID)
	if loadErr != nil {
		return BacktestConfig{DatasetManifestID: manifestID, DatasetManifestRequired: true}, nil, &pointintime.CoverageError{Report: pointintime.CoverageReport{SchemaVersion: pointintime.CoverageSchemaVersion, ManifestID: manifestID, Compatible: false, Failures: []pointintime.CoverageFailure{{Code: "manifest_not_found", Details: loadErr.Error()}}}}
	}
	start, err := parseTime(settings["backtest_start"])
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	end, err := parseTime(settings["backtest_end"])
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	if start.IsZero() {
		start, _ = time.Parse(time.RFC3339Nano, manifest.RequestedStart)
	}
	if end.IsZero() {
		end, _ = time.Parse(time.RFC3339Nano, manifest.RequestedEnd)
	}
	benchmark := strings.ToUpper(getSettingString(settings, "backtest_benchmark_symbol", "BTCUSDT"))
	symbols := parseSymbols(settings["backtest_symbols"])
	if len(symbols) == 0 {
		seen := map[string]bool{}
		for _, covered := range manifest.Series {
			if covered.Role == pointintime.RoleDecision && covered.Timeframe == "15m" && covered.Ticker != "" && !seen[strings.ToUpper(covered.Ticker)] {
				symbol := strings.ToUpper(covered.Ticker)
				seen[symbol] = true
				symbols = append(symbols, symbol)
			}
		}
		sort.Strings(symbols)
	}
	fetchExecution := getSettingBool(settings, "backtest_execution_1m", false)
	roles := map[string]string{pointintime.RoleDecision: "15m", pointintime.RoleBenchmark: "15m"}
	if fetchExecution {
		roles[pointintime.RoleExecution] = "1m"
	}
	validated, report, err := pointintime.ValidateManifest(database.DB, pointintime.ManifestRequirement{ManifestID: manifestID, Start: start, End: end, Symbols: symbols, Roles: roles, RequireComplete: true})
	if err != nil {
		return BacktestConfig{DatasetManifestID: manifestID, DatasetManifestRequired: true, DatasetLimitations: report.Limitations}, nil, err
	}
	hasExactSeries := func(ticker, role, frame string) bool {
		for _, covered := range validated.Series {
			if strings.EqualFold(covered.Ticker, ticker) && covered.Role == role && covered.Timeframe == frame && covered.Complete {
				return true
			}
		}
		return false
	}
	for _, symbol := range symbols {
		if !hasExactSeries(symbol, pointintime.RoleDecision, "15m") {
			report.Compatible = false
			report.Failures = append(report.Failures, pointintime.CoverageFailure{Code: "symbol_role_timeframe_missing", Series: symbol + ":decision:15m", Details: "required tradable decision series is absent or incomplete"})
		}
		if fetchExecution && !hasExactSeries(symbol, pointintime.RoleExecution, "1m") {
			report.Compatible = false
			report.Failures = append(report.Failures, pointintime.CoverageFailure{Code: "symbol_role_timeframe_missing", Series: symbol + ":execution:1m", Details: "required tradable execution series is absent or incomplete"})
		}
	}
	if !hasExactSeries(benchmark, pointintime.RoleBenchmark, "15m") {
		report.Compatible = false
		report.Failures = append(report.Failures, pointintime.CoverageFailure{Code: "benchmark_role_timeframe_missing", Series: benchmark + ":benchmark:15m", Details: "required benchmark series is absent or incomplete"})
	}
	if !report.Compatible {
		return BacktestConfig{DatasetManifestID: manifestID, DatasetManifestRequired: true, DatasetLimitations: report.Limitations}, nil, &pointintime.CoverageError{Report: report}
	}
	var exchangeSymbols []database.ExchangeSymbol
	if err := database.DB.Where("ticker IN ?", append(append([]string(nil), symbols...), benchmark)).Order("listed_at ASC").Find(&exchangeSymbols).Error; err != nil {
		return BacktestConfig{}, nil, err
	}
	byTicker := map[string][]database.ExchangeSymbol{}
	for _, s := range exchangeSymbols {
		byTicker[s.Ticker] = append(byTicker[s.Ticker], s)
	}
	repo := pointintime.Repository{DB: database.DB}
	series := map[string][]services.OHLCV{}
	execution := map[string][]services.OHLCV{}
	identities := map[string]string{}
	economicIdentities := map[string]string{}
	lifecycles := map[string]SymbolLifecycle{}
	loadTicker := func(ticker, role, frame string) ([]services.OHLCV, error) {
		rows := byTicker[ticker]
		combined := []services.OHLCV{}
		for _, s := range rows {
			bars, e := repo.Bars(manifestID, s.ID, role, frame, start, end, end)
			if e != nil {
				return nil, e
			}
			combined = append(combined, bars...)
			identities[ticker] = s.ID
			economicIdentities[ticker] = s.AssetID
			lifecycles[ticker] = SymbolLifecycle{ListedAt: s.ListedAt, DelistedAt: s.DelistedAt}
		}
		sort.Slice(combined, func(i, j int) bool { return combined[i].OpenTime < combined[j].OpenTime })
		return combined, nil
	}
	for _, symbol := range symbols {
		bars, e := loadTicker(symbol, pointintime.RoleDecision, "15m")
		if e != nil {
			return BacktestConfig{}, nil, e
		}
		series[symbol] = bars
		if fetchExecution {
			bars, e = loadTicker(symbol, pointintime.RoleExecution, "1m")
			if e != nil {
				return BacktestConfig{}, nil, e
			}
			execution[symbol] = bars
		}
	}
	benchmarkBars, err := loadTicker(benchmark, pointintime.RoleBenchmark, "15m")
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	engineMode, err := resolveBacktestEngine(settings)
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	revision, err := resolveBacktestRevision()
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	governance, err := services.ResolveGovernanceContext(settings, string(UniverseDynamicReplay))
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	modelArtifact, err := services.LoadConfiguredModel(settings)
	if err != nil {
		return BacktestConfig{}, nil, err
	}
	policy := services.GetUniversePolicy(settings)
	resolver := func(symbol string, at time.Time) (SymbolConstraints, bool) {
		id := ""
		for _, candidate := range byTicker[symbol] {
			if !candidate.ListedAt.After(at) && (candidate.DelistedAt == nil || candidate.DelistedAt.After(at)) {
				id = candidate.ID
			}
		}
		if id == "" {
			return SymbolConstraints{}, false
		}
		value, e := repo.ConstraintAsOf(id, at)
		if e != nil {
			return SymbolConstraints{}, false
		}
		return SymbolConstraints{QuantityStep: value.QuantityStep, PriceTick: value.PriceTick, MinQuantity: value.MinQuantity, MinNotional: value.MinNotional}, true
	}
	constraintsAvailable := true
	for _, symbol := range symbols {
		for _, version := range byTicker[symbol] {
			coverageStart, coverageEnd := start, end
			if version.ListedAt.After(coverageStart) {
				coverageStart = version.ListedAt
			}
			if version.DelistedAt != nil && version.DelistedAt.Before(coverageEnd) {
				coverageEnd = *version.DelistedAt
			}
			if coverageEnd.After(coverageStart) && !repo.ConstraintsCover(version.ID, coverageStart, coverageEnd) {
				constraintsAvailable = false
			}
		}
	}
	config := BacktestConfig{EngineMode: engineMode, CodeRevision: revision, ConfigVersion: getSettingString(settings, "backtest_config_version", "backtest-config-v1"), StrategyVersion: "legacy-rule-strategy-v1", Seed: int64(getSettingInt(settings, "backtest_seed", 0)), AccountID: "backtest", SettlementCurrency: getSettingString(settings, "backtest_settlement_currency", "USDT"), VenueID: getSettingString(settings, "backtest_venue_id", "binance"), BacktestMode: resolveBacktestMode(UniverseDynamicReplay, modelArtifact != nil), ExecutionSeries: execution, ExecutionSeriesRequired: fetchExecution, ExecutionTimeframe: "1m", ExecutionTimeframeMins: 1, BenchmarkSymbol: benchmark, BenchmarkSeries: benchmarkBars, BenchmarkRequired: true, ConstraintsAvailable: constraintsAvailable, Symbols: symbols, UniverseMode: UniverseDynamicReplay, UniversePolicy: policy, Governance: governance, Start: start, End: end, IndicatorConfig: services.GetIndicatorSettings(), IndicatorWeights: services.GetIndicatorWeights(), Timeframe: "15m", TimeframeMinutes: 15, InitialBalance: 1000, FeeBps: getSettingFloat(settings, "backtest_fee_bps", 10), SlippageBps: getSettingFloat(settings, "backtest_slippage_bps", 5), ModelArtifact: modelArtifact, ModelPolicy: services.GetModelSelectionPolicy(settings), MaxPositions: getSettingInt(settings, "max_positions", 5), TimeStopBars: getSettingInt(settings, "time_stop_bars", 0), EntryPercent: getSettingFloat(settings, "entry_percent", 5), StopLossPercent: getSettingFloat(settings, "stop_loss_percent", 5), TakeProfitPercent: getSettingFloat(settings, "take_profit_percent", 30), RiskPerTrade: getSettingFloat(settings, "risk_per_trade", .5), StopMult: getSettingFloat(settings, "stop_mult", 1.5), TpMult: getSettingFloat(settings, "tp_mult", 3), MaxPositionValue: getSettingFloat(settings, "max_position_value", 0), AtrPeriod: getSettingInt(settings, "atr_trailing_period", 14), AtrTrailingEnabled: getSettingBool(settings, "atr_trailing_enabled", false), AtrTrailingMult: getSettingFloat(settings, "atr_trailing_mult", 1), AtrAnnualizationEnabled: getSettingBool(settings, "atr_annualization_enabled", false), AtrAnnualizationDays: getSettingInt(settings, "atr_annualization_days", 365), BuyOnlyStrong: getSettingBool(settings, "buy_only_strong", true), MinConfidenceToBuy: getSettingFloat(settings, "min_confidence_to_buy", 4), SellOnSignal: getSettingBool(settings, "sell_on_signal", true), MinConfidenceToSell: getSettingFloat(settings, "min_confidence_to_sell", 3.5), AllowSellAtLoss: getSettingBool(settings, "allow_sell_at_loss", false), TrailingStopEnabled: getSettingBool(settings, "trailing_stop_enabled", false), TrailingStopPercent: getSettingFloat(settings, "trailing_stop_percent", 10), ExecutionPolicy: ExecutionPolicy{Version: "backtest-execution-v1", Timing: ExecutionNextExecutable, Liquidity: LiquidityFullFillOHLCV, CostVersion: "backtest-cost-v1", Constraints: map[string]SymbolConstraints{}}, DatasetManifestID: validated.ID, DatasetManifestValidated: true, DatasetManifestRequired: true, DatasetLimitations: validated.Limitations, SymbolIdentities: identities, EconomicAssetIdentities: economicIdentities, SymbolLifecycles: lifecycles, ConstraintResolver: resolver}
	if !constraintsAvailable {
		config.DatasetLimitations = append(config.DatasetLimitations, "historical_symbol_constraints_incomplete_safe_fallback")
	}
	if modelArtifact != nil {
		for _, feature := range modelArtifact.Features {
			config.CoveragePolicy.RequiredModelFeatures = append(config.CoveragePolicy.RequiredModelFeatures, feature.Name)
		}
		config.FeatureSeries = buildRuntimeFeatureCoverage(config.CoveragePolicy.RequiredModelFeatures, series, 15*time.Minute)
	}
	return config, series, nil
}

func parseSymbols(value string) []string {
	parts := strings.Split(value, ",")
	var symbols []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(strings.ToUpper(p))
		if trimmed != "" {
			symbols = append(symbols, trimmed)
		}
	}
	return symbols
}

func buildRuntimeFeatureCoverage(names []string, series map[string][]services.OHLCV, interval time.Duration) []FeatureSeries {
	symbols := sortedSymbols(series)
	result := make([]FeatureSeries, 0, len(names))
	if len(symbols) == 0 {
		return result
	}
	observations := map[string][]FeatureObservation{}
	source := series[symbols[0]]
	btc := series["BTCUSDT"]
	for i, bar := range source {
		if i < 119 {
			continue
		}
		available := time.UnixMilli(bar.CloseTime)
		candidate := services.UniverseCandidateMetrics{Symbol: symbols[0], LastPrice: bar.Close}
		btcEnd := i + 1
		if btcEnd > len(btc) {
			btcEnd = len(btc)
		}
		row := services.BuildModelFeatureRow(services.ModelFeatureInput{Timestamp: available, Symbol: symbols[0], Candles15m: candlesFromOHLCV(source[:i+1]), Candidate: candidate, ActiveUniverse: []services.UniverseCandidateMetrics{candidate}, BTCCandles15m: candlesFromOHLCV(btc[:btcEnd])})
		if !row.Valid {
			continue
		}
		for _, name := range names {
			if value, ok := row.Values[name]; ok {
				observations[name] = append(observations[name], FeatureObservation{EventAt: available, AvailableAt: available, Value: value})
			}
		}
	}
	for _, name := range names {
		result = append(result, FeatureSeries{Name: name, Version: services.ModelFeatureSpecVersion, Provenance: "services.BuildModelFeatureRow:" + symbols[0], Interval: interval, Observations: observations[name]})
	}
	return result
}

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid time format: %s", value)
}

func seriesTimeRange(series map[string][]services.OHLCV) (time.Time, time.Time) {
	var earliest int64
	var latest int64
	for _, candles := range series {
		if len(candles) == 0 {
			continue
		}
		first := candles[0].OpenTime
		last := candles[len(candles)-1].OpenTime
		if earliest == 0 || first < earliest {
			earliest = first
		}
		if latest == 0 || last > latest {
			latest = last
		}
	}
	if earliest == 0 || latest == 0 {
		return time.Time{}, time.Time{}
	}
	return time.UnixMilli(earliest), time.UnixMilli(latest)
}

func getSettingBool(settings map[string]string, key string, defaultVal bool) bool {
	val, ok := settings[key]
	if !ok {
		return defaultVal
	}
	return strings.ToLower(val) == "true"
}

func getSettingString(settings map[string]string, key, defaultVal string) string {
	if value, ok := settings[key]; ok && strings.TrimSpace(value) != "" {
		return value
	}
	return defaultVal
}

func getSettingInt(settings map[string]string, key string, defaultVal int) int {
	val, ok := settings[key]
	if !ok {
		return defaultVal
	}
	v, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return v
}

func getSettingFloat(settings map[string]string, key string, defaultVal float64) float64 {
	val, ok := settings[key]
	if !ok {
		return defaultVal
	}
	v, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return defaultVal
	}
	return v
}

func resolveUniverseMode(settings map[string]string) UniverseMode {
	mode := strings.ToLower(strings.TrimSpace(settings["backtest_universe_mode"]))
	switch mode {
	case string(UniverseDynamicRecompute):
		return UniverseDynamicRecompute
	case string(UniverseDynamicReplay):
		return UniverseDynamicReplay
	default:
		return UniverseStatic
	}
}

func resolveBacktestMode(universeMode UniverseMode, hasModel bool) BacktestMode {
	if hasModel {
		if universeMode == UniverseDynamicReplay {
			return BacktestModePaperReplay
		}
		if universeMode == UniverseDynamicRecompute {
			return BacktestModeDynamicModel
		}
		return BacktestModePaperReplay
	}
	if universeMode == UniverseDynamicRecompute {
		return BacktestModeDynamicRule
	}
	if universeMode == UniverseDynamicReplay {
		return BacktestModeDynamicRule
	}
	return BacktestModeLegacyStatic
}

func resolveBacktestEngine(settings map[string]string) (EngineMode, error) {
	mode := EngineMode(getSettingString(settings, "backtest_engine_mode", string(EngineShared)))
	if mode != EngineShared {
		return "", fmt.Errorf("production backtest engine must be %q, got %q", EngineShared, mode)
	}
	return mode, nil
}

func logActivity(logType, message string, details string) {
	log := database.ActivityLog{
		LogType:   logType,
		Message:   message,
		Timestamp: time.Now(),
	}
	if details != "" {
		log.Details = &details
	}
	database.DB.Create(&log)
	websocket.BroadcastActivityLogNew(log)
}

package backtest

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/services"
	"trading-go/internal/websocket"
)

type BacktestRunSummary struct {
	JobID            uint                       `json:"job_id"`
	StartedAt        time.Time                  `json:"started_at"`
	FinishedAt       time.Time                  `json:"finished_at"`
	BacktestMode     BacktestMode               `json:"backtest_mode"`
	ModelVersion     string                     `json:"model_version,omitempty"`
	PolicyVersion    string                     `json:"policy_version,omitempty"`
	UniverseMode     UniverseMode               `json:"universe_mode"`
	PolicyContext    services.GovernanceContext `json:"policy_context"`
	ExperimentID     string                     `json:"experiment_id,omitempty"`
	CandidateSymbols []string                   `json:"candidate_symbols,omitempty"`
	SettingsSnapshot map[string]string          `json:"settings_snapshot,omitempty"`
	Baseline         BacktestResult             `json:"baseline"`
	VolSizing        BacktestResult             `json:"vol_sizing"`
	Validation       ValidationSummary          `json:"validation"`
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
		failBacktestJob(jobID, err)
		return
	}

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
		failBacktestJob(jobID, baselineErr)
		return
	}
	if volErr != nil {
		failBacktestJob(jobID, volErr)
		return
	}

	updateBacktestJob(jobID, "running", 0.7, "Running validation")
	validation, err := RunValidation(config, series,
		getSettingInt(settingsSnapshot, "validation_train_months", 12),
		getSettingInt(settingsSnapshot, "validation_test_months", 3),
		getSettingInt(settingsSnapshot, "validation_bootstrap_iterations", 500),
	)
	if err != nil {
		failBacktestJob(jobID, err)
		return
	}

	finishedAt := time.Now()
	summary := BacktestRunSummary{
		JobID:            jobID,
		StartedAt:        startedAt,
		FinishedAt:       finishedAt,
		BacktestMode:     config.BacktestMode,
		ModelVersion:     config.Governance.ModelVersion,
		PolicyVersion:    config.Governance.PolicyVersions.CompositeVersion,
		UniverseMode:     config.UniverseMode,
		PolicyContext:    config.Governance,
		CandidateSymbols: append([]string(nil), config.Symbols...),
		SettingsSnapshot: settingsSnapshot,
		Baseline:         baselineResult,
		VolSizing:        volResult,
		Validation:       validation,
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

	summaryJSON, _ := json.Marshal(summary)
	compactSummaryJSON, err := MarshalBacktestJobSummary(summary)
	if err != nil {
		failBacktestJob(jobID, err)
		return
	}
	finalMessage := fmt.Sprintf("Backtest completed (%s)", outputDir)
	updateBacktestJobWithSummary(jobID, "completed", 1.0, finalMessage, string(summaryJSON), compactSummaryJSON)
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
		JobID:            0,
		StartedAt:        now,
		FinishedAt:       now,
		BacktestMode:     config.BacktestMode,
		ModelVersion:     config.Governance.ModelVersion,
		PolicyVersion:    config.Governance.PolicyVersions.CompositeVersion,
		UniverseMode:     config.UniverseMode,
		PolicyContext:    config.Governance,
		CandidateSymbols: append([]string(nil), config.Symbols...),
		SettingsSnapshot: settings,
		Baseline:         baselineResult,
		VolSizing:        volResult,
		Validation:       validation,
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

	summaryBytes, err := json.MarshalIndent(summary, "", "  ")
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
			"window_summaries":        summary.Validation.WindowSummaries,
			"promotion_readiness":     summary.Validation.PromotionReadiness,
			"baseline_regime_slices":  summary.Validation.BaselineRegimeSlices,
			"vol_regime_slices":       summary.Validation.VolSizingRegimeSlices,
			"baseline_symbol_cohorts":  summary.Validation.BaselineSymbolCohorts,
			"vol_symbol_cohorts":       summary.Validation.VolSizingSymbolCohorts,
			"baseline_decile_metrics":  summary.Validation.BaselineDecileMetrics,
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

	return outputDir, nil
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

func prepareBacktestInputs() (BacktestConfig, map[string][]services.OHLCV, error) {
	settings := services.GetAllSettings()
	return prepareBacktestInputsWithSettings(settings)
}

func prepareBacktestInputsWithSettings(settings map[string]string) (BacktestConfig, map[string][]services.OHLCV, error) {
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
	ex := services.GetExchange()
	fetchExecution := getSettingBool(settings, "backtest_execution_1m", false)

	type fetchResult struct {
		symbol    string
		candles   []services.OHLCV
		exec1m    []services.OHLCV
		err       error
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
				candles, fetchErr := ex.FetchOHLCVRange(sym, timeframe, start, end)
				res := fetchResult{symbol: sym, candles: candles, err: fetchErr}
				if fetchErr == nil && fetchExecution {
					exec1m, execErr := ex.FetchOHLCVRange(sym, "1m", start, end)
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
	if start.IsZero() || end.IsZero() {
		rangeStart, rangeEnd := seriesTimeRange(series)
		if start.IsZero() {
			start = rangeStart
		}
		if end.IsZero() {
			end = rangeEnd
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
		BacktestMode:            resolveBacktestMode(universeMode, modelArtifact != nil),
		ExecutionSeries:         executionSeries,
		ExecutionTimeframe:      "1m",
		ExecutionTimeframeMins:  1,
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

package backtest

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/services"
	"trading-go/internal/websocket"
)

type BacktestRunSummary struct {
	JobID            uint              `json:"job_id"`
	StartedAt        time.Time         `json:"started_at"`
	FinishedAt       time.Time         `json:"finished_at"`
	SettingsSnapshot map[string]string `json:"settings_snapshot,omitempty"`
	Baseline         BacktestResult    `json:"baseline"`
	VolSizing        BacktestResult    `json:"vol_sizing"`
	Validation       ValidationSummary `json:"validation"`
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

	config, series, err := prepareBacktestInputs()
	if err != nil {
		failBacktestJob(jobID, err)
		return
	}

	updateBacktestJob(jobID, "running", 0.35, "Running baseline backtest")
	baselineConfig := config
	baselineConfig.StrategyMode = StrategyBaseline
	baselineResult, err := RunBacktest(baselineConfig, series)
	if err != nil {
		failBacktestJob(jobID, err)
		return
	}

	updateBacktestJob(jobID, "running", 0.6, "Running volatility sizing backtest")
	volConfig := config
	volConfig.StrategyMode = StrategyVolSizing
	volResult, err := RunBacktest(volConfig, series)
	if err != nil {
		failBacktestJob(jobID, err)
		return
	}

	updateBacktestJob(jobID, "running", 0.8, "Running validation")
	validation, err := RunValidation(config, series, 12, 3, 500)
	if err != nil {
		failBacktestJob(jobID, err)
		return
	}

	finishedAt := time.Now()
	settingsSnapshot := services.GetAllSettings()
	summary := BacktestRunSummary{
		JobID:            jobID,
		StartedAt:        startedAt,
		FinishedAt:       finishedAt,
		SettingsSnapshot: settingsSnapshot,
		Baseline:         baselineResult,
		VolSizing:        volResult,
		Validation:       validation,
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

	baselineConfig := config
	baselineConfig.StrategyMode = StrategyBaseline
	baselineResult, err := RunBacktest(baselineConfig, series)
	if err != nil {
		return BacktestRunSummary{}, err
	}

	volConfig := config
	volConfig.StrategyMode = StrategyVolSizing
	volResult, err := RunBacktest(volConfig, series)
	if err != nil {
		return BacktestRunSummary{}, err
	}

	validation, err := RunValidation(config, series, 12, 3, 500)
	if err != nil {
		return BacktestRunSummary{}, err
	}

	now := time.Now()
	summary := BacktestRunSummary{
		JobID:            0,
		StartedAt:        now,
		FinishedAt:       now,
		SettingsSnapshot: settings,
		Baseline:         baselineResult,
		VolSizing:        volResult,
		Validation:       validation,
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
		"baseline":   summary.Baseline.Metrics,
		"vol_sizing": summary.VolSizing.Metrics,
		"validation": summary.Validation,
	}
	metricsBytes, err := json.MarshalIndent(metricsSummary, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(outputDir, "metrics_summary.json"), metricsBytes, 0o644); err != nil {
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

	symbols := parseSymbols(settings["backtest_symbols"])
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
	ex := services.GetExchange()
	for _, symbol := range symbols {
		candles, err := ex.FetchOHLCVRange(symbol, timeframe, start, end)
		if err != nil {
			return BacktestConfig{}, nil, err
		}
		series[symbol] = candles
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

	config := BacktestConfig{
		Symbols:                 symbols,
		Start:                   start,
		End:                     end,
		IndicatorConfig:         services.GetIndicatorSettings(),
		IndicatorWeights:        services.GetIndicatorWeights(),
		Timeframe:               timeframe,
		TimeframeMinutes:        timeframeMinutes,
		InitialBalance:          1000.0, // hardcoded starting balance for backtests
		FeeBps:                  getSettingFloat(settings, "backtest_fee_bps", 10),
		SlippageBps:             getSettingFloat(settings, "backtest_slippage_bps", 5),
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

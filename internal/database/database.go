package database

import (
	"errors"
	"log"
	"trading-go/internal/config"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

func Initialize(cfg *config.Config) error {
	var err error

	dbConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info),
	}

	DB, err = gorm.Open(sqlite.Open(cfg.DatabasePath), dbConfig)
	if err != nil {
		return err
	}

	if err := AutoMigrate(); err != nil {
		return err
	}

	if err := SeedData(); err != nil {
		return err
	}

	log.Println("Database initialized successfully")
	return nil
}

func AutoMigrate() error {
	return DB.AutoMigrate(
		&Wallet{},
		&Position{},
		&Order{},
		&Setting{},
		&AIProposal{},
		&IndicatorWeight{},
		&LLMConfig{},
		&ActivityLog{},
		&BacktestJob{},
		&TrendAnalysisHistory{},
		&PortfolioSnapshot{},
	)
}

func SeedData() error {
	var count int64
	DB.Model(&Wallet{}).Count(&count)
	if count == 0 {
		wallet := Wallet{
			Balance:  400.0,
			Currency: "USDT",
		}
		if err := DB.Create(&wallet).Error; err != nil {
			return err
		}
	}

	settings := []Setting{
		{Key: "entry_percent", Value: "5.0", Category: strPtr("trading")},
		{Key: "stop_loss_percent", Value: "5.0", Category: strPtr("trading")},
		{Key: "take_profit_percent", Value: "30.0", Category: strPtr("trading")},
		{Key: "rebuy_percent", Value: "2.5", Category: strPtr("trading")},
		{Key: "max_positions", Value: "5", Category: strPtr("trading")},
		{Key: "buy_only_strong", Value: "true", Category: strPtr("trading")},
		{Key: "sell_on_signal", Value: "true", Category: strPtr("trading")},
		{Key: "min_confidence_to_buy", Value: "4.0", Category: strPtr("trading")},
		{Key: "min_confidence_to_sell", Value: "3.5", Category: strPtr("trading")},
		{Key: "allow_sell_at_loss", Value: "false", Category: strPtr("trading")},
		{Key: "trailing_stop_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "trailing_stop_percent", Value: "10.0", Category: strPtr("trading")},
		{Key: "atr_trailing_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "atr_trailing_mult", Value: "1.0", Category: strPtr("trading")},
		{Key: "atr_trailing_period", Value: "14", Category: strPtr("trading")},
		{Key: "atr_annualization_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "atr_annualization_days", Value: "365", Category: strPtr("trading")},
		{Key: "pyramiding_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "max_pyramid_layers", Value: "3", Category: strPtr("trading")},
		{Key: "position_scale_percent", Value: "50.0", Category: strPtr("trading")},
		{Key: "auto_trade_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "trending_coins_to_analyze", Value: "5", Category: strPtr("trading")},
		{Key: "regime_gate_enabled", Value: "true", Category: strPtr("trading")},
		{Key: "regime_timeframe", Value: "1h", Category: strPtr("trading")},
		{Key: "regime_ema_fast", Value: "50", Category: strPtr("trading")},
		{Key: "regime_ema_slow", Value: "200", Category: strPtr("trading")},
		{Key: "vol_atr_period", Value: "14", Category: strPtr("trading")},
		{Key: "vol_ratio_min", Value: "0.002", Category: strPtr("trading")},
		{Key: "vol_ratio_max", Value: "0.02", Category: strPtr("trading")},
		{Key: "vol_sizing_enabled", Value: "false", Category: strPtr("trading")},
		{Key: "risk_per_trade", Value: "0.5", Category: strPtr("trading")},
		{Key: "stop_mult", Value: "1.5", Category: strPtr("trading")},
		{Key: "tp_mult", Value: "3.0", Category: strPtr("trading")},
		{Key: "max_position_value", Value: "0", Category: strPtr("trading")},
		{Key: "time_stop_bars", Value: "0", Category: strPtr("trading")},
		{Key: "backtest_fee_bps", Value: "10", Category: strPtr("backtest")},
		{Key: "backtest_slippage_bps", Value: "5", Category: strPtr("backtest")},
		{Key: "backtest_start", Value: "", Category: strPtr("backtest")},
		{Key: "backtest_end", Value: "", Category: strPtr("backtest")},
		{Key: "backtest_symbols", Value: "", Category: strPtr("backtest")},
		{Key: "prob_model_enabled", Value: "false", Category: strPtr("probabilistic")},
		{Key: "prob_model_beta0", Value: "0.0", Category: strPtr("probabilistic")},
		{Key: "prob_model_beta1", Value: "0.0", Category: strPtr("probabilistic")},
		{Key: "prob_model_beta2", Value: "0.0", Category: strPtr("probabilistic")},
		{Key: "prob_model_beta3", Value: "0.0", Category: strPtr("probabilistic")},
		{Key: "prob_model_beta4", Value: "0.0", Category: strPtr("probabilistic")},
		{Key: "prob_model_beta5", Value: "0.0", Category: strPtr("probabilistic")},
		{Key: "prob_model_beta6", Value: "0.0", Category: strPtr("probabilistic")},
		{Key: "prob_p_min", Value: "0.55", Category: strPtr("probabilistic")},
		{Key: "prob_ev_min", Value: "0.0", Category: strPtr("probabilistic")},
		{Key: "prob_avg_gain", Value: "0.02", Category: strPtr("probabilistic")},
		{Key: "prob_avg_loss", Value: "0.01", Category: strPtr("probabilistic")},
		{Key: "rsi_period", Value: "14", Category: strPtr("indicators")},
		{Key: "rsi_oversold", Value: "30.0", Category: strPtr("indicators")},
		{Key: "rsi_overbought", Value: "70.0", Category: strPtr("indicators")},
		{Key: "macd_fast_period", Value: "12", Category: strPtr("indicators")},
		{Key: "macd_slow_period", Value: "26", Category: strPtr("indicators")},
		{Key: "macd_signal_period", Value: "9", Category: strPtr("indicators")},
		{Key: "bb_period", Value: "20", Category: strPtr("indicators")},
		{Key: "bb_std", Value: "2.0", Category: strPtr("indicators")},
		{Key: "volume_ma_period", Value: "20", Category: strPtr("indicators")},
		{Key: "momentum_period", Value: "10", Category: strPtr("indicators")},
		{Key: "ai_analysis_interval", Value: "24", Category: strPtr("ai")},
		{Key: "ai_lookback_days", Value: "30", Category: strPtr("ai")},
		{Key: "ai_min_proposals", Value: "1", Category: strPtr("ai")},
		{Key: "ai_auto_apply_days", Value: "0", Category: strPtr("ai")},
		{Key: "ai_goal", Value: "", Category: strPtr("ai")},
		{Key: "ai_locked_keys", Value: "", Category: strPtr("ai")},
		{Key: "ai_change_budget_pct", Value: "10", Category: strPtr("ai")},
		{Key: "ai_max_proposals", Value: "5", Category: strPtr("ai")},
		{Key: "ai_max_keys_per_category", Value: "2", Category: strPtr("ai")},
		{Key: "ai_recent_decisions_limit", Value: "10", Category: strPtr("ai")},
		{Key: "ai_gate_metrics_limit", Value: "200", Category: strPtr("ai")},
	}
	for _, s := range settings {
		var existing Setting
		if err := DB.First(&existing, "key = ?", s.Key).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := DB.Create(&s).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		}
	}

	DB.Model(&IndicatorWeight{}).Count(&count)
	if count == 0 {
		weights := []IndicatorWeight{
			{Indicator: "rsi", Weight: 1.0},
			{Indicator: "macd", Weight: 1.0},
			{Indicator: "bollinger", Weight: 1.0},
			{Indicator: "volume", Weight: 0.5},
			{Indicator: "momentum", Weight: 1.0},
		}
		for _, w := range weights {
			if err := DB.Create(&w).Error; err != nil {
				return err
			}
		}
	}

	DB.Model(&LLMConfig{}).Count(&count)
	if count == 0 {
		llmConfig := LLMConfig{
			Provider: "openrouter",
			BaseURL:  "https://openrouter.ai/api/v1",
			Model:    "google/gemini-2.0-flash-001",
		}
		if err := DB.Create(&llmConfig).Error; err != nil {
			return err
		}
	}

	return nil
}

func strPtr(s string) *string {
	return &s
}

package backtest

import (
	"time"
	"trading-go/internal/services"
)

type StrategyMode string
type BacktestMode string
type UniverseMode string

const (
	StrategyBaseline         StrategyMode = "baseline"
	StrategyVolSizing        StrategyMode = "vol_sizing"
	BacktestModeLegacyStatic BacktestMode = "legacy_static"
	BacktestModeDynamicRule  BacktestMode = "dynamic_universe_rule_rank"
	BacktestModeDynamicModel BacktestMode = "dynamic_universe_model_rank"
	BacktestModePaperReplay  BacktestMode = "paper_replay"
	UniverseStatic           UniverseMode = "static"
	UniverseDynamicRecompute UniverseMode = "dynamic_recompute"
)

type BacktestConfig struct {
	BacktestMode            BacktestMode
	Symbols                 []string
	UniverseMode            UniverseMode
	UniversePolicy          services.UniversePolicy
	Governance              services.GovernanceContext
	Start                   time.Time
	End                     time.Time
	IndicatorConfig         services.IndicatorConfig
	IndicatorWeights        map[string]float64
	Timeframe               string
	TimeframeMinutes        int
	InitialBalance          float64
	FeeBps                  float64
	SlippageBps             float64
	ModelArtifact           *services.LogisticModelArtifact
	ModelPolicy             services.ModelSelectionPolicy
	MaxPositions            int
	TimeStopBars            int
	StrategyMode            StrategyMode
	EntryPercent            float64
	StopLossPercent         float64
	TakeProfitPercent       float64
	RiskPerTrade            float64
	StopMult                float64
	TpMult                  float64
	MaxPositionValue        float64
	AtrPeriod               int
	AtrTrailingEnabled      bool
	AtrTrailingMult         float64
	AtrAnnualizationEnabled bool
	AtrAnnualizationDays    int
	BuyOnlyStrong           bool
	MinConfidenceToBuy      float64
	SellOnSignal            bool
	MinConfidenceToSell     float64
	AllowSellAtLoss         bool
	TrailingStopEnabled     bool
	TrailingStopPercent     float64
}

type Trade struct {
	Symbol               string
	EntryTime            time.Time
	ExitTime             time.Time
	EntryPrice           float64
	ExitPrice            float64
	Size                 float64
	Pnl                  float64
	PnlPercent           float64
	Reason               string
	HoldBars             int
	EntryRank            int
	RegimeState          string
	BreadthRatio         float64
	UniverseMode         UniverseMode
	PolicyVersion        string
	RolloutState         string
	ExperimentID         string
	ModelVersion         string
	PredictedProbability *float64
	PredictedEV          *float64
}

type EquityPoint struct {
	Time  time.Time
	Value float64
}

type Metrics struct {
	Sharpe           float64
	MaxDrawdown      float64
	WinRate          float64
	ProfitFactor     float64
	AvgWin           float64
	AvgLoss          float64
	ReturnVolatility float64
	TradeCount       int
}

type RankBucketMetric struct {
	Rank     int     `json:"rank"`
	Trades   int     `json:"trades"`
	WinRate  float64 `json:"win_rate"`
	AvgPnl   float64 `json:"avg_pnl"`
	TotalPnl float64 `json:"total_pnl"`
	AvgProb  float64 `json:"avg_prob"`
	AvgEV    float64 `json:"avg_ev"`
}

type RankingDiagnostics struct {
	BucketsEvaluated  int     `json:"buckets_evaluated"`
	MonotonicWinRate  bool    `json:"monotonic_win_rate"`
	MonotonicAvgPnl   bool    `json:"monotonic_avg_pnl"`
	TopRankWinRate    float64 `json:"top_rank_win_rate"`
	BottomRankWinRate float64 `json:"bottom_rank_win_rate"`
	TopRankAvgPnl     float64 `json:"top_rank_avg_pnl"`
	BottomRankAvgPnl  float64 `json:"bottom_rank_avg_pnl"`
	PositiveSpread    float64 `json:"positive_spread"`
}

type RegimeSliceMetric struct {
	Regime   string  `json:"regime"`
	Trades   int     `json:"trades"`
	WinRate  float64 `json:"win_rate"`
	AvgPnl   float64 `json:"avg_pnl"`
	TotalPnl float64 `json:"total_pnl"`
}

type SymbolCohortMetric struct {
	Symbol   string  `json:"symbol"`
	Trades   int     `json:"trades"`
	WinRate  float64 `json:"win_rate"`
	AvgPnl   float64 `json:"avg_pnl"`
	TotalPnl float64 `json:"total_pnl"`
}

type ExposureDiagnostics struct {
	AvgConcurrentPositions float64 `json:"avg_concurrent_positions"`
	MaxConcurrentPositions int     `json:"max_concurrent_positions"`
	TurnoverPer30d         float64 `json:"turnover_per_30d"`
	AvgHoldBars            float64 `json:"avg_hold_bars"`
	AvgHoldHours           float64 `json:"avg_hold_hours"`
}

type StrategyDiagnostics struct {
	Ranking       *RankingDiagnostics  `json:"ranking,omitempty"`
	RegimeSlices  []RegimeSliceMetric  `json:"regime_slices,omitempty"`
	SymbolCohorts []SymbolCohortMetric `json:"symbol_cohorts,omitempty"`
	Exposure      ExposureDiagnostics  `json:"exposure"`
}

type RankingMetrics struct {
	ModelVersion string              `json:"model_version"`
	TopK         int                 `json:"top_k"`
	Selected     int                 `json:"selected"`
	ByRank       []RankBucketMetric  `json:"by_rank,omitempty"`
	Diagnostics  *RankingDiagnostics `json:"diagnostics,omitempty"`
}

type BacktestResult struct {
	Mode           StrategyMode             `json:"mode"`
	Metrics        Metrics                  `json:"metrics"`
	ModelVersion   string                   `json:"model_version,omitempty"`
	PolicyVersion  string                   `json:"policy_version,omitempty"`
	RolloutState   string                   `json:"rollout_state,omitempty"`
	RankingMetrics *RankingMetrics          `json:"ranking_metrics,omitempty"`
	Diagnostics    StrategyDiagnostics      `json:"diagnostics"`
	Equity         []EquityPoint            `json:"equity"`
	EquityBySymbol map[string][]EquityPoint `json:"equity_by_symbol"`
	Trades         []Trade                  `json:"trades"`
}

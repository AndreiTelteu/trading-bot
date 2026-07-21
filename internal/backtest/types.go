package backtest

import (
	"time"
	"trading-go/internal/services"
)

type StrategyMode string
type BacktestMode string
type UniverseMode string
type EngineMode string
type RunClassification string
type ExecutionTiming string
type LiquidityPolicy string

const (
	StrategyBaseline         StrategyMode      = "baseline"
	StrategyVolSizing        StrategyMode      = "vol_sizing"
	BacktestModeLegacyStatic BacktestMode      = "legacy_static"
	BacktestModeDynamicRule  BacktestMode      = "dynamic_universe_rule_rank"
	BacktestModeDynamicModel BacktestMode      = "dynamic_universe_model_rank"
	BacktestModePaperReplay  BacktestMode      = "paper_replay"
	UniverseStatic           UniverseMode      = "static"
	UniverseDynamicRecompute UniverseMode      = "dynamic_recompute"
	UniverseDynamicReplay    UniverseMode      = "dynamic_replay"
	EngineLegacy             EngineMode        = "legacy"
	EngineShared             EngineMode        = "shared"
	RunCoverageFailed        RunClassification = "coverage_failed"
	RunGatingZeroTrades      RunClassification = "gating_zero_trades"
	RunStrategyZeroTrades    RunClassification = "strategy_zero_trades"
	RunSuccessfulExecution   RunClassification = "successful_execution"
	ExecutionNextExecutable  ExecutionTiming   = "next_executable"
	ExecutionMarketOnClose   ExecutionTiming   = "market_on_close"
	LiquidityFullFillOHLCV   LiquidityPolicy   = "full_fill_ohlcv"
	LiquidityVolumeCapped    LiquidityPolicy   = "volume_capped"
	LiquidityPartialFill     LiquidityPolicy   = "partial_fill"
)

type BacktestConfig struct {
	EngineMode                             EngineMode
	AccountID, SettlementCurrency, VenueID string
	BacktestMode                           BacktestMode
	Symbols                                []string
	UniverseMode                           UniverseMode
	UniversePolicy                         services.UniversePolicy
	Governance                             services.GovernanceContext
	Start                                  time.Time
	End                                    time.Time
	IndicatorConfig                        services.IndicatorConfig
	IndicatorWeights                       map[string]float64
	Timeframe                              string
	TimeframeMinutes                       int
	InitialBalance                         float64
	FeeBps                                 float64
	SlippageBps                            float64
	ModelArtifact                          *services.LogisticModelArtifact
	ModelPolicy                            services.ModelSelectionPolicy
	MaxPositions                           int
	TimeStopBars                           int
	StrategyMode                           StrategyMode
	EntryPercent                           float64
	StopLossPercent                        float64
	TakeProfitPercent                      float64
	RiskPerTrade                           float64
	StopMult                               float64
	TpMult                                 float64
	MaxPositionValue                       float64
	AtrPeriod                              int
	AtrTrailingEnabled                     bool
	AtrTrailingMult                        float64
	AtrAnnualizationEnabled                bool
	AtrAnnualizationDays                   int
	BuyOnlyStrong                          bool
	MinConfidenceToBuy                     float64
	SellOnSignal                           bool
	MinConfidenceToSell                    float64
	AllowSellAtLoss                        bool
	TrailingStopEnabled                    bool
	TrailingStopPercent                    float64
	ExecutionSeries                        map[string][]services.OHLCV // 1m candle data for execution replay
	ExecutionSeriesRequired                bool
	ExecutionTimeframe                     string // e.g. "1m"
	ExecutionTimeframeMins                 int    // e.g. 1
	CoveragePolicy                         CoveragePolicy
	ExecutionPolicy                        ExecutionPolicy
	BenchmarkSymbol                        string
	BenchmarkSeries                        []services.OHLCV
	BenchmarkRequired                      bool
	ReplaySnapshots                        []ReplaySnapshot
	ReplaySnapshotsProvided                bool
	FeatureSeries                          []FeatureSeries
	ConstraintsAvailable                   bool
	DatasetManifestID                      string
	DatasetManifestValidated               bool
	DatasetManifestRequired                bool
	DatasetLimitations                     []string
	SymbolIdentities                       map[string]string
	EconomicAssetIdentities                map[string]string
	SymbolLifecycles                       map[string]SymbolLifecycle
	ConstraintResolver                     func(symbol string, at time.Time) (SymbolConstraints, error)
	DatasetKnowledgeCutoff                 string
	DatasetSeries                          []DatasetSeriesIdentity
	CodeRevision                           string
	ConfigVersion                          string
	StrategyVersion                        string
	StrategyID                             string
	StrategyParameters                     map[string]string
	Seed                                   int64
	// Progress is optional operator telemetry. It must never affect decisions,
	// fills, digests, or any deterministic backtest output.
	Progress ProgressFunc `json:"-"`
}

type CoveragePolicy struct {
	Version                string        `json:"version"`
	DecisionInterval       time.Duration `json:"decision_interval"`
	ExecutionInterval      time.Duration `json:"execution_interval"`
	MaxMissingIntervals    int           `json:"max_missing_intervals"`
	RequireRequestedBounds bool          `json:"require_requested_bounds"`
	RequiredReplayMembers  int           `json:"required_replay_members"`
	RequiredModelFeatures  []string      `json:"required_model_features,omitempty"`
	ReplayInterval         time.Duration `json:"replay_interval"`
	MaxReplayGapIntervals  int           `json:"max_replay_gap_intervals"`
}

type ExecutionPolicy struct {
	Version     string                       `json:"version"`
	Timing      ExecutionTiming              `json:"timing"`
	Liquidity   LiquidityPolicy              `json:"liquidity"`
	CostVersion string                       `json:"cost_version"`
	Constraints map[string]SymbolConstraints `json:"constraints,omitempty"`
}

type SymbolConstraints struct {
	QuantityStep float64 `json:"quantity_step"`
	PriceTick    float64 `json:"price_tick"`
	MinQuantity  float64 `json:"min_quantity,omitempty"`
	MinNotional  float64 `json:"min_notional,omitempty"`
}
type SymbolLifecycle struct {
	ListedAt   time.Time
	DelistedAt *time.Time
}

type DatasetSeriesIdentity struct {
	ExchangeSymbolID  string `json:"exchange_symbol_id"`
	SymbolVersion     int    `json:"symbol_version"`
	AssetID           string `json:"asset_id"`
	Ticker            string `json:"ticker"`
	Role              string `json:"role"`
	Timeframe         string `json:"timeframe"`
	ListedAt          string `json:"listed_at"`
	DelistedAt        string `json:"delisted_at,omitempty"`
	SymbolAvailableAt string `json:"symbol_available_at"`
	AssetAvailableAt  string `json:"asset_available_at"`
	Rows              int    `json:"rows"`
	SeriesHash        string `json:"series_hash"`
	TradabilityRows   int    `json:"tradability_rows"`
	TradabilityHash   string `json:"tradability_hash"`
	ConstraintRows    int    `json:"constraint_rows"`
	ConstraintHash    string `json:"constraint_hash"`
}

type DatasetAudit struct {
	ManifestID      string                  `json:"manifest_id"`
	KnowledgeCutoff string                  `json:"knowledge_cutoff"`
	Series          []DatasetSeriesIdentity `json:"series"`
}

type ReplaySnapshot struct {
	Timestamp        time.Time      `json:"timestamp"`
	RegimeState      string         `json:"regime_state"`
	BreadthRatio     float64        `json:"breadth_ratio"`
	ObservedComplete bool           `json:"observed_complete"`
	Members          []ReplayMember `json:"members"`
}

type ReplayMember struct {
	AssetID                                                                                                  string `json:"asset_id"`
	ExchangeSymbolID                                                                                         string `json:"exchange_symbol_id"`
	Symbol                                                                                                   string `json:"symbol"`
	Rank                                                                                                     int    `json:"rank"`
	Shortlisted                                                                                              bool   `json:"shortlisted"`
	Stage                                                                                                    string `json:"stage,omitempty"`
	ListingAgeDays                                                                                           int    `json:"listing_age_days,omitempty"`
	MedianDailyQuoteVolume, MedianIntradayQuoteVolume                                                        float64
	RankComponentsJSON, RejectionReason                                                                      string
	LastPrice, Change24h, QuoteVolume24h, GapRatio, VolatilityRatio, Return1D, Return3D, Return7D, Return30D float64
	RelativeStrength, TrendQuality, BreakoutProximity, VolumeAcceleration, OverextensionPenalty, RankScore   float64
}

type FeatureObservation struct {
	EventAt     time.Time `json:"event_at"`
	AvailableAt time.Time `json:"available_at"`
	Value       float64   `json:"value"`
}
type FeatureSeries struct {
	Name         string               `json:"name"`
	Version      string               `json:"version"`
	Provenance   string               `json:"provenance"`
	Interval     time.Duration        `json:"interval"`
	Observations []FeatureObservation `json:"observations"`
}

func (series FeatureSeries) AsOf(at time.Time) []FeatureObservation {
	result := []FeatureObservation{}
	for _, observation := range series.Observations {
		if !observation.AvailableAt.After(at) {
			result = append(result, observation)
		}
	}
	return result
}

type CoverageReason string

const (
	CoverageMissingSeries        CoverageReason = "missing_series"
	CoverageEmptySeries          CoverageReason = "empty_series"
	CoverageDuplicateTimestamp   CoverageReason = "duplicate_timestamp"
	CoverageNonMonotonic         CoverageReason = "non_monotonic_timestamp"
	CoverageInternalGap          CoverageReason = "internal_gap"
	CoverageBounds               CoverageReason = "requested_bounds_not_covered"
	CoverageReplayEmpty          CoverageReason = "replay_snapshots_empty"
	CoverageReplayMembersEmpty   CoverageReason = "replay_members_insufficient"
	CoverageBenchmarkMissing     CoverageReason = "benchmark_missing"
	CoverageFeatureMissing       CoverageReason = "model_feature_missing"
	CoverageInvalidBarWidth      CoverageReason = "invalid_bar_interval"
	CoverageReplayDuplicate      CoverageReason = "replay_duplicate_timestamp"
	CoverageReplayMemberDup      CoverageReason = "replay_duplicate_member"
	CoverageReplayNoEffective    CoverageReason = "replay_no_effective_start_snapshot"
	CoverageReplayGap            CoverageReason = "replay_internal_gap"
	CoverageManifestIncompatible CoverageReason = "dataset_manifest_incompatible"
)

type CoverageDiagnostic struct {
	Dataset string         `json:"dataset"`
	Symbol  string         `json:"symbol,omitempty"`
	Status  string         `json:"status"`
	Reason  CoverageReason `json:"reason,omitempty"`
	Count   int            `json:"count"`
	First   string         `json:"first,omitempty"`
	Last    string         `json:"last,omitempty"`
	Gaps    int            `json:"gaps,omitempty"`
}

type CoverageReport struct {
	SchemaVersion string               `json:"schema_version"`
	PolicyVersion string               `json:"policy_version"`
	Passed        bool                 `json:"passed"`
	Reasons       []CoverageReason     `json:"reasons,omitempty"`
	Diagnostics   []CoverageDiagnostic `json:"diagnostics"`
}

type ArtifactRefs struct {
	SchemaVersion string `json:"schema_version"`
	Manifest      string `json:"manifest"`
	Decisions     string `json:"decisions"`
	Orders        string `json:"orders"`
	Fills         string `json:"fills"`
	Trades        string `json:"trades"`
	Ledger        string `json:"ledger"`
	Equity        string `json:"equity"`
	Metrics       string `json:"metrics"`
	Exposure      string `json:"exposure"`
	Comparison    string `json:"comparison,omitempty"`
}

type StrategyDataRequirement struct {
	Role      string `json:"role"`
	Timeframe string `json:"timeframe"`
}

type StrategyParameterSpec struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Default     string   `json:"default"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type StrategyRiskDeclaration struct {
	MaxGrossExposure string `json:"max_gross_exposure"`
	MaxNetExposure   string `json:"max_net_exposure"`
	LongOnly         bool   `json:"long_only"`
	UsesSharedRisk   bool   `json:"uses_shared_risk"`
}

type StrategyFeatureDeclaration struct {
	Name       string `json:"name"`
	Timeframe  string `json:"timeframe"`
	WarmupBars int    `json:"warmup_bars"`
	TraceField string `json:"trace_field"`
}

// StrategyDescriptor is immutable registry metadata. ID plus Version is the
// stable economic identity of a strategy declaration and parameter schema.
type StrategyDescriptor struct {
	SchemaVersion       string                       `json:"schema_version"`
	ID                  string                       `json:"id"`
	Version             string                       `json:"version"`
	Description         string                       `json:"description"`
	RequiredData        []StrategyDataRequirement    `json:"required_data"`
	BenchmarkRequired   bool                         `json:"benchmark_required"`
	DecisionCadence     string                       `json:"decision_cadence"`
	RebalanceCadence    string                       `json:"rebalance_cadence"`
	WarmupBars          int                          `json:"warmup_bars"`
	WarmupFormula       string                       `json:"warmup_formula,omitempty"`
	MaximumWarmupBars   int                          `json:"maximum_warmup_bars,omitempty"`
	Risk                StrategyRiskDeclaration      `json:"risk"`
	Features            []StrategyFeatureDeclaration `json:"features,omitempty"`
	FactorTraceSchema   string                       `json:"factor_trace_schema,omitempty"`
	ResearchOnly        bool                         `json:"research_only,omitempty"`
	ExecutionIntents    []string                     `json:"execution_intents,omitempty"`
	AblationVariants    []string                     `json:"ablation_variants,omitempty"`
	Parameters          []StrategyParameterSpec      `json:"parameters"`
	Baseline            bool                         `json:"baseline"`
	LegacyCompatibility bool                         `json:"legacy_compatibility,omitempty"`
}

type SelectedStrategy struct {
	Descriptor StrategyDescriptor `json:"descriptor"`
	Parameters map[string]string  `json:"parameters"`
}

type RunManifest struct {
	SchemaVersion     string            `json:"schema_version"`
	Classification    RunClassification `json:"classification"`
	CodeRevision      string            `json:"code_revision"`
	ConfigVersion     string            `json:"config_version"`
	StrategyVersion   string            `json:"strategy_version"`
	Strategy          SelectedStrategy  `json:"strategy"`
	PolicyVersion     string            `json:"policy_version"`
	CostVersion       string            `json:"cost_version"`
	DatasetManifestID string            `json:"dataset_manifest_id"`
	Dataset           DatasetAudit      `json:"dataset"`
	UniverseMode      UniverseMode      `json:"universe_mode"`
	BenchmarkSymbol   string            `json:"benchmark_symbol,omitempty"`
	Seed              int64             `json:"seed"`
	FeeBPS            float64           `json:"fee_bps"`
	SlippageBPS       float64           `json:"slippage_bps"`
	CoveragePolicy    CoveragePolicy    `json:"coverage_policy"`
	ExecutionPolicy   ExecutionPolicy   `json:"execution_policy"`
	Start             string            `json:"start"`
	End               string            `json:"end"`
	Coverage          CoverageReport    `json:"coverage"`
	Limitations       []string          `json:"limitations,omitempty"`
	Artifacts         ArtifactRefs      `json:"artifacts"`
	ExecutionIntent   string            `json:"execution_intent,omitempty"`
	PromotionAllowed  bool              `json:"promotion_allowed"`
	FactorTraceSchema string            `json:"factor_trace_schema,omitempty"`
	Hypothesis        string            `json:"hypothesis,omitempty"`
	Ablation          string            `json:"ablation,omitempty"`
}

type DecisionArtifact struct {
	SchemaVersion  string         `json:"schema_version,omitempty"`
	IntentID       string         `json:"intent_id,omitempty"`
	SignalAt       string         `json:"signal_at"`
	DecisionAt     string         `json:"decision_at"`
	Symbol         string         `json:"symbol"`
	Code           string         `json:"code"`
	Stage          string         `json:"stage"`
	Side           string         `json:"side,omitempty"`
	Quantity       string         `json:"quantity,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	ReasonMetadata ReasonMetadata `json:"reason_metadata,omitempty"`
	PolicyVersion  string         `json:"policy_version,omitempty"`
}
type OrderArtifact struct {
	SchemaVersion  string            `json:"schema_version,omitempty"`
	IntentID       string            `json:"intent_id,omitempty"`
	OrderID        string            `json:"order_id,omitempty"`
	SignalAt       string            `json:"signal_at"`
	DecisionAt     string            `json:"decision_at"`
	OrderAt        string            `json:"order_at"`
	Symbol         string            `json:"symbol"`
	Side           string            `json:"side"`
	Quantity       string            `json:"quantity"`
	Reason         string            `json:"reason,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	ReasonMetadata ReasonMetadata    `json:"reason_metadata,omitempty"`
}
type FillArtifact struct {
	SchemaVersion string         `json:"schema_version,omitempty"`
	IntentID      string         `json:"intent_id,omitempty"`
	OrderID       string         `json:"order_id,omitempty"`
	FillID        string         `json:"fill_id,omitempty"`
	SignalAt      string         `json:"signal_at"`
	DecisionAt    string         `json:"decision_at"`
	OrderAt       string         `json:"order_at"`
	FillAt        string         `json:"fill_at"`
	Symbol        string         `json:"symbol"`
	Side          string         `json:"side"`
	Quantity      string         `json:"quantity"`
	Price         string         `json:"price"`
	Fee           string         `json:"fee"`
	CostVersion   string         `json:"cost_version"`
	Reason        ReasonMetadata `json:"reason_metadata,omitempty"`
}
type LedgerArtifact struct {
	SchemaVersion string         `json:"schema_version,omitempty"`
	IntentID      string         `json:"intent_id,omitempty"`
	OrderID       string         `json:"order_id,omitempty"`
	FillID        string         `json:"fill_id,omitempty"`
	At            string         `json:"at"`
	Symbol        string         `json:"symbol"`
	Side          string         `json:"side"`
	Quantity      string         `json:"quantity"`
	Price         string         `json:"price"`
	Fee           string         `json:"fee"`
	CashAfter     string         `json:"cash_after"`
	Reason        ReasonMetadata `json:"reason_metadata,omitempty"`
}

type ReasonMetadata struct {
	SchemaVersion string   `json:"schema_version,omitempty"`
	Primary       string   `json:"primary,omitempty"`
	Concurrent    []string `json:"concurrent,omitempty"`
}
type ExposureArtifact struct {
	At        string `json:"at"`
	Symbol    string `json:"symbol"`
	Quantity  string `json:"quantity"`
	MarkPrice string `json:"mark_price"`
	Value     string `json:"value"`
	Status    string `json:"status"`
}

type BacktestArtifacts struct {
	SchemaVersion string             `json:"schema_version"`
	Decisions     []DecisionArtifact `json:"decisions"`
	Orders        []OrderArtifact    `json:"orders"`
	Fills         []FillArtifact     `json:"fills"`
	Ledger        []LedgerArtifact   `json:"ledger"`
	Exposure      []ExposureArtifact `json:"exposure"`
}

type ArtifactBytes struct {
	Manifest  []byte
	Decisions []byte
	Orders    []byte
	Fills     []byte
	Trades    []byte
	Ledger    []byte
	Exposure  []byte
	Equity    []byte
	Metrics   []byte
}

type ArtifactEnvelope struct {
	SchemaVersion string `json:"schema_version"`
	Payload       any    `json:"payload"`
}

type RunEvidence struct {
	UniverseEvaluations, UniverseUnavailable, CandidateEvaluations, ShortlistCandidates         int
	StrategyNoActions, StrategyIntents, RiskRejections, BrokerRejections, AcceptedOrders, Fills int
	PreOrchestratorGates                                                                        int
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
	ReasonMetadata       ReasonMetadata
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

type DecileMetric struct {
	Decile   int     `json:"decile"`
	MinProb  float64 `json:"min_prob"`
	MaxProb  float64 `json:"max_prob"`
	Trades   int     `json:"trades"`
	WinRate  float64 `json:"win_rate"`
	AvgPnl   float64 `json:"avg_pnl"`
	TotalPnl float64 `json:"total_pnl"`
}

type StrategyDiagnostics struct {
	Ranking       *RankingDiagnostics  `json:"ranking,omitempty"`
	RegimeSlices  []RegimeSliceMetric  `json:"regime_slices,omitempty"`
	SymbolCohorts []SymbolCohortMetric `json:"symbol_cohorts,omitempty"`
	Exposure      ExposureDiagnostics  `json:"exposure"`
	DecileMetrics []DecileMetric       `json:"decile_metrics,omitempty"`
}

type RankingMetrics struct {
	ModelVersion string              `json:"model_version"`
	TopK         int                 `json:"top_k"`
	Selected     int                 `json:"selected"`
	ByRank       []RankBucketMetric  `json:"by_rank,omitempty"`
	Diagnostics  *RankingDiagnostics `json:"diagnostics,omitempty"`
}

type BacktestResult struct {
	Classification     RunClassification        `json:"classification"`
	Coverage           CoverageReport           `json:"coverage"`
	Manifest           RunManifest              `json:"manifest"`
	Artifacts          BacktestArtifacts        `json:"-"`
	SharedEngineRuns   int                      `json:"shared_engine_runs,omitempty"`
	SharedLedgerEvents int                      `json:"shared_ledger_events,omitempty"`
	Mode               StrategyMode             `json:"mode"`
	Metrics            Metrics                  `json:"metrics"`
	ModelVersion       string                   `json:"model_version,omitempty"`
	PolicyVersion      string                   `json:"policy_version,omitempty"`
	RolloutState       string                   `json:"rollout_state,omitempty"`
	UniverseMode       UniverseMode             `json:"universe_mode,omitempty"`
	RankingMetrics     *RankingMetrics          `json:"ranking_metrics,omitempty"`
	Diagnostics        StrategyDiagnostics      `json:"diagnostics"`
	Equity             []EquityPoint            `json:"equity"`
	EquityBySymbol     map[string][]EquityPoint `json:"equity_by_symbol"`
	Trades             []Trade                  `json:"trades"`
}

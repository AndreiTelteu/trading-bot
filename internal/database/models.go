package database

import (
	"time"
)

type Wallet struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Balance   float64   `json:"balance" gorm:"default:400.0"`
	Currency  string    `json:"currency" gorm:"size:20;default:USDT"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Position struct {
	ID                uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	Symbol            string     `json:"symbol" gorm:"size:20;uniqueIndex;index:idx_positions_symbol_status,priority:1"`
	Amount            float64    `json:"amount"`
	AvgPrice          float64    `json:"avg_price"`
	EntryPrice        *float64   `json:"entry_price"`
	CurrentPrice      *float64   `json:"current_price"`
	ExecutionMode     string     `json:"execution_mode" gorm:"size:20;default:paper;index"`
	EntrySource       string     `json:"entry_source" gorm:"size:30;default:manual"`
	ExitPending       bool       `json:"exit_pending" gorm:"default:false;index"`
	LastMarkPrice     *float64   `json:"last_mark_price"`
	LastMarkAt        *time.Time `json:"last_mark_at"`
	ClientPositionID  *string    `json:"client_position_id" gorm:"size:100;index"`
	DecisionTimeframe string     `json:"decision_timeframe" gorm:"size:10;default:15m"`
	StopPrice         *float64   `json:"stop_price"`
	TakeProfitPrice   *float64   `json:"take_profit_price"`
	TrailingStopPrice *float64   `json:"trailing_stop_price"`
	LastAtrValue      *float64   `json:"last_atr_value"`
	MaxBarsHeld       *int       `json:"max_bars_held"`
	Pnl               float64    `json:"pnl" gorm:"default:0"`
	PnlPercent        float64    `json:"pnl_percent" gorm:"default:0"`
	Status            string     `json:"status" gorm:"size:20;default:open;index:idx_positions_symbol_status,priority:2;index:idx_positions_status_opened,priority:1;index:idx_positions_status_closed,priority:1"`
	OpenedAt          time.Time  `json:"opened_at" gorm:"index:idx_positions_status_opened,priority:2;index:idx_positions_status_closed,priority:3"`
	ClosedAt          *time.Time `json:"closed_at" gorm:"index:idx_positions_status_closed,priority:2"`
	CloseReason       *string    `json:"close_reason" gorm:"size:50"`
}

type Order struct {
	ID              uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	OrderType       string     `json:"order_type" gorm:"size:10;not null"`
	Symbol          string     `json:"symbol" gorm:"size:20;not null"`
	AmountCrypto    float64    `json:"amount_crypto"`
	AmountUsdt      float64    `json:"amount_usdt"`
	Price           float64    `json:"price"`
	Fee             float64    `json:"fee" gorm:"default:0"`
	ExchangeOrderID *string    `json:"exchange_order_id" gorm:"size:100;index"`
	ClientOrderID   *string    `json:"client_order_id" gorm:"size:100;index"`
	Status          string     `json:"status" gorm:"size:20;default:filled;index"`
	ExecutionMode   string     `json:"execution_mode" gorm:"size:20;default:paper;index"`
	TriggerReason   *string    `json:"trigger_reason" gorm:"size:50;index"`
	RequestedPrice  *float64   `json:"requested_price"`
	FillPrice       *float64   `json:"fill_price"`
	ExecutedQty     *float64   `json:"executed_qty"`
	ExchangeFee     *float64   `json:"exchange_fee"`
	SubmittedAt     *time.Time `json:"submitted_at"`
	FilledAt        *time.Time `json:"filled_at"`
	ExecutedAt      time.Time  `json:"executed_at" gorm:"index"`
}

type Setting struct {
	Key       string    `json:"key" gorm:"primaryKey;size:50"`
	Value     string    `json:"value" gorm:"size:500"`
	Category  *string   `json:"category" gorm:"size:20;index"`
	UpdatedAt time.Time `json:"updated_at"`
}

type AIProposal struct {
	ID                 uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	ProposalType       string     `json:"proposal_type" gorm:"size:50;not null"`
	ParameterKey       *string    `json:"parameter_key" gorm:"size:50"`
	OldValue           *string    `json:"old_value" gorm:"size:200"`
	NewValue           *string    `json:"new_value" gorm:"size:200"`
	Reasoning          string     `json:"reasoning" gorm:"type:text"`
	Status             string     `json:"status" gorm:"size:20;default:pending;index"`
	CreatedAt          time.Time  `json:"created_at" gorm:"index"`
	ResolvedAt         *time.Time `json:"resolved_at"`
	PreviousProposalID *uint      `json:"previous_proposal_id" gorm:"index"`
}

type IndicatorWeight struct {
	Indicator string  `json:"indicator" gorm:"primaryKey;size:20"`
	Weight    float64 `json:"weight" gorm:"default:1.0"`
}

type LLMConfig struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	Provider  string    `json:"provider" gorm:"size:20;default:openrouter"`
	BaseURL   string    `json:"base_url" gorm:"size:200;default:https://openrouter.ai/api/v1"`
	APIKey    *string   `json:"api_key" gorm:"size:200"`
	Model     string    `json:"model" gorm:"size:50;default:google/gemini-2.0-flash-001"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ActivityLog struct {
	ID        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	LogType   string    `json:"log_type" gorm:"size:20;not null"`
	Message   string    `json:"message" gorm:"size:500;not null"`
	Details   *string   `json:"details" gorm:"type:text"`
	Timestamp time.Time `json:"timestamp" gorm:"index"`
}

type BacktestJob struct {
	ID                 uint       `json:"id" gorm:"primaryKey;autoIncrement"`
	Status             string     `json:"status" gorm:"size:20;default:pending"`
	Progress           float64    `json:"progress"`
	Message            *string    `json:"message" gorm:"size:500"`
	SummaryJSON        *string    `json:"summary_json" gorm:"type:text"`
	SummaryCompactJSON *string    `json:"summary_compact_json" gorm:"type:text"`
	Error              *string    `json:"error" gorm:"type:text"`
	StartedAt          *time.Time `json:"started_at"`
	FinishedAt         *time.Time `json:"finished_at"`
	CreatedAt          time.Time  `json:"created_at" gorm:"index"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type TrendAnalysisHistory struct {
	ID                  uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	Symbol              string    `json:"symbol" gorm:"size:20;index;index:idx_trend_symbol_analyzed_at,priority:1"`
	Timeframe           string    `json:"timeframe" gorm:"size:10;default:15m"`
	CurrentPrice        *float64  `json:"current_price"`
	Change24h           *float64  `json:"change_24h" gorm:"column:change_24h"`
	FinalSignal         *string   `json:"final_signal" gorm:"size:20"`
	FinalRating         *float64  `json:"final_rating"`
	ProbUp              *float64  `json:"prob_up" gorm:"column:prob_up"`
	ExpectedValue       *float64  `json:"expected_value" gorm:"column:expected_value"`
	AutoTrade           *bool     `json:"auto_trade"`
	SignalQualifies     *bool     `json:"signal_qualifies"`
	ConfidenceQualifies *bool     `json:"confidence_qualifies"`
	RegimeOk            *bool     `json:"regime_ok"`
	VolOk               *bool     `json:"vol_ok"`
	ProbOk              *bool     `json:"prob_ok"`
	Decision            *string   `json:"decision" gorm:"size:20"`
	DecisionReason      *string   `json:"decision_reason" gorm:"type:text"`
	IndicatorsJSON      string    `json:"indicators_json" gorm:"type:text;not null"`
	AnalyzedAt          time.Time `json:"analyzed_at" gorm:"index;index:idx_trend_symbol_analyzed_at,priority:2,sort:desc"`
}

type UniverseSymbol struct {
	ID              uint             `json:"id" gorm:"primaryKey;autoIncrement"`
	Symbol          string           `json:"symbol" gorm:"size:20;uniqueIndex"`
	BaseAsset       string           `json:"base_asset" gorm:"size:20;index"`
	QuoteAsset      string           `json:"quote_asset" gorm:"size:20;index"`
	Status          string           `json:"status" gorm:"size:20;index"`
	SpotTradable    bool             `json:"spot_tradable" gorm:"default:false"`
	IsExcluded      bool             `json:"is_excluded" gorm:"default:false;index"`
	ExclusionReason *string          `json:"exclusion_reason" gorm:"type:text"`
	FirstSeenAt     time.Time        `json:"first_seen_at" gorm:"index"`
	LastSeenAt      time.Time        `json:"last_seen_at" gorm:"index"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	Snapshots       []UniverseMember `json:"-" gorm:"foreignKey:Symbol;references:Symbol"`
}

type UniverseSnapshot struct {
	ID                uint             `json:"id" gorm:"primaryKey;autoIncrement"`
	SnapshotTime      time.Time        `json:"snapshot_time" gorm:"index"`
	RebalanceInterval string           `json:"rebalance_interval" gorm:"size:20"`
	RegimeState       string           `json:"regime_state" gorm:"size:20;index"`
	BreadthRatio      float64          `json:"breadth_ratio"`
	EligibleCount     int              `json:"eligible_count"`
	CandidateCount    int              `json:"candidate_count"`
	RankedCount       int              `json:"ranked_count"`
	ShortlistCount    int              `json:"shortlist_count"`
	Members           []UniverseMember `json:"members,omitempty"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

type UniverseMember struct {
	ID                        uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	UniverseSnapshotID        uint      `json:"universe_snapshot_id" gorm:"index"`
	Symbol                    string    `json:"symbol" gorm:"size:20;index"`
	Stage                     string    `json:"stage" gorm:"size:20;index"`
	LastPrice                 float64   `json:"last_price"`
	Change24h                 float64   `json:"change_24h"`
	QuoteVolume24h            float64   `json:"quote_volume_24h"`
	ListingAgeDays            int       `json:"listing_age_days"`
	MedianDailyQuoteVolume    float64   `json:"median_daily_quote_volume"`
	MedianIntradayQuoteVolume float64   `json:"median_intraday_quote_volume"`
	GapRatio                  float64   `json:"gap_ratio"`
	VolatilityRatio           float64   `json:"volatility_ratio"`
	Return1D                  float64   `json:"return_1d"`
	Return3D                  float64   `json:"return_3d"`
	Return7D                  float64   `json:"return_7d"`
	Return30D                 float64   `json:"return_30d"`
	RelativeStrength          float64   `json:"relative_strength"`
	TrendQuality              float64   `json:"trend_quality"`
	BreakoutProximity         float64   `json:"breakout_proximity"`
	VolumeAcceleration        float64   `json:"volume_acceleration"`
	OverextensionPenalty      float64   `json:"overextension_penalty"`
	RankScore                 float64   `json:"rank_score"`
	RankComponentsJSON        string    `json:"rank_components_json" gorm:"type:text"`
	Shortlisted               bool      `json:"shortlisted" gorm:"default:false;index"`
	RejectionReason           *string   `json:"rejection_reason" gorm:"type:text"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

type ModelArtifact struct {
	ID                 uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	Version            string    `json:"version" gorm:"size:100;uniqueIndex"`
	ModelFamily        string    `json:"model_family" gorm:"size:30;index"`
	FeatureSpecVersion string    `json:"feature_spec_version" gorm:"size:50;index"`
	LabelSpecVersion   string    `json:"label_spec_version" gorm:"size:50"`
	CalibrationMethod  string    `json:"calibration_method" gorm:"size:30"`
	TrainWindow        string    `json:"train_window" gorm:"size:120"`
	ValidationWindow   string    `json:"validation_window" gorm:"size:120"`
	TestWindow         string    `json:"test_window" gorm:"size:120"`
	MetricsSummaryJSON string    `json:"metrics_summary_json" gorm:"type:text"`
	ArtifactPath       string    `json:"artifact_path" gorm:"size:500"`
	ArtifactChecksum   string    `json:"artifact_checksum" gorm:"size:128"`
	RolloutState       string    `json:"rollout_state" gorm:"size:20;index"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type FeatureSnapshot struct {
	ID                 uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	SnapshotTime       time.Time `json:"snapshot_time" gorm:"index;index:idx_feature_symbol_time,priority:2,sort:desc"`
	Symbol             string    `json:"symbol" gorm:"size:20;index;index:idx_feature_symbol_time,priority:1"`
	UniverseSnapshotID *uint     `json:"universe_snapshot_id" gorm:"index"`
	ModelVersion       string    `json:"model_version" gorm:"size:100;index"`
	FeatureSpecVersion string    `json:"feature_spec_version" gorm:"size:50;index"`
	LastPrice          float64   `json:"last_price"`
	RegimeState        string    `json:"regime_state" gorm:"size:20;index"`
	BreadthRatio       float64   `json:"breadth_ratio"`
	RankScore          float64   `json:"rank_score"`
	FeaturesJSON       string    `json:"features_json" gorm:"type:text"`
	QualityFlagsJSON   string    `json:"quality_flags_json" gorm:"type:text"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type PredictionLog struct {
	ID                   uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	PredictionTime       time.Time `json:"prediction_time" gorm:"index;index:idx_prediction_symbol_time,priority:2,sort:desc;index:idx_prediction_model_time,priority:2,sort:desc"`
	Symbol               string    `json:"symbol" gorm:"size:20;index;index:idx_prediction_symbol_time,priority:1"`
	ModelVersion         string    `json:"model_version" gorm:"size:100;index;index:idx_prediction_model_time,priority:1"`
	FeatureSnapshotID    *uint     `json:"feature_snapshot_id" gorm:"index"`
	UniverseSnapshotID   *uint     `json:"universe_snapshot_id" gorm:"index"`
	PredictedProbability float64   `json:"predicted_probability"`
	PredictedEV          float64   `json:"predicted_ev"`
	RawScore             float64   `json:"raw_score"`
	Rank                 int       `json:"rank"`
	Selected             bool      `json:"selected" gorm:"index"`
	DecisionResult       string    `json:"decision_result" gorm:"size:30;index"`
	RolloutState         string    `json:"rollout_state" gorm:"size:20;index"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type TradeLabel struct {
	ID                uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	FeatureSnapshotID *uint     `json:"feature_snapshot_id" gorm:"index"`
	PredictionLogID   *uint     `json:"prediction_log_id" gorm:"index"`
	Symbol            string    `json:"symbol" gorm:"size:20;index"`
	ModelVersion      string    `json:"model_version" gorm:"size:100;index"`
	RealizedReturn    float64   `json:"realized_return"`
	Profitable        bool      `json:"profitable" gorm:"index"`
	ExitReason        *string   `json:"exit_reason" gorm:"size:50"`
	HoldBars          int       `json:"hold_bars"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type PortfolioSnapshot struct {
	ID                   uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	TotalValue           float64   `json:"total_value"`
	VolatilityAnnualized *float64  `json:"volatility_annualized"`
	Timestamp            time.Time `json:"timestamp" gorm:"index"`
}

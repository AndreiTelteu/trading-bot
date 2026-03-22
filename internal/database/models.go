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
	ID           uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	OrderType    string    `json:"order_type" gorm:"size:10;not null"`
	Symbol       string    `json:"symbol" gorm:"size:20;not null"`
	AmountCrypto float64   `json:"amount_crypto"`
	AmountUsdt   float64   `json:"amount_usdt"`
	Price        float64   `json:"price"`
	Fee          float64   `json:"fee" gorm:"default:0"`
	ExecutedAt   time.Time `json:"executed_at" gorm:"index"`
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

type PortfolioSnapshot struct {
	ID                   uint      `json:"id" gorm:"primaryKey;autoIncrement"`
	TotalValue           float64   `json:"total_value"`
	VolatilityAnnualized *float64  `json:"volatility_annualized"`
	Timestamp            time.Time `json:"timestamp" gorm:"index"`
}

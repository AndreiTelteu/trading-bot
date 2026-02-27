package database

import (
	"time"
)

type Wallet struct {
	ID        uint    `gorm:"primaryKey"`
	Balance   float64 `gorm:"default:400.0"`
	Currency  string  `gorm:"size:20;default:USDT"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Position struct {
	ID           uint   `gorm:"primaryKey;autoIncrement"`
	Symbol       string `gorm:"size:20;uniqueIndex"`
	Amount       float64
	AvgPrice     float64
	EntryPrice   *float64
	CurrentPrice *float64
	Pnl          float64 `gorm:"default:0"`
	PnlPercent   float64 `gorm:"default:0"`
	Status       string  `gorm:"size:20;default:open"`
	OpenedAt     time.Time
	ClosedAt     *time.Time
	CloseReason  *string `gorm:"size:50"`
}

type Order struct {
	ID           uint   `gorm:"primaryKey;autoIncrement"`
	OrderType    string `gorm:"size:10;not null"`
	Symbol       string `gorm:"size:20;not null"`
	AmountCrypto float64
	AmountUsdt   float64
	Price        float64
	Fee          float64 `gorm:"default:0"`
	ExecutedAt   time.Time
}

type Setting struct {
	Key       string  `gorm:"primaryKey;size:50"`
	Value     string  `gorm:"size:500"`
	Category  *string `gorm:"size:20"`
	UpdatedAt time.Time
}

type AIProposal struct {
	ID                 uint    `gorm:"primaryKey;autoIncrement"`
	ProposalType       string  `gorm:"size:50;not null"`
	ParameterKey       *string `gorm:"size:50"`
	OldValue           *string `gorm:"size:200"`
	NewValue           *string `gorm:"size:200"`
	Reasoning          string  `gorm:"type:text"`
	Status             string  `gorm:"size:20;default:pending"`
	CreatedAt          time.Time
	ResolvedAt         *time.Time
	PreviousProposalID *uint `gorm:"index"`
}

type IndicatorWeight struct {
	Indicator string  `gorm:"primaryKey;size:20"`
	Weight    float64 `gorm:"default:1.0"`
}

type LLMConfig struct {
	ID        uint    `gorm:"primaryKey"`
	Provider  string  `gorm:"size:20;default:openrouter"`
	BaseURL   string  `gorm:"size:200;default:https://openrouter.ai/api/v1"`
	APIKey    *string `gorm:"size:200"`
	Model     string  `gorm:"size:50;default:google/gemini-2.0-flash-001"`
	UpdatedAt time.Time
}

type ActivityLog struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	LogType   string    `gorm:"size:20;not null"`
	Message   string    `gorm:"size:500;not null"`
	Details   *string   `gorm:"type:text"`
	Timestamp time.Time `gorm:"index"`
}

type TrendAnalysisHistory struct {
	ID             uint   `gorm:"primaryKey;autoIncrement"`
	Symbol         string `gorm:"size:20;index"`
	Timeframe      string `gorm:"size:10;default:15m"`
	CurrentPrice   *float64
	Change24h      *float64
	FinalSignal    *string `gorm:"size:20"`
	FinalRating    *float64
	IndicatorsJSON string    `gorm:"type:text;not null"`
	AnalyzedAt     time.Time `gorm:"index"`
}

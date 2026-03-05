package backtest

import "time"

type StrategyMode string

const (
	StrategyBaseline  StrategyMode = "baseline"
	StrategyVolSizing StrategyMode = "vol_sizing"
)

type BacktestConfig struct {
	Symbols                 []string
	Start                   time.Time
	End                     time.Time
	Timeframe               string
	TimeframeMinutes        int
	InitialBalance          float64
	FeeBps                  float64
	SlippageBps             float64
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
	Symbol     string
	EntryTime  time.Time
	ExitTime   time.Time
	EntryPrice float64
	ExitPrice  float64
	Size       float64
	Pnl        float64
	PnlPercent float64
	Reason     string
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

type BacktestResult struct {
	Mode           StrategyMode
	Metrics        Metrics
	Equity         []EquityPoint
	EquityBySymbol map[string][]EquityPoint
	Trades         []Trade
}

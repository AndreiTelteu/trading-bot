package tradingcore

import (
	"fmt"
	"sort"
	"time"
)

type Instrument struct {
	ID                    InstrumentID
	BaseAsset, QuoteAsset AssetID
	Venue                 VenueID
	VenueSymbol           string
}

func NewInstrument(id InstrumentID, baseAsset, quoteAsset AssetID, venue VenueID, venueSymbol string) (Instrument, error) {
	symbol, err := validateIdentity("venue symbol", venueSymbol)
	if err != nil {
		return Instrument{}, err
	}
	instrument := Instrument{ID: id, BaseAsset: baseAsset, QuoteAsset: quoteAsset, Venue: venue, VenueSymbol: symbol}
	if err := instrument.Validate(); err != nil {
		return Instrument{}, err
	}
	return instrument, nil
}
func (instrument Instrument) Validate() error {
	if instrument.ID.String() == "" || instrument.BaseAsset.String() == "" || instrument.QuoteAsset.String() == "" || instrument.Venue.String() == "" {
		return fmt.Errorf("instrument id, assets, venue, and venue symbol are required")
	}
	if _, err := validateIdentity("venue symbol", instrument.VenueSymbol); err != nil {
		return err
	}
	if instrument.BaseAsset == instrument.QuoteAsset {
		return fmt.Errorf("base and quote asset must differ")
	}
	return nil
}

type Bar struct {
	Instrument             Instrument
	Interval               time.Duration
	OpenTime, CloseTime    time.Time
	Open, High, Low, Close Price
	Volume                 Quantity
}

func (bar Bar) Validate() error {
	if err := bar.Instrument.Validate(); err != nil {
		return err
	}
	if bar.Interval <= 0 || bar.OpenTime.IsZero() || !bar.CloseTime.After(bar.OpenTime) || bar.CloseTime.Sub(bar.OpenTime) != bar.Interval {
		return fmt.Errorf("bar timestamps must align to its positive interval")
	}
	if !bar.Open.Valid() || !bar.High.Valid() || !bar.Low.Valid() || !bar.Close.Valid() || !bar.Volume.Valid() {
		return fmt.Errorf("bar prices and volume must be positive exact values")
	}
	return nil
}

type Quote struct {
	Instrument     Instrument
	Bid, Ask, Last Price
	ObservedAt     time.Time
}

type Coverage struct {
	Instrument                               Instrument
	From, Through                            time.Time
	ExpectedObservations, ActualObservations int
	Complete                                 bool
	Source, DatasetVersion                   string
}

type BarsRequest struct {
	Instrument    Instrument
	Interval      time.Duration
	From, Through time.Time
}

func (request BarsRequest) Validate() error {
	if request.Instrument.ID.String() == "" {
		return fmt.Errorf("instrument is required")
	}
	if request.Interval <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	if request.From.IsZero() || !request.Through.After(request.From) {
		return fmt.Errorf("invalid coverage interval")
	}
	return nil
}

type BarSeries struct{ bars []Bar }

func NewBarSeries(bars []Bar) (BarSeries, error) {
	values := append([]Bar(nil), bars...)
	for _, bar := range values {
		if err := bar.Validate(); err != nil {
			return BarSeries{}, err
		}
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].OpenTime.Before(values[j].OpenTime) })
	return BarSeries{bars: values}, nil
}
func (series BarSeries) Bars() []Bar { return append([]Bar(nil), series.bars...) }
func (series BarSeries) Len() int    { return len(series.bars) }

type UniverseCandidate struct {
	Instrument                          Instrument
	Rank                                int
	Score                               Decimal
	Eligible                            bool
	RejectionReason                     string
	MembershipSource, MembershipVersion string
}

type UniverseSnapshot struct {
	asOf                  time.Time
	policyVersion, source string
	candidates            []UniverseCandidate
}

func NewUniverseSnapshot(asOf time.Time, policyVersion, source string, candidates []UniverseCandidate) (UniverseSnapshot, error) {
	if asOf.IsZero() {
		return UniverseSnapshot{}, fmt.Errorf("universe as-of time is required")
	}
	copyCandidates := append([]UniverseCandidate(nil), candidates...)
	for _, candidate := range copyCandidates {
		if err := candidate.Instrument.Validate(); err != nil {
			return UniverseSnapshot{}, err
		}
		if candidate.Rank < 0 {
			return UniverseSnapshot{}, fmt.Errorf("candidate rank cannot be negative")
		}
		if !candidate.Score.Valid() {
			return UniverseSnapshot{}, fmt.Errorf("candidate score must be an exact decimal")
		}
	}
	sort.SliceStable(copyCandidates, func(i, j int) bool {
		if copyCandidates[i].Rank != copyCandidates[j].Rank {
			return copyCandidates[i].Rank < copyCandidates[j].Rank
		}
		return copyCandidates[i].Instrument.ID.String() < copyCandidates[j].Instrument.ID.String()
	})
	return UniverseSnapshot{asOf: asOf, policyVersion: policyVersion, source: source, candidates: copyCandidates}, nil
}
func (snapshot UniverseSnapshot) AsOf() time.Time       { return snapshot.asOf }
func (snapshot UniverseSnapshot) PolicyVersion() string { return snapshot.policyVersion }
func (snapshot UniverseSnapshot) Source() string        { return snapshot.source }
func (snapshot UniverseSnapshot) Candidates() []UniverseCandidate {
	return append([]UniverseCandidate(nil), snapshot.candidates...)
}

type Position struct {
	ID                      PositionID
	Instrument              Instrument
	Quantity                Quantity
	AveragePrice, MarkPrice Price
	OpenedAt                time.Time
	RealizedPnL             SignedAmount
	PyramidLayers           int
}

type ExecutionMode string

const (
	ExecutionResearch    ExecutionMode = "research"
	ExecutionShadow      ExecutionMode = "shadow"
	ExecutionPaper       ExecutionMode = "paper"
	ExecutionLimitedLive ExecutionMode = "limited_live"
	ExecutionFullLive    ExecutionMode = "full_live"
)

type PendingOrder struct {
	ID          OrderID
	Instrument  Instrument
	Side        OrderSide
	Remaining   Quantity
	SubmittedAt time.Time
}

type RiskState struct {
	known                                 bool
	grossExposure, realizedPnL, dailyLoss SignedAmount
	openRisk                              SignedAmount
}

func NewRiskState(grossExposure, realizedPnL, dailyLoss, openRisk SignedAmount) (RiskState, error) {
	for _, value := range []SignedAmount{grossExposure, realizedPnL, dailyLoss, openRisk} {
		if !value.Valid() {
			return RiskState{}, fmt.Errorf("risk values must be exact signed amounts")
		}
	}
	return RiskState{known: true, grossExposure: grossExposure, realizedPnL: realizedPnL, dailyLoss: dailyLoss, openRisk: openRisk}, nil
}
func (state RiskState) Known() bool { return state.known }
func (state RiskState) Values() (SignedAmount, SignedAmount, SignedAmount, SignedAmount) {
	return state.grossExposure, state.realizedPnL, state.dailyLoss, state.openRisk
}

type PortfolioSnapshot struct {
	asOf          time.Time
	accountID     AccountID
	executionMode ExecutionMode
	cash          map[AssetID]SignedAmount
	positions     []Position
	pending       []PendingOrder
	risk          RiskState
}

func NewPortfolioSnapshot(asOf time.Time, accountID AccountID, mode ExecutionMode, cash map[AssetID]SignedAmount, positions []Position, pending []PendingOrder, risk RiskState) (PortfolioSnapshot, error) {
	if asOf.IsZero() {
		return PortfolioSnapshot{}, fmt.Errorf("portfolio as-of time is required")
	}
	if accountID.String() == "" {
		return PortfolioSnapshot{}, fmt.Errorf("account id is required")
	}
	switch mode {
	case ExecutionResearch, ExecutionShadow, ExecutionPaper, ExecutionLimitedLive, ExecutionFullLive:
	default:
		return PortfolioSnapshot{}, fmt.Errorf("unsupported execution mode %q", mode)
	}
	copyCash := make(map[AssetID]SignedAmount, len(cash))
	for asset, amount := range cash {
		if asset.String() == "" || !amount.Valid() {
			return PortfolioSnapshot{}, fmt.Errorf("cash requires valid asset and exact amount")
		}
		copyCash[asset] = amount
	}
	copyPositions := append([]Position(nil), positions...)
	copyPending := append([]PendingOrder(nil), pending...)
	for _, position := range copyPositions {
		if position.ID.String() == "" || position.Instrument.Validate() != nil || !position.Quantity.Valid() || !position.AveragePrice.Valid() || !position.MarkPrice.Valid() || position.OpenedAt.IsZero() || position.PyramidLayers < 0 {
			return PortfolioSnapshot{}, fmt.Errorf("position contains invalid identity, value, or time")
		}
	}
	for _, order := range copyPending {
		if order.ID.String() == "" || order.Instrument.Validate() != nil || (order.Side != Buy && order.Side != Sell) || !order.Remaining.Valid() || order.SubmittedAt.IsZero() {
			return PortfolioSnapshot{}, fmt.Errorf("pending order contains invalid identity, value, side, or time")
		}
	}
	sort.SliceStable(copyPositions, func(i, j int) bool { return copyPositions[i].ID.String() < copyPositions[j].ID.String() })
	sort.SliceStable(copyPending, func(i, j int) bool { return copyPending[i].ID.String() < copyPending[j].ID.String() })
	return PortfolioSnapshot{asOf: asOf, accountID: accountID, executionMode: mode, cash: copyCash, positions: copyPositions, pending: copyPending, risk: risk}, nil
}
func (snapshot PortfolioSnapshot) AsOf() time.Time              { return snapshot.asOf }
func (snapshot PortfolioSnapshot) AccountID() AccountID         { return snapshot.accountID }
func (snapshot PortfolioSnapshot) ExecutionMode() ExecutionMode { return snapshot.executionMode }
func (snapshot PortfolioSnapshot) Cash() map[AssetID]SignedAmount {
	result := make(map[AssetID]SignedAmount, len(snapshot.cash))
	for k, v := range snapshot.cash {
		result[k] = v
	}
	return result
}
func (snapshot PortfolioSnapshot) CashAssets() []AssetID {
	assets := make([]AssetID, 0, len(snapshot.cash))
	for asset := range snapshot.cash {
		assets = append(assets, asset)
	}
	sort.Slice(assets, func(i, j int) bool { return assets[i].String() < assets[j].String() })
	return assets
}
func (snapshot PortfolioSnapshot) CashAmount(asset AssetID) (SignedAmount, bool) {
	value, ok := snapshot.cash[asset]
	return value, ok
}
func (snapshot PortfolioSnapshot) Positions() []Position {
	return append([]Position(nil), snapshot.positions...)
}
func (snapshot PortfolioSnapshot) PendingOrders() []PendingOrder {
	return append([]PendingOrder(nil), snapshot.pending...)
}
func (snapshot PortfolioSnapshot) RiskState() RiskState { return snapshot.risk }

type DecisionContext struct {
	marketObservedAt, signalAt, decisionAt time.Time
	bars                                   map[InstrumentID]BarSeries
	quotes                                 map[InstrumentID]Quote
	universe                               UniverseSnapshot
	portfolio                              PortfolioSnapshot
	settings                               map[string]string
	versions                               VersionContext
}

type DecisionContextInput struct {
	MarketObservedAt, SignalAt, DecisionAt time.Time
	Bars                                   map[InstrumentID][]Bar
	Quotes                                 map[InstrumentID]Quote
	Universe                               UniverseSnapshot
	Portfolio                              PortfolioSnapshot
	Settings                               map[string]string
	Versions                               VersionContext
}

func NewDecisionContext(input DecisionContextInput) (DecisionContext, error) {
	if input.MarketObservedAt.IsZero() || input.SignalAt.IsZero() || input.DecisionAt.IsZero() {
		return DecisionContext{}, fmt.Errorf("market, signal, and decision timestamps are required")
	}
	if input.SignalAt.Before(input.MarketObservedAt) || input.DecisionAt.Before(input.SignalAt) {
		return DecisionContext{}, fmt.Errorf("decision timestamps must be ordered market <= signal <= decision")
	}
	bars := make(map[InstrumentID]BarSeries, len(input.Bars))
	for id, values := range input.Bars {
		series, err := NewBarSeries(values)
		if err != nil {
			return DecisionContext{}, err
		}
		bars[id] = series
	}
	quotes := make(map[InstrumentID]Quote, len(input.Quotes))
	for id, value := range input.Quotes {
		quotes[id] = value
	}
	settings := make(map[string]string, len(input.Settings))
	for key, value := range input.Settings {
		settings[key] = value
	}
	// Constructors below clone their inputs again so the context owns each level.
	universe, err := NewUniverseSnapshot(input.Universe.AsOf(), input.Universe.PolicyVersion(), input.Universe.Source(), input.Universe.Candidates())
	if err != nil {
		return DecisionContext{}, err
	}
	portfolio, err := NewPortfolioSnapshot(input.Portfolio.AsOf(), input.Portfolio.AccountID(), input.Portfolio.ExecutionMode(), input.Portfolio.Cash(), input.Portfolio.Positions(), input.Portfolio.PendingOrders(), input.Portfolio.RiskState())
	if err != nil {
		return DecisionContext{}, err
	}
	return DecisionContext{marketObservedAt: input.MarketObservedAt, signalAt: input.SignalAt, decisionAt: input.DecisionAt, bars: bars, quotes: quotes, universe: universe, portfolio: portfolio, settings: settings, versions: input.Versions}, nil
}
func (context DecisionContext) MarketObservedAt() time.Time { return context.marketObservedAt }
func (context DecisionContext) SignalAt() time.Time         { return context.signalAt }
func (context DecisionContext) DecisionAt() time.Time       { return context.decisionAt }
func (context DecisionContext) Bars(id InstrumentID) []Bar  { return context.bars[id].Bars() }
func (context DecisionContext) Quotes() map[InstrumentID]Quote {
	result := make(map[InstrumentID]Quote, len(context.quotes))
	for k, v := range context.quotes {
		result[k] = v
	}
	return result
}
func (context DecisionContext) Quote(id InstrumentID) (Quote, bool) {
	value, ok := context.quotes[id]
	return value, ok
}
func (context DecisionContext) QuoteInstruments() []InstrumentID {
	ids := make([]InstrumentID, 0, len(context.quotes))
	for id := range context.quotes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids
}
func (context DecisionContext) Universe() UniverseSnapshot {
	snapshot, _ := NewUniverseSnapshot(context.universe.AsOf(), context.universe.PolicyVersion(), context.universe.Source(), context.universe.Candidates())
	return snapshot
}
func (context DecisionContext) Portfolio() PortfolioSnapshot {
	snapshot, _ := NewPortfolioSnapshot(context.portfolio.AsOf(), context.portfolio.AccountID(), context.portfolio.ExecutionMode(), context.portfolio.Cash(), context.portfolio.Positions(), context.portfolio.PendingOrders(), context.portfolio.RiskState())
	return snapshot
}
func (context DecisionContext) Settings() map[string]string {
	result := make(map[string]string, len(context.settings))
	for k, v := range context.settings {
		result[k] = v
	}
	return result
}
func (context DecisionContext) Setting(key string) (string, bool) {
	value, ok := context.settings[key]
	return value, ok
}
func (context DecisionContext) SettingKeys() []string {
	keys := make([]string, 0, len(context.settings))
	for key := range context.settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func (context DecisionContext) Versions() VersionContext { return context.versions }

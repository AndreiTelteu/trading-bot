package services

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm/clause"
)

const (
	UniverseModeDynamic                  = "dynamic"
	UniverseRegimeRiskOn                 = "risk_on"
	UniverseRegimeNeutral                = "neutral"
	UniverseRegimeRiskOff                = "risk_off"
	BacktestUniverseModeStatic           = "static"
	BacktestUniverseModeDynamicRecompute = "dynamic_recompute"
)

type UniversePolicy struct {
	Mode                   string        `json:"mode"`
	RebalanceInterval      time.Duration `json:"-"`
	RebalanceIntervalLabel string        `json:"rebalance_interval"`
	MinListingDays         int           `json:"min_listing_days"`
	MinDailyQuoteVolume    float64       `json:"min_daily_quote_volume"`
	MinIntradayQuoteVolume float64       `json:"min_intraday_quote_volume"`
	MaxGapRatio            float64       `json:"max_gap_ratio"`
	VolRatioMin            float64       `json:"vol_ratio_min"`
	VolRatioMax            float64       `json:"vol_ratio_max"`
	Max24hMove             float64       `json:"max_24h_move"`
	TopK                   int           `json:"top_k"`
	AnalyzeTopN            int           `json:"analyze_top_n"`
}

type UniverseCandidateMetrics struct {
	Symbol                    string             `json:"symbol"`
	BaseAsset                 string             `json:"base_asset,omitempty"`
	QuoteAsset                string             `json:"quote_asset,omitempty"`
	LastPrice                 float64            `json:"last_price"`
	Change24h                 float64            `json:"change_24h"`
	QuoteVolume24h            float64            `json:"quote_volume_24h"`
	ListingAgeDays            int                `json:"listing_age_days"`
	MedianDailyQuoteVolume    float64            `json:"median_daily_quote_volume"`
	MedianIntradayQuoteVolume float64            `json:"median_intraday_quote_volume"`
	GapRatio                  float64            `json:"gap_ratio"`
	VolatilityRatio           float64            `json:"volatility_ratio"`
	Return1D                  float64            `json:"return_1d"`
	Return3D                  float64            `json:"return_3d"`
	Return7D                  float64            `json:"return_7d"`
	Return30D                 float64            `json:"return_30d"`
	RelativeStrength          float64            `json:"relative_strength"`
	TrendQuality              float64            `json:"trend_quality"`
	BreakoutProximity         float64            `json:"breakout_proximity"`
	VolumeAcceleration        float64            `json:"volume_acceleration"`
	OverextensionPenalty      float64            `json:"overextension_penalty"`
	RankScore                 float64            `json:"rank_score"`
	RankComponents            map[string]float64 `json:"rank_components,omitempty"`
	Shortlisted               bool               `json:"shortlisted,omitempty"`
	RejectionReason           string             `json:"rejection_reason,omitempty"`
}

type UniverseSelectionResult struct {
	SnapshotID     uint                       `json:"snapshot_id,omitempty"`
	Timestamp      string                     `json:"timestamp"`
	Policy         UniversePolicy             `json:"policy"`
	RegimeState    string                     `json:"regime_state"`
	BreadthRatio   float64                    `json:"breadth_ratio"`
	EligibleCount  int                        `json:"eligible_count"`
	CandidateCount int                        `json:"candidate_count"`
	RankedCount    int                        `json:"ranked_count"`
	ShortlistCount int                        `json:"shortlist_count"`
	ActiveUniverse []UniverseCandidateMetrics `json:"active_universe"`
	Shortlist      []UniverseCandidateMetrics `json:"shortlist"`
	Members        []UniverseCandidateMetrics `json:"members"`
	Trending       TrendingData               `json:"trending"`
}

func GetUniversePolicy(settings map[string]string) UniversePolicy {
	legacyAnalyze := getSettingInt(settings, "trending_coins_to_analyze", 8)
	intervalLabel := getSettingString(settings, "universe_rebalance_interval", "1h")
	interval, err := time.ParseDuration(intervalLabel)
	if err != nil || interval <= 0 {
		interval = time.Hour
		intervalLabel = "1h"
	}

	topK := getSettingInt(settings, "universe_top_k", 20)
	if topK <= 0 {
		topK = 20
	}
	analyzeTopN := getSettingInt(settings, "universe_analyze_top_n", legacyAnalyze)
	if analyzeTopN <= 0 {
		analyzeTopN = legacyAnalyze
	}
	if analyzeTopN > topK {
		analyzeTopN = topK
	}

	policy := UniversePolicy{
		Mode:                   strings.ToLower(getSettingString(settings, "universe_mode", UniverseModeDynamic)),
		RebalanceInterval:      interval,
		RebalanceIntervalLabel: intervalLabel,
		MinListingDays:         getSettingInt(settings, "universe_min_listing_days", 45),
		MinDailyQuoteVolume:    getSettingFloat(settings, "universe_min_daily_quote_volume", 2_000_000),
		MinIntradayQuoteVolume: getSettingFloat(settings, "universe_min_intraday_quote_volume", 75_000),
		MaxGapRatio:            getSettingFloat(settings, "universe_max_gap_ratio", 0.05),
		VolRatioMin:            getSettingFloat(settings, "universe_vol_ratio_min", 0.004),
		VolRatioMax:            getSettingFloat(settings, "universe_vol_ratio_max", 0.08),
		Max24hMove:             getSettingFloat(settings, "universe_max_24h_move", 25),
		TopK:                   topK,
		AnalyzeTopN:            analyzeTopN,
	}

	if policy.MinListingDays < 0 {
		policy.MinListingDays = 0
	}
	if policy.MaxGapRatio < 0 {
		policy.MaxGapRatio = 0
	}
	if policy.VolRatioMin < 0 {
		policy.VolRatioMin = 0
	}
	if policy.VolRatioMax <= 0 {
		policy.VolRatioMax = 0.08
	}
	if policy.VolRatioMax < policy.VolRatioMin {
		policy.VolRatioMin, policy.VolRatioMax = policy.VolRatioMax, policy.VolRatioMin
	}
	if policy.Max24hMove <= 0 {
		policy.Max24hMove = 25
	}

	return policy
}

func ResolveBacktestUniverseMode(settings map[string]string) string {
	mode := strings.ToLower(getSettingString(settings, "backtest_universe_mode", BacktestUniverseModeStatic))
	switch mode {
	case BacktestUniverseModeDynamicRecompute:
		return mode
	default:
		return BacktestUniverseModeStatic
	}
}

func DiscoverEligibleUniverseSymbols() ([]string, error) {
	metadata, err := RefreshUniverseMetadata()
	if err != nil {
		return nil, err
	}

	symbols := make([]string, 0, len(metadata))
	for _, row := range metadata {
		if row.IsExcluded || !row.SpotTradable || strings.ToUpper(row.Status) != "TRADING" {
			continue
		}
		symbols = append(symbols, strings.ToUpper(row.Symbol))
	}
	sort.Strings(symbols)
	return symbols, nil
}

func RefreshUniverseMetadata() ([]database.UniverseSymbol, error) {
	ex := GetExchange()
	info, err := ex.FetchExchangeInfoCached(6 * time.Hour)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch exchange metadata: %w", err)
	}

	var existing []database.UniverseSymbol
	database.DB.Find(&existing)
	existingBySymbol := make(map[string]database.UniverseSymbol, len(existing))
	for _, row := range existing {
		existingBySymbol[strings.ToUpper(row.Symbol)] = row
	}

	now := time.Now().UTC()
	rows := make([]database.UniverseSymbol, 0, len(info.Symbols))
	for _, symbolInfo := range info.Symbols {
		symbol := strings.ToUpper(symbolInfo.Symbol)
		reason := universeMetadataExclusionReason(symbolInfo)
		firstSeen := now
		if existingRow, ok := existingBySymbol[symbol]; ok && !existingRow.FirstSeenAt.IsZero() {
			firstSeen = existingRow.FirstSeenAt
		}

		row := database.UniverseSymbol{
			Symbol:       symbol,
			BaseAsset:    strings.ToUpper(symbolInfo.BaseAsset),
			QuoteAsset:   strings.ToUpper(symbolInfo.QuoteAsset),
			Status:       strings.ToUpper(symbolInfo.Status),
			SpotTradable: symbolInfo.IsSpotTradingAllowed || containsString(symbolInfo.Permissions, "SPOT"),
			IsExcluded:   reason != "",
			FirstSeenAt:  firstSeen,
			LastSeenAt:   now,
		}
		if reason != "" {
			row.ExclusionReason = stringPtr(reason)
		}
		rows = append(rows, row)
	}

	if len(rows) == 0 {
		return nil, nil
	}

	for i := range rows {
		if err := database.DB.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "symbol"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"base_asset",
				"quote_asset",
				"status",
				"spot_tradable",
				"is_excluded",
				"exclusion_reason",
				"first_seen_at",
				"last_seen_at",
				"updated_at",
			}),
		}).Create(&rows[i]).Error; err != nil {
			return nil, err
		}
	}

	return rows, nil
}

func BuildUniverseSnapshot(policy UniversePolicy) (*UniverseSelectionResult, error) {
	metadata, err := RefreshUniverseMetadata()
	if err != nil {
		return nil, err
	}

	ex := GetExchange()
	tickers, err := ex.FetchAllTickers()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch ticker universe: %w", err)
	}

	metadataBySymbol := make(map[string]database.UniverseSymbol, len(metadata))
	for _, row := range metadata {
		metadataBySymbol[strings.ToUpper(row.Symbol)] = row
	}

	eligibleCoins := make([]TrendingCoin, 0, len(tickers))
	broadCandidates := make([]TrendingCoin, 0, len(tickers))
	for _, ticker := range tickers {
		row, ok := metadataBySymbol[strings.ToUpper(ticker.Symbol)]
		if !ok || row.IsExcluded || !row.SpotTradable || strings.ToUpper(row.Status) != "TRADING" {
			continue
		}
		price := parseFloatOrZero(ticker.LastPrice)
		change24h := parseFloatOrZero(ticker.PriceChangePercent)
		quoteVolume := parseFloatOrZero(ticker.QuoteVolume)
		coin := TrendingCoin{
			Symbol:    strings.ToUpper(ticker.Symbol),
			Price:     price,
			Change24h: change24h,
			Volume24h: quoteVolume,
		}
		eligibleCoins = append(eligibleCoins, coin)
		if quoteVolume >= policy.MinDailyQuoteVolume*0.5 {
			broadCandidates = append(broadCandidates, coin)
		}
	}

	sort.Slice(broadCandidates, func(i, j int) bool {
		if broadCandidates[i].Volume24h == broadCandidates[j].Volume24h {
			return broadCandidates[i].Symbol < broadCandidates[j].Symbol
		}
		return broadCandidates[i].Volume24h > broadCandidates[j].Volume24h
	})
	if len(broadCandidates) > universeCandidatePoolLimit(policy) {
		broadCandidates = broadCandidates[:universeCandidatePoolLimit(policy)]
	}

	btcDaily, _ := ex.FetchOHLCV("BTCUSDT", "1d", maxInt(policy.MinListingDays+35, 60))
	btc4h, _ := ex.FetchOHLCV("BTCUSDT", "4h", 250)
	btcReturn7D := calculateOHLCVReturn(btcDaily, 7)
	breadthEligibleCount := len(eligibleCoins)

	members := make([]UniverseCandidateMetrics, 0, len(broadCandidates))
	accepted := make([]UniverseCandidateMetrics, 0, len(broadCandidates))
	for _, coin := range broadCandidates {
		meta := metadataBySymbol[coin.Symbol]
		daily, dailyErr := ex.FetchOHLCV(coin.Symbol, "1d", maxInt(policy.MinListingDays+35, 60))
		intraday, intradayErr := ex.FetchOHLCV(coin.Symbol, "1h", 24*10)
		candidate := UniverseCandidateMetrics{
			Symbol:         coin.Symbol,
			BaseAsset:      meta.BaseAsset,
			QuoteAsset:     meta.QuoteAsset,
			LastPrice:      coin.Price,
			Change24h:      coin.Change24h,
			QuoteVolume24h: coin.Volume24h,
		}
		if dailyErr != nil || intradayErr != nil || len(daily) == 0 || len(intraday) == 0 {
			candidate.RejectionReason = "market_data_unavailable"
			members = append(members, candidate)
			continue
		}

		candidate = BuildUniverseCandidateMetrics(
			coin.Symbol,
			meta.BaseAsset,
			meta.QuoteAsset,
			coin.Price,
			coin.Change24h,
			coin.Volume24h,
			daily,
			intraday,
			btcReturn7D,
		)
		if rejection := UniverseHardFilterReason(candidate, policy); rejection != "" {
			candidate.RejectionReason = rejection
			members = append(members, candidate)
			continue
		}
		members = append(members, candidate)
		accepted = append(accepted, candidate)
	}

	breadth := ComputeUniverseBreadth(accepted)
	regime := DetermineUniverseRegime(computeRegimeGate(ohlcvToCandles(btc4h), 50, 200), computeRegimeGate(ohlcvToCandles(btcDaily), 20, 50), breadth)
	ranked := RankUniverseCandidates(accepted, policy)
	activeUniverse, shortlist := SelectUniverseCandidates(ranked, policy, regime)
	rankedBySymbol := make(map[string]UniverseCandidateMetrics, len(ranked))
	for _, candidate := range ranked {
		rankedBySymbol[candidate.Symbol] = candidate
	}
	for i := range members {
		if rankedCandidate, ok := rankedBySymbol[members[i].Symbol]; ok {
			members[i] = rankedCandidate
		}
	}

	result := &UniverseSelectionResult{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		Policy:         policy,
		RegimeState:    regime,
		BreadthRatio:   breadth,
		EligibleCount:  breadthEligibleCount,
		CandidateCount: len(members),
		RankedCount:    len(activeUniverse),
		ShortlistCount: len(shortlist),
		ActiveUniverse: activeUniverse,
		Shortlist:      shortlist,
		Members:        members,
		Trending:       buildTrendingDataFromCoins(eligibleCoins, "dynamic_universe"),
	}

	if err := persistUniverseSnapshot(result); err != nil {
		return nil, err
	}

	return result, nil
}

func BuildUniverseCandidateMetrics(symbol string, baseAsset string, quoteAsset string, lastPrice float64, change24h float64, quoteVolume24h float64, daily []OHLCV, intraday []OHLCV, benchmarkReturn7D float64) UniverseCandidateMetrics {
	dailyQuoteVolumes := quoteVolumes(daily)
	intradayQuoteVolumes := quoteVolumes(intraday)
	dailyCloses := ohlcvCloses(daily)
	intradayCloses := ohlcvCloses(intraday)
	price := lastPrice
	if price <= 0 && len(dailyCloses) > 0 {
		price = dailyCloses[len(dailyCloses)-1]
	}
	listingAgeDays := 0
	if len(daily) > 0 {
		listingAgeDays = len(daily) - 1
	}
	volatilityRatio := 0.0
	if price > 0 {
		volatilityRatio = CalculateATR(ohlcvToCandles(intraday), 14) / price
	}
	trendQuality := 0.0
	if len(intradayCloses) >= 50 && price > 0 {
		ema20 := CalculateEMA(intradayCloses, 20)
		ema50 := CalculateEMA(intradayCloses, 50)
		trendQuality = ((ema20 - ema50) / price) * 100
	}
	breakoutProximity := 0.0
	if len(daily) > 0 && price > 0 {
		recentHigh := highestHigh(daily, minInt(20, len(daily)))
		if recentHigh > 0 {
			breakoutProximity = price / recentHigh
		}
	}
	medianDaily := CalculateMedian(dailyQuoteVolumes[maxInt(0, len(dailyQuoteVolumes)-20):])
	medianIntraday := CalculateMedian(intradayQuoteVolumes[maxInt(0, len(intradayQuoteVolumes)-24*7):])
	volumeAcceleration := 0.0
	if medianDaily > 0 {
		volumeAcceleration = quoteVolume24h / medianDaily
	}
	overextension := 0.0
	if math.Abs(change24h) > 10 {
		overextension = math.Abs(change24h) / 10
	}
	return UniverseCandidateMetrics{
		Symbol:                    strings.ToUpper(symbol),
		BaseAsset:                 strings.ToUpper(baseAsset),
		QuoteAsset:                strings.ToUpper(quoteAsset),
		LastPrice:                 price,
		Change24h:                 change24h,
		QuoteVolume24h:            quoteVolume24h,
		ListingAgeDays:            listingAgeDays,
		MedianDailyQuoteVolume:    medianDaily,
		MedianIntradayQuoteVolume: medianIntraday,
		GapRatio:                  CalculateMissingBarRatio(intraday, time.Hour),
		VolatilityRatio:           volatilityRatio,
		Return1D:                  calculateOHLCVReturn(daily, 1),
		Return3D:                  calculateOHLCVReturn(daily, 3),
		Return7D:                  calculateOHLCVReturn(daily, 7),
		Return30D:                 calculateOHLCVReturn(daily, 30),
		RelativeStrength:          calculateOHLCVReturn(daily, 7) - benchmarkReturn7D,
		TrendQuality:              trendQuality,
		BreakoutProximity:         breakoutProximity,
		VolumeAcceleration:        volumeAcceleration,
		OverextensionPenalty:      overextension,
	}
}

func UniverseHardFilterReason(candidate UniverseCandidateMetrics, policy UniversePolicy) string {
	if candidate.ListingAgeDays < policy.MinListingDays {
		return "listing_age"
	}
	if candidate.MedianDailyQuoteVolume < policy.MinDailyQuoteVolume {
		return "daily_quote_volume"
	}
	if candidate.MedianIntradayQuoteVolume < policy.MinIntradayQuoteVolume {
		return "intraday_quote_volume"
	}
	if candidate.GapRatio > policy.MaxGapRatio {
		return "missing_bar_ratio"
	}
	if candidate.VolatilityRatio < policy.VolRatioMin {
		return "volatility_too_low"
	}
	if candidate.VolatilityRatio > policy.VolRatioMax {
		return "volatility_too_high"
	}
	if math.Abs(candidate.Change24h) > policy.Max24hMove {
		return "excessive_24h_move"
	}
	return ""
}

func RankUniverseCandidates(candidates []UniverseCandidateMetrics, policy UniversePolicy) []UniverseCandidateMetrics {
	if len(candidates) == 0 {
		return nil
	}

	momentumRaw := make(map[string]float64, len(candidates))
	relativeRaw := make(map[string]float64, len(candidates))
	liquidityRaw := make(map[string]float64, len(candidates))
	trendRaw := make(map[string]float64, len(candidates))
	breakoutRaw := make(map[string]float64, len(candidates))
	volumeAccelRaw := make(map[string]float64, len(candidates))
	volFitRaw := make(map[string]float64, len(candidates))
	overextensionRaw := make(map[string]float64, len(candidates))
	targetVol := (policy.VolRatioMin + policy.VolRatioMax) / 2
	if targetVol <= 0 {
		targetVol = 0.02
	}

	for _, candidate := range candidates {
		symbol := candidate.Symbol
		momentumRaw[symbol] = candidate.Return1D*0.2 + candidate.Return3D*0.35 + candidate.Return7D*0.45
		relativeRaw[symbol] = candidate.RelativeStrength
		liquidityRaw[symbol] = math.Log1p(math.Max(candidate.MedianIntradayQuoteVolume, 0))
		trendRaw[symbol] = candidate.TrendQuality
		breakoutRaw[symbol] = candidate.BreakoutProximity
		volumeAccelRaw[symbol] = candidate.VolumeAcceleration
		volFitRaw[symbol] = -math.Abs(candidate.VolatilityRatio - targetVol)
		overextensionRaw[symbol] = candidate.OverextensionPenalty
	}

	momentum := zscoreMap(momentumRaw)
	relative := zscoreMap(relativeRaw)
	liquidity := zscoreMap(liquidityRaw)
	trend := zscoreMap(trendRaw)
	breakout := zscoreMap(breakoutRaw)
	volumeAccel := zscoreMap(volumeAccelRaw)
	volFit := zscoreMap(volFitRaw)
	overextension := zscoreMap(overextensionRaw)

	ranked := make([]UniverseCandidateMetrics, len(candidates))
	for i, candidate := range candidates {
		components := map[string]float64{
			"momentum":          momentum[candidate.Symbol],
			"relative_strength": relative[candidate.Symbol],
			"liquidity":         liquidity[candidate.Symbol],
			"trend_quality":     trend[candidate.Symbol],
			"breakout":          breakout[candidate.Symbol],
			"volume_accel":      volumeAccel[candidate.Symbol],
			"volatility_fit":    volFit[candidate.Symbol],
			"overextension":     overextension[candidate.Symbol],
		}
		candidate.RankComponents = components
		candidate.RankScore =
			components["relative_strength"]*0.24 +
				components["momentum"]*0.20 +
				components["trend_quality"]*0.16 +
				components["liquidity"]*0.14 +
				components["breakout"]*0.10 +
				components["volume_accel"]*0.08 +
				components["volatility_fit"]*0.08 -
				components["overextension"]*0.12
		ranked[i] = candidate
	}

	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].RankScore == ranked[j].RankScore {
			if ranked[i].QuoteVolume24h == ranked[j].QuoteVolume24h {
				return ranked[i].Symbol < ranked[j].Symbol
			}
			return ranked[i].QuoteVolume24h > ranked[j].QuoteVolume24h
		}
		return ranked[i].RankScore > ranked[j].RankScore
	})

	return ranked
}

func ComputeUniverseBreadth(candidates []UniverseCandidateMetrics) float64 {
	if len(candidates) == 0 {
		return 0
	}
	positive := 0
	for _, candidate := range candidates {
		if candidate.Return3D > 0 && candidate.TrendQuality >= 0 {
			positive++
		}
	}
	return float64(positive) / float64(len(candidates))
}

func DetermineUniverseRegime(higherTrendUp bool, dailyTrendUp bool, breadth float64) string {
	switch {
	case higherTrendUp && dailyTrendUp && breadth >= 0.55:
		return UniverseRegimeRiskOn
	case !higherTrendUp && !dailyTrendUp && breadth < 0.40:
		return UniverseRegimeRiskOff
	default:
		return UniverseRegimeNeutral
	}
}

func SelectUniverseCandidates(ranked []UniverseCandidateMetrics, policy UniversePolicy, regime string) ([]UniverseCandidateMetrics, []UniverseCandidateMetrics) {
	activeLimit := policy.TopK
	shortlistLimit := policy.AnalyzeTopN
	if shortlistLimit <= 0 {
		shortlistLimit = activeLimit
	}

	switch regime {
	case UniverseRegimeRiskOff:
		activeLimit = minInt(activeLimit, 3)
		shortlistLimit = minInt(shortlistLimit, 2)
	case UniverseRegimeNeutral:
		activeLimit = minInt(activeLimit, maxInt(shortlistLimit, maxInt(4, activeLimit/2)))
		shortlistLimit = minInt(shortlistLimit, maxInt(3, shortlistLimit/2+1))
	}
	if activeLimit <= 0 {
		activeLimit = len(ranked)
	}
	if shortlistLimit <= 0 {
		shortlistLimit = len(ranked)
	}
	if activeLimit > len(ranked) {
		activeLimit = len(ranked)
	}
	active := make([]UniverseCandidateMetrics, activeLimit)
	copy(active, ranked[:activeLimit])
	if shortlistLimit > activeLimit {
		shortlistLimit = activeLimit
	}
	shortlist := make([]UniverseCandidateMetrics, shortlistLimit)
	copy(shortlist, active[:shortlistLimit])
	for i := range active {
		active[i].Shortlisted = i < shortlistLimit
	}
	for i := range shortlist {
		shortlist[i].Shortlisted = true
	}
	return active, shortlist
}

func buildTrendingDataFromCoins(coins []TrendingCoin, source string) TrendingData {
	byVolume := append([]TrendingCoin(nil), coins...)
	gainers := append([]TrendingCoin(nil), coins...)
	losers := append([]TrendingCoin(nil), coins...)
	sort.Slice(byVolume, func(i, j int) bool { return byVolume[i].Volume24h > byVolume[j].Volume24h })
	sort.Slice(gainers, func(i, j int) bool { return gainers[i].Change24h > gainers[j].Change24h })
	sort.Slice(losers, func(i, j int) bool { return losers[i].Change24h < losers[j].Change24h })
	return TrendingData{
		Source:     source,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		TopVolume:  trimTrendingCoins(byVolume, 20),
		TopGainers: trimTrendingCoins(gainers, 20),
		TopLosers:  trimTrendingCoins(losers, 20),
	}
}

func persistUniverseSnapshot(result *UniverseSelectionResult) error {
	if result == nil {
		return nil
	}
	snapshotTime, err := time.Parse(time.RFC3339, result.Timestamp)
	if err != nil {
		snapshotTime = time.Now().UTC()
	}
	snapshot := database.UniverseSnapshot{
		SnapshotTime:      snapshotTime,
		RebalanceInterval: result.Policy.RebalanceIntervalLabel,
		RegimeState:       result.RegimeState,
		BreadthRatio:      result.BreadthRatio,
		EligibleCount:     result.EligibleCount,
		CandidateCount:    result.CandidateCount,
		RankedCount:       result.RankedCount,
		ShortlistCount:    result.ShortlistCount,
	}
	if err := database.DB.Create(&snapshot).Error; err != nil {
		return err
	}

	activeSet := make(map[string]struct{}, len(result.ActiveUniverse))
	for _, candidate := range result.ActiveUniverse {
		activeSet[candidate.Symbol] = struct{}{}
	}
	shortlistSet := make(map[string]struct{}, len(result.Shortlist))
	for _, candidate := range result.Shortlist {
		shortlistSet[candidate.Symbol] = struct{}{}
	}

	members := make([]database.UniverseMember, 0, len(result.Members))
	for _, candidate := range result.Members {
		stage := "ranked"
		if candidate.RejectionReason != "" {
			stage = "rejected"
		} else if _, ok := shortlistSet[candidate.Symbol]; ok {
			stage = "shortlist"
		} else if _, ok := activeSet[candidate.Symbol]; ok {
			stage = "active"
		}
		componentsJSON := ""
		if len(candidate.RankComponents) > 0 {
			payload, _ := json.Marshal(candidate.RankComponents)
			componentsJSON = string(payload)
		}
		member := database.UniverseMember{
			UniverseSnapshotID:        snapshot.ID,
			Symbol:                    candidate.Symbol,
			Stage:                     stage,
			LastPrice:                 candidate.LastPrice,
			Change24h:                 candidate.Change24h,
			QuoteVolume24h:            candidate.QuoteVolume24h,
			ListingAgeDays:            candidate.ListingAgeDays,
			MedianDailyQuoteVolume:    candidate.MedianDailyQuoteVolume,
			MedianIntradayQuoteVolume: candidate.MedianIntradayQuoteVolume,
			GapRatio:                  candidate.GapRatio,
			VolatilityRatio:           candidate.VolatilityRatio,
			Return1D:                  candidate.Return1D,
			Return3D:                  candidate.Return3D,
			Return7D:                  candidate.Return7D,
			Return30D:                 candidate.Return30D,
			RelativeStrength:          candidate.RelativeStrength,
			TrendQuality:              candidate.TrendQuality,
			BreakoutProximity:         candidate.BreakoutProximity,
			VolumeAcceleration:        candidate.VolumeAcceleration,
			OverextensionPenalty:      candidate.OverextensionPenalty,
			RankScore:                 candidate.RankScore,
			RankComponentsJSON:        componentsJSON,
			Shortlisted:               candidate.Shortlisted,
		}
		if candidate.RejectionReason != "" {
			member.RejectionReason = stringPtr(candidate.RejectionReason)
		}
		members = append(members, member)
	}
	if len(members) > 0 {
		if err := database.DB.Create(&members).Error; err != nil {
			return err
		}
	}
	result.SnapshotID = snapshot.ID
	return nil
}

func universeMetadataExclusionReason(symbolInfo ExchangeSymbolInfo) string {
	quote := strings.ToUpper(symbolInfo.QuoteAsset)
	base := strings.ToUpper(symbolInfo.BaseAsset)
	status := strings.ToUpper(symbolInfo.Status)
	if quote != "USDT" {
		return "quote_asset"
	}
	if !symbolInfo.IsSpotTradingAllowed && !containsString(symbolInfo.Permissions, "SPOT") {
		return "spot_not_allowed"
	}
	if status != "TRADING" {
		return "status_" + strings.ToLower(status)
	}
	if reason := universeBaseAssetExclusionReason(base); reason != "" {
		return reason
	}
	return ""
}

func universeBaseAssetExclusionReason(base string) string {
	for _, suffix := range []string{"UP", "DOWN", "BULL", "BEAR"} {
		if strings.HasSuffix(base, suffix) {
			return "leveraged_token"
		}
	}
	if containsString([]string{"USDC", "FDUSD", "BUSD", "USDP", "TUSD", "DAI", "PAX", "EUR", "TRY", "BRL", "GBP", "AUD"}, base) {
		return "wrapper_or_fiat"
	}
	return ""
}

func parseFloatOrZero(value string) float64 {
	result, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return 0
	}
	return result
}

func containsString(values []string, target string) bool {
	target = strings.ToUpper(target)
	for _, value := range values {
		if strings.ToUpper(value) == target {
			return true
		}
	}
	return false
}

func trimTrendingCoins(coins []TrendingCoin, limit int) []TrendingCoin {
	if len(coins) <= limit {
		return coins
	}
	return coins[:limit]
}

func ohlcvToCandles(values []OHLCV) []Candle {
	result := make([]Candle, len(values))
	for i, candle := range values {
		result[i] = Candle{Close: candle.Close, High: candle.High, Low: candle.Low, Volume: candle.Volume}
	}
	return result
}

func ohlcvCloses(values []OHLCV) []float64 {
	closes := make([]float64, len(values))
	for i, candle := range values {
		closes[i] = candle.Close
	}
	return closes
}

func quoteVolumes(values []OHLCV) []float64 {
	volumes := make([]float64, len(values))
	for i, candle := range values {
		volumes[i] = candle.Close * candle.Volume
	}
	return volumes
}

func calculateOHLCVReturn(values []OHLCV, lookback int) float64 {
	closes := ohlcvCloses(values)
	return CalculateReturn(closes, lookback)
}

func highestHigh(values []OHLCV, lookback int) float64 {
	if len(values) == 0 || lookback <= 0 {
		return 0
	}
	start := maxInt(0, len(values)-lookback)
	highest := 0.0
	for i := start; i < len(values); i++ {
		if values[i].High > highest {
			highest = values[i].High
		}
	}
	return highest
}

func zscoreMap(raw map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(raw))
	if len(raw) == 0 {
		return result
	}
	values := make([]float64, 0, len(raw))
	for _, value := range raw {
		values = append(values, value)
	}
	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		delta := value - mean
		variance += delta * delta
	}
	stdDev := math.Sqrt(variance / float64(len(values)))
	for key, value := range raw {
		if stdDev == 0 {
			result[key] = 0
			continue
		}
		result[key] = (value - mean) / stdDev
	}
	return result
}

func universeCandidatePoolLimit(policy UniversePolicy) int {
	limit := maxInt(30, maxInt(policy.TopK*4, policy.AnalyzeTopN*6))
	if limit > 80 {
		limit = 80
	}
	return limit
}

package pointintime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/services"

	"gorm.io/gorm"
)

type Repository struct{ DB *gorm.DB }

func (r Repository) SymbolsAsOf(manifestID string, asOf time.Time, tradableOnly bool) ([]database.ExchangeSymbol, error) {
	manifest, _, err := ValidateManifest(r.DB, ManifestRequirement{ManifestID: manifestID, Start: asOf, End: asOf})
	if err != nil {
		return nil, err
	}
	query := r.DB.Model(&database.ExchangeSymbol{}).
		Where("listed_at<=? AND (delisted_at IS NULL OR delisted_at>?)", asOf, asOf).
		Where("ticker IN ?", manifest.Symbols)
	if tradableOnly {
		query = query.Where(`EXISTS (SELECT 1 FROM tradability_intervals ti WHERE ti.exchange_symbol_id=exchange_symbols.id AND ti.spot_tradable=true AND ti.effective_from<=? AND (ti.effective_to IS NULL OR ti.effective_to>?))`, asOf, asOf)
	}
	var rows []database.ExchangeSymbol
	if err := query.Order("asset_id ASC,ticker ASC,version DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	// One economic identity per as-of result. A malformed overlapping rename is
	// deterministic and cannot duplicate portfolio exposure.
	seen := map[string]bool{}
	out := make([]database.ExchangeSymbol, 0, len(rows))
	for _, row := range rows {
		if seen[row.AssetID] {
			continue
		}
		seen[row.AssetID] = true
		out = append(out, row)
	}
	return out, nil
}

func (r Repository) Bars(manifestID, symbolID, role, timeframe string, start, end, asOf time.Time) ([]services.OHLCV, error) {
	manifest, _, err := ValidateManifest(r.DB, ManifestRequirement{ManifestID: manifestID, Start: start, End: end, Roles: map[string]string{role: timeframe}})
	if err != nil {
		return nil, err
	}
	if end.After(asOf) {
		return nil, ErrFutureData
	}
	duration, ok := timeframeDuration(timeframe)
	if !ok {
		return nil, fmt.Errorf("unsupported timeframe %q", timeframe)
	}
	availableEnd := asOf.Add(-duration).Add(time.Millisecond)
	if availableEnd.Before(end) {
		end = availableEnd
	}
	var symbol database.ExchangeSymbol
	if err := r.DB.First(&symbol, "id=?", symbolID).Error; err != nil {
		return nil, err
	}
	if symbol.ListedAt.After(start) {
		start = symbol.ListedAt
	}
	if symbol.DelistedAt != nil && !symbol.DelistedAt.After(end) {
		end = symbol.DelistedAt.Add(-duration)
	}
	var rows []database.HistoricalBar
	if err := r.DB.Where("dataset_version=? AND exchange_symbol_id=? AND role=? AND timeframe=? AND open_time>=? AND open_time<=? AND open_time<=?", manifest.DatasetVersion, symbolID, role, timeframe, start, end, asOf).Order("open_time ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]services.OHLCV, 0, len(rows))
	for _, row := range rows {
		open, _ := strconv.ParseFloat(row.Open, 64)
		high, _ := strconv.ParseFloat(row.High, 64)
		low, _ := strconv.ParseFloat(row.Low, 64)
		closeValue, _ := strconv.ParseFloat(row.Close, 64)
		volume, _ := strconv.ParseFloat(row.Volume, 64)
		quote, _ := strconv.ParseFloat(row.QuoteVolume, 64)
		_ = quote // retained canonically in persistence; services.OHLCV has base volume only.
		out = append(out, services.OHLCV{OpenTime: row.OpenTime.UnixMilli(), CloseTime: row.OpenTime.Add(duration).UnixMilli() - 1, Open: open, High: high, Low: low, Close: closeValue, Volume: volume})
	}
	return out, nil
}

func (r Repository) ConstraintAsOf(symbolID string, asOf time.Time) (Constraint, error) {
	var row database.SymbolConstraintVersion
	err := r.DB.Where("exchange_symbol_id=? AND effective_from<=? AND (effective_to IS NULL OR effective_to>?)", symbolID, asOf, asOf).Order("effective_from DESC").First(&row).Error
	if err != nil {
		return Constraint{}, err
	}
	parse := func(v string) float64 { f, _ := strconv.ParseFloat(v, 64); return f }
	return Constraint{row.ExchangeSymbolID, row.EffectiveFrom, parse(row.QuantityStep), parse(row.PriceTick), parse(row.MinQuantity), parse(row.MinNotional)}, nil
}

func (r Repository) ConstraintsCover(symbolID string, start, end time.Time) bool {
	var rows []database.SymbolConstraintVersion
	if r.DB.Where("exchange_symbol_id=? AND effective_from<=? AND (effective_to IS NULL OR effective_to>?)", symbolID, end, start).Order("effective_from ASC").Find(&rows).Error != nil || len(rows) == 0 {
		return false
	}
	cursor := start
	for _, row := range rows {
		if row.EffectiveFrom.After(cursor) {
			return false
		}
		if row.EffectiveTo == nil {
			return true
		}
		if row.EffectiveTo.After(cursor) {
			cursor = *row.EffectiveTo
		}
		if !cursor.Before(end) {
			return true
		}
	}
	return !cursor.Before(end)
}

func UpsertAssetLifecycle(db *gorm.DB, assets []database.Asset, symbols []database.ExchangeSymbol, intervals []database.TradabilityInterval, constraints []database.SymbolConstraintVersion) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for i := range assets {
			if assets[i].ProvenanceJSON == "" {
				assets[i].ProvenanceJSON = "{}"
			}
			var existing database.Asset
			err := tx.First(&existing, "id=?", assets[i].ID).Error
			if err == nil {
				if existing.CanonicalCode != assets[i].CanonicalCode || existing.Name != assets[i].Name || existing.Source != assets[i].Source || existing.ProvenanceJSON != assets[i].ProvenanceJSON {
					return fmt.Errorf("%w: asset %s", ErrMetadataConflict, assets[i].ID)
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(&assets[i]).Error; err != nil {
				return err
			}
		}
		for i := range symbols {
			if symbols[i].ProvenanceJSON == "" {
				symbols[i].ProvenanceJSON = "{}"
			}
			var existing database.ExchangeSymbol
			err := tx.First(&existing, "id=?", symbols[i].ID).Error
			if err == nil {
				if existing.VenueID != symbols[i].VenueID || existing.Ticker != symbols[i].Ticker || existing.AssetID != symbols[i].AssetID || existing.BaseAssetID != symbols[i].BaseAssetID || existing.QuoteAssetID != symbols[i].QuoteAssetID || !existing.ListedAt.Equal(symbols[i].ListedAt) || !equalTimePointers(existing.DelistedAt, symbols[i].DelistedAt) || existing.Version != symbols[i].Version || existing.Source != symbols[i].Source || existing.ProvenanceJSON != symbols[i].ProvenanceJSON {
					return fmt.Errorf("%w: symbol %s", ErrMetadataConflict, symbols[i].ID)
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(&symbols[i]).Error; err != nil {
				return err
			}
		}
		for i := range intervals {
			if intervals[i].ProvenanceJSON == "" {
				intervals[i].ProvenanceJSON = "{}"
			}
			var existing database.TradabilityInterval
			err := tx.Where("exchange_symbol_id=? AND effective_from=?", intervals[i].ExchangeSymbolID, intervals[i].EffectiveFrom).First(&existing).Error
			if err == nil {
				if !equalTimePointers(existing.EffectiveTo, intervals[i].EffectiveTo) || existing.SpotTradable != intervals[i].SpotTradable || existing.Status != intervals[i].Status || existing.Source != intervals[i].Source || existing.ProvenanceJSON != intervals[i].ProvenanceJSON {
					return fmt.Errorf("%w: tradability %s", ErrMetadataConflict, intervals[i].ExchangeSymbolID)
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(&intervals[i]).Error; err != nil {
				return err
			}
		}
		for i := range constraints {
			if constraints[i].ProvenanceJSON == "" {
				constraints[i].ProvenanceJSON = "{}"
			}
			var existing database.SymbolConstraintVersion
			err := tx.Where("exchange_symbol_id=? AND effective_from=?", constraints[i].ExchangeSymbolID, constraints[i].EffectiveFrom).First(&existing).Error
			if err == nil {
				if !equalTimePointers(existing.EffectiveTo, constraints[i].EffectiveTo) || existing.QuantityStep != constraints[i].QuantityStep || existing.PriceTick != constraints[i].PriceTick || existing.MinQuantity != constraints[i].MinQuantity || existing.MinNotional != constraints[i].MinNotional || existing.Source != constraints[i].Source || existing.ProvenanceJSON != constraints[i].ProvenanceJSON {
					return fmt.Errorf("%w: constraints %s", ErrMetadataConflict, constraints[i].ExchangeSymbolID)
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(&constraints[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func EncodeJSON(value any) string { b, _ := json.Marshal(value); return string(b) }
func equalTimePointers(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

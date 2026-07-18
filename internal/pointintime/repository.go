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

type SymbolCursor struct {
	AssetID string `json:"asset_id"`
	ID      string `json:"id"`
}

func (r Repository) SymbolsAsOf(manifestID string, asOf time.Time, tradableOnly bool) ([]database.ExchangeSymbol, error) {
	rows, next, err := r.SymbolsAsOfPage(manifestID, asOf, tradableOnly, SymbolCursor{}, 1000)
	if err != nil {
		return nil, err
	}
	if next != nil {
		return nil, fmt.Errorf("point-in-time symbol universe exceeds 1000 rows; use bounded pagination")
	}
	return rows, nil
}

func (r Repository) SymbolsAsOfPage(manifestID string, asOf time.Time, tradableOnly bool, cursor SymbolCursor, limit int) ([]database.ExchangeSymbol, *SymbolCursor, error) {
	if limit < 1 || limit > 1000 {
		return nil, nil, fmt.Errorf("limit out of range")
	}
	manifest, _, err := ValidateManifest(r.DB, ManifestRequirement{ManifestID: manifestID, Start: asOf, End: asOf})
	if err != nil {
		return nil, nil, err
	}
	cutoff := mustTime(manifest.KnowledgeCutoff)
	if len(manifest.Series) == 0 {
		return []database.ExchangeSymbol{}, nil, nil
	}
	query := r.DB.Model(&database.ExchangeSymbol{}).
		Where(`EXISTS (SELECT 1 FROM dataset_manifests dm, jsonb_array_elements(dm.roles_timeframes_json) elem WHERE dm.id=? AND elem->>'exchange_symbol_id'=exchange_symbols.id)`, manifestID).
		Where("listed_at<=? AND available_at<=? AND retrieved_at<=? AND (delisted_at IS NULL OR delisted_at>?)", asOf, asOf, cutoff, asOf).
		Where(`EXISTS (SELECT 1 FROM assets a WHERE a.id=exchange_symbols.asset_id AND a.available_at<=? AND a.retrieved_at<=?)`, asOf, cutoff)
	if tradableOnly {
		query = query.Where(`EXISTS (SELECT 1 FROM tradability_intervals ti WHERE ti.exchange_symbol_id=exchange_symbols.id AND ti.spot_tradable=true AND ti.effective_from<=? AND (ti.effective_to IS NULL OR ti.effective_to>?) AND ti.available_at<=? AND ti.retrieved_at<=?)`, asOf, asOf, asOf, cutoff)
	}
	selected := query.Select("DISTINCT ON (asset_id) exchange_symbols.*").Order("asset_id ASC,ticker ASC,version DESC,id ASC")
	page := r.DB.Table("(?) AS pit_symbols", selected).Order("asset_id ASC,id ASC")
	if cursor.AssetID != "" {
		page = page.Where("asset_id > ? OR (asset_id = ? AND id > ?)", cursor.AssetID, cursor.AssetID, cursor.ID)
	}
	var rows []database.ExchangeSymbol
	if err := page.Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	var next *SymbolCursor
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[len(rows)-1]
		next = &SymbolCursor{AssetID: last.AssetID, ID: last.ID}
	}
	for index := range rows {
		row := &rows[index]
		if row.DelistedAt != nil && row.DelistedAt.After(asOf) {
			row.DelistedAt = nil
		}
	}
	return rows, next, nil
}

func (r Repository) Bars(manifestID, symbolID, role, timeframe string, start, end, asOf time.Time) ([]services.OHLCV, error) {
	values, _, err := r.BarPage(manifestID, symbolID, role, timeframe, start, end, asOf, time.Time{}, 1_000_000)
	return values, err
}

// BarPage applies the half-open range and keyset cursor in SQL. The cursor is
// exclusive and the returned next cursor is the last emitted open timestamp.
func (r Repository) BarPage(manifestID, symbolID, role, timeframe string, start, end, asOf, cursor time.Time, limit int) ([]services.OHLCV, *time.Time, error) {
	if limit < 1 || limit > 1000_000 {
		return nil, nil, fmt.Errorf("limit out of range")
	}
	wanted := SeriesKey{ExchangeSymbolID: symbolID, Role: role, Timeframe: timeframe}
	manifest, _, err := ValidateManifest(r.DB, ManifestRequirement{ManifestID: manifestID, Start: start, End: end, Series: []SeriesKey{wanted}})
	if err != nil {
		return nil, nil, err
	}
	if end.After(asOf) {
		return nil, nil, ErrFutureData
	}
	duration, ok := timeframeDuration(timeframe)
	if !ok {
		return nil, nil, fmt.Errorf("unsupported timeframe %q", timeframe)
	}
	var covered SeriesCoverage
	found := false
	for _, s := range manifest.Series {
		if sameSeries(s.SeriesKey, wanted) {
			covered = s
			found = true
			break
		}
	}
	if !found {
		return nil, nil, &CoverageError{CoverageReport{SchemaVersion: CoverageSchemaVersion, ManifestID: manifestID, Compatible: false, Failures: []CoverageFailure{{Code: "exact_series_missing", Series: seriesID(wanted), Details: "series absent from manifest"}}}}
	}
	if listed := mustTime(covered.ListedAt); listed.After(start) {
		start = listed
	}
	if covered.DelistedAt != "" {
		if delisted := mustTime(covered.DelistedAt); delisted.Before(end) {
			end = delisted
		}
	}
	query := r.DB.Where("dataset_version=? AND exchange_symbol_id=? AND role=? AND timeframe=? AND open_time>=? AND open_time<? AND available_at<=? AND retrieved_at<=?", manifest.DatasetVersion, symbolID, role, timeframe, start, end, asOf, mustTime(manifest.KnowledgeCutoff))
	if !cursor.IsZero() {
		query = query.Where("open_time>?", cursor.UTC())
	}
	var rows []database.HistoricalBar
	if err := query.Order("open_time ASC,id ASC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	var next *time.Time
	if len(rows) > limit {
		rows = rows[:limit]
		value := rows[len(rows)-1].OpenTime.UTC()
		next = &value
	}
	out := make([]services.OHLCV, 0, len(rows))
	for _, row := range rows {
		open, _ := strconv.ParseFloat(row.Open, 64)
		high, _ := strconv.ParseFloat(row.High, 64)
		low, _ := strconv.ParseFloat(row.Low, 64)
		closeValue, _ := strconv.ParseFloat(row.Close, 64)
		volume, _ := strconv.ParseFloat(row.Volume, 64)
		out = append(out, services.OHLCV{OpenTime: row.OpenTime.UnixMilli(), CloseTime: row.OpenTime.Add(duration).UnixMilli() - 1, Open: open, High: high, Low: low, Close: closeValue, Volume: volume})
	}
	return out, next, nil
}

func (r Repository) ConstraintAsOfManifest(manifestID, symbolID string, asOf time.Time) (Constraint, error) {
	manifest, _, err := ValidateManifest(r.DB, ManifestRequirement{ManifestID: manifestID, Series: []SeriesKey{{ExchangeSymbolID: symbolID, Role: RoleDecision, Timeframe: "15m"}}})
	if err != nil {
		return Constraint{}, err
	}
	return r.constraintAsOf(symbolID, asOf, mustTime(manifest.KnowledgeCutoff))
}

// ConstraintAsOf is retained for isolated fixtures. Production manifest-backed
// execution must use ConstraintAsOfManifest so the retrieval cutoff is pinned.
func (r Repository) ConstraintAsOf(symbolID string, asOf time.Time) (Constraint, error) {
	return r.constraintAsOf(symbolID, asOf, time.Time{})
}
func (r Repository) constraintAsOf(symbolID string, asOf, cutoff time.Time) (Constraint, error) {
	query := r.DB.Where("exchange_symbol_id=? AND effective_from<=? AND (effective_to IS NULL OR effective_to>?) AND available_at<=?", symbolID, asOf, asOf, asOf)
	if !cutoff.IsZero() {
		query = query.Where("retrieved_at<=?", cutoff)
	}
	var row database.SymbolConstraintVersion
	if err := query.Order("effective_from DESC,id DESC").First(&row).Error; err != nil {
		return Constraint{}, err
	}
	parse := func(v string) float64 { f, _ := strconv.ParseFloat(v, 64); return f }
	return Constraint{ExchangeSymbolID: row.ExchangeSymbolID, EffectiveFrom: row.EffectiveFrom, AvailableAt: row.AvailableAt, QuantityStep: parse(row.QuantityStep), PriceTick: parse(row.PriceTick), MinQuantity: parse(row.MinQuantity), MinNotional: parse(row.MinNotional)}, nil
}

func (r Repository) ConstraintsCoverManifest(manifestID, symbolID string, start, end time.Time) bool {
	manifest, err := LoadManifest(r.DB, manifestID)
	if err != nil {
		return false
	}
	rows, e := constraintRowsAtCutoff(r.DB, symbolID, start, end, mustTime(manifest.KnowledgeCutoff))
	return e == nil && constraintsCoverRows(rows, start, end)
}
func (r Repository) ConstraintsCover(symbolID string, start, end time.Time) bool {
	var rows []database.SymbolConstraintVersion
	if r.DB.Where("exchange_symbol_id=? AND effective_from<? AND (effective_to IS NULL OR effective_to>?)", symbolID, end, start).Order("effective_from ASC").Find(&rows).Error != nil {
		return false
	}
	return constraintsCoverRows(rows, start, end)
}

func UpsertAssetLifecycle(db *gorm.DB, assets []database.Asset, symbols []database.ExchangeSymbol, intervals []database.TradabilityInterval, constraints []database.SymbolConstraintVersion) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for i := range assets {
			a := &assets[i]
			a.AvailableAt, a.RetrievedAt = databaseTime(a.AvailableAt), databaseTime(a.RetrievedAt)
			if a.ID == "" || a.CanonicalCode == "" || a.Source == "" || a.RetrievedAt.IsZero() {
				return fmt.Errorf("invalid asset metadata")
			}
			if a.AvailableAt.IsZero() {
				a.AvailableAt = a.RetrievedAt
			}
			if a.AvailableAt.After(a.RetrievedAt) {
				return fmt.Errorf("asset available_at cannot follow retrieved_at")
			}
			if a.ProvenanceJSON == "" {
				a.ProvenanceJSON = "{}"
			}
			var existing database.Asset
			err := tx.First(&existing, "id=?", a.ID).Error
			if err == nil {
				if existing.CanonicalCode != a.CanonicalCode || existing.Name != a.Name || existing.Source != a.Source || existing.ProvenanceJSON != a.ProvenanceJSON || !existing.AvailableAt.Equal(a.AvailableAt) || !existing.RetrievedAt.Equal(a.RetrievedAt) {
					return fmt.Errorf("%w: asset %s", ErrMetadataConflict, a.ID)
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(a).Error; err != nil {
				return err
			}
		}
		for i := range symbols {
			s := &symbols[i]
			s.ListedAt, s.AvailableAt, s.RetrievedAt = databaseTime(s.ListedAt), databaseTime(s.AvailableAt), databaseTime(s.RetrievedAt)
			if s.DelistedAt != nil {
				value := databaseTime(*s.DelistedAt)
				s.DelistedAt = &value
			}
			if s.ID == "" || s.Ticker == "" || s.AssetID == "" || s.BaseAssetID == "" || s.QuoteAssetID == "" || s.Source == "" || s.ListedAt.IsZero() || s.RetrievedAt.IsZero() {
				return fmt.Errorf("invalid exchange symbol metadata")
			}
			if s.AvailableAt.IsZero() {
				s.AvailableAt = s.ListedAt
			}
			if s.AvailableAt.After(s.RetrievedAt) {
				return fmt.Errorf("symbol available_at cannot follow retrieved_at")
			}
			if s.ProvenanceJSON == "" {
				s.ProvenanceJSON = "{}"
			}
			var existing database.ExchangeSymbol
			err := tx.First(&existing, "id=?", s.ID).Error
			if err == nil {
				if existing.VenueID != s.VenueID || existing.Ticker != s.Ticker || existing.AssetID != s.AssetID || existing.BaseAssetID != s.BaseAssetID || existing.QuoteAssetID != s.QuoteAssetID || !existing.ListedAt.Equal(s.ListedAt) || !equalTimePointers(existing.DelistedAt, s.DelistedAt) || existing.Version != s.Version || existing.Source != s.Source || existing.ProvenanceJSON != s.ProvenanceJSON || !existing.AvailableAt.Equal(s.AvailableAt) || !existing.RetrievedAt.Equal(s.RetrievedAt) {
					return fmt.Errorf("%w: symbol %s", ErrMetadataConflict, s.ID)
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(s).Error; err != nil {
				return err
			}
		}
		for i := range intervals {
			v := &intervals[i]
			v.EffectiveFrom, v.AvailableAt, v.RetrievedAt = databaseTime(v.EffectiveFrom), databaseTime(v.AvailableAt), databaseTime(v.RetrievedAt)
			if v.EffectiveTo != nil {
				value := databaseTime(*v.EffectiveTo)
				v.EffectiveTo = &value
			}
			if v.ExchangeSymbolID == "" || v.EffectiveFrom.IsZero() || v.Source == "" || v.RetrievedAt.IsZero() {
				return fmt.Errorf("invalid tradability metadata")
			}
			if v.AvailableAt.IsZero() {
				v.AvailableAt = v.EffectiveFrom
			}
			if v.AvailableAt.After(v.RetrievedAt) {
				return fmt.Errorf("tradability available_at cannot follow retrieved_at")
			}
			if v.ProvenanceJSON == "" {
				v.ProvenanceJSON = "{}"
			}
			var existing database.TradabilityInterval
			err := tx.Where("exchange_symbol_id=? AND effective_from=?", v.ExchangeSymbolID, v.EffectiveFrom).First(&existing).Error
			if err == nil {
				if !equalTimePointers(existing.EffectiveTo, v.EffectiveTo) || existing.SpotTradable != v.SpotTradable || existing.Status != v.Status || existing.Source != v.Source || existing.ProvenanceJSON != v.ProvenanceJSON || !existing.AvailableAt.Equal(v.AvailableAt) || !existing.RetrievedAt.Equal(v.RetrievedAt) {
					return fmt.Errorf("%w: tradability %s", ErrMetadataConflict, v.ExchangeSymbolID)
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(v).Error; err != nil {
				return err
			}
		}
		for i := range constraints {
			v := &constraints[i]
			v.EffectiveFrom, v.AvailableAt, v.RetrievedAt = databaseTime(v.EffectiveFrom), databaseTime(v.AvailableAt), databaseTime(v.RetrievedAt)
			if v.EffectiveTo != nil {
				value := databaseTime(*v.EffectiveTo)
				v.EffectiveTo = &value
			}
			if v.ExchangeSymbolID == "" || v.EffectiveFrom.IsZero() || v.Source == "" || v.RetrievedAt.IsZero() {
				return fmt.Errorf("invalid constraint metadata")
			}
			if v.AvailableAt.IsZero() {
				v.AvailableAt = v.EffectiveFrom
			}
			if v.AvailableAt.After(v.RetrievedAt) {
				return fmt.Errorf("constraint available_at cannot follow retrieved_at")
			}
			if v.ProvenanceJSON == "" {
				v.ProvenanceJSON = "{}"
			}
			var existing database.SymbolConstraintVersion
			err := tx.Where("exchange_symbol_id=? AND effective_from=?", v.ExchangeSymbolID, v.EffectiveFrom).First(&existing).Error
			if err == nil {
				if !equalTimePointers(existing.EffectiveTo, v.EffectiveTo) || existing.QuantityStep != v.QuantityStep || existing.PriceTick != v.PriceTick || existing.MinQuantity != v.MinQuantity || existing.MinNotional != v.MinNotional || existing.Source != v.Source || existing.ProvenanceJSON != v.ProvenanceJSON || !existing.AvailableAt.Equal(v.AvailableAt) || !existing.RetrievedAt.Equal(v.RetrievedAt) {
					return fmt.Errorf("%w: constraints %s", ErrMetadataConflict, v.ExchangeSymbolID)
				}
				continue
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err := tx.Create(v).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

var errDryRunRollback = errors.New("point-in-time metadata dry-run rollback")

type MetadataIngestRequest struct {
	Assets      []database.Asset
	Symbols     []database.ExchangeSymbol
	Tradability []database.TradabilityInterval
	Constraints []database.SymbolConstraintVersion
	Start, End  time.Time
	DryRun      bool
}

func IngestMetadata(db *gorm.DB, request MetadataIngestRequest) error {
	if db == nil || request.Start.IsZero() || !request.End.After(request.Start) {
		return fmt.Errorf("metadata ingestion requires a bounded half-open interval")
	}
	inside := func(at time.Time) bool { return !at.Before(request.Start) && at.Before(request.End) }
	for _, v := range request.Assets {
		if !inside(v.AvailableAt) {
			return fmt.Errorf("asset %s availability is outside metadata bounds", v.ID)
		}
	}
	for _, v := range request.Symbols {
		if !inside(v.ListedAt) {
			return fmt.Errorf("symbol %s listing is outside metadata bounds", v.ID)
		}
	}
	for _, v := range request.Tradability {
		if !inside(v.EffectiveFrom) {
			return fmt.Errorf("tradability %s effective time is outside metadata bounds", v.ExchangeSymbolID)
		}
	}
	for _, v := range request.Constraints {
		if !inside(v.EffectiveFrom) {
			return fmt.Errorf("constraint %s effective time is outside metadata bounds", v.ExchangeSymbolID)
		}
	}
	if request.DryRun {
		return ValidateAssetLifecycle(db, request.Assets, request.Symbols, request.Tradability, request.Constraints)
	}
	return UpsertAssetLifecycle(db, request.Assets, request.Symbols, request.Tradability, request.Constraints)
}

func ValidateAssetLifecycle(db *gorm.DB, assets []database.Asset, symbols []database.ExchangeSymbol, intervals []database.TradabilityInterval, constraints []database.SymbolConstraintVersion) error {
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := UpsertAssetLifecycle(tx, assets, symbols, intervals, constraints); err != nil {
			return err
		}
		return errDryRunRollback
	})
	if errors.Is(err, errDryRunRollback) {
		return nil
	}
	return err
}
func EncodeJSON(value any) string { b, _ := json.Marshal(value); return string(b) }
func equalTimePointers(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
func databaseTime(value time.Time) time.Time {
	if value.IsZero() {
		return value
	}
	return value.UTC().Truncate(time.Microsecond)
}

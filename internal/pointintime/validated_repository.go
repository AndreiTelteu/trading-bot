package pointintime

import (
	"fmt"
	"strconv"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/services"

	"gorm.io/gorm"
)

// ValidatedRepository is an immutable capability created from a manifest that
// has already passed ValidateManifest. Its read methods must not re-verify the
// complete manifest for every individual series.
type ValidatedRepository struct {
	db       *gorm.DB
	manifest Manifest
}

func (r Repository) Validate(requirement ManifestRequirement) (ValidatedRepository, CoverageReport, error) {
	manifest, report, err := ValidateManifest(r.DB, requirement)
	if err != nil {
		return ValidatedRepository{}, report, err
	}
	if r.DB == nil || manifest.ID == "" || manifest.SchemaVersion != ManifestSchemaVersion || manifest.ContentHash == "" {
		return ValidatedRepository{}, report, fmt.Errorf("invalid validated manifest capability")
	}
	return ValidatedRepository{db: r.DB, manifest: manifest}, report, nil
}

func (r ValidatedRepository) Manifest() Manifest { return r.manifest }

func (r ValidatedRepository) Bars(symbolID, role, timeframe string, start, end, asOf time.Time) ([]services.OHLCV, error) {
	wanted := SeriesKey{ExchangeSymbolID: symbolID, Role: role, Timeframe: timeframe}
	covered, err := coveredSeries(r.manifest, wanted)
	if err != nil {
		return nil, err
	}
	if end.After(asOf) {
		return nil, ErrFutureData
	}
	if listed := mustTime(covered.ListedAt); listed.After(start) {
		start = listed
	}
	if covered.DelistedAt != "" {
		if delisted := mustTime(covered.DelistedAt); delisted.Before(end) {
			end = delisted
		}
	}
	key := runtimeBarsCacheKey(r.manifest, wanted, start, end, asOf)
	return sharedRuntimeBars.get(key, func() ([]services.OHLCV, error) {
		return loadRuntimeBars(r.db, r.manifest, wanted, start, end, asOf)
	})
}

func coveredSeries(manifest Manifest, wanted SeriesKey) (SeriesCoverage, error) {
	for _, series := range manifest.Series {
		if sameSeries(series.SeriesKey, wanted) {
			return series, nil
		}
	}
	return SeriesCoverage{}, &CoverageError{Report: CoverageReport{SchemaVersion: CoverageSchemaVersion, ManifestID: manifest.ID, Compatible: false, Failures: []CoverageFailure{{Code: "exact_series_missing", Series: seriesID(wanted), Details: "series absent from manifest"}}}}
}

type runtimeBarRow struct {
	OpenTime time.Time
	Open     string
	High     string
	Low      string
	Close    string
	Volume   string
}

func loadRuntimeBars(db *gorm.DB, manifest Manifest, wanted SeriesKey, start, end, asOf time.Time) ([]services.OHLCV, error) {
	duration, ok := timeframeDuration(wanted.Timeframe)
	if !ok {
		return nil, fmt.Errorf("unsupported timeframe %q", wanted.Timeframe)
	}
	if !end.After(start) {
		return []services.OHLCV{}, nil
	}
	var rows []runtimeBarRow
	err := db.Model(&database.HistoricalBar{}).
		Select("open_time, open, high, low, close, volume").
		Where("dataset_version=? AND exchange_symbol_id=? AND role=? AND timeframe=? AND open_time>=? AND open_time<? AND available_at<=? AND retrieved_at<=?", manifest.DatasetVersion, wanted.ExchangeSymbolID, wanted.Role, wanted.Timeframe, start, end, asOf, mustTime(manifest.KnowledgeCutoff)).
		Order("open_time ASC,id ASC").Find(&rows).Error
	if err != nil {
		return nil, err
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
	return out, nil
}

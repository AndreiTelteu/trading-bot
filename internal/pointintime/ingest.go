package pointintime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BarClient interface {
	FetchBars(ctx context.Context, ticker, timeframe string, start, end time.Time, limit int) ([]Bar, error)
}
type SleepFunc func(context.Context, time.Duration) error
type IngestRequest struct {
	DatasetVersion, ExchangeSymbolID, Ticker, Timeframe, Role, Source string
	Start, End                                                        time.Time
	DryRun                                                            bool
	PageSize, MaxRetries                                              int
	RateLimit                                                         time.Duration
}
type IngestResult struct {
	Inserted, Duplicates int
	ResumedFrom          *time.Time
	Unresolved           []string
}
type Ingester struct {
	DB     *gorm.DB
	Client BarClient
	Sleep  SleepFunc
	Now    func() time.Time
}

func (i Ingester) Run(ctx context.Context, request IngestRequest) (IngestResult, error) {
	if i.DB == nil || i.Client == nil || request.DatasetVersion == "" || request.ExchangeSymbolID == "" || request.Source == "" || request.Start.IsZero() || request.End.Before(request.Start) || (request.Role != RoleDecision && request.Role != RoleExecution && request.Role != RoleBenchmark) {
		return IngestResult{}, fmt.Errorf("invalid ingestion request")
	}
	var symbol database.ExchangeSymbol
	if err := i.DB.First(&symbol, "id=?", request.ExchangeSymbolID).Error; err != nil {
		return IngestResult{}, err
	}
	if request.Start.Before(symbol.ListedAt) || symbol.DelistedAt != nil && !request.End.Before(*symbol.DelistedAt) {
		return IngestResult{}, fmt.Errorf("ingestion interval falls outside symbol lifecycle")
	}
	if request.PageSize <= 0 {
		request.PageSize = 1000
	}
	interval, intervalOK := timeframeDuration(request.Timeframe)
	if !intervalOK {
		return IngestResult{}, fmt.Errorf("unsupported ingestion timeframe %q", request.Timeframe)
	}
	if request.MaxRetries < 0 {
		request.MaxRetries = 0
	}
	if i.Sleep == nil {
		i.Sleep = func(ctx context.Context, d time.Duration) error {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
				return nil
			}
		}
	}
	if i.Now == nil {
		i.Now = time.Now
	}
	result := IngestResult{}
	cursor := request.Start.UTC()
	var checkpoint database.IngestionCheckpoint
	err := i.DB.Where("dataset_version=? AND exchange_symbol_id=? AND timeframe=? AND role=?", request.DatasetVersion, request.ExchangeSymbolID, request.Timeframe, request.Role).First(&checkpoint).Error
	if err == nil && checkpoint.LastOpenTime != nil && !checkpoint.LastOpenTime.Before(cursor) {
		if request.End.After(*checkpoint.LastOpenTime) {
			cursor = checkpoint.LastOpenTime.Add(interval)
			copy := cursor
			result.ResumedFrom = &copy
		}
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	for !cursor.After(request.End) {
		var bars []Bar
		var fetchErr error
		for attempt := 0; attempt <= request.MaxRetries; attempt++ {
			bars, fetchErr = i.Client.FetchBars(ctx, request.Ticker, request.Timeframe, cursor, request.End, request.PageSize)
			if fetchErr == nil {
				break
			}
			if attempt < request.MaxRetries {
				if err := i.Sleep(ctx, request.RateLimit*time.Duration(attempt+1)); err != nil {
					return result, err
				}
			}
		}
		if fetchErr != nil {
			result.Unresolved = append(result.Unresolved, cursor.UTC().Format(time.RFC3339Nano)+":"+fetchErr.Error())
			break
		}
		if len(bars) == 0 {
			result.Unresolved = append(result.Unresolved, cursor.UTC().Format(time.RFC3339Nano)+":no_data")
			break
		}
		sort.Slice(bars, func(a, b int) bool { return bars[a].OpenTime.Before(bars[b].OpenTime) })
		last := cursor
		processed := 0
		for _, bar := range bars {
			if bar.OpenTime.Before(cursor) || bar.OpenTime.After(request.End) {
				continue
			}
			row, err := historicalBar(request, bar, i.Now().UTC())
			if err != nil {
				return result, err
			}
			var existing database.HistoricalBar
			find := i.DB.Where("exchange_symbol_id=? AND timeframe=? AND open_time=? AND dataset_version=? AND role=?", row.ExchangeSymbolID, row.Timeframe, row.OpenTime, row.DatasetVersion, row.Role).First(&existing).Error
			if find == nil {
				if existing.ContentHash != row.ContentHash {
					return result, fmt.Errorf("%w: %s %s", ErrBarConflict, request.ExchangeSymbolID, canonicalTime(row.OpenTime))
				}
				result.Duplicates++
			} else if !errors.Is(find, gorm.ErrRecordNotFound) {
				return result, find
			} else if !request.DryRun {
				if err := i.DB.Create(&row).Error; err != nil {
					return result, err
				}
				result.Inserted++
			} else {
				result.Inserted++
			}
			processed++
			last = bar.OpenTime.UTC()
		}
		if processed == 0 {
			result.Unresolved = append(result.Unresolved, cursor.UTC().Format(time.RFC3339Nano)+":no_usable_data")
			break
		}
		if !request.DryRun {
			unresolved, _ := json.Marshal(result.Unresolved)
			cp := database.IngestionCheckpoint{DatasetVersion: request.DatasetVersion, ExchangeSymbolID: request.ExchangeSymbolID, Timeframe: request.Timeframe, Role: request.Role, LastOpenTime: &last, Status: "running", UnresolvedJSON: string(unresolved), UpdatedAt: i.Now().UTC()}
			if err := i.DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dataset_version"}, {Name: "exchange_symbol_id"}, {Name: "timeframe"}, {Name: "role"}}, DoUpdates: clause.AssignmentColumns([]string{"last_open_time", "status", "unresolved_json", "updated_at"})}).Create(&cp).Error; err != nil {
				return result, err
			}
		}
		next := last.Add(interval)
		if !next.After(cursor) {
			return result, fmt.Errorf("ingestion client made no progress")
		}
		cursor = next
		if request.RateLimit > 0 {
			if err := i.Sleep(ctx, request.RateLimit); err != nil {
				return result, err
			}
		}
	}
	if !request.DryRun {
		status := "complete"
		if len(result.Unresolved) > 0 {
			status = "unresolved"
		}
		i.DB.Model(&database.IngestionCheckpoint{}).Where("dataset_version=? AND exchange_symbol_id=? AND timeframe=? AND role=?", request.DatasetVersion, request.ExchangeSymbolID, request.Timeframe, request.Role).Updates(map[string]any{"status": status, "unresolved_json": EncodeJSON(result.Unresolved), "updated_at": i.Now().UTC()})
	}
	return result, nil
}

func historicalBar(request IngestRequest, bar Bar, retrieved time.Time) (database.HistoricalBar, error) {
	if bar.Quality == "" {
		bar.Quality = "valid"
	}
	if bar.Open == "" || bar.High == "" || bar.Low == "" || bar.Close == "" {
		return database.HistoricalBar{}, fmt.Errorf("bar prices are required")
	}
	canonical := struct {
		OpenTime, Open, High, Low, Close, Volume, QuoteVolume string
		TradeCount                                            int64
		Quality, Source                                       string
		Flags                                                 []string
		Provenance                                            map[string]string
	}{canonicalTime(bar.OpenTime), bar.Open, bar.High, bar.Low, bar.Close, bar.Volume, bar.QuoteVolume, bar.TradeCount, bar.Quality, request.Source, append([]string(nil), bar.Flags...), bar.Provenance}
	sort.Strings(canonical.Flags)
	payload, _ := json.Marshal(canonical)
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	return database.HistoricalBar{ExchangeSymbolID: request.ExchangeSymbolID, Timeframe: request.Timeframe, OpenTime: bar.OpenTime.UTC(), DatasetVersion: request.DatasetVersion, Role: request.Role, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume, QuoteVolume: bar.QuoteVolume, TradeCount: bar.TradeCount, QualityStatus: bar.Quality, QualityFlagsJSON: EncodeJSON(canonical.Flags), Source: request.Source, ProvenanceJSON: EncodeJSON(bar.Provenance), RetrievedAt: retrieved, ContentHash: hash, CreatedAt: retrieved}, nil
}

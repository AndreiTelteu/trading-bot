package pointintime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BarClient interface {
	FetchBars(context.Context, string, string, time.Time, time.Time, int) ([]Bar, error)
}
type SleepFunc func(context.Context, time.Duration) error
type IngestRequest struct {
	DatasetVersion, ExchangeSymbolID, Ticker, Timeframe, Role, Source string
	Start, End                                                        time.Time // half-open [Start,End)
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
	if i.DB == nil || i.Client == nil || request.DatasetVersion == "" || request.ExchangeSymbolID == "" || request.Source == "" || request.Start.IsZero() || !request.End.After(request.Start) || (request.Role != RoleDecision && request.Role != RoleExecution && request.Role != RoleBenchmark) {
		return IngestResult{}, fmt.Errorf("invalid ingestion request: range must be half-open [start,end)")
	}
	var symbol database.ExchangeSymbol
	if err := i.DB.First(&symbol, "id=?", request.ExchangeSymbolID).Error; err != nil {
		return IngestResult{}, err
	}
	if request.Ticker == "" {
		request.Ticker = symbol.Ticker
	} else if !strings.EqualFold(request.Ticker, symbol.Ticker) {
		return IngestResult{}, fmt.Errorf("ticker %q does not match exchange symbol %s ticker %q", request.Ticker, symbol.ID, symbol.Ticker)
	}
	request.Ticker = symbol.Ticker
	if request.Start.Before(symbol.ListedAt) || (symbol.DelistedAt != nil && request.End.After(*symbol.DelistedAt)) {
		return IngestResult{}, fmt.Errorf("ingestion interval falls outside symbol lifecycle")
	}
	if request.PageSize <= 0 {
		request.PageSize = 1000
	}
	if request.PageSize > 1000 {
		request.PageSize = 1000
	}
	interval, ok := timeframeDuration(request.Timeframe)
	if !ok {
		return IngestResult{}, fmt.Errorf("unsupported ingestion timeframe %q", request.Timeframe)
	}
	if request.MaxRetries < 0 {
		return IngestResult{}, fmt.Errorf("max retries cannot be negative")
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
	request.Start, request.End = request.Start.UTC(), request.End.UTC()
	result := IngestResult{}
	cursor := request.Start
	var checkpoint database.IngestionCheckpoint
	err := i.DB.Where("dataset_version=? AND exchange_symbol_id=? AND timeframe=? AND role=?", request.DatasetVersion, request.ExchangeSymbolID, request.Timeframe, request.Role).First(&checkpoint).Error
	if err == nil && checkpoint.Status != "complete" && checkpoint.LastOpenTime != nil && !checkpoint.LastOpenTime.Before(cursor) {
		cursor = checkpoint.LastOpenTime.Add(interval)
		if cursor.Before(request.End) {
			copy := cursor
			result.ResumedFrom = &copy
		}
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	for cursor.Before(request.End) {
		pageEnd := cursor.Add(time.Duration(request.PageSize) * interval)
		if pageEnd.After(request.End) {
			pageEnd = request.End
		}
		var values []Bar
		var fetchErr error
		for attempt := 0; attempt <= request.MaxRetries; attempt++ {
			values, fetchErr = i.Client.FetchBars(ctx, request.Ticker, request.Timeframe, cursor, pageEnd, request.PageSize)
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
			result.Unresolved = append(result.Unresolved, intervalFailure(cursor, pageEnd, fetchErr.Error()))
			break
		}
		sort.Slice(values, func(a, b int) bool { return values[a].OpenTime.Before(values[b].OpenTime) })
		contiguous := make([]Bar, 0, len(values))
		expected := cursor
		for _, bar := range values {
			at := bar.OpenTime.UTC()
			if at.Before(cursor) {
				continue
			}
			if !at.Before(pageEnd) {
				continue
			}
			if at.After(expected) {
				result.Unresolved = append(result.Unresolved, intervalFailure(expected, at, "missing_internal_page_interval"))
				break
			}
			if at.Before(expected) {
				continue
			}
			contiguous = append(contiguous, bar)
			expected = expected.Add(interval)
		}
		if len(contiguous) == 0 {
			if len(result.Unresolved) == 0 {
				result.Unresolved = append(result.Unresolved, intervalFailure(cursor, pageEnd, "no_data"))
			}
			break
		}
		retrieved := i.Now().UTC()
		last := contiguous[len(contiguous)-1].OpenTime.UTC()
		inserted, duplicates := 0, 0
		if request.DryRun {
			for _, bar := range contiguous {
				row, err := historicalBar(request, bar, retrieved)
				if err != nil {
					return result, err
				}
				var existing database.HistoricalBar
				find := i.DB.Where("exchange_symbol_id=? AND timeframe=? AND open_time=? AND dataset_version=? AND role=?", row.ExchangeSymbolID, row.Timeframe, row.OpenTime, row.DatasetVersion, row.Role).First(&existing).Error
				if find == nil {
					if existing.ContentHash != row.ContentHash {
						return result, fmt.Errorf("%w: %s %s", ErrBarConflict, request.ExchangeSymbolID, canonicalTime(row.OpenTime))
					}
					duplicates++
					continue
				}
				if !errors.Is(find, gorm.ErrRecordNotFound) {
					return result, find
				}
				inserted++
			}
		} else {
			err = i.DB.Transaction(func(tx *gorm.DB) error {
				// A transaction-scoped advisory lock serializes checkpoint advancement
				// even when the checkpoint row does not exist yet.
				lockKey := request.DatasetVersion + "|" + request.ExchangeSymbolID + "|" + request.Timeframe + "|" + request.Role
				if e := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", lockKey).Error; e != nil {
					return e
				}
				for _, bar := range contiguous {
					row, e := historicalBar(request, bar, retrieved)
					if e != nil {
						return e
					}
					var existing database.HistoricalBar
					find := tx.Where("exchange_symbol_id=? AND timeframe=? AND open_time=? AND dataset_version=? AND role=?", row.ExchangeSymbolID, row.Timeframe, row.OpenTime, row.DatasetVersion, row.Role).First(&existing).Error
					if find == nil {
						if existing.ContentHash != row.ContentHash {
							return fmt.Errorf("%w: %s %s", ErrBarConflict, request.ExchangeSymbolID, canonicalTime(row.OpenTime))
						}
						duplicates++
						continue
					}
					if !errors.Is(find, gorm.ErrRecordNotFound) {
						return find
					}
					if e := tx.Create(&row).Error; e != nil {
						return e
					}
					inserted++
				}
				unresolved := EncodeJSON(result.Unresolved)
				cp := database.IngestionCheckpoint{DatasetVersion: request.DatasetVersion, ExchangeSymbolID: request.ExchangeSymbolID, Timeframe: request.Timeframe, Role: request.Role, LastOpenTime: &last, Status: "running", UnresolvedJSON: unresolved, UpdatedAt: retrieved}
				return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "dataset_version"}, {Name: "exchange_symbol_id"}, {Name: "timeframe"}, {Name: "role"}}, DoUpdates: clause.Assignments(map[string]any{"last_open_time": gorm.Expr("GREATEST(COALESCE(ingestion_checkpoints.last_open_time, EXCLUDED.last_open_time), EXCLUDED.last_open_time)"), "status": "running", "unresolved_json": unresolved, "updated_at": retrieved})}).Create(&cp).Error
			})
			if err != nil {
				return result, err
			}
		}
		result.Inserted += inserted
		result.Duplicates += duplicates
		next := last.Add(interval)
		if !next.After(cursor) {
			return result, fmt.Errorf("ingestion client made no progress")
		}
		cursor = next
		if len(result.Unresolved) > 0 {
			break
		}
		if cursor.Before(pageEnd) {
			result.Unresolved = append(result.Unresolved, intervalFailure(cursor, pageEnd, "sparse_page"))
			break
		}
		if request.RateLimit > 0 {
			if err := i.Sleep(ctx, request.RateLimit); err != nil {
				return result, err
			}
		}
	}
	if !request.DryRun {
		status := "complete"
		if len(result.Unresolved) > 0 || cursor.Before(request.End) {
			status = "unresolved"
		}
		update := i.DB.Model(&database.IngestionCheckpoint{}).Where("dataset_version=? AND exchange_symbol_id=? AND timeframe=? AND role=?", request.DatasetVersion, request.ExchangeSymbolID, request.Timeframe, request.Role).Updates(map[string]any{"status": status, "unresolved_json": EncodeJSON(result.Unresolved), "updated_at": i.Now().UTC()})
		if update.Error != nil {
			return result, update.Error
		}
		if update.RowsAffected != 1 {
			return result, fmt.Errorf("checkpoint finalization affected %d rows", update.RowsAffected)
		}
	}
	return result, nil
}

func intervalFailure(start, end time.Time, reason string) string {
	return canonicalTime(start) + "/" + canonicalTime(end) + ":" + reason
}
func historicalBar(request IngestRequest, bar Bar, retrieved time.Time) (database.HistoricalBar, error) {
	if bar.Quality == "" {
		bar.Quality = "valid"
	}
	if bar.Open == "" || bar.High == "" || bar.Low == "" || bar.Close == "" {
		return database.HistoricalBar{}, fmt.Errorf("bar prices are required")
	}
	duration, ok := timeframeDuration(request.Timeframe)
	if !ok {
		return database.HistoricalBar{}, fmt.Errorf("unsupported timeframe")
	}
	closeAt := bar.OpenTime.UTC().Add(duration).Add(-time.Millisecond)
	if bar.AvailableAt.IsZero() {
		bar.AvailableAt = closeAt
	}
	bar.AvailableAt = bar.AvailableAt.UTC()
	if bar.AvailableAt.Before(closeAt) {
		return database.HistoricalBar{}, fmt.Errorf("bar availability precedes close")
	}
	canonical := struct {
		OpenTime, AvailableAt, Open, High, Low, Close, Volume, QuoteVolume string
		TradeCount                                                         int64
		Quality, Source                                                    string
		Flags                                                              []string
		Provenance                                                         map[string]string
	}{canonicalTime(bar.OpenTime), canonicalTime(bar.AvailableAt), bar.Open, bar.High, bar.Low, bar.Close, bar.Volume, bar.QuoteVolume, bar.TradeCount, bar.Quality, request.Source, append([]string(nil), bar.Flags...), bar.Provenance}
	sort.Strings(canonical.Flags)
	payload, _ := json.Marshal(canonical)
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	return database.HistoricalBar{ExchangeSymbolID: request.ExchangeSymbolID, Timeframe: request.Timeframe, OpenTime: bar.OpenTime.UTC(), DatasetVersion: request.DatasetVersion, Role: request.Role, Open: bar.Open, High: bar.High, Low: bar.Low, Close: bar.Close, Volume: bar.Volume, QuoteVolume: bar.QuoteVolume, TradeCount: bar.TradeCount, QualityStatus: bar.Quality, QualityFlagsJSON: EncodeJSON(canonical.Flags), Source: request.Source, ProvenanceJSON: EncodeJSON(bar.Provenance), AvailableAt: bar.AvailableAt, RetrievedAt: retrieved, ContentHash: hash, CreatedAt: retrieved}, nil
}

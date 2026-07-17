package pointintime

import (
	"errors"
	"fmt"
	"time"
)

const (
	ManifestSchemaVersion = "point-in-time-dataset-manifest-v2"
	CoverageSchemaVersion = "point-in-time-coverage-v2"
	BarsSchemaVersion     = "point-in-time-bars-v2"
	BuilderVersion        = "stage04-builder-v2"
	RoleDecision          = "decision"
	RoleExecution         = "execution"
	RoleBenchmark         = "benchmark"
)

var (
	ErrBarConflict      = errors.New("historical bar conflicts with immutable dataset revision")
	ErrManifestNotFound = errors.New("dataset manifest not found")
	ErrFutureData       = errors.New("requested record is effective after as-of timestamp")
	ErrMetadataConflict = errors.New("point-in-time metadata conflicts with immutable version")
)

type SeriesKey struct {
	ExchangeSymbolID string `json:"exchange_symbol_id"`
	AssetID          string `json:"asset_id"`
	Ticker           string `json:"ticker"`
	Role             string `json:"role"`
	Timeframe        string `json:"timeframe"`
}

type SeriesCoverage struct {
	SeriesKey
	SymbolVersion       int      `json:"symbol_version"`
	ListedAt            string   `json:"listed_at"`
	DelistedAt          string   `json:"delisted_at,omitempty"`
	SymbolAvailableAt   string   `json:"symbol_available_at"`
	AssetAvailableAt    string   `json:"asset_available_at"`
	SeriesHash          string   `json:"series_hash"`
	TradabilityHash     string   `json:"tradability_hash,omitempty"`
	TradabilityRows     int      `json:"tradability_rows"`
	ConstraintHash      string   `json:"constraint_hash,omitempty"`
	ConstraintRows      int      `json:"constraint_rows"`
	ConstraintsComplete bool     `json:"constraints_complete"`
	Rows                int      `json:"rows"`
	ExpectedRows        int      `json:"expected_rows"`
	First               string   `json:"first,omitempty"`
	Last                string   `json:"last,omitempty"`
	Gaps                int      `json:"gaps"`
	Duplicates          int      `json:"duplicates"`
	Quality             string   `json:"quality"`
	QualityFlags        []string `json:"quality_flags,omitempty"`
	Complete            bool     `json:"complete"`
}

type Manifest struct {
	SchemaVersion   string            `json:"schema_version"`
	ID              string            `json:"id"`
	DatasetVersion  string            `json:"dataset_version"`
	RequestedStart  string            `json:"requested_start"`
	RequestedEnd    string            `json:"requested_end"`
	EffectiveStart  string            `json:"effective_start"`
	EffectiveEnd    string            `json:"effective_end"`
	KnowledgeCutoff string            `json:"knowledge_cutoff"`
	Symbols         []string          `json:"symbols"`
	Assets          []string          `json:"assets"`
	Series          []SeriesCoverage  `json:"series"`
	Source          string            `json:"source"`
	Provenance      map[string]string `json:"provenance"`
	BuildVersion    string            `json:"build_version"`
	Limitations     []string          `json:"limitations,omitempty"`
	ContentHash     string            `json:"content_hash"`
}

type BuildRequest struct {
	DatasetVersion  string
	RequestedStart  time.Time
	RequestedEnd    time.Time
	KnowledgeCutoff time.Time
	SymbolIDs       []string
	Series          []SeriesKey
	Source          string
	Provenance      map[string]string
	BuildVersion    string
	Limitations     []string
}

type ManifestRequirement struct {
	ManifestID      string
	DatasetVersion  string
	Start           time.Time
	End             time.Time
	Symbols         []string
	Series          []SeriesKey
	Roles           map[string]string // role -> timeframe
	RequireComplete bool
}

type CoverageFailure struct {
	Code    string `json:"code"`
	Series  string `json:"series,omitempty"`
	Details string `json:"details"`
}

type CoverageReport struct {
	SchemaVersion string            `json:"schema_version"`
	ManifestID    string            `json:"manifest_id"`
	Compatible    bool              `json:"compatible"`
	Failures      []CoverageFailure `json:"failures,omitempty"`
	Limitations   []string          `json:"limitations,omitempty"`
	Series        []SeriesCoverage  `json:"series"`
}

type CoverageError struct{ Report CoverageReport }

func (e *CoverageError) Error() string {
	if len(e.Report.Failures) == 0 {
		return "point-in-time dataset coverage failed"
	}
	return fmt.Sprintf("point-in-time dataset coverage failed: %s", e.Report.Failures[0].Code)
}

func IsCoverageError(err error) bool {
	var target *CoverageError
	return errors.As(err, &target)
}

type Bar struct {
	OpenTime    time.Time         `json:"open_time"`
	AvailableAt time.Time         `json:"available_at"`
	Open        string            `json:"open"`
	High        string            `json:"high"`
	Low         string            `json:"low"`
	Close       string            `json:"close"`
	Volume      string            `json:"volume"`
	QuoteVolume string            `json:"quote_volume"`
	TradeCount  int64             `json:"trade_count"`
	Quality     string            `json:"quality"`
	Flags       []string          `json:"flags,omitempty"`
	Provenance  map[string]string `json:"provenance,omitempty"`
}

type Constraint struct {
	ExchangeSymbolID string    `json:"exchange_symbol_id"`
	EffectiveFrom    time.Time `json:"effective_from"`
	AvailableAt      time.Time `json:"available_at"`
	QuantityStep     float64   `json:"quantity_step"`
	PriceTick        float64   `json:"price_tick"`
	MinQuantity      float64   `json:"min_quantity"`
	MinNotional      float64   `json:"min_notional"`
}

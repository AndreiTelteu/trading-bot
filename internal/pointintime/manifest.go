package pointintime

// Dataset intervals are half-open [start,end). A bar belongs to the interval
// when its open timestamp is inside it, but it is usable by a decision only
// when available_at <= decision time. available_at is source/event knowledge;
// retrieved_at is the independent ingestion clock. A manifest knowledge cutoff
// pins which retrieved immutable rows form the dataset without making post-hoc
// historical backfills unusable.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type canonicalManifest struct {
	SchemaVersion, DatasetVersion, RequestedStart, RequestedEnd, EffectiveStart, EffectiveEnd, KnowledgeCutoff string
	Symbols, Assets, Limitations                                                                               []string
	Series                                                                                                     []SeriesCoverage
	Source, BuildVersion                                                                                       string
	Provenance                                                                                                 [][2]string
}

type canonicalBar struct {
	SymbolID, Role, Timeframe, OpenTime, AvailableAt, Open, High, Low, Close, Volume, QuoteVolume, Quality, Source, Provenance, ContentHash string
	TradeCount                                                                                                                              int64
}

type canonicalConstraint struct {
	SymbolID, From, To, AvailableAt, QuantityStep, PriceTick, MinQuantity, MinNotional, Source, Provenance string
}

type canonicalTradability struct {
	SymbolID, From, To, AvailableAt, Status, Source, Provenance string
	SpotTradable                                                bool
}

func BuildManifest(db *gorm.DB, request BuildRequest) (Manifest, error) {
	if db == nil || request.DatasetVersion == "" || request.Source == "" || request.RequestedStart.IsZero() || !request.RequestedEnd.After(request.RequestedStart) {
		return Manifest{}, fmt.Errorf("invalid manifest build request: interval must be half-open [start,end) with end after start")
	}
	if request.BuildVersion == "" {
		request.BuildVersion = BuilderVersion
	}
	request.RequestedStart, request.RequestedEnd = request.RequestedStart.UTC(), request.RequestedEnd.UTC()
	if request.KnowledgeCutoff.IsZero() {
		// A deterministic default is the newest retrieval represented by the
		// requested dataset range, not wall-clock build time.
		var cutoff *time.Time
		if err := db.Raw(`SELECT MAX(retrieved_at) FROM (
			SELECT hb.retrieved_at FROM historical_bars hb WHERE hb.dataset_version=? AND hb.open_time>=? AND hb.open_time<?
			UNION ALL SELECT es.retrieved_at FROM exchange_symbols es JOIN historical_bars hb ON hb.exchange_symbol_id=es.id WHERE hb.dataset_version=? AND hb.open_time>=? AND hb.open_time<?
			UNION ALL SELECT a.retrieved_at FROM assets a JOIN exchange_symbols es ON es.asset_id=a.id JOIN historical_bars hb ON hb.exchange_symbol_id=es.id WHERE hb.dataset_version=? AND hb.open_time>=? AND hb.open_time<?
			UNION ALL SELECT ti.retrieved_at FROM tradability_intervals ti JOIN historical_bars hb ON hb.exchange_symbol_id=ti.exchange_symbol_id WHERE hb.dataset_version=? AND hb.open_time>=? AND hb.open_time<?
			UNION ALL SELECT sc.retrieved_at FROM symbol_constraint_versions sc JOIN historical_bars hb ON hb.exchange_symbol_id=sc.exchange_symbol_id WHERE hb.dataset_version=? AND hb.open_time>=? AND hb.open_time<?
		) knowledge_rows`, request.DatasetVersion, request.RequestedStart, request.RequestedEnd, request.DatasetVersion, request.RequestedStart, request.RequestedEnd, request.DatasetVersion, request.RequestedStart, request.RequestedEnd, request.DatasetVersion, request.RequestedStart, request.RequestedEnd, request.DatasetVersion, request.RequestedStart, request.RequestedEnd).Scan(&cutoff).Error; err != nil {
			return Manifest{}, err
		}
		if cutoff == nil {
			return Manifest{}, fmt.Errorf("cannot derive knowledge cutoff from an empty dataset; provide an explicit cutoff")
		}
		request.KnowledgeCutoff = cutoff.UTC()
	} else {
		request.KnowledgeCutoff = request.KnowledgeCutoff.UTC()
	}

	series := append([]SeriesKey(nil), request.Series...)
	if len(series) == 0 {
		query := db.Table("historical_bars hb").
			Select("DISTINCT hb.exchange_symbol_id, es.asset_id, es.ticker, hb.role, hb.timeframe").
			Joins("JOIN exchange_symbols es ON es.id=hb.exchange_symbol_id").
			Where("hb.dataset_version=? AND hb.open_time>=? AND hb.open_time<? AND hb.retrieved_at<=? AND es.retrieved_at<=?", request.DatasetVersion, request.RequestedStart, request.RequestedEnd, request.KnowledgeCutoff, request.KnowledgeCutoff)
		if len(request.SymbolIDs) > 0 {
			query = query.Where("hb.exchange_symbol_id IN ?", request.SymbolIDs)
		}
		if err := query.Scan(&series).Error; err != nil {
			return Manifest{}, err
		}
	}
	sort.Slice(series, func(i, j int) bool { return seriesID(series[i]) < seriesID(series[j]) })
	coverage := make([]SeriesCoverage, 0, len(series))
	effectiveStart, effectiveEnd := time.Time{}, time.Time{}
	symbolSet, assetSet := map[string]bool{}, map[string]bool{}
	for _, supplied := range series {
		var symbol database.ExchangeSymbol
		if err := db.Where("id=? AND retrieved_at<=?", supplied.ExchangeSymbolID, request.KnowledgeCutoff).First(&symbol).Error; err != nil {
			return Manifest{}, fmt.Errorf("manifest symbol %s is unavailable at knowledge cutoff: %w", supplied.ExchangeSymbolID, err)
		}
		key := supplied
		if key.AssetID == "" {
			key.AssetID = symbol.AssetID
		}
		if key.Ticker == "" {
			key.Ticker = symbol.Ticker
		}
		if key.AssetID != symbol.AssetID || !strings.EqualFold(key.Ticker, symbol.Ticker) {
			return Manifest{}, fmt.Errorf("manifest series identity disagrees with exchange symbol %s", symbol.ID)
		}
		var asset database.Asset
		if err := db.Where("id=? AND retrieved_at<=?", symbol.AssetID, request.KnowledgeCutoff).First(&asset).Error; err != nil {
			return Manifest{}, fmt.Errorf("manifest asset %s is unavailable at knowledge cutoff: %w", symbol.AssetID, err)
		}
		coverageStart, coverageEnd := request.RequestedStart, request.RequestedEnd
		if symbol.ListedAt.After(coverageStart) {
			coverageStart = symbol.ListedAt
		}
		if symbol.DelistedAt != nil && symbol.DelistedAt.Before(coverageEnd) {
			coverageEnd = *symbol.DelistedAt
		}
		var rows []database.HistoricalBar
		if coverageEnd.After(coverageStart) {
			if err := db.Where("dataset_version=? AND exchange_symbol_id=? AND role=? AND timeframe=? AND open_time>=? AND open_time<? AND retrieved_at<=?", request.DatasetVersion, symbol.ID, key.Role, key.Timeframe, coverageStart, coverageEnd, request.KnowledgeCutoff).Order("open_time ASC,id ASC").Find(&rows).Error; err != nil {
				return Manifest{}, err
			}
		}
		d := diagnoseSeries(key, rows, coverageStart, coverageEnd)
		d.SymbolVersion = symbol.Version
		d.ListedAt = canonicalTime(symbol.ListedAt)
		if symbol.DelistedAt != nil && !symbol.DelistedAt.After(request.RequestedEnd) {
			d.DelistedAt = canonicalTime(*symbol.DelistedAt)
		}
		d.SymbolAvailableAt = canonicalTime(symbol.AvailableAt)
		d.AssetAvailableAt = canonicalTime(asset.AvailableAt)
		if symbol.AvailableAt.After(coverageStart) {
			d.QualityFlags = uniqueSorted(append(d.QualityFlags, "metadata_unavailable_at_interval_start"))
			d.Complete = false
			d.Quality = "warning"
		}
		if asset.AvailableAt.After(coverageStart) {
			d.QualityFlags = uniqueSorted(append(d.QualityFlags, "asset_unavailable_at_interval_start"))
			d.Complete = false
			d.Quality = "warning"
		}
		d.SeriesHash = digestBars(rows)
		tradability, err := tradabilityRowsAtCutoff(db, symbol.ID, coverageStart, coverageEnd, request.KnowledgeCutoff)
		if err != nil {
			return Manifest{}, err
		}
		d.TradabilityRows, d.TradabilityHash = len(tradability), digestTradability(tradability)
		constraints, err := constraintRowsAtCutoff(db, symbol.ID, coverageStart, coverageEnd, request.KnowledgeCutoff)
		if err != nil {
			return Manifest{}, err
		}
		d.ConstraintRows = len(constraints)
		d.ConstraintHash = digestConstraints(constraints)
		d.ConstraintsComplete = constraintsCoverRows(constraints, coverageStart, coverageEnd)
		coverage = append(coverage, d)
		symbolSet[key.Ticker], assetSet[key.AssetID] = true, true
		for _, row := range rows {
			if effectiveStart.IsZero() || row.OpenTime.Before(effectiveStart) {
				effectiveStart = row.OpenTime
			}
			barEnd := row.OpenTime
			if duration, ok := timeframeDuration(row.Timeframe); ok {
				barEnd = barEnd.Add(duration)
			}
			if effectiveEnd.IsZero() || barEnd.After(effectiveEnd) {
				effectiveEnd = barEnd
			}
		}
	}
	limitations := uniqueSorted(append([]string(nil), request.Limitations...))
	provenance := make([][2]string, 0, len(request.Provenance))
	for k, v := range request.Provenance {
		provenance = append(provenance, [2]string{k, v})
	}
	sort.Slice(provenance, func(i, j int) bool { return provenance[i][0] < provenance[j][0] })
	canon := canonicalManifest{ManifestSchemaVersion, request.DatasetVersion, canonicalTime(request.RequestedStart), canonicalTime(request.RequestedEnd), canonicalTime(effectiveStart), canonicalTime(effectiveEnd), canonicalTime(request.KnowledgeCutoff), sortedKeys(symbolSet), sortedKeys(assetSet), limitations, coverage, request.Source, request.BuildVersion, provenance}
	payload, err := json.Marshal(canon)
	if err != nil {
		return Manifest{}, err
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, ID: hash, DatasetVersion: request.DatasetVersion, RequestedStart: canon.RequestedStart, RequestedEnd: canon.RequestedEnd, EffectiveStart: canon.EffectiveStart, EffectiveEnd: canon.EffectiveEnd, KnowledgeCutoff: canon.KnowledgeCutoff, Symbols: canon.Symbols, Assets: canon.Assets, Series: coverage, Source: request.Source, Provenance: request.Provenance, BuildVersion: request.BuildVersion, Limitations: limitations, ContentHash: hash}
	row, err := manifestRow(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func diagnoseSeries(key SeriesKey, rows []database.HistoricalBar, start, end time.Time) SeriesCoverage {
	d := SeriesCoverage{SeriesKey: key, Rows: len(rows), Quality: "valid", Complete: true}
	interval, ok := timeframeDuration(key.Timeframe)
	if ok && end.After(start) {
		d.ExpectedRows = int(end.Sub(start) / interval)
	}
	if len(rows) == 0 {
		d.Quality, d.Complete, d.QualityFlags = "missing", false, []string{"missing_series"}
		return d
	}
	d.First, d.Last = canonicalTime(rows[0].OpenTime), canonicalTime(rows[len(rows)-1].OpenTime)
	seen := map[time.Time]bool{}
	previous := time.Time{}
	for _, row := range rows {
		at := row.OpenTime.UTC()
		if seen[at] {
			d.Duplicates++
		}
		seen[at] = true
		if ok && !previous.IsZero() {
			if missing := int(at.Sub(previous)/interval) - 1; missing > 0 {
				d.Gaps += missing
			}
		}
		if row.QualityStatus != "valid" {
			d.Quality = "warning"
			d.QualityFlags = append(d.QualityFlags, "bar_quality_"+row.QualityStatus)
		}
		closeAt := row.OpenTime.Add(interval).Add(-time.Millisecond)
		if ok && row.AvailableAt.Before(closeAt) {
			d.QualityFlags = append(d.QualityFlags, "availability_precedes_close")
		}
		if ok && row.AvailableAt.After(closeAt) {
			d.QualityFlags = append(d.QualityFlags, "delayed_bar_availability")
		}
		previous = at
	}
	lastCovered := rows[len(rows)-1].OpenTime
	if ok {
		lastCovered = lastCovered.Add(interval)
	}
	if rows[0].OpenTime.After(start) || lastCovered.Before(end) {
		d.QualityFlags = append(d.QualityFlags, "requested_bounds_not_covered")
	}
	if d.Gaps > 0 {
		d.QualityFlags = append(d.QualityFlags, "internal_gaps")
	}
	if d.Duplicates > 0 {
		d.QualityFlags = append(d.QualityFlags, "duplicates")
	}
	if d.ExpectedRows > 0 && d.Rows != d.ExpectedRows {
		d.QualityFlags = append(d.QualityFlags, "row_count_mismatch")
	}
	d.QualityFlags = uniqueSorted(d.QualityFlags)
	d.Complete = len(d.QualityFlags) == 0
	if !d.Complete && d.Quality == "valid" {
		d.Quality = "warning"
	}
	return d
}

func LoadManifest(db *gorm.DB, id string) (Manifest, error) {
	var row database.DatasetManifest
	if err := db.First(&row, "id=?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return Manifest{}, ErrManifestNotFound
		}
		return Manifest{}, err
	}
	if row.SchemaVersion != ManifestSchemaVersion {
		return Manifest{}, fmt.Errorf("unsupported dataset manifest schema %q", row.SchemaVersion)
	}
	var symbols, assets, limitations []string
	var series []SeriesCoverage
	provenance := map[string]string{}
	for value, target := range map[string]any{row.SymbolsJSON: &symbols, row.AssetsJSON: &assets, row.CoverageJSON: &series, row.LimitationsJSON: &limitations, row.ProvenanceJSON: &provenance} {
		if err := json.Unmarshal([]byte(value), target); err != nil {
			return Manifest{}, err
		}
	}
	return Manifest{SchemaVersion: row.SchemaVersion, ID: row.ID, DatasetVersion: row.DatasetVersion, RequestedStart: canonicalTime(row.RequestedStart), RequestedEnd: canonicalTime(row.RequestedEnd), EffectiveStart: canonicalTime(row.EffectiveStart), EffectiveEnd: canonicalTime(row.EffectiveEnd), KnowledgeCutoff: canonicalTime(row.KnowledgeCutoff), Symbols: symbols, Assets: assets, Series: series, Source: row.Source, Provenance: provenance, BuildVersion: row.BuildVersion, Limitations: limitations, ContentHash: row.ContentHash}, nil
}

func ValidateManifest(db *gorm.DB, requirement ManifestRequirement) (Manifest, CoverageReport, error) {
	manifest, err := LoadManifest(db, requirement.ManifestID)
	report := CoverageReport{SchemaVersion: CoverageSchemaVersion, ManifestID: requirement.ManifestID, Compatible: true, Series: []SeriesCoverage{}}
	if err != nil {
		report.Compatible = false
		report.Failures = append(report.Failures, CoverageFailure{Code: "manifest_not_found", Details: err.Error()})
		return Manifest{}, report, &CoverageError{report}
	}
	report.Limitations, report.Series = manifest.Limitations, manifest.Series
	add := func(code, series, details string) {
		report.Compatible = false
		report.Failures = append(report.Failures, CoverageFailure{code, series, details})
	}
	if len(manifest.Series) == 0 {
		add("manifest_empty", "", "manifest contains no covered series")
	}
	if requirement.DatasetVersion != "" && manifest.DatasetVersion != requirement.DatasetVersion {
		add("dataset_version_mismatch", "", manifest.DatasetVersion)
	}
	rs, _ := time.Parse(time.RFC3339Nano, manifest.RequestedStart)
	re, _ := time.Parse(time.RFC3339Nano, manifest.RequestedEnd)
	if !requirement.Start.IsZero() && rs.After(requirement.Start) {
		add("interval_start_not_covered", "", manifest.RequestedStart)
	}
	if !requirement.End.IsZero() && re.Before(requirement.End) {
		add("interval_end_not_covered", "", manifest.RequestedEnd)
	}
	for _, wanted := range requirement.Series {
		found := false
		for _, got := range manifest.Series {
			if sameSeries(got.SeriesKey, wanted) {
				found = true
				if requirement.RequireComplete && !got.Complete {
					add("series_incomplete", seriesID(wanted), strings.Join(got.QualityFlags, ","))
				}
				break
			}
		}
		if !found {
			add("exact_series_missing", seriesID(wanted), "exchange symbol, role, and timeframe series absent")
		}
	}
	for _, symbol := range requirement.Symbols {
		found := false
		for _, got := range manifest.Symbols {
			if strings.EqualFold(got, symbol) {
				found = true
			}
		}
		if !found {
			add("symbol_missing", symbol, "symbol absent from manifest")
		}
	}
	for role, frame := range requirement.Roles {
		found := false
		for _, s := range manifest.Series {
			if s.Role == role && s.Timeframe == frame {
				found = true
				if requirement.RequireComplete && !s.Complete {
					add("series_incomplete", seriesID(s.SeriesKey), strings.Join(s.QualityFlags, ","))
				}
			}
		}
		if !found {
			add("role_timeframe_missing", role+":"+frame, "required series absent")
		}
	}
	if err := VerifyManifestContent(db, manifest); err != nil {
		add("manifest_content_mismatch", "", err.Error())
	}
	sort.Slice(report.Failures, func(i, j int) bool {
		if report.Failures[i].Code != report.Failures[j].Code {
			return report.Failures[i].Code < report.Failures[j].Code
		}
		return report.Failures[i].Series < report.Failures[j].Series
	})
	if !report.Compatible {
		return manifest, report, &CoverageError{report}
	}
	return manifest, report, nil
}

func VerifyManifestContent(db *gorm.DB, manifest Manifest) error {
	cutoff, err := time.Parse(time.RFC3339Nano, manifest.KnowledgeCutoff)
	if err != nil {
		return fmt.Errorf("invalid knowledge cutoff")
	}
	for _, series := range manifest.Series {
		start := mustTime(manifest.RequestedStart)
		end := mustTime(manifest.RequestedEnd)
		if listed := mustTime(series.ListedAt); listed.After(start) {
			start = listed
		}
		if series.DelistedAt != "" {
			if delisted := mustTime(series.DelistedAt); delisted.Before(end) {
				end = delisted
			}
		}
		var rows []database.HistoricalBar
		if end.After(start) {
			if err := db.Where("dataset_version=? AND exchange_symbol_id=? AND role=? AND timeframe=? AND open_time>=? AND open_time<? AND retrieved_at<=?", manifest.DatasetVersion, series.ExchangeSymbolID, series.Role, series.Timeframe, start, end, cutoff).Order("open_time ASC,id ASC").Find(&rows).Error; err != nil {
				return err
			}
		}
		if len(rows) != series.Rows || digestBars(rows) != series.SeriesHash {
			return fmt.Errorf("series %s immutable row count/digest differs", seriesID(series.SeriesKey))
		}
		constraints, e := constraintRowsAtCutoff(db, series.ExchangeSymbolID, start, end, cutoff)
		if e != nil {
			return e
		}
		if len(constraints) != series.ConstraintRows || digestConstraints(constraints) != series.ConstraintHash {
			return fmt.Errorf("constraints %s immutable row count/digest differs", series.ExchangeSymbolID)
		}
		tradability, e := tradabilityRowsAtCutoff(db, series.ExchangeSymbolID, start, end, cutoff)
		if e != nil {
			return e
		}
		if len(tradability) != series.TradabilityRows || digestTradability(tradability) != series.TradabilityHash {
			return fmt.Errorf("tradability %s immutable row count/digest differs", series.ExchangeSymbolID)
		}
	}
	return nil
}

func manifestRow(m Manifest) (database.DatasetManifest, error) {
	parse := func(v string) (time.Time, error) {
		if v == "" {
			return time.Time{}, nil
		}
		return time.Parse(time.RFC3339Nano, v)
	}
	rs, e := parse(m.RequestedStart)
	if e != nil {
		return database.DatasetManifest{}, e
	}
	re, e := parse(m.RequestedEnd)
	if e != nil {
		return database.DatasetManifest{}, e
	}
	es, _ := parse(m.EffectiveStart)
	ee, _ := parse(m.EffectiveEnd)
	kc, e := parse(m.KnowledgeCutoff)
	if e != nil {
		return database.DatasetManifest{}, e
	}
	marshal := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	keys := make([]SeriesKey, len(m.Series))
	for i := range m.Series {
		keys[i] = m.Series[i].SeriesKey
	}
	return database.DatasetManifest{ID: m.ID, SchemaVersion: m.SchemaVersion, DatasetVersion: m.DatasetVersion, RequestedStart: rs, RequestedEnd: re, EffectiveStart: es, EffectiveEnd: ee, KnowledgeCutoff: kc, Source: m.Source, ProvenanceJSON: marshal(m.Provenance), BuildVersion: m.BuildVersion, ContentHash: m.ContentHash, SymbolsJSON: marshal(m.Symbols), AssetsJSON: marshal(m.Assets), RolesTimeframesJSON: marshal(keys), CoverageJSON: marshal(m.Series), LimitationsJSON: marshal(m.Limitations)}, nil
}

func digestBars(rows []database.HistoricalBar) string {
	values := make([]canonicalBar, len(rows))
	for i, r := range rows {
		values[i] = canonicalBar{r.ExchangeSymbolID, r.Role, r.Timeframe, canonicalTime(r.OpenTime), canonicalTime(r.AvailableAt), r.Open, r.High, r.Low, r.Close, r.Volume, r.QuoteVolume, r.QualityStatus, r.Source, r.ProvenanceJSON, r.ContentHash, r.TradeCount}
	}
	return digest(values)
}
func digestConstraints(rows []database.SymbolConstraintVersion) string {
	values := make([]canonicalConstraint, len(rows))
	for i, r := range rows {
		to := ""
		if r.EffectiveTo != nil {
			to = canonicalTime(*r.EffectiveTo)
		}
		values[i] = canonicalConstraint{r.ExchangeSymbolID, canonicalTime(r.EffectiveFrom), to, canonicalTime(r.AvailableAt), r.QuantityStep, r.PriceTick, r.MinQuantity, r.MinNotional, r.Source, r.ProvenanceJSON}
	}
	return digest(values)
}
func digestTradability(rows []database.TradabilityInterval) string {
	values := make([]canonicalTradability, len(rows))
	for i, r := range rows {
		to := ""
		if r.EffectiveTo != nil {
			to = canonicalTime(*r.EffectiveTo)
		}
		values[i] = canonicalTradability{r.ExchangeSymbolID, canonicalTime(r.EffectiveFrom), to, canonicalTime(r.AvailableAt), r.Status, r.Source, r.ProvenanceJSON, r.SpotTradable}
	}
	return digest(values)
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
func constraintRowsAtCutoff(db *gorm.DB, id string, start, end, cutoff time.Time) ([]database.SymbolConstraintVersion, error) {
	var rows []database.SymbolConstraintVersion
	err := db.Where("exchange_symbol_id=? AND effective_from<? AND (effective_to IS NULL OR effective_to>?) AND retrieved_at<=?", id, end, start, cutoff).Order("effective_from ASC,id ASC").Find(&rows).Error
	return rows, err
}
func tradabilityRowsAtCutoff(db *gorm.DB, id string, start, end, cutoff time.Time) ([]database.TradabilityInterval, error) {
	var rows []database.TradabilityInterval
	err := db.Where("exchange_symbol_id=? AND effective_from<? AND (effective_to IS NULL OR effective_to>?) AND retrieved_at<=?", id, end, start, cutoff).Order("effective_from ASC,id ASC").Find(&rows).Error
	return rows, err
}
func constraintsCoverRows(rows []database.SymbolConstraintVersion, start, end time.Time) bool {
	if !end.After(start) {
		return true
	}
	cursor := start
	for _, r := range rows {
		usable := r.EffectiveFrom
		if r.AvailableAt.After(usable) {
			usable = r.AvailableAt
		}
		if usable.After(cursor) {
			return false
		}
		if r.EffectiveTo == nil {
			return true
		}
		if r.EffectiveTo.After(cursor) {
			cursor = *r.EffectiveTo
		}
		if !cursor.Before(end) {
			return true
		}
	}
	return false
}
func sameSeries(a, b SeriesKey) bool {
	return a.ExchangeSymbolID == b.ExchangeSymbolID && a.Role == b.Role && a.Timeframe == b.Timeframe
}
func canonicalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func seriesID(k SeriesKey) string { return k.ExchangeSymbolID + ":" + k.Role + ":" + k.Timeframe }
func sortedKeys(m map[string]bool) []string {
	r := make([]string, 0, len(m))
	for k := range m {
		r = append(r, k)
	}
	sort.Strings(r)
	return r
}
func uniqueSorted(v []string) []string {
	m := map[string]bool{}
	for _, x := range v {
		m[x] = true
	}
	return sortedKeys(m)
}
func timeframeDuration(frame string) (time.Duration, bool) {
	d, err := time.ParseDuration(frame)
	if err == nil {
		return d, true
	}
	if frame == "1d" {
		return 24 * time.Hour, true
	}
	return 0, false
}

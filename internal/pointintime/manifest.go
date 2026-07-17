package pointintime

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
	SchemaVersion, DatasetVersion, RequestedStart, RequestedEnd, EffectiveStart, EffectiveEnd string
	Symbols, Assets, Limitations                                                              []string
	Series                                                                                    []SeriesCoverage
	Source, BuildVersion                                                                      string
	Provenance                                                                                [][2]string
	Bars                                                                                      []canonicalBar
}

type canonicalBar struct {
	SymbolID, Role, Timeframe, OpenTime, Open, High, Low, Close, Volume, QuoteVolume, Quality, Source, Provenance, ContentHash string
	TradeCount                                                                                                                 int64
}

func BuildManifest(db *gorm.DB, request BuildRequest) (Manifest, error) {
	if db == nil || request.DatasetVersion == "" || request.Source == "" || request.RequestedStart.IsZero() || request.RequestedEnd.Before(request.RequestedStart) {
		return Manifest{}, fmt.Errorf("invalid manifest build request")
	}
	if request.BuildVersion == "" {
		request.BuildVersion = BuilderVersion
	}
	request.RequestedStart, request.RequestedEnd = request.RequestedStart.UTC(), request.RequestedEnd.UTC()
	series := append([]SeriesKey(nil), request.Series...)
	if len(series) == 0 {
		query := db.Table("historical_bars hb").
			Select("DISTINCT hb.exchange_symbol_id, es.asset_id, es.ticker, hb.role, hb.timeframe").
			Joins("JOIN exchange_symbols es ON es.id=hb.exchange_symbol_id").
			Where("hb.dataset_version=? AND hb.open_time>=? AND hb.open_time<=?", request.DatasetVersion, request.RequestedStart, request.RequestedEnd)
		if len(request.SymbolIDs) > 0 {
			query = query.Where("hb.exchange_symbol_id IN ?", request.SymbolIDs)
		}
		if err := query.Scan(&series).Error; err != nil {
			return Manifest{}, err
		}
	}
	sort.Slice(series, func(i, j int) bool { return seriesID(series[i]) < seriesID(series[j]) })
	coverage := make([]SeriesCoverage, 0, len(series))
	allBars := []canonicalBar{}
	effectiveStart, effectiveEnd := time.Time{}, time.Time{}
	symbolSet, assetSet := map[string]bool{}, map[string]bool{}
	for _, key := range series {
		var symbolMeta database.ExchangeSymbol
		if err := db.First(&symbolMeta, "id=?", key.ExchangeSymbolID).Error; err != nil {
			return Manifest{}, err
		}
		coverageStart, coverageEnd := request.RequestedStart, request.RequestedEnd
		if symbolMeta.ListedAt.After(coverageStart) {
			coverageStart = symbolMeta.ListedAt
		}
		if symbolMeta.DelistedAt != nil && !symbolMeta.DelistedAt.After(coverageEnd) {
			if duration, ok := timeframeDuration(key.Timeframe); ok {
				coverageEnd = symbolMeta.DelistedAt.Add(-duration)
			} else {
				coverageEnd = symbolMeta.DelistedAt.Add(-time.Nanosecond)
			}
		}
		var rows []database.HistoricalBar
		if err := db.Where("dataset_version=? AND exchange_symbol_id=? AND role=? AND timeframe=? AND open_time>=? AND open_time<=?", request.DatasetVersion, key.ExchangeSymbolID, key.Role, key.Timeframe, coverageStart, coverageEnd).Order("open_time ASC").Find(&rows).Error; err != nil {
			return Manifest{}, err
		}
		if key.AssetID == "" || key.Ticker == "" {
			key.AssetID, key.Ticker = symbolMeta.AssetID, symbolMeta.Ticker
		}
		d := diagnoseSeries(key, rows, coverageStart, coverageEnd)
		coverage = append(coverage, d)
		symbolSet[key.Ticker], assetSet[key.AssetID] = true, true
		for _, row := range rows {
			allBars = append(allBars, canonicalBar{row.ExchangeSymbolID, row.Role, row.Timeframe, canonicalTime(row.OpenTime), row.Open, row.High, row.Low, row.Close, row.Volume, row.QuoteVolume, row.QualityStatus, row.Source, row.ProvenanceJSON, row.ContentHash, row.TradeCount})
			if effectiveStart.IsZero() || row.OpenTime.Before(effectiveStart) {
				effectiveStart = row.OpenTime
			}
			if effectiveEnd.IsZero() || row.OpenTime.After(effectiveEnd) {
				effectiveEnd = row.OpenTime
			}
		}
	}
	symbols, assets := sortedKeys(symbolSet), sortedKeys(assetSet)
	limitations := append([]string(nil), request.Limitations...)
	sort.Strings(limitations)
	provenance := make([][2]string, 0, len(request.Provenance))
	for k, v := range request.Provenance {
		provenance = append(provenance, [2]string{k, v})
	}
	sort.Slice(provenance, func(i, j int) bool { return provenance[i][0] < provenance[j][0] })
	canon := canonicalManifest{ManifestSchemaVersion, request.DatasetVersion, canonicalTime(request.RequestedStart), canonicalTime(request.RequestedEnd), canonicalTime(effectiveStart), canonicalTime(effectiveEnd), symbols, assets, limitations, coverage, request.Source, request.BuildVersion, provenance, allBars}
	payload, err := json.Marshal(canon)
	if err != nil {
		return Manifest{}, err
	}
	sum := sha256.Sum256(payload)
	hash := hex.EncodeToString(sum[:])
	manifest := Manifest{ManifestSchemaVersion, hash, request.DatasetVersion, canon.RequestedStart, canon.RequestedEnd, canon.EffectiveStart, canon.EffectiveEnd, symbols, assets, coverage, request.Source, request.Provenance, request.BuildVersion, limitations, hash}
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
	if ok {
		d.ExpectedRows = int(end.Sub(start)/interval) + 1
	}
	if len(rows) == 0 {
		d.Quality = "missing"
		d.Complete = false
		d.QualityFlags = []string{"missing_series"}
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
			missing := int(at.Sub(previous)/interval) - 1
			if missing > 0 {
				d.Gaps += missing
			}
		}
		if row.QualityStatus != "valid" {
			d.Quality = "warning"
			d.QualityFlags = append(d.QualityFlags, "bar_quality_"+row.QualityStatus)
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
	if d.ExpectedRows > 0 && d.Rows < d.ExpectedRows {
		d.QualityFlags = append(d.QualityFlags, "row_count_below_expected")
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
	return Manifest{row.SchemaVersion, row.ID, row.DatasetVersion, canonicalTime(row.RequestedStart), canonicalTime(row.RequestedEnd), canonicalTime(row.EffectiveStart), canonicalTime(row.EffectiveEnd), symbols, assets, series, row.Source, provenance, row.BuildVersion, limitations, row.ContentHash}, nil
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
	wanted := map[string]bool{}
	for _, s := range requirement.Symbols {
		wanted[strings.ToUpper(s)] = true
	}
	for symbol := range wanted {
		found := false
		for _, s := range manifest.Symbols {
			if strings.EqualFold(s, symbol) {
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
	marshal := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	keys := make([]SeriesKey, len(m.Series))
	for idx := range m.Series {
		keys[idx] = m.Series[idx].SeriesKey
	}
	return database.DatasetManifest{ID: m.ID, SchemaVersion: m.SchemaVersion, DatasetVersion: m.DatasetVersion, RequestedStart: rs, RequestedEnd: re, EffectiveStart: es, EffectiveEnd: ee, Source: m.Source, ProvenanceJSON: marshal(m.Provenance), BuildVersion: m.BuildVersion, ContentHash: m.ContentHash, SymbolsJSON: marshal(m.Symbols), AssetsJSON: marshal(m.Assets), RolesTimeframesJSON: marshal(keys), CoverageJSON: marshal(m.Series), LimitationsJSON: marshal(m.Limitations)}, nil
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

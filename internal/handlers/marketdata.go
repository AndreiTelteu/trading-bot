package handlers

import (
	"strconv"
	"strings"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/pointintime"

	"github.com/gofiber/fiber/v2"
)

func GetDatasetManifest(c *fiber.Ctx) error {
	manifest, err := pointintime.LoadManifest(database.DB, c.Params("id"))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	if raw := c.Query("as_of"); raw != "" {
		asOf, e := time.Parse(time.RFC3339, raw)
		if e != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid as_of"})
		}
		effective, e := time.Parse(time.RFC3339Nano, manifest.EffectiveEnd)
		if e == nil && effective.After(asOf) {
			return c.Status(404).JSON(fiber.Map{"error": "manifest contains records effective after requested as_of"})
		}
	}
	return c.JSON(manifest)
}

func ListHistoricalBars(c *fiber.Ctx) error {
	start, e := time.Parse(time.RFC3339, c.Query("start"))
	if e != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid start"})
	}
	end, e := time.Parse(time.RFC3339, c.Query("end"))
	if e != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid end"})
	}
	asOf := end
	if raw := c.Query("as_of"); raw != "" {
		asOf, e = time.Parse(time.RFC3339, raw)
		if e != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid as_of"})
		}
	}
	bars, e := (pointintime.Repository{DB: database.DB}).Bars(c.Query("manifest_id"), c.Query("symbol_id"), c.Query("role"), c.Query("timeframe"), start, end, asOf)
	if e != nil {
		return c.Status(422).JSON(fiber.Map{"error": e.Error()})
	}
	limit := 1000
	if raw := c.Query("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed < limit {
			limit = parsed
		}
	}
	if len(bars) > limit {
		bars = bars[:limit]
	}
	return c.JSON(fiber.Map{"schema_version": "point-in-time-bars-v1", "count": len(bars), "bars": bars})
}

// InspectDatasetCoverage returns compact, schema-versioned manifest diagnostics.
// GET /api/market-data/coverage?manifest_id=...&start=...&end=...&symbols=A,B&roles=decision:15m,execution:1m
func InspectDatasetCoverage(c *fiber.Ctx) error {
	start, err := parseOptionalRFC3339(c.Query("start"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid start"})
	}
	end, err := parseOptionalRFC3339(c.Query("end"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid end"})
	}
	requirement := pointintime.ManifestRequirement{ManifestID: strings.TrimSpace(c.Query("manifest_id")), DatasetVersion: strings.TrimSpace(c.Query("dataset_version")), Start: start, End: end, Symbols: splitNonEmpty(c.Query("symbols")), Roles: parseRoles(c.Query("roles")), RequireComplete: c.Query("require_complete", "true") != "false"}
	if raw := c.Query("as_of"); raw != "" {
		asOf, e := time.Parse(time.RFC3339, raw)
		if e != nil {
			return c.Status(400).JSON(fiber.Map{"error": "invalid as_of"})
		}
		manifest, e := pointintime.LoadManifest(database.DB, requirement.ManifestID)
		if e == nil {
			effective, _ := time.Parse(time.RFC3339Nano, manifest.EffectiveEnd)
			if effective.After(asOf) {
				return c.Status(fiber.StatusUnprocessableEntity).JSON(pointintime.CoverageReport{SchemaVersion: pointintime.CoverageSchemaVersion, ManifestID: requirement.ManifestID, Compatible: false, Failures: []pointintime.CoverageFailure{{Code: "as_of_precedes_manifest_effective_end", Details: manifest.EffectiveEnd}}})
			}
		}
	}
	_, report, validateErr := pointintime.ValidateManifest(database.DB, requirement)
	if validateErr != nil {
		return c.Status(fiber.StatusUnprocessableEntity).JSON(report)
	}
	return c.JSON(report)
}

func parseOptionalRFC3339(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, value)
}
func splitNonEmpty(value string) []string {
	out := []string{}
	for _, v := range strings.Split(value, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}
func parseRoles(value string) map[string]string {
	out := map[string]string{}
	for _, v := range splitNonEmpty(value) {
		parts := strings.SplitN(v, ":", 2)
		if len(parts) == 2 {
			out[parts[0]] = parts[1]
		}
	}
	return out
}

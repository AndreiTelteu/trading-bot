package operations

import (
	"context"
	"fmt"
	"strings"
	"time"
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	"trading-go/internal/ledger"
)

func Monitor(ctx context.Context) error {
	flags, ok := cutover.Active()
	if !ok || database.DB == nil {
		return fmt.Errorf("Stage 08 operations unavailable")
	}
	s := New(database.DB, flags)
	report, err := ledger.New(database.DB).Reconcile(ctx, ledger.DefaultAccountID, time.Time{})
	if err != nil || !report.Balanced {
		details := map[string]any{}
		if err != nil {
			details["error"] = err.Error()
		} else {
			details["actionable_issues"] = report.ActionableIssues
		}
		_, raiseErr := s.RaiseIncident(ctx, IncidentInput{DedupeKey: "ledger:primary", Type: "reconciliation_break", Severity: "critical", Summary: "primary ledger reconciliation is not balanced", Details: details})
		return raiseErr
	}
	var manifest database.DatasetManifest
	if err := database.DB.Order("created_at desc").First(&manifest).Error; err != nil {
		_, _ = s.RaiseIncident(ctx, IncidentInput{DedupeKey: "market-data:manifest", Type: "missing_benchmark_or_universe", Severity: "warning", Summary: "no authoritative Stage 04 dataset manifest is available", Details: map[string]any{"error": err.Error()}})
		return nil
	}
	var total, incomplete int64
	database.DB.Model(&database.UniverseSnapshot{}).Where("dataset_manifest_id=?", manifest.ID).Count(&total)
	database.DB.Model(&database.UniverseSnapshot{}).Where("dataset_manifest_id=? AND coverage_state <> ?", manifest.ID, "complete").Count(&incomplete)
	if total == 0 || incomplete > 0 {
		_, _ = s.RaiseIncident(ctx, IncidentInput{DedupeKey: "market-data:universe:" + manifest.ID, Type: "missing_benchmark_or_universe", Severity: "critical", Summary: "point-in-time universe coverage is incomplete", Details: map[string]any{"dataset_manifest_id": manifest.ID, "incomplete_snapshots": incomplete}})
	}
	return nil
}

func RecordGovernanceBypass(err error) {
	if err == nil {
		return
	}
	flags, ok := cutover.Active()
	if !ok || database.DB == nil {
		return
	}
	_, _ = New(database.DB, flags).RaiseIncident(context.Background(), IncidentInput{DedupeKey: "governance:bypass", Type: "governance_bypass", Severity: "critical", Summary: "authority selection was rejected by governance", Details: map[string]any{"error": err.Error()}})
}
func RecordBrokerConflict(key string, err error) {
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "idempot") {
		return
	}
	flags, ok := cutover.Active()
	if !ok || database.DB == nil {
		return
	}
	_, _ = New(database.DB, flags).RaiseIncident(context.Background(), IncidentInput{DedupeKey: "broker-idempotency:" + key, Type: "repeated_broker_idempotency_conflict", Severity: "critical", Summary: "broker idempotency conflict repeated", Details: map[string]any{"error": err.Error()}})
}

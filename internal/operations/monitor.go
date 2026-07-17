package operations

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	var state database.CutoverState
	if err := database.DB.First(&state, 1).Error; err != nil {
		return err
	}
	if err := s.persistReconciliation(ctx, state, state.FlagSnapshotID, report); err != nil {
		return err
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
func RecordMissingMarketData(boundary, contextID string, err error) {
	if err == nil || database.DB == nil {
		return
	}
	flags, ok := cutover.Active()
	if !ok {
		return
	}
	if len(contextID) > 120 {
		contextID = contextID[:120]
	}
	_, _ = New(database.DB, flags).RaiseIncident(context.Background(), IncidentInput{DedupeKey: "market-data:" + boundary + ":" + contextID, Type: "missing_benchmark_or_universe", Severity: "critical", Summary: "authoritative benchmark or universe evidence is unavailable at " + boundary, Details: map[string]any{"context_id": contextID, "error": err.Error()}})
}
func RecordBrokerConflict(key string, err error) {
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "idempot") {
		return
	}
	flags, ok := cutover.Active()
	if !ok || database.DB == nil {
		return
	}
	dedupe := "broker-idempotency:" + key
	now := time.Now().UTC()
	count := int64(0)
	if txErr := database.DB.Transaction(func(tx *gorm.DB) error {
		var row database.BrokerConflictCounter
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "dedupe_key=?", dedupe)
		if q.Error == gorm.ErrRecordNotFound {
			row = database.BrokerConflictCounter{DedupeKey: dedupe, Count: 1, WindowStart: now, LastSeenAt: now}
			count = 1
			return tx.Create(&row).Error
		}
		if q.Error != nil {
			return q.Error
		}
		if now.Sub(row.WindowStart) > 15*time.Minute {
			row.Count = 0
			row.WindowStart = now
		}
		row.Count++
		row.LastSeenAt = now
		count = row.Count
		return tx.Save(&row).Error
	}); txErr != nil {
		return
	}
	if count < 3 {
		return
	}
	_, _ = New(database.DB, flags).RaiseIncident(context.Background(), IncidentInput{DedupeKey: dedupe, Type: "repeated_broker_idempotency_conflict", Severity: "critical", Summary: "broker idempotency conflict repeated", Details: map[string]any{"error": err.Error(), "window_count": count}})
}

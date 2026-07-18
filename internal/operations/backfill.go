package operations

import (
	"context"
	"fmt"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/ledger"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const BackfillPlanSchemaVersion = "stage08-backfill-plan-v1"

func (s Service) PlanBackfill(ctx context.Context, account string) (database.BackfillPlan, error) {
	report, err := ledger.New(s.DB).Backfill(ctx, ledger.BackfillOptions{AccountID: account})
	if err != nil {
		return database.BackfillPlan{}, err
	}
	digest, payload, err := hash(report)
	if err != nil {
		return database.BackfillPlan{}, err
	}
	id, _, _ := hash(struct{ Schema, Account, Digest string }{BackfillPlanSchemaVersion, account, digest})
	row := database.BackfillPlan{ID: id, AccountID: account, SchemaVersion: BackfillPlanSchemaVersion, ReportJSON: string(payload), ReportDigest: digest, Status: "planned", CreatedAt: s.now()}
	err = s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	return row, err
}

// ApproveBackfill binds a trusted principal to the server-derived persisted
// plan digest. No caller-supplied report or economic amount is accepted.
func (s Service) ApproveBackfill(ctx context.Context, planID, observedDigest, principal string) (database.BackfillPlan, error) {
	if principal == "" {
		return database.BackfillPlan{}, fmt.Errorf("trusted human principal required")
	}
	var plan database.BackfillPlan
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&plan, "id=?", planID).Error; err != nil {
			return err
		}
		if plan.Status != "planned" {
			return fmt.Errorf("backfill plan is not awaiting approval")
		}
		if observedDigest == "" || observedDigest != plan.ReportDigest {
			return fmt.Errorf("stale or wrong backfill report digest")
		}
		// Recompute under current source state; approval cannot bless a stale dry run.
		current, err := ledger.New(tx).Backfill(ctx, ledger.BackfillOptions{AccountID: plan.AccountID})
		if err != nil {
			return err
		}
		currentDigest, _, _ := hash(current)
		if currentDigest != plan.ReportDigest {
			return fmt.Errorf("backfill source changed since plan")
		}
		now := s.now()
		approval, _, _ := hash(struct {
			Plan, Digest, Principal string
			At                      time.Time
		}{plan.ID, plan.ReportDigest, principal, now})
		plan.Status = "approved"
		plan.ApprovedAt = &now
		plan.ApprovedBy = &principal
		plan.ApprovalDigest = &approval
		return tx.Save(&plan).Error
	})
	return plan, err
}

func (s Service) ApplyBackfill(ctx context.Context, planID, approvalDigest string) (database.BackfillPlan, error) {
	var plan database.BackfillPlan
	if s.LedgerDB == nil {
		return plan, fmt.Errorf("isolated ledger writer is unavailable")
	}
	err := s.LedgerDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The ledger principal can read the approved immutable envelope, but it
		// cannot update operational tables directly. The final status transition
		// is performed by the narrowly validated SECURITY DEFINER function below.
		if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
			return fmt.Errorf("assume ledger writer role for approved backfill: %w", err)
		}
		if err := tx.First(&plan, "id=?", planID).Error; err != nil {
			return err
		}
		if plan.Status == "applied" {
			if plan.ApprovalDigest != nil && *plan.ApprovalDigest == approvalDigest {
				return nil
			}
			return fmt.Errorf("backfill idempotency payload conflict")
		}
		if plan.Status != "approved" || plan.ApprovedBy == nil || plan.ApprovalDigest == nil || *plan.ApprovalDigest != approvalDigest {
			return fmt.Errorf("valid approved backfill plan required")
		}
		current, err := ledger.New(tx).Backfill(ctx, ledger.BackfillOptions{AccountID: plan.AccountID})
		if err != nil {
			return err
		}
		currentDigest, _, _ := hash(current)
		if currentDigest != plan.ReportDigest {
			return fmt.Errorf("approved backfill plan became stale")
		}
		if _, err := ledger.New(tx).Backfill(ctx, ledger.BackfillOptions{
			Apply: true, Approval: ledger.BackfillApproval, ApprovedBy: *plan.ApprovedBy, AccountID: plan.AccountID,
			PlanID: plan.ID, ReportDigest: plan.ReportDigest, ApprovalDigest: approvalDigest,
		}); err != nil {
			return err
		}
		now := s.now()
		if err := tx.Exec("SELECT finalize_applied_backfill_plan(?,?,?)", plan.ID, approvalDigest, now).Error; err != nil {
			return err
		}
		return tx.First(&plan, "id=?", planID).Error
	})
	return plan, err
}

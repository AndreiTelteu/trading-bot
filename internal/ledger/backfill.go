package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"trading-go/internal/accounting"
	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const BackfillApproval = "APPROVE_LEDGER_OPENING_BALANCE"

type BackfillOptions struct {
	Apply      bool   `json:"apply"`
	Approval   string `json:"approval"`
	ApprovedBy string `json:"approved_by"`
	AccountID  string `json:"account_id"`
}

type BackfillReport struct {
	DryRun            bool     `json:"dry_run"`
	WouldOpenCash     string   `json:"would_open_cash"`
	Currency          string   `json:"currency"`
	LegacyPositionIDs []uint   `json:"legacy_position_ids"`
	LegacyOrderIDs    []uint   `json:"legacy_order_ids"`
	Unresolved        []string `json:"unresolved"`
	Applied           bool     `json:"applied"`
}

// Backfill records only the observed cutover cash balance. Legacy positions and
// orders remain unresolved because mutable rows are not evidence of fills.
func (s *Service) Backfill(ctx context.Context, options BackfillOptions) (BackfillReport, error) {
	if options.AccountID == "" {
		options.AccountID = DefaultAccountID
	}
	var wallet database.Wallet
	if err := s.DB.WithContext(ctx).First(&wallet).Error; err != nil {
		return BackfillReport{}, err
	}
	balance, err := accounting.FromFloat(wallet.Balance)
	if err != nil {
		return BackfillReport{}, err
	}
	report := BackfillReport{DryRun: !options.Apply, WouldOpenCash: balance.String(), Currency: wallet.Currency, LegacyPositionIDs: []uint{}, LegacyOrderIDs: []uint{}, Unresolved: []string{}}
	if err := s.DB.WithContext(ctx).Model(&database.Position{}).Pluck("id", &report.LegacyPositionIDs).Error; err != nil {
		return report, err
	}
	if err := s.DB.WithContext(ctx).Model(&database.Order{}).Pluck("id", &report.LegacyOrderIDs).Error; err != nil {
		return report, err
	}
	if len(report.LegacyPositionIDs) > 0 {
		report.Unresolved = append(report.Unresolved, "legacy positions were not converted into asset events or cost basis")
	}
	if len(report.LegacyOrderIDs) > 0 {
		report.Unresolved = append(report.Unresolved, "legacy orders were not assumed to be executed fills")
	}
	if !options.Apply {
		return report, nil
	}
	if options.Approval != BackfillApproval || options.ApprovedBy == "" {
		return report, fmt.Errorf("explicit approval %q and approved-by are required", BackfillApproval)
	}

	now := s.now()
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockAccount(tx, options.AccountID); err != nil {
			return err
		}
		var state database.LedgerMigrationState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, "account_id = ?", options.AccountID).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if state.Status == "ready" {
			return fmt.Errorf("ledger backfill already applied")
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&wallet).Error; err != nil {
			return err
		}
		wallet.BalanceExact = decimalPtr(balance)
		if err := tx.Save(&wallet).Error; err != nil {
			return err
		}
		batchID := "legacy-opening-" + options.AccountID
		hash := stableID("payload", options.AccountID+balance.String())
		if err := tx.Create(&database.LedgerBatch{ID: batchID, AccountID: options.AccountID, PayloadHash: hash, CreatedAt: now}).Error; err != nil {
			return err
		}
		eventID := stableID("event-opening", batchID)
		metadata, _ := json.Marshal(map[string]interface{}{"legacy_cutover": true, "unresolved": report.Unresolved})
		event := database.LedgerEvent{ID: eventID, LedgerBatchID: batchID, Sequence: 1, IdempotencyKey: batchID + ":capital", EventType: EventCapitalDeposit, AccountID: options.AccountID, Currency: wallet.Currency, CashDelta: balance, AssetDelta: accounting.Zero(), ExecutionMode: "administrative", Actor: options.ApprovedBy, Reason: "approved legacy cutover opening cash balance", RealizedPnL: accounting.Zero(), MetadataJSON: string(metadata), OccurredAt: now, RecordedAt: now}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		unresolved, _ := json.Marshal(report.Unresolved)
		state = database.LedgerMigrationState{AccountID: options.AccountID, Status: "ready", OpeningEventID: &eventID, UnresolvedJSON: string(unresolved), ApprovedBy: &options.ApprovedBy, ApprovedAt: &now, CreatedAt: now, UpdatedAt: now}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "account_id"}}, DoUpdates: clause.AssignmentColumns([]string{"status", "opening_event_id", "unresolved_json", "approved_by", "approved_at", "updated_at"})}).Create(&state).Error
	})
	if err == nil {
		report.Applied = true
		report.DryRun = false
	}
	return report, err
}

package database

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
	"trading-go/internal/accounting"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const primaryLedgerAccount = "primary"

// seedLedgerBoundary distinguishes a genuinely new default wallet from an
// upgrade. Only the former receives an automatic opening-capital event.
func seedLedgerBoundary(tx *gorm.DB, wallet Wallet, walletCreated bool) error {
	if err := tx.Exec("SET LOCAL trading_bot.ledger_write = 'on'").Error; err != nil {
		return err
	}
	var existing LedgerMigrationState
	if err := tx.First(&existing, "account_id = ?", primaryLedgerAccount).Error; err == nil {
		return nil
	}
	now := time.Now().UTC()
	if walletCreated {
		balance, err := accounting.FromFloat(wallet.Balance)
		if err != nil {
			return err
		}
		wallet.BalanceExact = &balance
		if err := tx.Save(&wallet).Error; err != nil {
			return err
		}
		batchID := "fresh-install-opening-balance"
		eventID := stableSeedID("opening", batchID)
		if err := tx.Create(&LedgerBatch{ID: batchID, AccountID: primaryLedgerAccount, PayloadHash: stableSeedID("payload", balance.String()), CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&LedgerEvent{
			ID: eventID, LedgerBatchID: batchID, Sequence: 1, IdempotencyKey: batchID + ":capital",
			EventType: "capital_deposit", AccountID: primaryLedgerAccount, VenueID: "internal", Currency: wallet.Currency,
			CashDelta: balance, AssetDelta: accounting.Zero(), ExecutionMode: "administrative",
			Actor: "system_seed", Reason: "fresh install opening balance", RealizedPnL: accounting.Zero(), CostBasisDelta: accounting.Zero(), FeeDelta: accounting.Zero(),
			MetadataJSON: `{"source":"configured_default_balance"}`, OccurredAt: now, RecordedAt: now,
		}).Error; err != nil {
			return err
		}
		return tx.Create(&LedgerMigrationState{AccountID: primaryLedgerAccount, Status: "ready", OpeningEventID: &eventID, UnresolvedJSON: "[]", CreatedAt: now, UpdatedAt: now}).Error
	}

	issues := []string{"legacy wallet balance has no immutable capital provenance"}
	var positions, orders int64
	_ = tx.Model(&Position{}).Count(&positions).Error
	_ = tx.Model(&Order{}).Count(&orders).Error
	if positions > 0 {
		issues = append(issues, "legacy positions require evidence or explicit administrative correction")
	}
	if orders > 0 {
		issues = append(issues, "legacy orders are not assumed to be fills")
	}
	unresolved, _ := json.Marshal(issues)
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&LedgerMigrationState{
		AccountID: primaryLedgerAccount, Status: "pending_approval", UnresolvedJSON: string(unresolved), CreatedAt: now, UpdatedAt: now,
	}).Error
}

func stableSeedID(kind, value string) string {
	sum := sha256.Sum256([]byte(kind + ":" + value))
	return kind + "_" + hex.EncodeToString(sum[:16])
}

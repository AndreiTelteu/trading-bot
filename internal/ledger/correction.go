package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AssetCorrectionCommand struct {
	IdempotencyKey string
	AccountID      string
	Symbol         string
	Quantity       accounting.Decimal
	CostBasis      accounting.Decimal
	Currency       string
	Actor          string
	Reason         string
	Evidence       map[string]interface{}
	OccurredAt     time.Time
}

// ApplyAssetCorrection establishes exact current exposure from reviewed legacy
// evidence. It never invents fills and is available only during migration
// resolution or for an already-ready account's explicit administrative repair.
func (s *Service) ApplyAssetCorrection(ctx context.Context, command AssetCorrectionCommand) (database.LedgerEvent, error) {
	if err := requirePrimaryAccount(command.AccountID); err != nil {
		return database.LedgerEvent{}, err
	}
	if command.AccountID == "" {
		command.AccountID = DefaultAccountID
	}
	if command.IdempotencyKey == "" || command.Symbol == "" || command.Quantity.Sign() <= 0 || command.CostBasis.Sign() < 0 || command.Actor == "" || command.Reason == "" {
		return database.LedgerEvent{}, validation("invalid_asset_correction", "idempotency key, symbol, positive quantity, non-negative basis, actor and reason are required")
	}
	if command.OccurredAt.IsZero() {
		command.OccurredAt = s.now()
	}
	recordedAt := s.now()
	if command.OccurredAt.After(recordedAt) {
		return database.LedgerEvent{}, validation("future_occurred_at", "occurred_at must be no later than recorded_at")
	}
	hash, _, err := hashPayload(command)
	if err != nil {
		return database.LedgerEvent{}, err
	}
	metadata, _ := json.Marshal(command.Evidence)
	if len(metadata) == 0 {
		metadata = []byte("{}")
	}
	var event database.LedgerEvent
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := allowLedgerProjectionWrites(tx); err != nil {
			return err
		}
		if err := lockAccount(tx, command.AccountID); err != nil {
			return err
		}
		already, err := beginBatch(tx, command.IdempotencyKey, command.AccountID, hash, command.OccurredAt)
		if err != nil {
			return err
		}
		if already {
			return tx.Where("ledger_batch_id = ?", command.IdempotencyKey).First(&event).Error
		}
		var state database.LedgerMigrationState
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&state, "account_id = ?", command.AccountID).Error; err != nil {
			return err
		}
		if state.Status != "pending_resolution" && state.Status != "ready" {
			return ErrUnreconciledLegacyState
		}
		var wallet database.Wallet
		if err := tx.First(&wallet).Error; err != nil {
			return err
		}
		if wallet.Currency != command.Currency {
			return validation("unsupported_currency", fmt.Sprintf("correction currency must be %s", wallet.Currency))
		}
		var position database.Position
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND symbol = ? AND status = ?", command.AccountID, command.Symbol, "open").First(&position).Error; err != nil {
			return err
		}
		if position.AmountExact != nil || position.CostBasisExact != nil {
			return conflict("already_reconciled", "position already has an exact projection", nil)
		}
		legacyQty, err := accounting.FromFloat(position.Amount)
		if err != nil || legacyQty.Cmp(command.Quantity) != 0 {
			return validation("legacy_quantity_mismatch", "correction quantity must equal the observed legacy position quantity")
		}
		avg, _ := command.CostBasis.Div(command.Quantity)
		zero := accounting.Zero()
		position.AmountExact = &command.Quantity
		position.CostBasisExact = &command.CostBasis
		position.RealizedPnLExact = &zero
		position.FeesExact = &zero
		position.AvgPrice = avg.Float64()
		if err := tx.Save(&position).Error; err != nil {
			return err
		}
		event = database.LedgerEvent{ID: stableID("event-asset-correction", command.IdempotencyKey), LedgerBatchID: command.IdempotencyKey, Sequence: 1, IdempotencyKey: command.IdempotencyKey + ":asset", EventType: EventAdminCorrection, AccountID: command.AccountID, VenueID: "internal", Currency: command.Currency, Symbol: command.Symbol, CashDelta: zero, AssetDelta: command.Quantity, PositionID: &position.ID, ExecutionMode: "administrative", Actor: command.Actor, Reason: command.Reason, RealizedPnL: zero, CostBasisDelta: command.CostBasis, FeeDelta: zero, MetadataJSON: string(metadata), OccurredAt: command.OccurredAt, RecordedAt: recordedAt}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		var remaining int64
		if err := tx.Model(&database.Position{}).Where("account_id = ? AND status = ? AND (amount_exact IS NULL OR cost_basis_exact IS NULL)", command.AccountID, "open").Count(&remaining).Error; err != nil {
			return err
		}
		if remaining == 0 {
			state.Status = "ready"
			state.UnresolvedJSON = `["legacy historical orders/fills remain unreconstructed"]`
			if err := tx.Save(&state).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return event, normalizePersistenceError(err)
}

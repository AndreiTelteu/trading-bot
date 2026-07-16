package ledger

import (
	"context"
	"fmt"
	"strings"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type AdjustmentCommand struct {
	EventID        string             `json:"event_id"`
	IdempotencyKey string             `json:"idempotency_key"`
	AccountID      string             `json:"account_id"`
	Type           string             `json:"type"`
	Amount         accounting.Decimal `json:"amount"`
	Currency       string             `json:"currency"`
	Actor          string             `json:"actor"`
	Reason         string             `json:"reason"`
	OccurredAt     time.Time          `json:"occurred_at"`
}

type AdjustmentResult struct {
	AlreadyApplied bool                 `json:"already_applied"`
	Wallet         database.Wallet      `json:"wallet"`
	Event          database.LedgerEvent `json:"event"`
}

func (s *Service) ApplyAdjustment(ctx context.Context, command AdjustmentCommand) (AdjustmentResult, error) {
	if err := requirePrimaryAccount(command.AccountID); err != nil {
		return AdjustmentResult{}, err
	}
	if command.AccountID == "" {
		command.AccountID = DefaultAccountID
	}
	if command.OccurredAt.IsZero() {
		command.OccurredAt = s.now()
	}
	recordedAt := s.now()
	if command.OccurredAt.After(recordedAt) {
		return AdjustmentResult{}, validation("future_occurred_at", "occurred_at must be no later than recorded_at")
	}
	if command.IdempotencyKey == "" || command.Currency == "" || command.Actor == "" || strings.TrimSpace(command.Reason) == "" || command.Amount.Sign() == 0 {
		return AdjustmentResult{}, validation("invalid_adjustment", "idempotency key, non-zero amount, currency, actor, and reason are required")
	}
	if command.Type != EventCapitalDeposit && command.Type != EventCapitalWithdrawal && command.Type != EventFundingInterest && command.Type != EventAdminCorrection {
		return AdjustmentResult{}, validation("unsupported_adjustment_type", fmt.Sprintf("unsupported adjustment type %q", command.Type))
	}
	if (command.Type == EventCapitalDeposit || command.Type == EventCapitalWithdrawal) && command.Amount.Sign() < 0 {
		return AdjustmentResult{}, validation("invalid_adjustment_amount", "capital deposit/withdrawal amount must be positive")
	}
	payloadHash, _, err := hashPayload(command)
	if err != nil {
		return AdjustmentResult{}, err
	}
	var result AdjustmentResult
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := allowLedgerProjectionWrites(tx); err != nil {
			return err
		}
		if err := lockAccount(tx, command.AccountID); err != nil {
			return err
		}
		already, err := beginBatch(tx, command.IdempotencyKey, command.AccountID, payloadHash, command.OccurredAt)
		if err != nil {
			return err
		}
		if already {
			if err := tx.Where("ledger_batch_id = ?", command.IdempotencyKey).First(&result.Event).Error; err != nil {
				return err
			}
			if err := tx.First(&result.Wallet).Error; err != nil {
				return err
			}
			result.AlreadyApplied = true
			return nil
		}
		if err := requireReady(tx, command.AccountID); err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&result.Wallet).Error; err != nil {
			return err
		}
		if result.Wallet.BalanceExact == nil {
			return ErrProjectionUnavailable
		}
		if !strings.EqualFold(result.Wallet.Currency, command.Currency) {
			return validation("unsupported_currency", fmt.Sprintf("adjustment currency %s does not match wallet currency %s", command.Currency, result.Wallet.Currency))
		}
		delta := command.Amount
		if command.Type == EventCapitalWithdrawal {
			delta = delta.Neg()
		}
		balance := result.Wallet.BalanceExact.Add(delta)
		if balance.Sign() < 0 {
			return ErrInsufficientCash
		}
		result.Wallet.BalanceExact, result.Wallet.Balance = decimalPtr(balance), balance.Float64()
		if err := tx.Save(&result.Wallet).Error; err != nil {
			return err
		}
		eventID := command.EventID
		if eventID == "" {
			eventID = stableID("event-adjustment", command.IdempotencyKey)
		}
		result.Event = database.LedgerEvent{
			ID: eventID, LedgerBatchID: command.IdempotencyKey,
			Sequence: 1, IdempotencyKey: command.IdempotencyKey + ":adjustment", EventType: command.Type,
			AccountID: command.AccountID, VenueID: "internal", Currency: command.Currency, CashDelta: delta, AssetDelta: accounting.Zero(),
			ExecutionMode: "administrative", Actor: command.Actor, Reason: command.Reason,
			RealizedPnL: accounting.Zero(), MetadataJSON: "{}", OccurredAt: command.OccurredAt, RecordedAt: recordedAt,
		}
		if err := tx.Create(&result.Event).Error; err != nil {
			return err
		}
		return s.fail("ledger")
	})
	return result, normalizePersistenceError(err)
}

type ReversalCommand struct {
	IdempotencyKey, OriginalEventID, Actor, Reason string
	OccurredAt                                     time.Time
}

// ReverseCashEvent appends an equal and opposite correction. Fill events are
// intentionally rejected here: reversing matched fills requires a compensating
// fill or an explicitly reviewed asset correction, not a hidden cost-basis edit.
func (s *Service) ReverseCashEvent(ctx context.Context, command ReversalCommand) (database.LedgerEvent, error) {
	if command.IdempotencyKey == "" || command.OriginalEventID == "" || command.Actor == "" || command.Reason == "" {
		return database.LedgerEvent{}, validation("invalid_reversal", "all reversal fields are required")
	}
	if command.OccurredAt.IsZero() {
		command.OccurredAt = s.now()
	}
	recordedAt := s.now()
	if command.OccurredAt.After(recordedAt) {
		return database.LedgerEvent{}, validation("future_occurred_at", "occurred_at must be no later than recorded_at")
	}
	var reversal database.LedgerEvent
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := allowLedgerProjectionWrites(tx); err != nil {
			return err
		}
		var original database.LedgerEvent
		if err := tx.First(&original, "id = ?", command.OriginalEventID).Error; err != nil {
			return err
		}
		if original.FillID != nil || (original.AssetDelta.Sign() != 0 && original.EventType != EventAdminCorrection) {
			return validation("unsupported_reversal", "fill reversals require a compensating fill")
		}
		if err := lockAccount(tx, original.AccountID); err != nil {
			return err
		}
		hash, _, _ := hashPayload(command)
		already, err := beginBatch(tx, command.IdempotencyKey, original.AccountID, hash, command.OccurredAt)
		if err != nil {
			return err
		}
		if already {
			return tx.Where("ledger_batch_id = ?", command.IdempotencyKey).First(&reversal).Error
		}
		if err := requireReady(tx, original.AccountID); err != nil {
			return err
		}
		var wallet database.Wallet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&wallet).Error; err != nil {
			return err
		}
		if wallet.BalanceExact == nil {
			return ErrProjectionUnavailable
		}
		balance := wallet.BalanceExact.Sub(original.CashDelta)
		if balance.Sign() < 0 {
			return ErrInsufficientCash
		}
		wallet.BalanceExact, wallet.Balance = decimalPtr(balance), balance.Float64()
		if err := tx.Save(&wallet).Error; err != nil {
			return err
		}
		if original.AssetDelta.Sign() != 0 {
			var position database.Position
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND symbol = ?", original.AccountID, original.Symbol).First(&position).Error; err != nil {
				return err
			}
			if position.AmountExact == nil || position.CostBasisExact == nil {
				return ErrProjectionUnavailable
			}
			quantity := position.AmountExact.Sub(original.AssetDelta)
			basis := position.CostBasisExact.Sub(original.CostBasisDelta)
			if quantity.Sign() < 0 || basis.Sign() < 0 {
				return conflict("reversal_projection_conflict", "correction reversal would make projection negative", nil)
			}
			position.AmountExact = &quantity
			position.CostBasisExact = &basis
			position.Amount = quantity.Float64()
			if quantity.Sign() == 0 {
				position.Status = "closed"
				position.ClosedAt = &command.OccurredAt
				position.CloseReason = stringPtrOrNil("administrative_reversal")
			} else {
				avg, _ := basis.Div(quantity)
				position.AvgPrice = avg.Float64()
			}
			if err := tx.Save(&position).Error; err != nil {
				return err
			}
		}
		reversal = database.LedgerEvent{
			ID: stableID("event-reversal", command.IdempotencyKey), LedgerBatchID: command.IdempotencyKey,
			Sequence: 1, IdempotencyKey: command.IdempotencyKey + ":reversal", EventType: EventReversal,
			AccountID: original.AccountID, VenueID: original.VenueID, Currency: original.Currency, Symbol: original.Symbol,
			CashDelta: original.CashDelta.Neg(), AssetDelta: original.AssetDelta.Neg(), ExecutionMode: "administrative",
			Actor: command.Actor, Reason: command.Reason, ReversesEventID: &original.ID,
			RealizedPnL: original.RealizedPnL.Neg(), CostBasisDelta: original.CostBasisDelta.Neg(), FeeDelta: original.FeeDelta.Neg(), MetadataJSON: "{}", OccurredAt: command.OccurredAt, RecordedAt: recordedAt,
		}
		return tx.Create(&reversal).Error
	})
	return reversal, normalizePersistenceError(err)
}

func IsConflict(err error) bool {
	kind, _ := ErrorDetails(err)
	return kind == KindConflict || kind == KindUnavailable
}

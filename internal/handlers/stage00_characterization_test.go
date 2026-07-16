package handlers

import (
	"errors"
	"testing"
	"time"
	"trading-go/internal/database"
)

type fakeDirectPositionStore struct {
	position           database.Position
	findErr, deleteErr error
	finds, deletes     int
}

func (store *fakeDirectPositionStore) FindBySymbol(string) (database.Position, error) {
	store.finds++
	return store.position, store.findErr
}
func (store *fakeDirectPositionStore) Delete(database.Position) error {
	store.deletes++
	return store.deleteErr
}

func TestCharacterizationDirectCloseChangesProjectionWithoutAccountingInputs(t *testing.T) {
	entry := 100.0
	mark := 110.0
	reason := "manual_direct"
	closedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	position := database.Position{ID: 7, Symbol: "BTC", Amount: 2, AvgPrice: entry, EntryPrice: &entry, CurrentPrice: &mark, Pnl: 20, PnlPercent: 10, Status: "open", ExitPending: true, OpenedAt: closedAt.Add(-time.Hour)}
	if err := applyDirectCloseProjection(&position, &reason, closedAt); err != nil {
		t.Fatal(err)
	}
	if position.Status != "closed" || position.ExitPending || position.ClosedAt == nil || !position.ClosedAt.Equal(closedAt) || position.CloseReason == nil || *position.CloseReason != reason {
		t.Fatalf("close projection=%+v", position)
	}
	if position.ID != 7 || position.Symbol != "BTC" || position.Amount != 2 || position.AvgPrice != entry || position.Pnl != 20 || position.PnlPercent != 10 {
		t.Fatalf("direct close unexpectedly changed economic projection=%+v", position)
	}
}

func TestCharacterizationDirectCloseRejectsAlreadyClosedProjection(t *testing.T) {
	position := database.Position{Status: "closed"}
	if err := applyDirectCloseProjection(&position, nil, time.Now()); err == nil {
		t.Fatal("already-closed projection should be rejected")
	}
}

func TestCharacterizationDirectDeleteOnlyRemovesPositionProjection(t *testing.T) {
	store := &fakeDirectPositionStore{position: database.Position{ID: 8, Symbol: "ETH", Amount: 3, AvgPrice: 50, Status: "open"}}
	deleted, err := performDirectPositionDelete(store, "ETH")
	if err != nil {
		t.Fatal(err)
	}
	if store.finds != 1 || store.deletes != 1 || deleted.ID != 8 {
		t.Fatalf("direct delete result=%+v store=%+v", deleted, store)
	}
	// The Stage 00 store contract deliberately has no wallet/order/ledger method;
	// this freezes the accounting bypass until Stage 01 replaces the endpoint.
	store = &fakeDirectPositionStore{position: database.Position{ID: 8}, deleteErr: errors.New("delete failed")}
	if _, err := performDirectPositionDelete(store, "ETH"); !errors.Is(err, errDirectPositionDelete) {
		t.Fatalf("delete error=%v", err)
	}
}

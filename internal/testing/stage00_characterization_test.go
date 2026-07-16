package testing

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
	"trading-go/internal/database"
)

func TestStage01DirectCloseFailsClosedWithoutExactLedgerProjection(t *testing.T) {
	SetupTestDB(t)
	application := SetupTestApp()
	cookie := loginCookie(t, application)

	position := database.Position{Symbol: "DIRECTCLOSE", Amount: 2, AvgPrice: 100, Status: "open", OpenedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := database.DB.Create(&position).Error; err != nil {
		t.Fatal(err)
	}
	var walletBefore database.Wallet
	if err := database.DB.First(&walletBefore).Error; err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/positions/"+strconv.Itoa(int(position.ID))+"/close", bytes.NewBufferString(`{"close_reason":"manual_direct"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Cookie", cookie)
	response, err := application.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("direct close status = %d, want 409", response.StatusCode)
	}

	var refreshed database.Position
	if err := database.DB.First(&refreshed, position.ID).Error; err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != "open" || refreshed.ExitPending || refreshed.ClosedAt != nil {
		t.Fatalf("failed close mutated economic projection: %+v", refreshed)
	}
	var walletAfter database.Wallet
	if err := database.DB.First(&walletAfter).Error; err != nil {
		t.Fatal(err)
	}
	if walletAfter.Balance != walletBefore.Balance || walletAfter.BalanceExact == nil || walletBefore.BalanceExact == nil || walletAfter.BalanceExact.String() != walletBefore.BalanceExact.String() {
		t.Fatalf("wallet changed from %+v to %+v", walletBefore, walletAfter)
	}
	var orders []database.Order
	if err := database.DB.Where("symbol = ? AND order_type = ?", position.Symbol, "sell").Find(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if len(orders) != 1 || orders[0].Status != "failed" {
		t.Fatalf("expected one failed audit order, got %+v", orders)
	}
}

func TestStage01DirectDeleteIsFencedAndRetainsEconomicHistory(t *testing.T) {
	SetupTestDB(t)
	application := SetupTestApp()
	cookie := loginCookie(t, application)

	position := database.Position{Symbol: "DIRECTDELETE", Amount: 3, AvgPrice: 50, Status: "open", OpenedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := database.DB.Create(&position).Error; err != nil {
		t.Fatal(err)
	}
	var walletBefore database.Wallet
	if err := database.DB.First(&walletBefore).Error; err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodDelete, "/api/positions/DIRECTDELETE", nil)
	request.Header.Set("Cookie", cookie)
	response, err := application.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("delete status = %d, want 409", response.StatusCode)
	}

	var count int64
	if err := database.DB.Model(&database.Position{}).Where("id = ?", position.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("position history was deleted")
	}
	var walletAfter database.Wallet
	if err := database.DB.First(&walletAfter).Error; err != nil {
		t.Fatal(err)
	}
	if walletAfter.Balance != walletBefore.Balance || walletAfter.BalanceExact == nil || walletBefore.BalanceExact == nil || walletAfter.BalanceExact.String() != walletBefore.BalanceExact.String() {
		t.Fatalf("wallet changed from %+v to %+v", walletBefore, walletAfter)
	}
	var orderCount int64
	if err := database.DB.Model(&database.Order{}).Count(&orderCount).Error; err != nil {
		t.Fatal(err)
	}
	if orderCount != 0 {
		t.Fatalf("direct delete created %d orders, want 0", orderCount)
	}
}

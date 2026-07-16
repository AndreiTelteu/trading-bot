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

func TestCharacterizationDirectCloseBypassesWalletAndOrderAccounting(t *testing.T) {
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
	if response.StatusCode != http.StatusOK {
		t.Fatalf("direct close status = %d, want 200", response.StatusCode)
	}

	var closed database.Position
	if err := database.DB.First(&closed, position.ID).Error; err != nil {
		t.Fatal(err)
	}
	if closed.Status != "closed" || closed.ClosedAt == nil || closed.CloseReason == nil || *closed.CloseReason != "manual_direct" {
		t.Fatalf("direct close did not update only position status fields: %+v", closed)
	}
	var walletAfter database.Wallet
	if err := database.DB.First(&walletAfter).Error; err != nil {
		t.Fatal(err)
	}
	if walletAfter.Balance != walletBefore.Balance {
		t.Fatalf("wallet changed from %v to %v", walletBefore.Balance, walletAfter.Balance)
	}
	var orderCount int64
	if err := database.DB.Model(&database.Order{}).Count(&orderCount).Error; err != nil {
		t.Fatal(err)
	}
	if orderCount != 0 {
		t.Fatalf("direct close created %d orders, want 0", orderCount)
	}
}

func TestCharacterizationDeleteRemovesPositionWithoutWalletOrOrderAccounting(t *testing.T) {
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
	if response.StatusCode != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", response.StatusCode)
	}

	var count int64
	if err := database.DB.Model(&database.Position{}).Where("id = ?", position.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("position still exists after direct delete")
	}
	var walletAfter database.Wallet
	if err := database.DB.First(&walletAfter).Error; err != nil {
		t.Fatal(err)
	}
	if walletAfter.Balance != walletBefore.Balance {
		t.Fatalf("wallet changed from %v to %v", walletBefore.Balance, walletAfter.Balance)
	}
	var orderCount int64
	if err := database.DB.Model(&database.Order{}).Count(&orderCount).Error; err != nil {
		t.Fatal(err)
	}
	if orderCount != 0 {
		t.Fatalf("direct delete created %d orders, want 0", orderCount)
	}
}

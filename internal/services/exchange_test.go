package services

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrderResponsePreservesBinanceDecimalStrings(t *testing.T) {
	var response OrderResponse
	payload := []byte(`{"orderId":42,"symbol":"BTCUSDT","side":"BUY","type":"MARKET","status":"PARTIALLY_FILLED","origQty":"0.123456789123456789","price":"64123.000000000000000001","executedQty":"0.023456789123456789","transactionTime":1700000000000}`)
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatal(err)
	}
	if response.QuantityExact.String() != "0.123456789123456789" || response.PriceExact.String() != "64123.000000000000000001" || response.ExecutedQtyExact.String() != "0.023456789123456789" {
		t.Fatalf("lost precision: qty=%s price=%s executed=%s", response.QuantityExact.String(), response.PriceExact.String(), response.ExecutedQtyExact.String())
	}
}

func TestPositionPairSymbolUsesConfiguredSettlementCurrency(t *testing.T) {
	if got := PositionPairSymbol("eth", "EUR"); got != "ETHEUR" {
		t.Fatalf("pair=%s", got)
	}
	if got := PositionPairSymbol("ETH/EUR", "EUR"); got != "ETHEUR" {
		t.Fatalf("normalized pair=%s", got)
	}
}

func TestNewExchangeService(t *testing.T) {
	es := NewExchangeService("test-key", "test-secret")

	if es.APIKey != "test-key" {
		t.Errorf("NewExchangeService() APIKey = %v, want test-key", es.APIKey)
	}
	if es.APISecret != "test-secret" {
		t.Errorf("NewExchangeService() APISecret = %v, want test-secret", es.APISecret)
	}
	if es.BaseURL != "https://api.binance.com" {
		t.Errorf("NewExchangeService() BaseURL = %v, want https://api.binance.com", es.BaseURL)
	}
	if es.HTTPClient == nil {
		t.Error("NewExchangeService() HTTPClient should not be nil")
	}
}

func TestSign(t *testing.T) {
	es := NewExchangeService("test-key", "test-secret")

	signature := es.sign("symbol=BNBUSDT&timestamp=123456789")
	if signature == "" {
		t.Error("sign() should return a non-empty signature")
	}
}

func TestMakeRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"symbol":"BNBUSDT","price":"500.00"}`))
	}))
	defer ts.Close()

	es := &ExchangeService{
		APIKey:     "",
		APISecret:  "",
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	data, err := es.makeRequest("GET", "/test", map[string]string{})
	if err != nil {
		t.Errorf("makeRequest() error = %v", err)
	}

	expected := `{"symbol":"BNBUSDT","price":"500.00"}`
	if string(data) != expected {
		t.Errorf("makeRequest() = %v, want %v", string(data), expected)
	}
}

func TestMakeRequestUnauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	es := &ExchangeService{
		APIKey:     "test-key",
		APISecret:  "test-secret",
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	_, err := es.makeRequest("GET", "/test", map[string]string{})
	if err == nil {
		t.Error("makeRequest() should return error for 401 status")
	}
}

func TestFetchMultipleTickerPrices(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"symbol":"BNBUSDT","price":"500.00"}`))
	}))
	defer ts.Close()

	es := &ExchangeService{
		APIKey:     "",
		APISecret:  "",
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
	}

	result, err := es.FetchMultipleTickerPrices([]string{"BNBUSDT", "ETHUSDT"})
	if err != nil {
		t.Errorf("FetchMultipleTickerPrices() error = %v", err)
	}

	if len(result) != 2 {
		t.Errorf("FetchMultipleTickerPrices() length = %v, want 2", len(result))
	}
}

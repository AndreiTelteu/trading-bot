package services

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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

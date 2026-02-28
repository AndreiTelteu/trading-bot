package services

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ExchangeService struct {
	APIKey     string
	APISecret  string
	BaseURL    string
	HTTPClient *http.Client
}

type TickerPrice struct {
	Symbol             string `json:"symbol"`
	LastPrice          string `json:"lastPrice"`
	PriceChange        string `json:"priceChange"`
	PriceChangePercent string `json:"priceChangePercent"`
	HighPrice          string `json:"highPrice"`
	LowPrice           string `json:"lowPrice"`
	Volume             string `json:"volume"`
	QuoteVolume        string `json:"quoteVolume"`
}

type OrderRequest struct {
	Symbol      string  `json:"symbol"`
	Side        string  `json:"side"`
	Type        string  `json:"type"`
	Quantity    float64 `json:"quantity"`
	Price       float64 `json:"price,omitempty"`
	TimeInForce string  `json:"timeInForce,omitempty"`
}

type OrderResponse struct {
	OrderID         int64   `json:"orderId"`
	Symbol          string  `json:"symbol"`
	Side            string  `json:"side"`
	Type            string  `json:"type"`
	Quantity        float64 `json:"origQty"`
	Price           float64 `json:"price"`
	Status          string  `json:"status"`
	ExecutedQty     float64 `json:"executedQty"`
	Time            int64   `json:"time"`
	TransactionTime int64   `json:"transactionTime"`
}

type AccountBalance struct {
	Asset  string `json:"asset"`
	Free   string `json:"free"`
	Locked string `json:"locked"`
}

type AccountInfo struct {
	Balances []AccountBalance `json:"balances"`
}

type OHLCV struct {
	OpenTime  int64
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CloseTime int64
}

func NewExchangeService(apiKey, apiSecret string) *ExchangeService {
	return &ExchangeService{
		APIKey:     apiKey,
		APISecret:  apiSecret,
		BaseURL:    "https://api.binance.com",
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (s *ExchangeService) sign(queryString string) string {
	mac := hmac.New(sha256.New, []byte(s.APISecret))
	mac.Write([]byte(queryString))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *ExchangeService) makeRequest(method, endpoint string, params map[string]string) ([]byte, error) {
	baseURL := s.BaseURL + endpoint

	isPublicEndpoint := strings.Contains(endpoint, "/api/v3/ticker/") || 
                        strings.Contains(endpoint, "/api/v3/klines") || 
                        strings.Contains(endpoint, "/api/v3/exchangeInfo")

	if method == "GET" && len(params) > 0 {
		queryString := ""
		for key, value := range params {
			if queryString != "" {
				queryString += "&"
			}
			queryString += key + "=" + url.QueryEscape(value)
		}

		// Only add signature if we have both API key and secret and it's NOT a public endpoint
		if !isPublicEndpoint && s.APIKey != "" && s.APISecret != "" {
			signature := s.sign(queryString)
			baseURL += "?" + queryString + "&signature=" + signature
		} else {
			baseURL += "?" + queryString
		}
	}

	req, err := http.NewRequest(method, baseURL, bytes.NewBuffer([]byte{}))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	
	// Only add API key header if it's NOT a public endpoint
	if !isPublicEndpoint && s.APIKey != "" {
		req.Header.Set("X-MBX-APIKEY", s.APIKey)
	}

	resp, err := s.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API request failed with status: %d, response: %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	return body, nil
}

func (s *ExchangeService) FetchTickerPrice(symbol string) (*TickerPrice, error) {
	params := map[string]string{
		"symbol": symbol,
	}

	data, err := s.makeRequest("GET", "/api/v3/ticker/24hr", params)
	if err != nil {
		return nil, err
	}

	var ticker TickerPrice
	if err := json.Unmarshal(data, &ticker); err != nil {
		return nil, fmt.Errorf("failed to parse ticker response: %w", err)
	}

	return &ticker, nil
}

func (s *ExchangeService) FetchMultipleTickerPrices(symbols []string) (map[string]TickerPrice, error) {
	result := make(map[string]TickerPrice)

	for _, symbol := range symbols {
		ticker, err := s.FetchTickerPrice(symbol)
		if err != nil {
			continue
		}
		result[symbol] = *ticker
	}

	return result, nil
}

func (s *ExchangeService) PlaceOrder(order OrderRequest) (*OrderResponse, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	params := map[string]string{
		"symbol":     order.Symbol,
		"side":       order.Side,
		"type":       order.Type,
		"quantity":   strconv.FormatFloat(order.Quantity, 'f', -1, 64),
		"timestamp":  timestamp,
		"recvWindow": "5000",
	}

	if order.Type == "LIMIT" {
		params["price"] = strconv.FormatFloat(order.Price, 'f', -1, 64)
		params["timeInForce"] = order.TimeInForce
	}

	queryString := ""
	for key, value := range params {
		if queryString != "" {
			queryString += "&"
		}
		queryString += key + "=" + url.QueryEscape(value)
	}

	signature := s.sign(queryString)
	params["signature"] = signature

	data, err := s.makeRequest("POST", "/api/v3/order", params)
	if err != nil {
		return nil, err
	}

	var orderResp OrderResponse
	if err := json.Unmarshal(data, &orderResp); err != nil {
		return nil, fmt.Errorf("failed to parse order response: %w", err)
	}

	return &orderResp, nil
}

func (s *ExchangeService) ExecuteBuy(symbol string, quantity float64, price float64) (*OrderResponse, error) {
	orderType := "MARKET"
	timeInForce := ""

	if price > 0 {
		orderType = "LIMIT"
		timeInForce = "GTC"
	}

	order := OrderRequest{
		Symbol:      symbol,
		Side:        "BUY",
		Type:        orderType,
		Quantity:    quantity,
		Price:       price,
		TimeInForce: timeInForce,
	}

	return s.PlaceOrder(order)
}

func (s *ExchangeService) ExecuteSell(symbol string, quantity float64, price float64) (*OrderResponse, error) {
	orderType := "MARKET"
	timeInForce := ""

	if price > 0 {
		orderType = "LIMIT"
		timeInForce = "GTC"
	}

	order := OrderRequest{
		Symbol:      symbol,
		Side:        "SELL",
		Type:        orderType,
		Quantity:    quantity,
		Price:       price,
		TimeInForce: timeInForce,
	}

	return s.PlaceOrder(order)
}

func (s *ExchangeService) GetAccountBalance() ([]AccountBalance, error) {
	timestamp := strconv.FormatInt(time.Now().UnixMilli(), 10)

	params := map[string]string{
		"timestamp":  timestamp,
		"recvWindow": "5000",
	}

	data, err := s.makeRequest("GET", "/api/v3/account", params)
	if err != nil {
		return nil, err
	}

	var accountInfo AccountInfo
	if err := json.Unmarshal(data, &accountInfo); err != nil {
		return nil, fmt.Errorf("failed to parse account response: %w", err)
	}

	return accountInfo.Balances, nil
}

func (s *ExchangeService) GetBalance(asset string) (float64, error) {
	balances, err := s.GetAccountBalance()
	if err != nil {
		return 0, err
	}

	for _, balance := range balances {
		if balance.Asset == asset {
			free, _ := strconv.ParseFloat(balance.Free, 64)
			return free, nil
		}
	}

	return 0, nil
}

func (s *ExchangeService) FetchOHLCV(symbol, interval string, limit int) ([]OHLCV, error) {
	params := map[string]string{
		"symbol":   symbol,
		"interval": interval,
		"limit":    strconv.Itoa(limit),
	}

	data, err := s.makeRequest("GET", "/api/v3/klines", params)
	if err != nil {
		return nil, err
	}

	var klines [][]interface{}
	if err := json.Unmarshal(data, &klines); err != nil {
		return nil, fmt.Errorf("failed to parse klines response: %w", err)
	}

	result := make([]OHLCV, len(klines))
	for i, kline := range klines {
		// Parse fields safely from Binance response (can be string or float64)
		var open, high, low, close, volume float64
		var openTime, closeTime int64

		if v, ok := kline[0].(float64); ok {
			openTime = int64(v)
		} else if v, ok := kline[0].(string); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				openTime = int64(f)
			}
		}

		if v, ok := kline[1].(float64); ok {
			open = v
		} else if v, ok := kline[1].(string); ok {
			open, _ = strconv.ParseFloat(v, 64)
		}

		if v, ok := kline[2].(float64); ok {
			high = v
		} else if v, ok := kline[2].(string); ok {
			high, _ = strconv.ParseFloat(v, 64)
		}

		if v, ok := kline[3].(float64); ok {
			low = v
		} else if v, ok := kline[3].(string); ok {
			low, _ = strconv.ParseFloat(v, 64)
		}

		if v, ok := kline[4].(float64); ok {
			close = v
		} else if v, ok := kline[4].(string); ok {
			close, _ = strconv.ParseFloat(v, 64)
		}

		if v, ok := kline[5].(float64); ok {
			volume = v
		} else if v, ok := kline[5].(string); ok {
			volume, _ = strconv.ParseFloat(v, 64)
		}

		if v, ok := kline[6].(float64); ok {
			closeTime = int64(v)
		} else if v, ok := kline[6].(string); ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				closeTime = int64(f)
			}
		}

		result[i] = OHLCV{
			OpenTime:  openTime,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close,
			Volume:    volume,
			CloseTime: closeTime,
		}
	}

	return result, nil
}

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	fastws "github.com/fasthttp/websocket"
)

type PriceEvent struct {
	Symbol    string
	LastPrice float64
	BidPrice  float64
	AskPrice  float64
	MarkPrice float64
	Timestamp time.Time
	Source    string
}

type PriceStream interface {
	Subscribe(symbol string) (<-chan PriceEvent, func(), error)
}

type BinanceTickerStream struct {
	baseURL string
	dialer  *fastws.Dialer
	mu      sync.Mutex
}

type binanceTickerPayload struct {
	Symbol    string `json:"s"`
	LastPrice string `json:"c"`
	BidPrice  string `json:"b"`
	AskPrice  string `json:"a"`
	EventTime int64  `json:"E"`
}

func NewBinanceTickerStream() *BinanceTickerStream {
	return &BinanceTickerStream{
		baseURL: "wss://stream.binance.com:9443/ws",
		dialer: &fastws.Dialer{
			Proxy:            http.ProxyFromEnvironment,
			HandshakeTimeout: 15 * time.Second,
		},
	}
}

func (s *BinanceTickerStream) Subscribe(symbol string) (<-chan PriceEvent, func(), error) {
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan PriceEvent, 32)
	go s.run(ctx, positionPairSymbol(symbol), out)
	return out, cancel, nil
}

func (s *BinanceTickerStream) run(ctx context.Context, symbol string, out chan<- PriceEvent) {
	defer close(out)

	streamName := strings.ToLower(symbol) + "@ticker"
	url := fmt.Sprintf("%s/%s", s.baseURL, streamName)
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		conn, _, err := s.dialer.DialContext(ctx, url, nil)
		if err != nil {
			log.Printf("ticker stream dial failed for %s: %v", symbol, err)
			if !sleepWithContext(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff)
			continue
		}

		backoff = time.Second
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))

		for {
			_, payload, readErr := conn.ReadMessage()
			if readErr != nil {
				_ = conn.Close()
				break
			}

			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Minute))

			event, parseErr := parseTickerEvent(payload)
			if parseErr != nil {
				continue
			}

			select {
			case out <- event:
			case <-ctx.Done():
				_ = conn.Close()
				return
			}
		}

		if !sleepWithContext(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff)
	}
}

func parseTickerEvent(payload []byte) (PriceEvent, error) {
	var message binanceTickerPayload
	if err := json.Unmarshal(payload, &message); err != nil {
		return PriceEvent{}, err
	}

	last, _ := strconv.ParseFloat(message.LastPrice, 64)
	bid, _ := strconv.ParseFloat(message.BidPrice, 64)
	ask, _ := strconv.ParseFloat(message.AskPrice, 64)
	mark := last
	if bid > 0 && ask > 0 {
		mark = (bid + ask) / 2
	}

	timestamp := time.Now()
	if message.EventTime > 0 {
		timestamp = time.UnixMilli(message.EventTime)
	}

	return PriceEvent{
		Symbol:    strings.ToUpper(message.Symbol),
		LastPrice: last,
		BidPrice:  bid,
		AskPrice:  ask,
		MarkPrice: mark,
		Timestamp: timestamp,
		Source:    "binance_ticker_stream",
	}, nil
}

func nextBackoff(current time.Duration) time.Duration {
	if current >= 30*time.Second {
		return 30 * time.Second
	}
	next := current * 2
	if next > 30*time.Second {
		return 30 * time.Second
	}
	return next
}

func sleepWithContext(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

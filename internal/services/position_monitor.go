package services

import (
	"log"
	"sync"
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm"
)

type PositionMonitor struct {
	stream      PriceStream
	coordinator *ExecutionCoordinator
	staleAfter  time.Duration

	mu      sync.RWMutex
	workers map[string]*positionWorker
}

type positionWorker struct {
	symbol     string
	cancel     func()
	lastEvent  time.Time
	lastSource string
	mu         sync.RWMutex
}

func NewPositionMonitor(stream PriceStream, coordinator *ExecutionCoordinator, staleAfter time.Duration) *PositionMonitor {
	return &PositionMonitor{
		stream:      stream,
		coordinator: coordinator,
		staleAfter:  staleAfter,
		workers:     make(map[string]*positionWorker),
	}
}

func (m *PositionMonitor) Reconcile(positions []database.Position) error {
	required := make(map[string]struct{}, len(positions))
	for _, position := range positions {
		if position.Status != "open" {
			continue
		}
		required[position.Symbol] = struct{}{}
		if err := m.ensureWorker(position.Symbol); err != nil {
			return err
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for symbol, worker := range m.workers {
		if _, ok := required[symbol]; ok {
			continue
		}
		worker.cancel()
		delete(m.workers, symbol)
	}
	return nil
}

func (m *PositionMonitor) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for symbol, worker := range m.workers {
		worker.cancel()
		delete(m.workers, symbol)
	}
}

func (m *PositionMonitor) IsHealthy(symbol string) bool {
	m.mu.RLock()
	worker := m.workers[symbol]
	m.mu.RUnlock()
	if worker == nil {
		return false
	}
	worker.mu.RLock()
	lastEvent := worker.lastEvent
	worker.mu.RUnlock()
	if lastEvent.IsZero() {
		return false
	}
	return time.Since(lastEvent) <= m.staleAfter
}

func (m *PositionMonitor) ensureWorker(symbol string) error {
	m.mu.Lock()
	if _, exists := m.workers[symbol]; exists {
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	events, cancel, err := m.stream.Subscribe(symbol)
	if err != nil {
		return err
	}

	worker := &positionWorker{symbol: symbol, cancel: cancel}
	m.mu.Lock()
	if _, exists := m.workers[symbol]; exists {
		m.mu.Unlock()
		cancel()
		return nil
	}
	m.workers[symbol] = worker
	m.mu.Unlock()

	go m.runWorker(worker, events)
	return nil
}

func (m *PositionMonitor) runWorker(worker *positionWorker, events <-chan PriceEvent) {
	for event := range events {
		worker.mu.Lock()
		worker.lastEvent = event.Timestamp
		worker.lastSource = event.Source
		worker.mu.Unlock()

		if err := m.processPriceEvent(worker.symbol, event); err != nil && err != gorm.ErrRecordNotFound {
			log.Printf("position monitor failed for %s: %v", worker.symbol, err)
		}
	}

	m.mu.Lock()
	if current := m.workers[worker.symbol]; current == worker {
		delete(m.workers, worker.symbol)
	}
	m.mu.Unlock()
}

func (m *PositionMonitor) processPriceEvent(symbol string, event PriceEvent) error {
	var position database.Position
	if err := database.DB.Where("symbol = ? AND status = ?", symbol, "open").First(&position).Error; err != nil {
		return err
	}

	markPrice := event.MarkPrice
	if markPrice <= 0 {
		markPrice = event.LastPrice
	}
	if markPrice <= 0 {
		return nil
	}

	settings := GetAllSettings()
	policy := BuildExitPolicy(settings)
	position.CurrentPrice = floatPtr(markPrice)
	position.LastMarkPrice = floatPtr(markPrice)
	position.LastMarkAt = &event.Timestamp
	position.Pnl = (markPrice - position.AvgPrice) * position.Amount
	if position.AvgPrice > 0 {
		position.PnlPercent = ((markPrice - position.AvgPrice) / position.AvgPrice) * 100
	}

	entryPrice := position.AvgPrice
	if position.EntryPrice != nil && *position.EntryPrice > 0 {
		entryPrice = *position.EntryPrice
	}

	if policy.ATRTrailingEnabled && position.LastAtrValue != nil {
		position.TrailingStopPrice = RatchetATRTrailingStop(position.TrailingStopPrice, markPrice, entryPrice, *position.LastAtrValue, policy.ATRTrailingMult)
	} else if policy.TrailingStopEnabled {
		position.TrailingStopPrice = RatchetPercentTrailingStop(position.TrailingStopPrice, markPrice, entryPrice, policy.TrailingStopPercent)
	}

	if err := database.DB.Save(&position).Error; err != nil {
		return err
	}

	if position.ExitPending {
		return nil
	}

	decision := EvaluateProtectiveExit(ExitEvaluationInput{
		CurrentPrice:      markPrice,
		HighPrice:         markPrice,
		LowPrice:          markPrice,
		EntryPrice:        entryPrice,
		StopPrice:         position.StopPrice,
		TakeProfitPrice:   position.TakeProfitPrice,
		TrailingStopPrice: position.TrailingStopPrice,
		ExecutionMode:     position.ExecutionMode,
		ObservedAt:        event.Timestamp,
	}, policy)
	if decision.Reason == "" {
		return nil
	}

	_, err := m.coordinator.RequestClose(CloseRequest{
		PositionID:     position.ID,
		Reason:         decision.Reason,
		RequestedPrice: decision.TriggerPrice,
		TriggeredAt:    event.Timestamp,
		Source:         event.Source,
	})
	return err
}

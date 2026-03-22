package services

import (
	"log"
	"sync"
	"time"
	"trading-go/internal/database"
)

type StreamSupervisor struct {
	monitor *PositionMonitor

	mu      sync.RWMutex
	started bool
}

var streamSupervisor *StreamSupervisor

func StartExecutionRuntime() error {
	InitExecutionCoordinator(GetExchange())
	streamSupervisor = &StreamSupervisor{
		monitor: NewPositionMonitor(NewBinanceTickerStream(), GetExecutionCoordinator(), 90*time.Second),
	}
	streamSupervisor.started = true
	return streamSupervisor.ReconcileOpenPositions()
}

func StopExecutionRuntime() {
	if streamSupervisor == nil {
		return
	}
	streamSupervisor.monitor.Close()
	streamSupervisor = nil
}

func GetStreamSupervisor() *StreamSupervisor {
	return streamSupervisor
}

func NotifyPositionChanged() {
	if streamSupervisor == nil {
		return
	}
	if err := streamSupervisor.ReconcileOpenPositions(); err != nil {
		log.Printf("stream supervisor reconcile failed: %v", err)
	}
}

func (s *StreamSupervisor) ReconcileOpenPositions() error {
	if s == nil || s.monitor == nil {
		return nil
	}
	if !s.Enabled() {
		s.monitor.Close()
		return nil
	}

	var positions []database.Position
	if err := database.DB.Where("status = ?", "open").Find(&positions).Error; err != nil {
		return err
	}
	return s.monitor.Reconcile(positions)
}

func (s *StreamSupervisor) ShouldFallback(symbol string) bool {
	if s == nil || s.monitor == nil || !s.Enabled() {
		return true
	}
	return !s.monitor.IsHealthy(symbol)
}

// LogShadowComparison logs when a stream-driven exit would have triggered,
// providing operational visibility for shadow mode verification. This allows
// comparing stream exit timing against cron-based fallback exits.
func (s *StreamSupervisor) LogShadowComparison(symbol string, streamTriggeredAt time.Time, reason string) {
	log.Printf("SHADOW_COMPARE symbol=%s stream_triggered_at=%s reason=%s",
		symbol, streamTriggeredAt.UTC().Format(time.RFC3339Nano), reason)
}

func (s *StreamSupervisor) Enabled() bool {
	settings := GetAllSettings()
	return getSettingBool(settings, "stream_exit_enabled", true)
}

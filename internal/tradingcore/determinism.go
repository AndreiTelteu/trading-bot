package tradingcore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

type FixedClock struct{ instant time.Time }

func NewFixedClock(instant time.Time) FixedClock { return FixedClock{instant: instant} }
func (clock FixedClock) Now() time.Time          { return clock.instant }

type IDGenerator interface {
	NewID() (string, error)
}

type RandomIDGenerator struct{ Prefix string }

func (generator RandomIDGenerator) NewID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate random id: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	id := fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32])
	if generator.Prefix != "" {
		return generator.Prefix + "-" + id, nil
	}
	return id, nil
}

type SequenceIDGenerator struct {
	mu     sync.Mutex
	prefix string
	next   uint64
}

func NewSequenceIDGenerator(prefix string, first uint64) *SequenceIDGenerator {
	return &SequenceIDGenerator{prefix: prefix, next: first}
}

func (generator *SequenceIDGenerator) NewID() (string, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	value := generator.next
	generator.next++
	if generator.prefix == "" {
		return fmt.Sprintf("%d", value), nil
	}
	return fmt.Sprintf("%s-%d", generator.prefix, value), nil
}

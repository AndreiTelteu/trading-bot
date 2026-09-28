package decisionmodel

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrCacheConflict reports an attempt to store different content under an
// existing immutable request digest.
var ErrCacheConflict = errors.New("decision model cache record conflicts with stored content")

// MemoryStore is an immutable in-process Store for tests and offline tools.
// It enforces the same insert-once/verify-equal contract as the PostgreSQL
// store.
type MemoryStore struct {
	mu      sync.Mutex
	records map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{records: map[string][]byte{}}
}

func (s *MemoryStore) Load(_ context.Context, digest string) (Response, bool, error) {
	s.mu.Lock()
	payload, ok := s.records[digest]
	s.mu.Unlock()
	if !ok {
		return Response{}, false, nil
	}
	response, err := decodeStoredResponse(payload)
	if err != nil {
		return Response{}, false, fmt.Errorf("%w: stored response cannot be decoded: %v", ErrCacheCorrupt, err)
	}
	response.RequestDigest = digest
	return response, true, nil
}

func (s *MemoryStore) Save(_ context.Context, request Request, response Response) error {
	if err := validateStorable(request, response); err != nil {
		return err
	}
	payload, err := canonicalResponseJSON(response)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.records[response.RequestDigest]; ok {
		oldResponse, oldErr := decodeStoredResponse(existing)
		newResponse, newErr := decodeStoredResponse(payload)
		if oldErr != nil || newErr != nil {
			return ErrCacheCorrupt
		}
		if !equalResponses(oldResponse, newResponse) {
			return fmt.Errorf("%w: %s", ErrCacheConflict, response.RequestDigest)
		}
		return nil
	}
	s.records[response.RequestDigest] = payload
	return nil
}

// Len reports the number of stored responses.
func (s *MemoryStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

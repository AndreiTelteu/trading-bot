package decisionmodel

import (
	"context"
	"fmt"
	"reflect"

	"gorm.io/gorm"
)

// PostgresStore persists immutable, digest-addressed provider evidence.
// The supplied pool must use the runtime role, never the migration role.
type PostgresStore struct{ db *gorm.DB }

func NewPostgresStore(db *gorm.DB) *PostgresStore { return &PostgresStore{db: db} }

type responseRow struct {
	RequestDigest string
	ModelIdentity string
	Driver        string
	ResolvedModel string
	RequestJSON   []byte
	ResponseJSON  []byte
	InputTokens   *int
	OutputTokens  *int
}

func (s *PostgresStore) Load(ctx context.Context, digest string) (Response, bool, error) {
	if s == nil || s.db == nil {
		return Response{}, false, fmt.Errorf("decisionmodel: PostgreSQL store is not configured")
	}
	if !digestPattern.MatchString(digest) {
		return Response{}, false, fmt.Errorf("%w: malformed request digest", ErrInvalidRequest)
	}
	var row responseRow
	result := s.db.WithContext(ctx).Raw(`SELECT request_digest, model_identity, driver, resolved_model,
		request_json, response_json, input_tokens, output_tokens
		FROM decision_model_responses WHERE request_digest = ?`, digest).Scan(&row)
	if result.Error != nil {
		return Response{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		return Response{}, false, nil
	}
	identity, driver, request, err := decodeStoredRequest(row.RequestJSON)
	if err != nil {
		return Response{}, false, fmt.Errorf("%w: request cannot be decoded: %v", ErrCacheCorrupt, err)
	}
	response, err := decodeStoredResponse(row.ResponseJSON)
	if err != nil {
		return Response{}, false, fmt.Errorf("%w: response cannot be decoded: %v", ErrCacheCorrupt, err)
	}
	response.RequestDigest = digest
	if identity != row.ModelIdentity || driver != row.Driver || response.ModelIdentity != row.ModelIdentity ||
		response.Driver != row.Driver || response.ResolvedModel != row.ResolvedModel ||
		!reflect.DeepEqual(response.Usage.InputTokens, row.InputTokens) ||
		!reflect.DeepEqual(response.Usage.OutputTokens, row.OutputTokens) ||
		validateStorable(request, response) != nil {
		return Response{}, false, ErrCacheCorrupt
	}
	return response, true, nil
}

func (s *PostgresStore) Save(ctx context.Context, request Request, response Response) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("decisionmodel: PostgreSQL store is not configured")
	}
	if err := validateStorable(request, response); err != nil {
		return err
	}
	requestJSON := canonicalRequestJSON(response.ModelIdentity, response.Driver, request)
	responseJSON, err := canonicalResponseJSON(response)
	if err != nil {
		return err
	}
	err = s.db.WithContext(ctx).Exec(`INSERT INTO decision_model_responses
		(request_digest, model_identity, driver, resolved_model, request_json, response_json, input_tokens, output_tokens)
		VALUES (?, ?, ?, ?, CAST(? AS jsonb), CAST(? AS jsonb), ?, ?)
		ON CONFLICT (request_digest) DO NOTHING`, response.RequestDigest, response.ModelIdentity,
		response.Driver, response.ResolvedModel, string(requestJSON), string(responseJSON),
		response.Usage.InputTokens, response.Usage.OutputTokens).Error
	if err != nil {
		return err
	}
	stored, found, err := s.Load(ctx, response.RequestDigest)
	if err != nil {
		return err
	}
	if !found || !equalResponses(stored, response) {
		return ErrCacheConflict
	}
	return nil
}

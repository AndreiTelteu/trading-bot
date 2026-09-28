// Package decisionmodel is the provider-neutral boundary for structured
// decision/classifier models. A model identity has the form
// "<provider>/<model>", for example "aihubmix/decision-model-preview".
// Providers select a wire driver; the first driver is
// "openai-compatible-systemone" (POST {base}/v1/systemone).
//
// Model output is advisory research evidence. It never bypasses strategy,
// risk, governance, or accounting invariants. Replays must be served from the
// immutable PostgreSQL response cache so that historical runs are deterministic.
package decisionmodel

import (
	"context"
	"errors"
	"fmt"
)

type QuestionType string

const (
	QuestionScore  QuestionType = "score"
	QuestionChoice QuestionType = "choice"
	QuestionNoul   QuestionType = "noul"
)

// Question is one isolated typed question evaluated against the same state.
// Score criteria are ordered levels (lowest first); choice criteria map an
// option key to its meaning; noul has no criteria.
type Question struct {
	Type           QuestionType
	Instructions   string
	ScoreCriteria  []string
	ChoiceCriteria map[string]string
}

type Request struct {
	State     string
	Questions map[string]Question
}

type Answer struct {
	Type QuestionType
	// Score is the provider-reported level index (may be fractional).
	Score float64
	// Choice is one of the request's ChoiceCriteria keys.
	Choice string
	// Probability is the noul "yes" probability in [0,1].
	Probability float64
	// Probabilities is keyed by score level index ("0".."n-1") or choice key.
	Probabilities map[string]float64
	Confidence    float64
}

type Usage struct {
	InputTokens  *int
	OutputTokens *int
}

type Response struct {
	// ModelIdentity is the requested "<provider>/<model>" identity.
	ModelIdentity string
	// Driver is the wire driver that produced the response; it is part of the
	// request digest and of the immutable cache record.
	Driver string
	// ResolvedModel is the provider-echoed upstream build, e.g. "jev-1.13.0".
	ResolvedModel string
	RequestDigest string
	Answers       map[string]Answer
	Usage         Usage
	// Cached is true when the response was served from the immutable store.
	Cached bool
}

// Model is a resolved provider/model pair ready to answer requests.
type Model interface {
	Identity() string
	Decide(ctx context.Context, request Request) (Response, error)
}

// Endpoint is the resolved transport target for a driver call. Token values
// must never be logged, persisted, or included in errors.
type Endpoint struct {
	BaseURL string
	Token   string `json:"-"`
}

func (e Endpoint) String() string {
	return fmt.Sprintf("Endpoint{BaseURL:%q Token:[redacted]}", e.BaseURL)
}
func (e Endpoint) GoString() string { return e.String() }

// Driver implements one wire protocol shared by several providers.
type Driver interface {
	Name() string
	Decide(ctx context.Context, endpoint Endpoint, model string, request Request) (Response, error)
}

// Store is the immutable response cache keyed by RequestDigest.
type Store interface {
	Load(ctx context.Context, digest string) (Response, bool, error)
	Save(ctx context.Context, request Request, response Response) error
}

var (
	ErrUnknownProvider = errors.New("decision model provider is not registered")
	ErrUnknownDriver   = errors.New("decision model driver is not registered")
	ErrMissingToken    = errors.New("decision model provider token is not configured")
	ErrInvalidRequest  = errors.New("decision model request is invalid")
	ErrInvalidResponse = errors.New("decision model response is invalid")
	ErrUnavailable     = errors.New("decision model is unavailable")
	ErrCacheCorrupt    = errors.New("decision model cache record is corrupt")
)

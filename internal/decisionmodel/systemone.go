package decisionmodel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SystemOneDriverName is the wire driver for POST {base}/v1/systemone.
const SystemOneDriverName = "openai-compatible-systemone"

const (
	// SystemOneTimeout is the default per-attempt HTTP client timeout.
	SystemOneTimeout = 20 * time.Second
	// MaxResponseBytes bounds every provider response body.
	MaxResponseBytes  = 1 << 20
	maxErrorBodyBytes = 64 << 10
	systemOnePath     = "/v1/systemone"
)

// ErrTransient marks a failure that may succeed when retried (timeouts,
// network errors, HTTP 429 and 5xx). It is always combined with
// ErrUnavailable.
var ErrTransient = errors.New("decision model failure is transient")

var tidPattern = regexp.MustCompile(`tid:\s*([A-Za-z0-9_-]+)`)
var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
var resolvedModelPattern = regexp.MustCompile(`^[A-Za-z0-9._:/-]{1,200}$`)

type systemOneDriver struct {
	client *http.Client
}

// NewSystemOneDriver returns the openai-compatible-systemone driver. A nil
// client uses a dedicated client with SystemOneTimeout.
func NewSystemOneDriver(client *http.Client) Driver {
	if client == nil {
		client = &http.Client{}
	}
	clone := *client
	if clone.Timeout == 0 || clone.Timeout > SystemOneTimeout {
		clone.Timeout = SystemOneTimeout
	}
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &systemOneDriver{client: &clone}
}

func (d *systemOneDriver) Name() string { return SystemOneDriverName }

type systemOneRequest struct {
	Model     string                       `json:"model"`
	State     string                       `json:"state"`
	Questions map[string]canonicalQuestion `json:"questions"`
}

type systemOneAnswer struct {
	Type          QuestionType        `json:"type"`
	Score         *float64            `json:"score"`
	Legend        map[string]string   `json:"legend"`
	Choice        *string             `json:"choice"`
	Noul          *float64            `json:"noul"`
	Probabilities *map[string]float64 `json:"probabilities"`
	Confidence    *float64            `json:"confidence"`
}

type systemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]json.RawMessage `json:"answers"`
	Usage   *struct {
		InputTokens  *int `json:"input_tokens"`
		OutputTokens *int `json:"output_tokens"`
	} `json:"usage"`
}

func (d *systemOneDriver) Decide(ctx context.Context, endpoint Endpoint, model string, request Request) (Response, error) {
	if err := ValidateRequest(request); err != nil {
		return Response{}, err
	}
	if strings.TrimSpace(model) == "" {
		return Response{}, fmt.Errorf("%w: provider model is empty", ErrInvalidRequest)
	}
	if endpoint.Token == "" {
		return Response{}, ErrMissingToken
	}
	target, err := systemOneURL(endpoint.BaseURL)
	if err != nil {
		return Response{}, err
	}
	body, err := json.Marshal(systemOneRequest{Model: model, State: request.State, Questions: canonicalQuestions(request.Questions)})
	if err != nil {
		return Response{}, fmt.Errorf("%w: encode request: %v", ErrInvalidRequest, err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("%w: build request: %s", ErrInvalidRequest, redact(err.Error(), endpoint.Token))
	}
	httpRequest.Header.Set("Authorization", "Bearer "+endpoint.Token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	httpResponse, err := d.client.Do(httpRequest)
	if err != nil {
		return Response{}, transportError(ctx, err, endpoint.Token)
	}
	defer httpResponse.Body.Close()

	if httpResponse.StatusCode < 200 || httpResponse.StatusCode > 299 {
		payload, _ := io.ReadAll(io.LimitReader(httpResponse.Body, maxErrorBodyBytes))
		return Response{}, statusError(httpResponse, payload, endpoint.Token)
	}
	payload, err := io.ReadAll(io.LimitReader(httpResponse.Body, MaxResponseBytes+1))
	if err != nil {
		return Response{}, transportError(ctx, err, endpoint.Token)
	}
	if len(payload) > MaxResponseBytes {
		return Response{}, fmt.Errorf("%w: response body exceeds %d bytes", ErrInvalidResponse, MaxResponseBytes)
	}
	parsed, err := parseSystemOneResponse(payload, request)
	if err != nil {
		return Response{}, err
	}
	if strings.Contains(parsed.ResolvedModel, endpoint.Token) {
		return Response{}, fmt.Errorf("%w: unsafe resolved model", ErrInvalidResponse)
	}
	return parsed, nil
}

func systemOneURL(base string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || parsed.Scheme != "https" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.Opaque != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", fmt.Errorf("%w: endpoint base URL must be https://host without path, query, or credentials", ErrInvalidRequest)
	}
	return parsed.Scheme + "://" + parsed.Host + systemOnePath, nil
}

func transportError(ctx context.Context, err error, token string) error {
	message := redact(err.Error(), token)
	if ctxErr := ctx.Err(); ctxErr != nil {
		// The caller cancelled; do not retry.
		return ctxErr
	}
	// Client timeouts, connection failures, and truncated bodies are all
	// network faults and therefore transient.
	return fmt.Errorf("%w: %w: transport: %s", ErrUnavailable, ErrTransient, message)
}

func statusError(response *http.Response, payload []byte, token string) error {
	detail := "status " + strconv.Itoa(response.StatusCode)
	if tid := providerTID(response, payload, token); tid != "" {
		detail += " tid " + tid
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return fmt.Errorf("%w: %w: %s", ErrUnavailable, ErrTransient, detail)
	}
	return fmt.Errorf("%w: %s", ErrUnavailable, detail)
}

// providerTID extracts only a provider trace identifier. The raw error body is
// never echoed because providers may reflect request material.
func providerTID(response *http.Response, payload []byte, token string) string {
	if match := tidPattern.FindSubmatch(payload); match != nil {
		value := string(match[1])
		if len(value) <= 64 && (token == "" || (!strings.Contains(value, token) && !strings.Contains(token, value))) {
			return value
		}
	}
	for _, header := range []string{"X-Aihubmix-Request-Id", "X-Request-Id"} {
		if value := strings.TrimSpace(response.Header.Get(header)); safeIDPattern.MatchString(value) && (token == "" || (!strings.Contains(value, token) && !strings.Contains(token, value))) {
			return value
		}
	}
	return ""
}

func redact(message, token string) string {
	if token == "" {
		return message
	}
	return strings.ReplaceAll(message, token, "[redacted]")
}

func finiteUnit(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 1
}

func parseSystemOneResponse(payload []byte, request Request) (Response, error) {
	var decoded systemOneResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return Response{}, fmt.Errorf("%w: malformed JSON", ErrInvalidResponse)
	}
	resolved := decoded.Model
	if !resolvedModelPattern.MatchString(resolved) {
		return Response{}, fmt.Errorf("%w: missing resolved model", ErrInvalidResponse)
	}
	if decoded.Answers == nil {
		return Response{}, fmt.Errorf("%w: missing answers", ErrInvalidResponse)
	}
	response := Response{ResolvedModel: resolved, Answers: make(map[string]Answer, len(decoded.Answers))}
	if decoded.Usage != nil {
		if decoded.Usage.InputTokens != nil {
			response.Usage.InputTokens = decoded.Usage.InputTokens
		}
		if decoded.Usage.OutputTokens != nil {
			response.Usage.OutputTokens = decoded.Usage.OutputTokens
		}
		if !validUsage(response.Usage.InputTokens) || !validUsage(response.Usage.OutputTokens) {
			return Response{}, fmt.Errorf("%w: negative token usage", ErrInvalidResponse)
		}
	}
	for name, raw := range decoded.Answers {
		question, ok := request.Questions[name]
		if !ok {
			return Response{}, fmt.Errorf("%w: unexpected answer %q", ErrInvalidResponse, name)
		}
		var wire systemOneAnswer
		if err := json.Unmarshal(raw, &wire); err != nil {
			return Response{}, fmt.Errorf("%w: answer %q is malformed", ErrInvalidResponse, name)
		}
		if wire.Type != question.Type {
			return Response{}, fmt.Errorf("%w: answer %q type %q does not match %q", ErrInvalidResponse, name, wire.Type, question.Type)
		}
		answer := Answer{Type: wire.Type}
		if wire.Confidence != nil {
			if !finiteUnit(wire.Confidence) {
				return Response{}, fmt.Errorf("%w: answer %q confidence outside [0,1]", ErrInvalidResponse, name)
			}
			answer.Confidence = *wire.Confidence
		}
		if wire.Probabilities != nil {
			if len(*wire.Probabilities) == 0 {
				return Response{}, fmt.Errorf("%w: answer %q has empty probabilities", ErrInvalidResponse, name)
			}
			answer.Probabilities = *wire.Probabilities
		}
		switch wire.Type {
		case QuestionScore:
			if wire.Score == nil || wire.Choice != nil || wire.Noul != nil {
				return Response{}, fmt.Errorf("%w: score answer %q has wrong fields", ErrInvalidResponse, name)
			}
			answer.Score = *wire.Score
			for key, label := range wire.Legend {
				index, err := strconv.Atoi(key)
				if err != nil || strconv.Itoa(index) != key || index < 0 || index >= len(question.ScoreCriteria) || label != question.ScoreCriteria[index] {
					return Response{}, fmt.Errorf("%w: answer %q legend does not match requested levels", ErrInvalidResponse, name)
				}
			}
		case QuestionChoice:
			if wire.Choice == nil || wire.Score != nil || wire.Noul != nil || len(wire.Legend) != 0 {
				return Response{}, fmt.Errorf("%w: choice answer %q has wrong fields", ErrInvalidResponse, name)
			}
			answer.Choice = *wire.Choice
		case QuestionNoul:
			if wire.Noul == nil || wire.Score != nil || wire.Choice != nil || wire.Legend != nil || wire.Probabilities != nil {
				return Response{}, fmt.Errorf("%w: noul answer %q has wrong fields", ErrInvalidResponse, name)
			}
			if !finiteUnit(wire.Noul) {
				return Response{}, fmt.Errorf("%w: answer %q noul outside [0,1]", ErrInvalidResponse, name)
			}
			answer.Probability = *wire.Noul
		default:
			return Response{}, fmt.Errorf("%w: answer %q has unsupported type", ErrInvalidResponse, name)
		}
		response.Answers[name] = answer
	}
	if err := ValidateAnswers(request, response.Answers); err != nil {
		return Response{}, err
	}
	return response, nil
}

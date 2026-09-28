package decisionmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RequestSchemaVersion identifies the canonical request encoding hashed by
// RequestDigest. Changing the encoding requires a new schema version.
const RequestSchemaVersion = "decision-model-request-v1"

// ResponseSchemaVersion identifies the canonical cached response encoding.
const ResponseSchemaVersion = "decision-model-response-v1"

const (
	maxStateBytes        = 64 << 10
	maxQuestions         = 32
	maxInstructionsBytes = 8 << 10
	maxCriteria          = 32
	maxCriterionBytes    = 1 << 10
	// probabilitySumTolerance accepts provider rounding of reported
	// probabilities (two decimals per entry) while rejecting nonsense vectors.
	probabilitySumTolerance = 0.02
)

var (
	questionNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	digestPattern       = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// canonicalQuestion is both the digest form and the systemone wire form of a
// question. Criteria is a []string for score and a map[string]string for
// choice (encoding/json sorts map keys), and absent for noul.
type canonicalQuestion struct {
	Type         QuestionType `json:"type"`
	Instructions string       `json:"instructions"`
	Criteria     any          `json:"criteria,omitempty"`
}

type canonicalRequest struct {
	Schema        string                       `json:"schema"`
	ModelIdentity string                       `json:"model_identity"`
	Driver        string                       `json:"driver"`
	State         string                       `json:"state"`
	Questions     map[string]canonicalQuestion `json:"questions"`
}

// storedQuestion is used only to decode persisted canonical requests.
type storedQuestion struct {
	Type         QuestionType    `json:"type"`
	Instructions string          `json:"instructions"`
	Criteria     json.RawMessage `json:"criteria,omitempty"`
}

type storedRequest struct {
	Schema        string                    `json:"schema"`
	ModelIdentity string                    `json:"model_identity"`
	Driver        string                    `json:"driver"`
	State         string                    `json:"state"`
	Questions     map[string]storedQuestion `json:"questions"`
}

func canonicalQuestions(questions map[string]Question) map[string]canonicalQuestion {
	out := make(map[string]canonicalQuestion, len(questions))
	for name, question := range questions {
		cq := canonicalQuestion{Type: question.Type, Instructions: question.Instructions}
		switch question.Type {
		case QuestionScore:
			criteria := make([]string, len(question.ScoreCriteria))
			copy(criteria, question.ScoreCriteria)
			cq.Criteria = criteria
		case QuestionChoice:
			criteria := make(map[string]string, len(question.ChoiceCriteria))
			for key, meaning := range question.ChoiceCriteria {
				criteria[key] = meaning
			}
			cq.Criteria = criteria
		}
		out[name] = cq
	}
	return out
}

func canonicalRequestJSON(identity, driver string, request Request) []byte {
	payload, err := json.Marshal(canonicalRequest{
		Schema:        RequestSchemaVersion,
		ModelIdentity: identity,
		Driver:        driver,
		State:         request.State,
		Questions:     canonicalQuestions(request.Questions),
	})
	if err != nil {
		// Only strings, slices of strings, and string maps are encoded.
		panic(fmt.Sprintf("decisionmodel: canonical request encoding failed: %v", err))
	}
	return payload
}

// RequestDigest is the lowercase sha256 hex digest of the canonical JSON
// {schema, model_identity, driver, state, questions}. It is the immutable
// cache key; it never includes endpoints or credentials.
func RequestDigest(identity, driver string, req Request) string {
	sum := sha256.Sum256(canonicalRequestJSON(identity, driver, req))
	return hex.EncodeToString(sum[:])
}

func decodeStoredRequest(payload []byte) (identity, driver string, request Request, err error) {
	var stored storedRequest
	if err := json.Unmarshal(payload, &stored); err != nil {
		return "", "", Request{}, err
	}
	if stored.Schema != RequestSchemaVersion {
		return "", "", Request{}, fmt.Errorf("unsupported request schema %q", stored.Schema)
	}
	request = Request{State: stored.State, Questions: make(map[string]Question, len(stored.Questions))}
	for name, sq := range stored.Questions {
		question := Question{Type: sq.Type, Instructions: sq.Instructions}
		switch sq.Type {
		case QuestionScore:
			if err := json.Unmarshal(sq.Criteria, &question.ScoreCriteria); err != nil {
				return "", "", Request{}, fmt.Errorf("question %q score criteria: %w", name, err)
			}
		case QuestionChoice:
			if err := json.Unmarshal(sq.Criteria, &question.ChoiceCriteria); err != nil {
				return "", "", Request{}, fmt.Errorf("question %q choice criteria: %w", name, err)
			}
		case QuestionNoul:
			if len(sq.Criteria) != 0 {
				return "", "", Request{}, fmt.Errorf("question %q noul has criteria", name)
			}
		}
		request.Questions[name] = question
	}
	return stored.ModelIdentity, stored.Driver, request, nil
}

// ValidateRequest checks the typed question contract before any digest,
// cache, or provider interaction.
func ValidateRequest(request Request) error {
	if strings.TrimSpace(request.State) == "" {
		return fmt.Errorf("%w: state is empty", ErrInvalidRequest)
	}
	if len(request.State) > maxStateBytes {
		return fmt.Errorf("%w: state exceeds %d bytes", ErrInvalidRequest, maxStateBytes)
	}
	if len(request.Questions) == 0 {
		return fmt.Errorf("%w: no questions", ErrInvalidRequest)
	}
	if len(request.Questions) > maxQuestions {
		return fmt.Errorf("%w: more than %d questions", ErrInvalidRequest, maxQuestions)
	}
	for _, name := range sortedQuestionNames(request.Questions) {
		question := request.Questions[name]
		if !questionNamePattern.MatchString(name) {
			return fmt.Errorf("%w: invalid question name %q", ErrInvalidRequest, name)
		}
		if strings.TrimSpace(question.Instructions) == "" || len(question.Instructions) > maxInstructionsBytes {
			return fmt.Errorf("%w: question %q instructions are empty or too long", ErrInvalidRequest, name)
		}
		switch question.Type {
		case QuestionScore:
			if len(question.ChoiceCriteria) != 0 {
				return fmt.Errorf("%w: score question %q has choice criteria", ErrInvalidRequest, name)
			}
			if len(question.ScoreCriteria) < 2 || len(question.ScoreCriteria) > maxCriteria {
				return fmt.Errorf("%w: score question %q needs 2..%d levels", ErrInvalidRequest, name, maxCriteria)
			}
			for _, level := range question.ScoreCriteria {
				if strings.TrimSpace(level) == "" || len(level) > maxCriterionBytes {
					return fmt.Errorf("%w: score question %q has an empty or oversized level", ErrInvalidRequest, name)
				}
			}
		case QuestionChoice:
			if len(question.ScoreCriteria) != 0 {
				return fmt.Errorf("%w: choice question %q has score criteria", ErrInvalidRequest, name)
			}
			if len(question.ChoiceCriteria) < 2 || len(question.ChoiceCriteria) > maxCriteria {
				return fmt.Errorf("%w: choice question %q needs 2..%d options", ErrInvalidRequest, name, maxCriteria)
			}
			for key, meaning := range question.ChoiceCriteria {
				if !questionNamePattern.MatchString(key) || strings.TrimSpace(meaning) == "" || len(meaning) > maxCriterionBytes {
					return fmt.Errorf("%w: choice question %q has an invalid option", ErrInvalidRequest, name)
				}
			}
		case QuestionNoul:
			if len(question.ScoreCriteria) != 0 || len(question.ChoiceCriteria) != 0 {
				return fmt.Errorf("%w: noul question %q must not have criteria", ErrInvalidRequest, name)
			}
		default:
			return fmt.Errorf("%w: question %q has unsupported type %q", ErrInvalidRequest, name, question.Type)
		}
	}
	return nil
}

func sortedQuestionNames(questions map[string]Question) []string {
	names := make([]string, 0, len(questions))
	for name := range questions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func unitInterval(value float64) bool {
	return !math.IsNaN(value) && value >= 0 && value <= 1
}

// ValidateAnswers checks that answers exactly cover the request with matching
// types and in-range values. It is applied to live responses and to every
// cache load so corrupt evidence cannot be served.
func ValidateAnswers(request Request, answers map[string]Answer) error {
	if len(answers) != len(request.Questions) {
		return fmt.Errorf("%w: %d answers for %d questions", ErrInvalidResponse, len(answers), len(request.Questions))
	}
	for name := range answers {
		if _, ok := request.Questions[name]; !ok {
			return fmt.Errorf("%w: unexpected answer %q", ErrInvalidResponse, name)
		}
	}
	for _, name := range sortedQuestionNames(request.Questions) {
		question := request.Questions[name]
		answer, ok := answers[name]
		if !ok {
			return fmt.Errorf("%w: missing answer %q", ErrInvalidResponse, name)
		}
		if answer.Type != question.Type {
			return fmt.Errorf("%w: answer %q type %q does not match %q", ErrInvalidResponse, name, answer.Type, question.Type)
		}
		if !unitInterval(answer.Confidence) {
			return fmt.Errorf("%w: answer %q confidence outside [0,1]", ErrInvalidResponse, name)
		}
		var allowed map[string]bool
		switch question.Type {
		case QuestionScore:
			top := float64(len(question.ScoreCriteria) - 1)
			if math.IsNaN(answer.Score) || answer.Score < 0 || answer.Score > top {
				return fmt.Errorf("%w: answer %q score outside [0,%d]", ErrInvalidResponse, name, len(question.ScoreCriteria)-1)
			}
			if answer.Choice != "" || answer.Probability != 0 {
				return fmt.Errorf("%w: score answer %q carries choice/noul values", ErrInvalidResponse, name)
			}
			allowed = make(map[string]bool, len(question.ScoreCriteria))
			for index := range question.ScoreCriteria {
				allowed[strconv.Itoa(index)] = true
			}
		case QuestionChoice:
			if _, ok := question.ChoiceCriteria[answer.Choice]; !ok {
				return fmt.Errorf("%w: answer %q choice is not a requested option", ErrInvalidResponse, name)
			}
			if answer.Score != 0 || answer.Probability != 0 {
				return fmt.Errorf("%w: choice answer %q carries score/noul values", ErrInvalidResponse, name)
			}
			allowed = make(map[string]bool, len(question.ChoiceCriteria))
			for key := range question.ChoiceCriteria {
				allowed[key] = true
			}
		case QuestionNoul:
			if !unitInterval(answer.Probability) {
				return fmt.Errorf("%w: answer %q noul outside [0,1]", ErrInvalidResponse, name)
			}
			if answer.Score != 0 || answer.Choice != "" || len(answer.Probabilities) != 0 {
				return fmt.Errorf("%w: noul answer %q carries score/choice values", ErrInvalidResponse, name)
			}
		default:
			return fmt.Errorf("%w: answer %q has unsupported type", ErrInvalidResponse, name)
		}
		if len(answer.Probabilities) > 0 {
			sum := 0.0
			for key, probability := range answer.Probabilities {
				if !allowed[key] {
					return fmt.Errorf("%w: answer %q has probability for unknown key %q", ErrInvalidResponse, name, key)
				}
				if !unitInterval(probability) {
					return fmt.Errorf("%w: answer %q probability outside [0,1]", ErrInvalidResponse, name)
				}
				sum += probability
			}
			if math.Abs(sum-1) > probabilitySumTolerance {
				return fmt.Errorf("%w: answer %q probabilities sum to %.4f", ErrInvalidResponse, name, sum)
			}
		}
	}
	return nil
}

// storedAnswer/storedResponse are the canonical persisted response form.
type storedAnswer struct {
	Type          QuestionType       `json:"type"`
	Score         float64            `json:"score"`
	Choice        string             `json:"choice"`
	Probability   float64            `json:"probability"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
}

type storedUsage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

type storedResponse struct {
	Schema        string                  `json:"schema"`
	ModelIdentity string                  `json:"model_identity"`
	Driver        string                  `json:"driver"`
	ResolvedModel string                  `json:"resolved_model"`
	Answers       map[string]storedAnswer `json:"answers"`
	Usage         storedUsage             `json:"usage"`
}

func canonicalResponseJSON(response Response) ([]byte, error) {
	stored := storedResponse{
		Schema:        ResponseSchemaVersion,
		ModelIdentity: response.ModelIdentity,
		Driver:        response.Driver,
		ResolvedModel: response.ResolvedModel,
		Answers:       make(map[string]storedAnswer, len(response.Answers)),
		Usage:         storedUsage{InputTokens: response.Usage.InputTokens, OutputTokens: response.Usage.OutputTokens},
	}
	for name, answer := range response.Answers {
		probabilities := map[string]float64{}
		for key, value := range answer.Probabilities {
			probabilities[key] = value
		}
		stored.Answers[name] = storedAnswer{Type: answer.Type, Score: answer.Score, Choice: answer.Choice, Probability: answer.Probability, Probabilities: probabilities, Confidence: answer.Confidence}
	}
	return json.Marshal(stored)
}

func decodeStoredResponse(payload []byte) (Response, error) {
	var stored storedResponse
	if err := json.Unmarshal(payload, &stored); err != nil {
		return Response{}, err
	}
	if stored.Schema != ResponseSchemaVersion {
		return Response{}, fmt.Errorf("unsupported response schema %q", stored.Schema)
	}
	response := Response{
		ModelIdentity: stored.ModelIdentity,
		Driver:        stored.Driver,
		ResolvedModel: stored.ResolvedModel,
		Answers:       make(map[string]Answer, len(stored.Answers)),
		Usage:         Usage{InputTokens: stored.Usage.InputTokens, OutputTokens: stored.Usage.OutputTokens},
	}
	for name, answer := range stored.Answers {
		var probabilities map[string]float64
		if len(answer.Probabilities) > 0 {
			probabilities = answer.Probabilities
		}
		response.Answers[name] = Answer{Type: answer.Type, Score: answer.Score, Choice: answer.Choice, Probability: answer.Probability, Probabilities: probabilities, Confidence: answer.Confidence}
	}
	return response, nil
}

// validateStorable checks the invariants shared by every Store.Save.
func validateStorable(request Request, response Response) error {
	if err := ValidateRequest(request); err != nil {
		return err
	}
	if response.ModelIdentity == "" || response.Driver == "" || !resolvedModelPattern.MatchString(response.ResolvedModel) {
		return fmt.Errorf("%w: response identity, driver, and resolved model are required", ErrInvalidResponse)
	}
	if !validUsage(response.Usage.InputTokens) || !validUsage(response.Usage.OutputTokens) {
		return fmt.Errorf("%w: negative token usage", ErrInvalidResponse)
	}
	if want := RequestDigest(response.ModelIdentity, response.Driver, request); response.RequestDigest != want {
		return fmt.Errorf("%w: response digest does not match request", ErrInvalidResponse)
	}
	return ValidateAnswers(request, response.Answers)
}

func validUsage(value *int) bool { return value == nil || (*value >= 0 && *value <= math.MaxInt32) }

func equalResponses(a, b Response) bool {
	a.RequestDigest, b.RequestDigest = "", ""
	a.Cached, b.Cached = false, false
	return reflect.DeepEqual(a, b)
}

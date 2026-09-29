package decisionmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "SENTINEL_SECRET_TOKEN"

func sampleRequest() Request {
	return Request{State: "anonymous state", Questions: map[string]Question{
		"score":  {Type: QuestionScore, Instructions: "score", ScoreCriteria: []string{"low", "high"}},
		"choice": {Type: QuestionChoice, Instructions: "choose", ChoiceCriteria: map[string]string{"buy": "long", "sell": "exit"}},
		"noul":   {Type: QuestionNoul, Instructions: "probability"},
	}}
}

const validWireResponse = `{"model":"jev-1.13.0","answers":{"score":{"type":"score","score":0.8,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.2,"1":0.8},"confidence":0.7},"choice":{"type":"choice","choice":"buy","legend":{},"probabilities":{"buy":0.8,"sell":0.2},"confidence":0.9},"noul":{"type":"noul","noul":0.6}},"usage":{"input_tokens":86}}`

func TestSystemOneWireShapeAndNullableUsage(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer "+testToken {
			t.Errorf("unexpected method, path or authorization")
		}
		var body struct {
			Model     string                     `json:"model"`
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "decision-model-preview" || body.State != "anonymous state" {
			t.Errorf("wire model/state mismatch")
		}
		for name, want := range map[string]string{"score": "\"criteria\":[\"low\",\"high\"]", "choice": "\"criteria\":{\"buy\":\"long\",\"sell\":\"exit\"}"} {
			if !strings.Contains(string(body.Questions[name]), want) {
				t.Errorf("%s wire criteria: %s", name, body.Questions[name])
			}
		}
		if strings.Contains(string(body.Questions["noul"]), "criteria") {
			t.Error("noul carried criteria")
		}
		_, _ = io.WriteString(w, validWireResponse)
	}))
	defer server.Close()
	response, err := NewSystemOneDriver(server.Client()).Decide(context.Background(), Endpoint{BaseURL: server.URL, Token: testToken}, "decision-model-preview", sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if response.ResolvedModel != "jev-1.13.0" || response.Usage.InputTokens == nil || *response.Usage.InputTokens != 86 || response.Usage.OutputTokens != nil {
		t.Fatalf("response metadata mismatch: %+v", response.Usage)
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", Endpoint{Token: testToken}, Endpoint{Token: testToken}, Endpoint{Token: testToken}), testToken) {
		t.Fatal("endpoint formatting leaked token")
	}
	encoded, _ := json.Marshal(Endpoint{Token: testToken})
	if strings.Contains(string(encoded), testToken) {
		t.Fatal("endpoint JSON leaked token")
	}
}

func TestSystemOneRejectsTokenReflectedAsResolvedModel(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, strings.Replace(validWireResponse, "jev-1.13.0", testToken, 1))
	}))
	defer server.Close()
	registry := NewRegistry()
	registry.RegisterDriver(NewSystemOneDriver(server.Client()))
	if err := registry.RegisterProvider(Provider{Name: "test", Driver: SystemOneDriverName,
		BaseURL: func() (string, error) { return server.URL, nil },
		Token:   func() (string, error) { return testToken, nil }}); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	model, err := registry.Resolve("test/model", store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = model.Decide(context.Background(), sampleRequest())
	if !errors.Is(err, ErrInvalidResponse) || strings.Contains(err.Error(), testToken) || len(store.records) != 0 {
		t.Fatal("reflected credential was accepted, cached, or exposed")
	}
}

func TestStrictResponseValidation(t *testing.T) {
	request := sampleRequest()
	for name, change := range map[string]func(map[string]any){
		"missing": func(v map[string]any) { delete(v["answers"].(map[string]any), "noul") },
		"extra": func(v map[string]any) {
			v["answers"].(map[string]any)["extra"] = map[string]any{"type": "noul", "noul": 0.5}
		},
		"type":  func(v map[string]any) { v["answers"].(map[string]any)["score"].(map[string]any)["type"] = "choice" },
		"score": func(v map[string]any) { v["answers"].(map[string]any)["score"].(map[string]any)["score"] = 2.0 },
		"legend": func(v map[string]any) {
			v["answers"].(map[string]any)["score"].(map[string]any)["legend"] = map[string]string{"0": "wrong"}
		},
		"choice": func(v map[string]any) { v["answers"].(map[string]any)["choice"].(map[string]any)["choice"] = "wait" },
		"probability": func(v map[string]any) {
			v["answers"].(map[string]any)["score"].(map[string]any)["probabilities"] = map[string]float64{"0": 1.0, "1": 0.09}
		},
		"noul":  func(v map[string]any) { v["answers"].(map[string]any)["noul"].(map[string]any)["noul"] = 1.2 },
		"model": func(v map[string]any) { v["model"] = "unsafe model" },
		"usage": func(v map[string]any) { v["usage"].(map[string]any)["input_tokens"] = int64(1) << 31 },
	} {
		t.Run(name, func(t *testing.T) {
			var payload map[string]any
			if err := json.Unmarshal([]byte(validWireResponse), &payload); err != nil {
				t.Fatal(err)
			}
			change(payload)
			encoded, _ := json.Marshal(payload)
			if _, err := parseSystemOneResponse(encoded, request); !errors.Is(err, ErrInvalidResponse) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, err := parseSystemOneResponse([]byte(validWireResponse), request); err != nil {
		t.Fatal(err)
	}
}

func TestRequestValidationAndDigest(t *testing.T) {
	request := sampleRequest()
	if err := ValidateRequest(request); err != nil {
		t.Fatal(err)
	}
	other := sampleRequest()
	other.Questions = map[string]Question{"noul": other.Questions["noul"], "choice": other.Questions["choice"], "score": other.Questions["score"]}
	if RequestDigest("test/model", SystemOneDriverName, request) != RequestDigest("test/model", SystemOneDriverName, other) {
		t.Fatal("map order changed digest")
	}
	if RequestDigest("test/model", SystemOneDriverName, request) == RequestDigest("test/other", SystemOneDriverName, request) {
		t.Fatal("model identity absent from digest")
	}
	provider, model, err := ParseIdentity("tokenrouter/typesafe/jev-1.13")
	if err != nil || provider != "tokenrouter" || model != "typesafe/jev-1.13" {
		t.Fatalf("nested provider model identity: %q %q %v", provider, model, err)
	}
	other.State = " "
	if err := ValidateRequest(other); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("empty state: %v", err)
	}
	other = sampleRequest()
	other.Questions["score"] = Question{Type: QuestionScore, Instructions: "score", ScoreCriteria: []string{"one"}}
	if err := ValidateRequest(other); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid criteria: %v", err)
	}
}

func TestHTTPRetryRedactionAndBodyLimit(t *testing.T) {
	for _, tc := range []struct {
		name             string
		status, attempts int
		body             string
		want             error
		sleeps           []time.Duration
	}{
		{"429", 429, 3, "tid: " + testToken, ErrUnavailable, []time.Duration{time.Second, 2 * time.Second}},
		{"503", 503, 3, "tid: " + testToken, ErrUnavailable, []time.Duration{time.Second, 2 * time.Second}},
		{"400", 400, 1, "tid: " + testToken, ErrUnavailable, nil},
		{"invalid", 200, 1, `{"model":"unsafe model"}`, ErrInvalidResponse, nil},
		{"oversize", 200, 1, strings.Repeat("x", MaxResponseBytes+1), ErrInvalidResponse, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			registry := NewRegistry()
			registry.RegisterDriver(NewSystemOneDriver(server.Client()))
			if err := registry.RegisterProvider(Provider{Name: "test", Driver: SystemOneDriverName, BaseURL: func() (string, error) { return server.URL, nil }, Token: func() (string, error) { return testToken, nil }}); err != nil {
				t.Fatal(err)
			}
			var sleeps []time.Duration
			registry.Sleep = func(_ context.Context, d time.Duration) error { sleeps = append(sleeps, d); return nil }
			store := NewMemoryStore()
			model, err := registry.Resolve("test/decision-model-preview", store)
			if err != nil {
				t.Fatal(err)
			}
			_, err = model.Decide(context.Background(), sampleRequest())
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), testToken) {
				t.Fatalf("error classification/redaction: %v", err)
			}
			if int(calls.Load()) != tc.attempts || !reflect.DeepEqual(sleeps, tc.sleeps) || store.Len() != 0 {
				t.Fatalf("calls=%d sleeps=%v cache=%d", calls.Load(), sleeps, store.Len())
			}
		})
	}
}

func TestContextAndClientControls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := transportError(ctx, errors.New(testToken), testToken); !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("cancel classification: %v", err)
	}
	client := &http.Client{Timeout: time.Minute}
	driver := NewSystemOneDriver(client).(*systemOneDriver)
	if driver.client.Timeout != SystemOneTimeout || driver.client.CheckRedirect == nil || client.CheckRedirect != nil {
		t.Fatal("injected client was not safely cloned")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/again", http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	driver = NewSystemOneDriver(server.Client()).(*systemOneDriver)
	_, err := driver.Decide(context.Background(), Endpoint{BaseURL: server.URL, Token: testToken}, "model", sampleRequest())
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "307") {
		t.Fatalf("redirect: %v", err)
	}
}

func TestBaseURLAndTokenLoading(t *testing.T) {
	for _, value := range []string{"http://host", "https://host/path", "https://host?x=1", "https://host?", "https://user@host", "https://host#fragment"} {
		if _, err := NormalizeBaseURL(value); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("accepted base URL %q", value)
		}
		if _, err := systemOneURL(value); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("driver accepted base URL %q", value)
		}
	}
	t.Setenv("DECISION_TEST_TOKEN", "")
	t.Setenv("DECISION_TEST_FILE", "")
	load := EnvToken("DECISION_TEST_TOKEN", "DECISION_TEST_FILE")
	if _, err := load(); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("missing env: %v", err)
	}
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte(testToken), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DECISION_TEST_FILE", path)
	if _, err := load(); !errors.Is(err, ErrMissingToken) || strings.Contains(err.Error(), path) {
		t.Fatalf("unsafe file: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if token, err := load(); err != nil || token != testToken {
		t.Fatal("safe token file did not load")
	}
}

func TestMemoryCacheCorruptionAndConflict(t *testing.T) {
	request := sampleRequest()
	response, err := parseSystemOneResponse([]byte(validWireResponse), request)
	if err != nil {
		t.Fatal(err)
	}
	response.ModelIdentity = "test/model"
	response.Driver = SystemOneDriverName
	response.RequestDigest = RequestDigest(response.ModelIdentity, response.Driver, request)
	store := NewMemoryStore()
	if err := store.Save(context.Background(), request, response); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), request, response); err != nil {
		t.Fatal(err)
	}
	changed := response
	changed.ResolvedModel = "other"
	if err := store.Save(context.Background(), request, changed); !errors.Is(err, ErrCacheConflict) {
		t.Fatalf("conflict: %v", err)
	}
	store.records[response.RequestDigest] = []byte("broken")
	_, _, err = store.Load(context.Background(), response.RequestDigest)
	if !errors.Is(err, ErrCacheCorrupt) || errors.Is(err, ErrInvalidResponse) || errors.Is(err, ErrUnavailable) {
		t.Fatalf("corrupt classification: %v", err)
	}
}

func TestCacheFirstReturnsSameDecisionFields(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, validWireResponse)
	}))
	defer server.Close()
	registry := NewRegistry()
	registry.RegisterDriver(NewSystemOneDriver(server.Client()))
	if err := registry.RegisterProvider(Provider{Name: "test", Driver: SystemOneDriverName,
		BaseURL: func() (string, error) { return server.URL, nil },
		Token:   func() (string, error) { return testToken, nil }}); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	model, err := registry.Resolve("test/model", store)
	if err != nil {
		t.Fatal(err)
	}
	first, err := model.Decide(context.Background(), sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.Decide(context.Background(), sampleRequest())
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached || !second.Cached || !equalResponses(first, second) || calls.Load() != 1 {
		t.Fatalf("cache parity: live=%v replay=%v calls=%d", first.Cached, second.Cached, calls.Load())
	}
	bad := first
	bad.Answers = map[string]Answer{"score": {Type: QuestionScore, Score: 9}}
	encoded, _ := canonicalResponseJSON(bad)
	store.records[first.RequestDigest] = encoded
	_, err = model.Decide(context.Background(), sampleRequest())
	if !errors.Is(err, ErrCacheCorrupt) || errors.Is(err, ErrInvalidResponse) || calls.Load() != 1 {
		t.Fatalf("corrupt cache fallback: %v", err)
	}
}

func TestResolveRejectsInvalidProviderConfiguration(t *testing.T) {
	registry := NewRegistry()
	registry.RegisterDriver(NewSystemOneDriver(nil))
	provider := Provider{Name: "test", Driver: SystemOneDriverName, BaseURL: func() (string, error) { return "https://host/path", nil }, Token: func() (string, error) { return testToken, nil }}
	if err := registry.RegisterProvider(provider); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve("test/model", NewMemoryStore()); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid base: %v", err)
	}
	provider.BaseURL = func() (string, error) { return "https://host", nil }
	provider.Token = func() (string, error) { return " ", nil }
	if err := registry.RegisterProvider(provider); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve("test/model", NewMemoryStore()); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("missing token: %v", err)
	}
}

func TestDefaultRegistryTokenRouterProvider(t *testing.T) {
	var gotModel string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/alpha/decisions" || r.Header.Get("Authorization") != "Bearer "+testToken || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Model     string                     `json:"model"`
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotModel = body.Model
		if body.State != "anonymous state" || len(body.Questions) != 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"model":"typesafe/jev-1.13-20260917","answers":{"direction":{"type":"choice","choice":"up","confidence":0.9,"probabilities":{"up":0.9,"down":0.1}}},"usage":{"input_tokens":10,"output_tokens":2,"cost":0.0},"provider":"TypeSafe"}`))
	}))
	defer server.Close()
	t.Setenv(TokenRouterBaseURLEnv, server.URL)
	t.Setenv(TokenRouterTokenEnv, testToken)
	t.Setenv(TokenRouterTokenFileEnv, "")
	tokenEnv, fileEnv, err := ProviderTokenEnv(TokenRouterIdentity)
	if err != nil || tokenEnv != TokenRouterTokenEnv || fileEnv != TokenRouterTokenFileEnv {
		t.Fatalf("token env: %q %q %v", tokenEnv, fileEnv, err)
	}
	model, err := DefaultRegistry(server.Client()).Resolve(TokenRouterIdentity, NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	request := Request{State: "anonymous state", Questions: map[string]Question{"direction": {Type: QuestionChoice, Instructions: "Pick one.", ChoiceCriteria: map[string]string{"up": "Up.", "down": "Down."}}}}
	response, err := model.Decide(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if gotModel != "typesafe/jev-1.13" || response.ResolvedModel != "typesafe/jev-1.13-20260917" || response.Answers["direction"].Choice != "up" {
		t.Fatalf("model=%q response=%+v", gotModel, response)
	}
}

func TestDefaultRegistryExperientialProvider(t *testing.T) {
	var gotModel string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotModel = body.Model
		_, _ = w.Write([]byte(`{"id":"decision_1","model":"jev-latest","answers":{"direction":{"type":"choice","choice":"up","confidence":0.9,"probabilities":{"up":0.9,"down":0.1}}},"usage":{"input_tokens":10,"output_tokens":2,"cost":0.0,"is_byok":false},"provider":"typesafe"}`))
	}))
	defer server.Close()
	t.Setenv(ExperientialBaseURLEnv, server.URL)
	t.Setenv(ExperientialTokenEnv, testToken)
	t.Setenv(ExperientialTokenFileEnv, "")
	if DefaultIdentity != ExperientialIdentity {
		t.Fatalf("default identity %q", DefaultIdentity)
	}
	tokenEnv, fileEnv, err := ProviderTokenEnv(ExperientialIdentity)
	if err != nil || tokenEnv != ExperientialTokenEnv || fileEnv != ExperientialTokenFileEnv {
		t.Fatalf("token env: %q %q %v", tokenEnv, fileEnv, err)
	}
	if _, _, err := ProviderTokenEnv("unknown/model"); !errors.Is(err, ErrUnknownProvider) {
		t.Fatalf("unknown provider: %v", err)
	}
	model, err := DefaultRegistry(server.Client()).Resolve(ExperientialIdentity, NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	request := Request{State: "anonymous state", Questions: map[string]Question{"direction": {Type: QuestionChoice, Instructions: "Pick one.", ChoiceCriteria: map[string]string{"up": "Up.", "down": "Down."}}}}
	response, err := model.Decide(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if gotModel != "jev-latest" || response.ResolvedModel != "jev-latest" || response.Answers["direction"].Choice != "up" || response.Answers["direction"].Probabilities["up"] != 0.9 {
		t.Fatalf("response: model=%q %+v", gotModel, response)
	}
}

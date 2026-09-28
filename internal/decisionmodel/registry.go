package decisionmodel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	// DefaultIdentity is the default council decision model identity.
	DefaultIdentity = ExperientialIdentity
	// AIHubMixIdentity is the AIHubMix preview decision model identity.
	AIHubMixIdentity = "aihubmix/decision-model-preview"

	AIHubMixProvider       = "aihubmix"
	AIHubMixDefaultBaseURL = "https://aihubmix.com"
	AIHubMixBaseURLEnv     = "AIHUBMIX_BASE_URL"
	AIHubMixTokenEnv       = "AIHUBMIX_API_TOKEN"
	AIHubMixTokenFileEnv   = "AIHUBMIX_API_TOKEN_FILE"

	// ExperientialIdentity is the Experiential Labs SystemOne decision model.
	ExperientialIdentity       = "experiential/jev-latest"
	ExperientialProvider       = "experiential"
	ExperientialDefaultBaseURL = "https://api.experientiallabs.ai"
	ExperientialBaseURLEnv     = "EXPERIENTIAL_BASE_URL"
	ExperientialTokenEnv       = "EXPERIENTIAL_API_TOKEN"
	ExperientialTokenFileEnv   = "EXPERIENTIAL_API_TOKEN_FILE"

	// MaxAttempts bounds live calls per Decide for transient failures.
	MaxAttempts = 3
)

func retryDelay(attempt int) time.Duration {
	if attempt == 2 {
		return time.Second
	}
	return 2 * time.Second
}

var (
	modelNamePattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	providerNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
)

// TokenLoader returns the provider credential. Implementations must not log
// or wrap the credential in errors and return ErrMissingToken when absent.
type TokenLoader func() (string, error)

// BaseURLLoader returns the provider base URL (https scheme and host only).
type BaseURLLoader func() (string, error)

// Provider maps a provider name to a wire driver and endpoint configuration.
// Future providers reuse existing drivers by name.
type Provider struct {
	Name    string
	Driver  string
	BaseURL BaseURLLoader
	Token   TokenLoader
}

// Registry resolves "<provider>/<model>" identities.
type Registry struct {
	providers map[string]Provider
	drivers   map[string]Driver
	// Sleep waits between retry attempts and must honour ctx. Tests inject a
	// recorder; nil uses a real timer.
	Sleep func(ctx context.Context, delay time.Duration) error
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}, drivers: map[string]Driver{}}
}

// DefaultRegistry registers the systemone driver (with the given client, or a
// 20s-timeout client when nil) and the aihubmix and experiential providers
// configured from the process environment.
func DefaultRegistry(client *http.Client) *Registry {
	registry := NewRegistry()
	registry.RegisterDriver(NewSystemOneDriver(client))
	if err := registry.RegisterProvider(Provider{
		Name:    AIHubMixProvider,
		Driver:  SystemOneDriverName,
		BaseURL: EnvBaseURL(AIHubMixBaseURLEnv, AIHubMixDefaultBaseURL),
		Token:   EnvToken(AIHubMixTokenEnv, AIHubMixTokenFileEnv),
	}); err != nil {
		panic(err)
	}
	if err := registry.RegisterProvider(Provider{
		Name:    ExperientialProvider,
		Driver:  SystemOneDriverName,
		BaseURL: EnvBaseURL(ExperientialBaseURLEnv, ExperientialDefaultBaseURL),
		Token:   EnvToken(ExperientialTokenEnv, ExperientialTokenFileEnv),
	}); err != nil {
		panic(err)
	}
	return registry
}

// ProviderTokenEnv returns the token and token-file environment keys for a
// built-in provider identity so callers can fail closed before replay.
func ProviderTokenEnv(identity string) (tokenEnv, tokenFileEnv string, err error) {
	provider, _, err := ParseIdentity(identity)
	if err != nil {
		return "", "", err
	}
	switch provider {
	case AIHubMixProvider:
		return AIHubMixTokenEnv, AIHubMixTokenFileEnv, nil
	case ExperientialProvider:
		return ExperientialTokenEnv, ExperientialTokenFileEnv, nil
	}
	return "", "", fmt.Errorf("%w: %q", ErrUnknownProvider, provider)
}

// Resolve resolves identity with DefaultRegistry(nil).
func Resolve(identity string, store Store) (Model, error) {
	return DefaultRegistry(nil).Resolve(identity, store)
}

func (r *Registry) RegisterDriver(driver Driver) {
	r.drivers[driver.Name()] = driver
}

func (r *Registry) RegisterProvider(provider Provider) error {
	if !providerNamePattern.MatchString(provider.Name) || provider.Driver == "" || provider.BaseURL == nil || provider.Token == nil {
		return fmt.Errorf("decisionmodel: invalid provider registration %q", provider.Name)
	}
	r.providers[provider.Name] = provider
	return nil
}

// Providers lists registered provider names in sorted order.
func (r *Registry) Providers() []string {
	names := make([]string, 0, len(r.providers))
	for name := range r.providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ParseIdentity splits "<provider>/<model>".
func ParseIdentity(identity string) (provider, model string, err error) {
	provider, model, ok := strings.Cut(identity, "/")
	if !ok || provider == "" || !modelNamePattern.MatchString(model) || strings.Contains(model, "/") {
		return "", "", fmt.Errorf("%w: model identity must be <provider>/<model>", ErrInvalidRequest)
	}
	return provider, model, nil
}

// Resolve fails closed before any replay when the provider, driver, base URL,
// token, or store is missing. The token and base URL are loaded once.
func (r *Registry) Resolve(identity string, store Store) (Model, error) {
	providerName, modelName, err := ParseIdentity(identity)
	if err != nil {
		return nil, err
	}
	provider, ok := r.providers[providerName]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, providerName)
	}
	driver, ok := r.drivers[provider.Driver]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDriver, provider.Driver)
	}
	if store == nil {
		return nil, errors.New("decisionmodel: an immutable response store is required")
	}
	baseURL, err := provider.BaseURL()
	if err != nil {
		return nil, err
	}
	baseURL, err = NormalizeBaseURL(baseURL)
	if err != nil {
		return nil, err
	}
	token, err := provider.Token()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, ErrMissingToken
	}
	sleep := r.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	return &cachedModel{
		identity: identity,
		model:    modelName,
		driver:   driver,
		endpoint: Endpoint{BaseURL: baseURL, Token: token},
		store:    store,
		sleep:    sleep,
	}, nil
}

// EnvToken loads a token from envKey or the file named by fileKey (trimmed).
func EnvToken(envKey, fileKey string) TokenLoader {
	return func() (string, error) {
		if value := strings.TrimSpace(os.Getenv(envKey)); value != "" {
			return value, nil
		}
		path := strings.TrimSpace(os.Getenv(fileKey))
		if path == "" {
			return "", fmt.Errorf("%w: set %s or %s", ErrMissingToken, envKey, fileKey)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("%w: %s is unavailable or has unsafe permissions", ErrMissingToken, fileKey)
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%w: %s could not be read", ErrMissingToken, fileKey)
		}
		value := strings.TrimSpace(string(payload))
		if value == "" {
			return "", fmt.Errorf("%w: %s is empty", ErrMissingToken, fileKey)
		}
		return value, nil
	}
}

// EnvBaseURL loads an https base URL override from envKey, else fallback.
func EnvBaseURL(envKey, fallback string) BaseURLLoader {
	return func() (string, error) {
		value := strings.TrimSpace(os.Getenv(envKey))
		if value == "" {
			value = fallback
		}
		normalized, err := NormalizeBaseURL(value)
		if err != nil {
			return "", fmt.Errorf("%s: %w", envKey, err)
		}
		return normalized, nil
	}
}

// NormalizeBaseURL accepts only https://host[:port] with an optional trailing
// slash; paths, queries, fragments, and userinfo are rejected.
func NormalizeBaseURL(value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || parsed.Opaque != "" {
		return "", fmt.Errorf("%w: base URL must be https://host without path, query, or credentials", ErrInvalidRequest)
	}
	return "https://" + parsed.Host, nil
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type cachedModel struct {
	identity string
	model    string
	driver   Driver
	endpoint Endpoint
	store    Store
	sleep    func(context.Context, time.Duration) error
}

func (m *cachedModel) Identity() string { return m.identity }

// Decide serves the immutable cache first, otherwise calls the provider with
// bounded retries, validates, and persists the response before returning it.
func (m *cachedModel) Decide(ctx context.Context, request Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	if err := ValidateRequest(request); err != nil {
		return Response{}, err
	}
	digest := RequestDigest(m.identity, m.driver.Name(), request)
	cached, ok, err := m.store.Load(ctx, digest)
	if err != nil {
		return Response{}, fmt.Errorf("decisionmodel: cache load: %w", err)
	}
	if ok {
		if cached.RequestDigest != digest || cached.ModelIdentity != m.identity || cached.Driver != m.driver.Name() {
			return Response{}, fmt.Errorf("%w: cached response identity does not match request", ErrCacheCorrupt)
		}
		if err := validateStorable(request, cached); err != nil {
			return Response{}, fmt.Errorf("%w: cached response fails validation: %v", ErrCacheCorrupt, err)
		}
		cached.Cached = true
		return cached, nil
	}

	var lastErr error
	attempts := 0
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if attempt > 1 {
			if err := m.sleep(ctx, retryDelay(attempt)); err != nil {
				if ctx.Err() != nil {
					return Response{}, ctx.Err()
				}
				return Response{}, err
			}
		}
		attempts = attempt
		response, err := m.driver.Decide(ctx, m.endpoint, m.model, request)
		if err == nil {
			response.ModelIdentity = m.identity
			response.Driver = m.driver.Name()
			response.RequestDigest = digest
			response.Cached = false
			if err := ValidateAnswers(request, response.Answers); err != nil {
				return Response{}, err
			}
			if err := m.store.Save(ctx, request, response); err != nil {
				return Response{}, fmt.Errorf("decisionmodel: cache save: %w", err)
			}
			return response, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		if !errors.Is(err, ErrTransient) || ctx.Err() != nil {
			break
		}
	}
	if errors.Is(lastErr, ErrTransient) {
		return Response{}, fmt.Errorf("%w (after %d attempts)", lastErr, attempts)
	}
	if errors.Is(lastErr, ErrUnavailable) || errors.Is(lastErr, ErrInvalidResponse) || errors.Is(lastErr, ErrInvalidRequest) || errors.Is(lastErr, ErrMissingToken) {
		return Response{}, lastErr
	}
	return Response{}, fmt.Errorf("%w: %s", ErrUnavailable, redact(lastErr.Error(), m.endpoint.Token))
}

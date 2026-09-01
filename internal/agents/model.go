package agents

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/openaimodel"

	"github.com/geoffjay/agent-toolbox/internal/agents/anthropicmodel"
)

// Provider identifies which LLM backend to use.
type Provider string

const (
	// ProviderOpenAI uses the OpenAI-compatible API (works with OpenAI,
	// Ollama, LM Studio, vLLM, LiteLLM, etc.).
	ProviderOpenAI Provider = "openai"
	// ProviderAnthropic uses Anthropic's native Messages API for Claude
	// models.
	ProviderAnthropic Provider = "anthropic"
)

// ModelConfig configures an LLM used by the review agents. Empty fields
// fall back to environment variables.
type ModelConfig struct {
	// Provider selects the model backend. When empty, it defaults to
	// ProviderOpenAI unless ANTHROPIC_API_KEY is set and OPENAI_API_KEY
	// is not.
	Provider Provider

	// ModelName is the model identifier (e.g. "gpt-4o-mini",
	// "claude-sonnet-4-20250514"). Falls back to OPENAI_MODEL (OpenAI
	// provider) or ANTHROPIC_MODEL (Anthropic provider) when empty.
	ModelName string

	// APIKey for the endpoint. Falls back to OPENAI_API_KEY (OpenAI) or
	// ANTHROPIC_API_KEY (Anthropic) when empty.
	APIKey string

	// BaseURL for the endpoint. Falls back to OPENAI_BASE_URL or
	// ANTHROPIC_BASE_URL when empty.
	BaseURL string

	// AuthToken is an Authorization: Bearer credential. It applies to
	// the Anthropic provider only and is what gateways/proxies in front
	// of Anthropic expect (the native API uses APIKey via x-api-key).
	// Falls back to ANTHROPIC_AUTH_TOKEN when empty.
	AuthToken string
}

// NewModel builds the LLM from cfg and the relevant environment variables.
// The provider is determined by cfg.Provider, defaulting to OpenAI unless
// ANTHROPIC_API_KEY is set and OPENAI_API_KEY is not.
func NewModel(ctx context.Context, cfg ModelConfig) (model.LLM, error) {
	provider := cfg.Provider
	if provider == "" {
		provider = defaultProvider()
	}
	switch provider {
	case ProviderOpenAI:
		return newOpenAIModel(ctx, cfg)
	case ProviderAnthropic:
		return newAnthropicModel(ctx, cfg)
	default:
		return nil, fmt.Errorf("unknown provider %q: must be %q or %q", provider, ProviderOpenAI, ProviderAnthropic)
	}
}

func defaultProvider() Provider {
	if os.Getenv("ANTHROPIC_API_KEY") != "" && os.Getenv("OPENAI_API_KEY") == "" {
		return ProviderAnthropic
	}
	return ProviderOpenAI
}

func newOpenAIModel(ctx context.Context, cfg ModelConfig) (model.LLM, error) {
	name := cfg.ModelName
	if name == "" {
		name = os.Getenv("OPENAI_MODEL")
	}
	if name == "" {
		return nil, fmt.Errorf("model name is required: set ModelConfig.ModelName or OPENAI_MODEL")
	}

	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("OPENAI_BASE_URL")
	}

	m, err := openaimodel.NewModel(ctx, name, &openaimodel.ClientConfig{
		APIKey:  apiKey,
		BaseURL: baseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("create openai model: %w", err)
	}
	slog.Info("model built", "provider", "openai", "model", name, "base_url", baseURL, "api_key", redactedKey(apiKey))
	return wrapWithRetry(m), nil
}

// redactedKey reports whether an API key is present without ever
// exposing its value in logs.
func redactedKey(key string) string {
	if key == "" {
		return "unset"
	}
	return "set"
}

func newAnthropicModel(ctx context.Context, cfg ModelConfig) (model.LLM, error) {
	name := cfg.ModelName
	if name == "" {
		name = os.Getenv("ANTHROPIC_MODEL")
	}
	if name == "" {
		return nil, fmt.Errorf("model name is required: set ModelConfig.ModelName or ANTHROPIC_MODEL")
	}

	apiKey := cfg.APIKey
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = os.Getenv("ANTHROPIC_BASE_URL")
	}

	authToken := cfg.AuthToken
	if authToken == "" {
		authToken = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}
	authToken = resolveAnthropicAuthToken(authToken, baseURL, apiKey)

	m, err := anthropicmodel.NewModel(ctx, name, &anthropicmodel.ClientConfig{
		APIKey:    apiKey,
		AuthToken: authToken,
		BaseURL:   baseURL,
	})
	if err != nil {
		return nil, fmt.Errorf("create anthropic model: %w", err)
	}
	slog.Info("model built",
		"provider", "anthropic",
		"model", name,
		"base_url", baseURL,
		"api_key", redactedKey(apiKey),
		"auth_token", redactedKey(authToken))
	return wrapWithRetry(m), nil
}

// resolveAnthropicAuthToken decides the Authorization: Bearer token for
// the Anthropic client. Gateways and proxies in front of Anthropic (any
// custom base URL) authenticate with a bearer token rather than the
// native x-api-key header, so when the user points at a custom endpoint
// with only an API key, that key is reused as the bearer token. Direct
// Anthropic (empty/default base URL) keeps x-api-key only and returns the
// token unchanged.
func resolveAnthropicAuthToken(authToken, baseURL, apiKey string) string {
	if authToken != "" {
		return authToken
	}
	if baseURL != "" && apiKey != "" {
		return apiKey
	}
	return authToken
}

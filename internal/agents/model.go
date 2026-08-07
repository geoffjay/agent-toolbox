package agents

import (
	"context"
	"fmt"
	"os"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/model/openaimodel"
)

// ModelConfig configures an OpenAI-compatible LLM used by the review
// agents. Empty fields fall back to environment variables.
type ModelConfig struct {
	// ModelName is the model identifier (e.g. "gpt-4o-mini",
	// "llama3.1:latest"). Falls back to OPENAI_MODEL when empty.
	ModelName string
	// APIKey for the endpoint. Falls back to OPENAI_API_KEY when empty.
	APIKey string
	// BaseURL for OpenAI-compatible endpoints (e.g. Ollama at
	// http://localhost:11434/v1). Falls back to OPENAI_BASE_URL when empty.
	BaseURL string
}

// NewModel builds the OpenAI-compatible LLM from cfg and the
// OPENAI_MODEL, OPENAI_API_KEY, OPENAI_BASE_URL environment variables.
func NewModel(ctx context.Context, cfg ModelConfig) (model.LLM, error) {
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
		return nil, fmt.Errorf("create model: %w", err)
	}
	return m, nil
}
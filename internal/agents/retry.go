package agents

import (
	"context"
	"iter"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

const (
	defaultMaxRetries = 3
	defaultRetryDelay = 500 * time.Millisecond
)

var transientErrors = []string{
	"response output did not contain text or tool content",
	"connection reset",
	"EOF",
	"deadline exceeded",
	"unexpected EOF",
}

func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, sub := range transientErrors {
		if strings.Contains(msg, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

type retryModel struct {
	inner      model.LLM
	maxRetries int
	delay      time.Duration
}

func (r *retryModel) Name() string { return r.inner.Name() }

func (r *retryModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for attempt := 0; attempt <= r.maxRetries; attempt++ {
			var lastErr error
			for resp, err := range r.inner.GenerateContent(ctx, req, stream) {
				if err != nil {
					lastErr = err
					break
				}
				if !yield(resp, nil) {
					return
				}
			}
			if lastErr == nil {
				return
			}
			if !isTransientError(lastErr) || attempt == r.maxRetries {
				yield(&model.LLMResponse{
					Content: &genai.Content{
						Role:  "model",
						Parts: []*genai.Part{{Text: ""}},
					},
					ErrorCode:    "transient_error_exhausted",
					ErrorMessage: lastErr.Error(),
					FinishReason: genai.FinishReasonStop,
					TurnComplete: true,
				}, nil)
				return
			}
			select {
			case <-time.After(r.delay):
			case <-ctx.Done():
				yield(nil, ctx.Err())
				return
			}
		}
	}
}

func wrapWithRetry(m model.LLM) model.LLM {
	return &retryModel{
		inner:      m,
		maxRetries: defaultMaxRetries,
		delay:      defaultRetryDelay,
	}
}

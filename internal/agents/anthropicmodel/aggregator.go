package anthropicmodel

import (
	"context"
	"iter"
	"reflect"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// streamAggregator aggregates partial streaming responses into a final
// LLMResponse. This mirrors the internal llminternal.StreamingResponseAggregator
// but is self-contained so we don't depend on the ADK's internal package.
type streamAggregator struct {
	usageMetadata *genai.GenerateContentResponseUsageMetadata
	response      *model.LLMResponse

	sequence             []*genai.Part
	currentTextBuffer    string
	currentTextIsThought bool
	finishReason         genai.FinishReason
}

func newStreamAggregator() *streamAggregator {
	return &streamAggregator{}
}

func (s *streamAggregator) processResponse(_ context.Context, genResp *genai.GenerateContentResponse) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp := genaiToLLMResponse(genResp)
		if len(genResp.Candidates) > 0 {
			candidate := genResp.Candidates[0]
			resp.TurnComplete = candidate.FinishReason != ""
		}
		s.aggregate(resp)
		if !yield(resp, nil) {
			return
		}
	}
}

func (s *streamAggregator) aggregate(llmResponse *model.LLMResponse) {
	s.response = llmResponse
	s.usageMetadata = llmResponse.UsageMetadata
	if llmResponse.FinishReason != "" {
		s.finishReason = llmResponse.FinishReason
	}
	llmResponse.Partial = true

	if llmResponse.Content == nil {
		return
	}

	for _, part := range llmResponse.Content.Parts {
		if reflect.ValueOf(*part).IsZero() {
			continue
		}
		if part.Text != "" {
			if s.currentTextBuffer != "" && part.Thought != s.currentTextIsThought {
				s.flushTextBuffer()
			}
			if s.currentTextBuffer == "" {
				s.currentTextIsThought = part.Thought
			}
			s.currentTextBuffer += part.Text
		} else if part.FunctionCall != nil {
			s.flushTextBuffer()
			s.sequence = append(s.sequence, part)
		} else {
			s.flushTextBuffer()
			s.sequence = append(s.sequence, part)
		}
	}
}

func (s *streamAggregator) flushTextBuffer() {
	if s.currentTextBuffer != "" {
		s.sequence = append(s.sequence, &genai.Part{
			Text:    s.currentTextBuffer,
			Thought: s.currentTextIsThought,
		})
		s.currentTextBuffer = ""
		s.currentTextIsThought = false
	}
}

func (s *streamAggregator) close() *model.LLMResponse {
	if s.response == nil {
		return nil
	}
	s.flushTextBuffer()
	return &model.LLMResponse{
		Content: &genai.Content{
			Parts: s.sequence,
			Role:  genai.RoleModel,
		},
		UsageMetadata: s.usageMetadata,
		FinishReason:  s.finishReason,
	}
}

package anthropicmodel

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// genaiToLLMResponse converts a genai.GenerateContentResponse into a
// model.LLMResponse, mirroring the internal converters.Genai2LLMResponse
// logic used by the openaimodel provider.
func genaiToLLMResponse(res *genai.GenerateContentResponse) *model.LLMResponse {
	usageMetadata := res.UsageMetadata
	if len(res.Candidates) > 0 && res.Candidates[0] != nil {
		candidate := res.Candidates[0]
		if (candidate.Content != nil && len(candidate.Content.Parts) > 0) || candidate.FinishReason == genai.FinishReasonStop {
			return &model.LLMResponse{
				Content:       candidate.Content,
				FinishReason:  candidate.FinishReason,
				UsageMetadata: usageMetadata,
				ModelVersion:  res.ModelVersion,
			}
		}
		return &model.LLMResponse{
			ErrorCode:     string(candidate.FinishReason),
			FinishReason:  candidate.FinishReason,
			UsageMetadata: usageMetadata,
			ModelVersion:  res.ModelVersion,
		}
	}
	return &model.LLMResponse{
		Content:       &genai.Content{Parts: []*genai.Part{}, Role: "model"},
		UsageMetadata: usageMetadata,
		ModelVersion:  res.ModelVersion,
	}
}

// convertResponse translates an Anthropic Message response into the generic
// genai.GenerateContentResponse format.
func convertResponse(resp *anthropic.Message) (*genai.GenerateContentResponse, error) {
	if resp == nil {
		return nil, ErrNoTextOrToolContent
	}
	parts, err := convertContentBlocks(resp.Content)
	if err != nil {
		return nil, err
	}
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Role:  string(genai.RoleModel),
					Parts: parts,
				},
				FinishReason: finishReason(resp.StopReason),
			},
		},
		ModelVersion:  resp.Model,
		ResponseID:    resp.ID,
		UsageMetadata: convertUsage(resp.Usage),
	}, nil
}

func convertContentBlocks(blocks []anthropic.ContentBlockUnion) ([]*genai.Part, error) {
	var parts []*genai.Part
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				parts = append(parts, &genai.Part{Text: block.Text})
			}
		case "thinking":
			if block.Thinking != "" {
				parts = append(parts, &genai.Part{Text: block.Thinking, Thought: true})
			}
		case "tool_use":
			args := map[string]any{}
			if len(block.Input) > 0 {
				if err := json.Unmarshal(block.Input, &args); err != nil {
					return nil, fmt.Errorf("anthropic: parse tool_use input: %w", err)
				}
			}
			parts = append(parts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					Name: block.Name,
					ID:   block.ID,
					Args: args,
				},
			})
		default:
			// Ignore unsupported block types (server_tool_use, web_search_tool_result, etc.)
		}
	}
	if len(parts) == 0 {
		return nil, ErrNoTextOrToolContent
	}
	return parts, nil
}

func finishReason(reason anthropic.StopReason) genai.FinishReason {
	switch reason {
	case anthropic.StopReasonEndTurn:
		return genai.FinishReasonStop
	case anthropic.StopReasonMaxTokens, anthropic.StopReasonModelContextWindowExceeded:
		return genai.FinishReasonMaxTokens
	case anthropic.StopReasonStopSequence:
		return genai.FinishReasonStop
	case anthropic.StopReasonToolUse:
		return genai.FinishReasonStop
	case anthropic.StopReasonRefusal:
		return genai.FinishReasonSafety
	default:
		return genai.FinishReasonUnspecified
	}
}

func safeInt32(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < 0 {
		// Token counts are never negative; clamp defensively instead
		// of letting the conversion wrap around.
		return 0
	}
	return int32(v)
}

func convertUsage(usage anthropic.Usage) *genai.GenerateContentResponseUsageMetadata {
	return &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        safeInt32(usage.InputTokens),
		CandidatesTokenCount:    safeInt32(usage.OutputTokens),
		TotalTokenCount:         safeInt32(usage.InputTokens + usage.OutputTokens),
		CachedContentTokenCount: safeInt32(usage.CacheReadInputTokens),
		PromptTokensDetails: []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityText, TokenCount: safeInt32(usage.InputTokens)},
		},
		CandidatesTokensDetails: []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityText, TokenCount: safeInt32(usage.OutputTokens)},
		},
	}
}

package anthropicmodel

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"google.golang.org/genai"
)

// streamTranslator processes Anthropic streaming events and converts them
// into genai.GenerateContentResponse chunks. It buffers tool-use input
// deltas until the block is complete.
type streamTranslator struct {
	toolInputs map[string]*strings.Builder
	toolMeta   map[string]streamToolMeta
}

type streamToolMeta struct {
	id   string
	name string
}

func newStreamTranslator() *streamTranslator {
	return &streamTranslator{
		toolInputs: make(map[string]*strings.Builder),
		toolMeta:   make(map[string]streamToolMeta),
	}
}

func (t *streamTranslator) process(event anthropic.MessageStreamEventUnion) (*genai.GenerateContentResponse, error) {
	switch event.Type {
	case "content_block_start":
		start := event.AsContentBlockStart()
		idx := fmt.Sprintf("%d", start.Index)
		if start.ContentBlock.Type == "tool_use" {
			t.toolInputs[idx] = &strings.Builder{}
			t.toolMeta[idx] = streamToolMeta{
				id:   start.ContentBlock.ID,
				name: start.ContentBlock.Name,
			}
		}
		return nil, nil

	case "content_block_delta":
		delta := event.AsContentBlockDelta()
		idx := fmt.Sprintf("%d", delta.Index)
		switch delta.Delta.Type {
		case "text_delta":
			if delta.Delta.Text == "" {
				return nil, nil
			}
			return singlePartResponse(&genai.Part{Text: delta.Delta.Text}), nil
		case "thinking_delta":
			if delta.Delta.Thinking == "" {
				return nil, nil
			}
			return singlePartResponse(&genai.Part{Text: delta.Delta.Thinking, Thought: true}), nil
		case "input_json_delta":
			if delta.Delta.PartialJSON != "" {
				if buf, ok := t.toolInputs[idx]; ok {
					buf.WriteString(delta.Delta.PartialJSON)
				}
			}
			return nil, nil
		default:
			return nil, nil
		}

	case "content_block_stop":
		stop := event.AsContentBlockStop()
		idx := fmt.Sprintf("%d", stop.Index)
		if buf, ok := t.toolInputs[idx]; ok {
			meta := t.toolMeta[idx]
			delete(t.toolInputs, idx)
			delete(t.toolMeta, idx)

			payload := buf.String()
			if payload == "" {
				payload = "{}"
			}
			var args map[string]any
			if err := json.Unmarshal([]byte(payload), &args); err != nil {
				return nil, fmt.Errorf("anthropic: parse streamed tool input: %w", err)
			}
			return singlePartResponse(&genai.Part{
				FunctionCall: &genai.FunctionCall{
					Name: meta.name,
					ID:   meta.id,
					Args: args,
				},
			}), nil
		}
		return nil, nil

	case "message_start", "message_delta", "message_stop":
		return nil, nil

	default:
		return nil, nil
	}
}

func singlePartResponse(part *genai.Part) *genai.GenerateContentResponse {
	if part == nil {
		return nil
	}
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Role:  string(genai.RoleModel),
					Parts: []*genai.Part{part},
				},
				FinishReason: genai.FinishReasonUnspecified,
			},
		},
	}
}
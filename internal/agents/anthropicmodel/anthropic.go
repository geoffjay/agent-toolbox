// Package anthropicmodel implements [model.LLM] for Anthropic's native
// Messages API (api.anthropic.com/v1/messages).
//
// Unlike the OpenAI-compatible provider, this talks directly to Anthropic's
// API schema, which supports Claude models natively without a translating
// proxy.
package anthropicmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// ClientConfig configures the Anthropic client. Empty APIKey/BaseURL fall
// back to ANTHROPIC_API_KEY / ANTHROPIC_BASE_URL env vars (handled by the
// SDK's default options).
//
// APIKey is sent as the x-api-key header (Anthropic's native scheme).
// AuthToken is sent as an Authorization: Bearer header, which is what
// gateways and OpenAI-style proxies in front of Anthropic expect. Both
// may be set; direct Anthropic uses APIKey, gateways use AuthToken.
type ClientConfig struct {
	APIKey     string
	AuthToken  string
	BaseURL    string
	HTTPClient *http.Client
	Options    []option.RequestOption
}

type anthropicModel struct {
	client *anthropic.Client
	name   string
}

// NewModel constructs an Anthropic-backed model.LLM.
// The context is unused but kept for signature parity with other model
// constructors (e.g., openaimodel.NewModel).
func NewModel(_ context.Context, modelName string, cfg *ClientConfig) (model.LLM, error) {
	if modelName == "" {
		return nil, ErrModelNameRequired
	}
	if cfg == nil {
		cfg = &ClientConfig{}
	}
	var opts []option.RequestOption
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	}
	if cfg.AuthToken != "" {
		opts = append(opts, option.WithAuthToken(cfg.AuthToken))
	}
	if cfg.BaseURL != "" {
		if err := validateBaseURL(cfg.BaseURL); err != nil {
			return nil, err
		}
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	opts = append(opts, cfg.Options...)
	client := anthropic.NewClient(opts...)
	return &anthropicModel{client: &client, name: modelName}, nil
}

func (m *anthropicModel) Name() string { return m.name }

// GenerateContent converts an LLMRequest into an Anthropic Messages request
// and handles both streaming and non-streaming responses.
func (m *anthropicModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	if req == nil {
		return singleErrorSequence(ErrRequestNil)
	}
	params, err := buildParams(m.name, req)
	if err != nil {
		return singleErrorSequence(err)
	}
	if stream {
		return m.generateStream(ctx, params)
	}
	return m.generate(ctx, params)
}

// debugLog logs only when GRAPH_REVIEW_DEBUG is set; the model emits a
// line per response block, which would otherwise flood stderr on every
// run.
func debugLogf(format string, args ...any) {
	if os.Getenv("GRAPH_REVIEW_DEBUG") == "" {
		return
	}
	log.Printf("[anthropic] "+format, args...)
}

func (m *anthropicModel) generate(ctx context.Context, params anthropic.MessageNewParams) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp, err := m.client.Messages.New(ctx, params)
		if err != nil {
			debugLogf("API call failed: %v", err)
			yield(nil, fmt.Errorf("anthropic: call failed: %w", err))
			return
		}
		debugLogf("response: stop_reason=%s, %d content blocks, model=%s, input_tokens=%d, output_tokens=%d",
			resp.StopReason, len(resp.Content), resp.Model, resp.Usage.InputTokens, resp.Usage.OutputTokens)
		for i, block := range resp.Content {
			debugLogf("block[%d]: type=%s, text_len=%d, tool_use=%v", i, block.Type, len(block.Text), block.Type == "tool_use")
		}
		genaiResp, err := convertResponse(resp)
		if err != nil {
			debugLogf("convertResponse error: %v", err)
			yield(nil, err)
			return
		}
		llmResp := genaiToLLMResponse(genaiResp)
		attachMetadata(llmResp, resp)
		yield(llmResp, nil)
	}
}

func (m *anthropicModel) generateStream(ctx context.Context, params anthropic.MessageNewParams) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		stream := m.client.Messages.NewStreaming(ctx, params)
		defer func() { _ = stream.Close() }()

		aggregator := newStreamAggregator()
		translator := newStreamTranslator()

		var lastMessage *anthropic.Message

		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "message_start":
				start := event.AsMessageStart()
				lastMessage = &start.Message
			case "message_delta":
				delta := event.AsMessageDelta()
				if lastMessage != nil {
					lastMessage.StopReason = delta.Delta.StopReason
					lastMessage.StopSequence = delta.Delta.StopSequence
					lastMessage.Usage = anthropic.Usage{
						InputTokens:              delta.Usage.InputTokens,
						OutputTokens:             delta.Usage.OutputTokens,
						CacheCreationInputTokens: delta.Usage.CacheCreationInputTokens,
						CacheReadInputTokens:     delta.Usage.CacheReadInputTokens,
						OutputTokensDetails:      delta.Usage.OutputTokensDetails,
						ServerToolUse:            delta.Usage.ServerToolUse,
					}
				}
			}

			genaiResp, err := translator.process(event)
			if err != nil {
				yield(nil, err)
				return
			}
			if genaiResp == nil {
				continue
			}
			for resp, err := range aggregator.processResponse(ctx, genaiResp) {
				if err == nil && lastMessage != nil {
					attachMetadata(resp, lastMessage)
				}
				if !yield(resp, err) {
					return
				}
			}
		}
		if err := stream.Err(); err != nil {
			yield(nil, fmt.Errorf("anthropic: stream: %w", err))
			return
		}

		if final := aggregator.close(); final != nil {
			if lastMessage != nil {
				attachMetadata(final, lastMessage)
				final.UsageMetadata = convertUsage(lastMessage.Usage)
			}
			final.TurnComplete = true
			if !yield(final, nil) {
				return
			}
		}
	}
}

func attachMetadata(resp *model.LLMResponse, msg *anthropic.Message) {
	if resp == nil || msg == nil {
		return
	}
	if resp.CustomMetadata == nil {
		resp.CustomMetadata = map[string]any{}
	}
	resp.CustomMetadata["anthropic_message_id"] = msg.ID
	resp.CustomMetadata["anthropic_model"] = msg.Model
}

func singleErrorSequence(err error) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		yield(nil, err)
	}
}

func validateBaseURL(base string) error {
	u, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("anthropic: invalid base URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("anthropic: base URL must use https scheme, got %q", u.Scheme)
	}
	return nil
}

// Errors returned by the provider.
var (
	ErrModelNameRequired   = errors.New("anthropic: model name is required")
	ErrRequestNil          = errors.New("anthropic: request is nil")
	ErrNoContents          = errors.New("anthropic: LLM request has no contents to convert")
	ErrNoTextOrToolContent = errors.New("anthropic: response output did not contain text or tool content")
)

// buildParams converts a generic LLMRequest into Anthropic MessageNewParams.
func buildParams(modelName string, req *model.LLMRequest) (anthropic.MessageNewParams, error) {
	params := anthropic.MessageNewParams{
		Model:     modelName,
		MaxTokens: 8192,
	}
	if req.Model != "" {
		params.Model = req.Model
	}

	system, messages, err := convertContents(req.Contents)
	if err != nil {
		debugLogf("convertContents error: %v", err)
		return anthropic.MessageNewParams{}, err
	}

	// The ADK passes the agent instruction as Config.SystemInstruction,
	// not as a content message with role "system".
	system = mergeSystemInstruction(system, req)
	if len(messages) == 0 {
		debugLogf("no messages to send")
		return anthropic.MessageNewParams{}, ErrNoContents
	}
	params.Messages = messages
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	if err := applyGenerationConfig(&params, req.Config); err != nil {
		debugLogf("applyGenerationConfig error: %v", err)
		return anthropic.MessageNewParams{}, err
	}

	tools, err := convertTools(req.Config)
	if err != nil {
		debugLogf("convertTools error: %v", err)
		return anthropic.MessageNewParams{}, err
	}
	if len(tools) > 0 {
		params.Tools = tools
		debugLogf("sending %d tools, %d messages, system_len=%d", len(tools), len(messages), len(system))
	} else {
		debugLogf("sending 0 tools, %d messages, system_len=%d", len(messages), len(system))
	}

	if cfg := req.Config; cfg != nil && cfg.ToolConfig != nil {
		choice := convertToolChoice(cfg.ToolConfig)
		if choice != nil {
			params.ToolChoice = *choice
		}
	}

	return params, nil
}

// mergeSystemInstruction prepends the ADK agent instruction (passed as
// Config.SystemInstruction rather than as a "system"-role content message)
// to any system content extracted from the request, so the effective
// system prompt keeps both. The instruction leads because it defines the
// reviewer's role.
func mergeSystemInstruction(system string, req *model.LLMRequest) string {
	if req.Config == nil || req.Config.SystemInstruction == nil {
		return system
	}
	var sb strings.Builder
	for _, part := range req.Config.SystemInstruction.Parts {
		if part != nil && part.Text != "" {
			sb.WriteString(part.Text)
			sb.WriteString("\n")
		}
	}
	si := strings.TrimSpace(sb.String())
	if si == "" {
		return system
	}
	if system == "" {
		return si
	}
	return si + "\n\n" + system
}

// convertContents translates genai.Content slices into Anthropic messages.
// It returns the system prompt (if any) and the message list.
func convertContents(contents []*genai.Content) (system string, messages []anthropic.MessageParam, err error) {
	var systemParts []string

	for _, content := range contents {
		if content == nil || len(content.Parts) == 0 {
			continue
		}
		role := content.Role
		switch role {
		case "", "user":
			msg, err := newMessage(anthropic.MessageParamRoleUser, content.Parts)
			if err != nil {
				return "", nil, err
			}
			messages = append(messages, msg)
		case "model", "assistant":
			msg, err := newMessage(anthropic.MessageParamRoleAssistant, content.Parts)
			if err != nil {
				return "", nil, err
			}
			messages = append(messages, msg)
		case "system":
			for _, part := range content.Parts {
				if part == nil {
					continue
				}
				if part.Text != "" {
					systemParts = append(systemParts, part.Text)
				}
			}
		default:
			return "", nil, fmt.Errorf("anthropic: unsupported role %q", role)
		}
	}

	system = strings.Join(systemParts, "\n")
	return system, messages, nil
}

func newMessage(role anthropic.MessageParamRole, parts []*genai.Part) (anthropic.MessageParam, error) {
	var blocks []anthropic.ContentBlockParamUnion
	for _, part := range parts {
		if part == nil {
			continue
		}
		switch {
		case part.Text != "":
			blocks = append(blocks, anthropic.ContentBlockParamUnion{
				OfText: &anthropic.TextBlockParam{Text: part.Text},
			})
		case part.FunctionCall != nil:
			blocks = append(blocks, convertFunctionCallToBlock(part.FunctionCall))
		case part.FunctionResponse != nil:
			blocks = append(blocks, convertFunctionResponseToBlock(part.FunctionResponse))
		default:
			return anthropic.MessageParam{}, fmt.Errorf("anthropic: unsupported content part %T", part)
		}
	}
	if len(blocks) == 0 {
		return anthropic.MessageParam{}, fmt.Errorf("anthropic: message has no content blocks")
	}
	return anthropic.MessageParam{
		Role:    role,
		Content: blocks,
	}, nil
}

func convertFunctionCallToBlock(fc *genai.FunctionCall) anthropic.ContentBlockParamUnion {
	// Input must marshal as a JSON object. Assigning marshaled bytes to the
	// any-typed Input field would encode them as a base64 string, which the
	// API rejects ("Input should be an object"), so pass the args map.
	var input any = fc.Args
	if fc.Args == nil {
		input = map[string]any{}
	}
	return anthropic.ContentBlockParamUnion{
		OfToolUse: &anthropic.ToolUseBlockParam{
			ID:    fc.ID,
			Name:  fc.Name,
			Input: input,
		},
	}
}

func convertFunctionResponseToBlock(fr *genai.FunctionResponse) anthropic.ContentBlockParamUnion {
	payload, _ := json.Marshal(fr.Response)
	return anthropic.ContentBlockParamUnion{
		OfToolResult: &anthropic.ToolResultBlockParam{
			ToolUseID: fr.ID,
			Content: []anthropic.ToolResultBlockParamContentUnion{
				{OfText: &anthropic.TextBlockParam{Text: string(payload)}},
			},
		},
	}
}

func applyGenerationConfig(params *anthropic.MessageNewParams, cfg *genai.GenerateContentConfig) error {
	if cfg == nil {
		return nil
	}
	if cfg.Temperature != nil {
		params.Temperature = param.NewOpt(float64(*cfg.Temperature))
	}
	if cfg.TopP != nil {
		params.TopP = param.NewOpt(float64(*cfg.TopP))
	}
	if cfg.TopK != nil {
		params.TopK = param.NewOpt(int64(*cfg.TopK))
	}
	if cfg.MaxOutputTokens > 0 {
		params.MaxTokens = int64(cfg.MaxOutputTokens)
	}
	if len(cfg.StopSequences) > 0 {
		params.StopSequences = cfg.StopSequences
	}
	if cfg.CandidateCount > 1 {
		return errors.New("anthropic: multiple candidates are not supported")
	}
	if cfg.SafetySettings != nil {
		return errors.New("anthropic: gemini safety settings are not supported")
	}
	return nil
}

func convertTools(cfg *genai.GenerateContentConfig) ([]anthropic.ToolUnionParam, error) {
	if cfg == nil || len(cfg.Tools) == 0 {
		return nil, nil
	}
	var tools []anthropic.ToolUnionParam
	for _, tool := range cfg.Tools {
		if tool == nil {
			continue
		}
		for _, decl := range tool.FunctionDeclarations {
			fn, err := convertFunctionDeclaration(decl)
			if err != nil {
				return nil, err
			}
			tools = append(tools, anthropic.ToolUnionParam{OfTool: fn})
		}
	}
	return tools, nil
}

func convertFunctionDeclaration(fn *genai.FunctionDeclaration) (*anthropic.ToolParam, error) {
	if fn == nil {
		return nil, errors.New("anthropic: nil function declaration")
	}
	if fn.Name == "" {
		return nil, errors.New("anthropic: function declaration missing name")
	}

	paramsMap, err := schemaToMap(fn.Parameters)
	if err != nil {
		return nil, err
	}
	if paramsMap == nil && fn.ParametersJsonSchema != nil {
		paramsMap, err = normalizeSchema(fn.ParametersJsonSchema)
		if err != nil {
			return nil, err
		}
	}

	toolParam := &anthropic.ToolParam{
		Name:        fn.Name,
		InputSchema: toolInputSchema(paramsMap),
	}
	if fn.Description != "" {
		toolParam.Description = param.NewOpt(fn.Description)
	}
	return toolParam, nil
}

// toolInputSchema maps a JSON Schema object (the function parameters) onto
// the Anthropic ToolInputSchemaParam. The SDK's Properties field holds only
// the property map — not the whole schema — and Required is a separate
// field; any other top-level keywords (additionalProperties, $defs, etc.)
// are preserved via ExtraFields. The "type" keyword is dropped because the
// param always marshals type "object". Anthropic validates this schema
// against JSON Schema draft 2020-12, so the shape must be exact.
func toolInputSchema(schema map[string]any) anthropic.ToolInputSchemaParam {
	in := anthropic.ToolInputSchemaParam{Properties: map[string]any{}}
	if schema == nil {
		return in
	}
	extra := map[string]any{}
	for k, v := range schema {
		switch k {
		case "type":
			// Always "object" for tool parameters; the param defaults it.
		case "properties":
			in.Properties = v
		case "required":
			in.Required = toStringSlice(v)
		default:
			extra[k] = v
		}
	}
	if len(extra) > 0 {
		in.ExtraFields = extra
	}
	return in
}

// toStringSlice converts a decoded JSON array of strings to []string.
func toStringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func convertToolChoice(toolCfg *genai.ToolConfig) *anthropic.ToolChoiceUnionParam {
	if toolCfg == nil || toolCfg.FunctionCallingConfig == nil {
		return nil
	}
	cfg := toolCfg.FunctionCallingConfig
	choice := &anthropic.ToolChoiceUnionParam{}
	switch cfg.Mode {
	case "", genai.FunctionCallingConfigModeUnspecified, genai.FunctionCallingConfigModeAuto:
		choice.OfAuto = &anthropic.ToolChoiceAutoParam{}
	case genai.FunctionCallingConfigModeNone:
		choice.OfNone = &anthropic.ToolChoiceNoneParam{}
	case genai.FunctionCallingConfigModeAny:
		if len(cfg.AllowedFunctionNames) == 1 {
			choice.OfTool = &anthropic.ToolChoiceToolParam{Name: cfg.AllowedFunctionNames[0]}
		} else {
			choice.OfAny = &anthropic.ToolChoiceAnyParam{}
		}
	default:
		return nil
	}
	return choice
}

func schemaToMap(schema *genai.Schema) (map[string]any, error) {
	if schema == nil {
		return nil, nil
	}
	b, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal schema: %w", err)
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, fmt.Errorf("anthropic: unmarshal schema: %w", err)
	}
	lowercaseSchemaTypes(result)
	return result, nil
}

func normalizeSchema(schema any) (map[string]any, error) {
	switch s := schema.(type) {
	case map[string]any:
		return s, nil
	case nil:
		return nil, errors.New("anthropic: empty json schema")
	default:
		b, err := json.Marshal(s)
		if err != nil {
			return nil, fmt.Errorf("anthropic: marshal json schema: %w", err)
		}
		var result map[string]any
		if err := json.Unmarshal(b, &result); err != nil {
			return nil, fmt.Errorf("anthropic: unmarshal json schema: %w", err)
		}
		return result, nil
	}
}

func lowercaseSchemaTypes(val any) {
	switch v := val.(type) {
	case map[string]any:
		if t, ok := v["type"]; ok {
			switch tVal := t.(type) {
			case string:
				v["type"] = strings.ToLower(tVal)
			case []any:
				for i, item := range tVal {
					if str, ok := item.(string); ok {
						tVal[i] = strings.ToLower(str)
					}
				}
			}
		}
		for _, child := range v {
			lowercaseSchemaTypes(child)
		}
	case []any:
		for _, child := range v {
			lowercaseSchemaTypes(child)
		}
	}
}

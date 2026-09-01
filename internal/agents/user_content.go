package agents

import (
	"log/slog"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// EnsureUserContent returns a BeforeModelCallback that keeps the agent's
// user content (its task input, e.g. the diff under review) at the head of
// every LLM request for single-turn agents. name identifies the agent in
// the emitted log lines (the callback context cannot resolve the agent).
//
// Why this exists: single-turn agents running as workflow nodes build each
// request from the "current turn" — the events after the latest user (or
// foreign-agent) event in the session. That backward pivot scan in
// google.golang.org/adk/v2 (internal/llminternal/contents_processor.go,
// buildContentsCurrentTurnContextOnly) checks the isolation scope but not
// the event branch. When reviewer agents run in parallel (workflow fan-out)
// they share one session on sibling branches, so a sibling's events land
// after this agent's seeded user event and hijack the pivot: the request
// loses the seeded user content — the diff — and starts with a bare
// assistant message. Models reject that shape: glm served through Ollama's
// Responses API answers it with an empty message, which the ADK surfaces as
// ErrNoTextOrToolContent ("openai: response output did not contain text or
// tool content") and the retry wrapper exhausts itself re-sending the same
// broken request. Affected versions: adk/v2 v2.0.0–v2.2.0.
//
// The callback reinstates the intended request shape: if no user-role
// content carrying the invocation's user text is present, a copy of
// ctx.UserContent() is prepended. Healthy requests are left untouched.
func EnsureUserContent(name string) llmagent.BeforeModelCallback {
	return func(ctx agent.Context, req *model.LLMRequest) (*model.LLMResponse, error) {
		// Log the request composition at debug level so a pasted log shows
		// exactly what the model received (role sequence), which is what
		// the model reacts to.
		defer func() {
			slog.Debug("llm request contents",
				"agent", name,
				"roles", contentRoles(req.Contents))
		}()

		user := ctx.UserContent()
		if user == nil || len(user.Parts) == 0 {
			return nil, nil
		}
		want := contentText(user)
		if want == "" {
			return nil, nil
		}
		for _, c := range req.Contents {
			if c == nil || c.Role != genai.RoleUser {
				continue
			}
			// Function-response contents carry role "user" but no text;
			// they are tool plumbing, not the task input.
			if contentText(c) == want {
				return nil, nil
			}
		}
		slog.Warn("llm request was missing the diff; restored the agent's user content",
			"agent", name,
			"contents", len(req.Contents))
		// Copy the parts by value: the seed must not alias the invocation's
		// user content, so later request mutations cannot reach it.
		parts := make([]*genai.Part, len(user.Parts))
		for i, part := range user.Parts {
			if part != nil {
				cp := *part
				parts[i] = &cp
			}
		}
		seed := &genai.Content{
			Role:  genai.RoleUser,
			Parts: parts,
		}
		req.Contents = append([]*genai.Content{seed}, req.Contents...)
		return nil, nil
	}
}

// contentText concatenates the text parts of a content. Contents whose
// parts are function calls or responses yield "".
func contentText(c *genai.Content) string {
	var sb strings.Builder
	for _, p := range c.Parts {
		if p != nil && p.Text != "" {
			sb.WriteString(p.Text)
		}
	}
	return sb.String()
}

// contentRoles renders the content role sequence, e.g. "user→model→user".
func contentRoles(contents []*genai.Content) string {
	roles := make([]string, 0, len(contents))
	for _, c := range contents {
		if c == nil {
			continue
		}
		roles = append(roles, c.Role)
	}
	return strings.Join(roles, "→")
}

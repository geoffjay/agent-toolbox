package agents

import (
	"bytes"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// stubUserContext satisfies agent.Context by embedding the interface and
// overriding only UserContent; EnsureUserContent touches nothing else.
type stubUserContext struct {
	agent.Context
	user *genai.Content
}

func (c *stubUserContext) UserContent() *genai.Content { return c.user }

func diffContent() *genai.Content {
	return genai.NewContentFromText("Please review the following diff:\n```diff\n+ hello\n```", genai.RoleUser)
}

// hijackedRequest mirrors the shape produced when a parallel sibling agent's
// events steal the ADK's current-turn pivot: the assistant's own turn and
// its function responses, with the seeded user content gone and the request
// starting from a bare assistant message.
func hijackedRequest() *model.LLMRequest {
	return &model.LLMRequest{
		Contents: []*genai.Content{
			{
				Role: genai.RoleModel,
				Parts: []*genai.Part{
					{Text: "Let me look at the files."},
					{FunctionCall: &genai.FunctionCall{Name: "list_files", ID: "call_1"}},
				},
			},
			{
				// Function responses carry role user but no text parts.
				Role: genai.RoleUser,
				Parts: []*genai.Part{
					{FunctionResponse: &genai.FunctionResponse{Name: "list_files", ID: "call_1"}},
				},
			},
		},
	}
}

func TestEnsureUserContentPrependsWhenMissing(t *testing.T) {
	ctx := &stubUserContext{user: diffContent()}
	req := hijackedRequest()

	resp, err := EnsureUserContent("test-agent")(ctx, req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if resp != nil {
		t.Fatalf("expected callback to proceed, got response %v", resp)
	}
	if len(req.Contents) != 3 {
		t.Fatalf("len(Contents) = %d, want 3 (seeded user content prepended)", len(req.Contents))
	}
	first := req.Contents[0]
	if first.Role != genai.RoleUser {
		t.Errorf("first content role = %q, want %q", first.Role, genai.RoleUser)
	}
	if got := contentText(first); got != contentText(ctx.user) {
		t.Errorf("seeded text = %q, want the invocation user content %q", got, contentText(ctx.user))
	}
	// The original turn must follow the seed unchanged.
	if req.Contents[1].Role != genai.RoleModel || req.Contents[2].Role != genai.RoleUser {
		t.Errorf("original contents reordered: roles = %q", []string{req.Contents[1].Role, req.Contents[2].Role})
	}
}

func TestEnsureUserContentNoOpWhenPresent(t *testing.T) {
	ctx := &stubUserContext{user: diffContent()}
	req := &model.LLMRequest{
		Contents: []*genai.Content{
			ctx.user,
			{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{Text: "Finding: none."}},
			},
		},
	}
	before := slices.Clone(req.Contents)

	if _, err := EnsureUserContent("test-agent")(ctx, req); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(before, req.Contents) {
		t.Errorf("request modified: %+v", req.Contents)
	}
}

func TestEnsureUserContentNoOpWithoutUserContent(t *testing.T) {
	req := hijackedRequest()
	before := slices.Clone(req.Contents)

	if _, err := EnsureUserContent("test-agent")(&stubUserContext{}, req); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(before, req.Contents) {
		t.Errorf("request modified without ctx user content: %+v", req.Contents)
	}

	// User content without text parts offers nothing to anchor on.
	blank := &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{}}}}
	if _, err := EnsureUserContent("test-agent")(&stubUserContext{user: blank}, req); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(before, req.Contents) {
		t.Errorf("request modified with blank ctx user content: %+v", req.Contents)
	}
}

func TestEnsureUserContentPrependsCopy(t *testing.T) {
	ctx := &stubUserContext{user: diffContent()}
	req := hijackedRequest()

	if _, err := EnsureUserContent("test-agent")(ctx, req); err != nil {
		t.Fatalf("err = %v", err)
	}
	// The seed must not alias the caller's parts: later request mutations
	// must never reach the invocation's user content.
	if &req.Contents[0].Parts[0] == &ctx.user.Parts[0] {
		t.Error("seeded content aliases ctx.UserContent() parts")
	}
}

func TestEnsureUserContentLogs(t *testing.T) {
	var buf syncBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	if _, err := EnsureUserContent("test-agent")(&stubUserContext{user: diffContent()}, hijackedRequest()); err != nil {
		t.Fatalf("err = %v", err)
	}
	if s := buf.String(); !strings.Contains(s, "missing the diff") {
		t.Errorf("repair warning missing from log: %q", s)
	}

	buf.Reset()
	ctx := &stubUserContext{user: diffContent()}
	if _, err := EnsureUserContent("test-agent")(ctx, &model.LLMRequest{Contents: []*genai.Content{ctx.user}}); err != nil {
		t.Fatalf("err = %v", err)
	}
	if s := buf.String(); strings.Contains(s, "missing the diff") {
		t.Errorf("healthy request warned: %q", s)
	}
	if !strings.Contains(buf.String(), "llm request contents") {
		t.Errorf("request-shape debug line missing: %q", buf.String())
	}
}

// syncBuffer is a minimal slog sink.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

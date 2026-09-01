package graph_test

import (
	"context"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/workflow"
	"google.golang.org/genai"

	"github.com/geoffjay/agent-toolbox/internal/agents"
	"github.com/geoffjay/agent-toolbox/internal/graph"
)

// scriptedModel routes each LLM call by the marker text in its system
// instruction, and answers reviewer calls differently depending on whether
// the prompt carries the gate's "## Human feedback" revision section. This
// makes the loop-back observable: revised findings can only reach the
// summary if the gate's revise route actually re-ran the reviewers.
type scriptedModel struct {
	mu sync.Mutex
	// calls records which agent marker each LLM call carried. The
	// reviewers run as parallel workflow nodes, so the order between
	// concurrently recorded markers is nondeterministic.
	calls []string
}

func (m *scriptedModel) Name() string { return "scripted" }

func (m *scriptedModel) recordCall(which string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, which)
}

// recordedCalls returns a snapshot of the recorded markers.
func (m *scriptedModel) recordedCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.calls)
}

func (m *scriptedModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		prompt := ""
		if req.Config != nil && req.Config.SystemInstruction != nil {
			prompt += contentText(req.Config.SystemInstruction)
		}
		for _, c := range req.Contents {
			prompt += contentText(c)
		}

		var which, out string
		switch {
		case strings.Contains(prompt, "triage step"):
			which = "triage"
			out = "both"
		case strings.Contains(prompt, "static analysis reviewer") || strings.Contains(prompt, "security reviewer"):
			which = "reviewer"
			if strings.Contains(prompt, "## Human feedback") {
				out = "- `revised.go:1` [major] fixed per feedback"
			} else {
				out = "- `a.go:1` [nit] first-round finding"
			}
		case strings.Contains(prompt, "summary step"):
			which = "summary"
			// Echo the reviewer findings it was given so the test can
			// assert which round reached the summary.
			out = "## Verdict\nNeeds discussion — scripted.\n\n## Findings\n" +
				extractScriptedFindings(prompt)
		default:
			which = "unknown"
			out = "unexpected prompt: " + prompt
		}
		m.recordCall(which)
		yield(&model.LLMResponse{Content: &genai.Content{
			Role:  "model",
			Parts: []*genai.Part{{Text: out}},
		}}, nil)
	}
}

func contentText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range c.Parts {
		if p.Text != "" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// extractScriptedFindings pulls the reviewer finding lines out of the
// summary prompt (FormatFindings emits labeled sections per reviewer).
func extractScriptedFindings(prompt string) string {
	var lines []string
	for line := range strings.SplitSeq(prompt, "\n") {
		if strings.HasPrefix(line, "- `") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// drainRun runs one turn, collecting summary-agent text and the last
// RequestInput the workflow paused on.
func drainRun(t *testing.T, r *runner.Runner, sessionID string, msg *genai.Content) (summary string, pending *session.RequestInput) {
	t.Helper()
	var b strings.Builder
	for ev, err := range r.Run(t.Context(), "gate-test-user", sessionID, msg, agent.RunConfig{}) {
		if err != nil {
			t.Fatalf("run error: %v", err)
		}
		if ev == nil {
			continue
		}
		if ev.RequestedInput != nil {
			pending = ev.RequestedInput
		}
		if ev.Author == agents.SummaryAgentName && ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p.Text != "" && !p.Thought {
					b.WriteString(p.Text)
				}
			}
		}
	}
	return b.String(), pending
}

// resumeContent builds the resume turn for a paused gate.
func resumeContent(t *testing.T, pending *session.RequestInput, answer map[string]any) *genai.Content {
	t.Helper()
	if pending == nil {
		t.Fatal("expected the workflow to pause on the findings gate")
	}
	return &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{{
			FunctionResponse: &genai.FunctionResponse{
				ID:       pending.InterruptID,
				Name:     workflow.WorkflowInputFunctionCallName,
				Response: map[string]any{"payload": answer},
			},
		}},
	}
}

func TestFindingsGateReviseLoop(t *testing.T) {
	ctx := context.Background()
	m := &scriptedModel{}

	root, err := graph.New(ctx, graph.Config{Model: m, FindingsGate: true})
	if err != nil {
		t.Fatalf("build gated graph: %v", err)
	}
	r, err := runner.NewInMemory("gate-test", root)
	if err != nil {
		t.Fatalf("build runner: %v", err)
	}

	userMsg := &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{{
			Text: "Please review the following diff:\n\n```diff\n+package a\n```",
		}},
	}

	// Turn 1: pipeline pauses on the gate with the first-round findings.
	_, pending := drainRun(t, r, "gate-loop", userMsg)
	if pending == nil {
		t.Fatal("turn 1: expected a RequestInput pause on the findings gate")
	}
	if payload, ok := pending.Payload.(string); !ok || !strings.Contains(payload, "first-round finding") {
		t.Fatalf("turn 1 payload = %#v, want the first-round findings", pending.Payload)
	}

	// Turn 2: revise — the gate loops back to the reviewers with the
	// human feedback, they re-run, and the gate pauses again on the
	// revised findings.
	_, pending = drainRun(t, r, "gate-loop",
		resumeContent(t, pending, map[string]any{"decision": "revise", "feedback": "dig into error handling"}))
	if pending == nil {
		t.Fatal("turn 2: expected a second RequestInput pause after the revision round")
	}
	if payload, ok := pending.Payload.(string); !ok || !strings.Contains(payload, "fixed per feedback") {
		t.Fatalf("turn 2 payload = %#v, want the revised findings", pending.Payload)
	}

	// Turn 3: approve — the summary consumes the revised findings and the
	// run completes without pausing again.
	summary, pending := drainRun(t, r, "gate-loop",
		resumeContent(t, pending, map[string]any{"decision": "approve"}))
	if pending != nil {
		t.Fatalf("turn 3: unexpected pause after approval: %+v", pending)
	}
	if !strings.Contains(summary, "fixed per feedback") {
		t.Fatalf("summary output does not contain the revised findings:\n%s", summary)
	}
	if strings.Contains(summary, "first-round finding") {
		t.Fatalf("summary output still contains the stale first-round findings:\n%s", summary)
	}

	// The reviewers must have run twice (round 1 + revision round).
	calls := m.recordedCalls()
	reviewerRuns := 0
	for _, c := range calls {
		if c == "reviewer" {
			reviewerRuns++
		}
	}
	if reviewerRuns < 4 { // "both" fans out to static + security per round
		t.Errorf("reviewer LLM calls = %d, want at least 4 (2 reviewers × 2 rounds); calls: %v", reviewerRuns, calls)
	}
}

func TestFindingsGateAbortFailsRun(t *testing.T) {
	ctx := context.Background()
	m := &scriptedModel{}

	root, err := graph.New(ctx, graph.Config{Model: m, FindingsGate: true})
	if err != nil {
		t.Fatalf("build gated graph: %v", err)
	}
	r, err := runner.NewInMemory("gate-test", root)
	if err != nil {
		t.Fatalf("build runner: %v", err)
	}

	userMsg := &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{Text: "Please review the following diff:\n\n```diff\n+package a\n```"}},
	}

	_, pending := drainRun(t, r, "gate-abort", userMsg)
	var runErr error
	for _, err := range r.Run(ctx, "gate-test-user", "gate-abort",
		resumeContent(t, pending, map[string]any{"decision": "abort"}), agent.RunConfig{}) {
		if err != nil {
			runErr = err
			break
		}
	}
	if runErr == nil {
		t.Fatal("abort decision should fail the run")
	}
	if !strings.Contains(runErr.Error(), "aborted by human") {
		t.Errorf("abort error = %v, want findings-gate abort message", runErr)
	}
}

func TestFindingsGateDisabledBuilds(t *testing.T) {
	// The ungated graph must keep building unchanged.
	if _, err := graph.New(context.Background(), graph.Config{Model: &scriptedModel{}}); err != nil {
		t.Fatalf("build ungated graph: %v", err)
	}
	if _, err := graph.New(context.Background(), graph.Config{Model: &scriptedModel{}, FindingsGate: true}); err != nil {
		t.Fatalf("build gated graph: %v", err)
	}
}

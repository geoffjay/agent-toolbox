package graph_test

import (
	"context"
	"iter"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/genai"

	"github.com/geoffjay/agent-toolbox/internal/agents"
	"github.com/geoffjay/agent-toolbox/internal/graph"
)

// revisedFinding is what a reviewer answers when its prompt carries the
// findings gate's "## Human feedback" section, i.e. on a revise round.
const revisedFinding = "- `revised.go:1` [major] fixed per feedback"

// singleRouteModel answers triage with a fixed category and each reviewer
// with labeled findings, so tests can exercise any reviewer being routed
// and the others skipped. Reviewer calls whose prompt carries the gate's
// "## Human feedback" section answer with revisedFinding, which makes a
// revise round observable in the gate payload and the final summary. The
// summary echoes the finding lines it was given, so the assertions can
// tell which reviewers' output actually reached it.
type singleRouteModel struct {
	mu       sync.Mutex
	category string            // triage answer
	findings map[string]string // reviewer name → round-1 finding text
	calls    []string          // markers of calls made
}

func (m *singleRouteModel) Name() string { return "single-route" }

func (m *singleRouteModel) recordCall(which string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, which)
}

// recordedCalls returns a snapshot of the recorded markers.
func (m *singleRouteModel) recordedCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.calls...)
}

// callCounts tallies the recorded markers by kind.
func (m *singleRouteModel) callCounts() map[string]int {
	counts := map[string]int{}
	for _, c := range m.recordedCalls() {
		counts[c]++
	}
	return counts
}

func (m *singleRouteModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
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
			out = m.category
		case strings.Contains(prompt, "static analysis reviewer"):
			which = "static"
			out = m.findings["static"]
			if strings.Contains(prompt, "## Human feedback") {
				out = revisedFinding
			}
		case strings.Contains(strings.ToLower(prompt), "security reviewer"):
			which = "security"
			out = m.findings["security"]
			if strings.Contains(prompt, "## Human feedback") {
				out = revisedFinding
			}
		case strings.Contains(prompt, "summary step"):
			which = "summary"
			// Echo the reviewer findings it was given so the test can
			// assert which reviewers' output reached the summary.
			out = "## Verdict\nNeeds discussion — scripted.\n\n## Findings\n" +
				extractScriptedFindings(prompt)
		default:
			which = "unknown"
			out = "unexpected prompt: " + prompt
		}
		m.recordCall(which)
		yield(&model.LLMResponse{Content: &genai.Content{
			Role:  genai.RoleModel,
			Parts: []*genai.Part{{Text: out}},
		}}, nil)
	}
}

// drainSummary runs one turn and returns the summary agent's text output
// along with any run error.
func drainSummary(t *testing.T, r *runner.Runner, sessionID string, msg *genai.Content) (summary string, runErr error) {
	t.Helper()
	var b strings.Builder
	for ev, err := range r.Run(t.Context(), "route-test-user", sessionID, msg, agent.RunConfig{}) {
		if err != nil {
			return b.String(), err
		}
		if ev == nil {
			continue
		}
		if ev.Author == agents.SummaryAgentName && ev.Content != nil {
			for _, p := range ev.Content.Parts {
				if p.Text != "" && !p.Thought {
					b.WriteString(p.Text)
				}
			}
		}
	}
	return b.String(), nil
}

// newRouteGraph builds the review graph around m and an in-memory runner
// for it.
func newRouteGraph(t *testing.T, m *singleRouteModel, gate bool) (*runner.Runner, error) {
	t.Helper()
	root, err := graph.New(t.Context(), graph.Config{Model: m, FindingsGate: gate})
	if err != nil {
		return nil, err
	}
	return runner.NewInMemory("route-test", root)
}

// TestSingleReviewerRouteReachesSummary is the regression test for the
// gather-barrier deadlock: triage answers "security" so the static
// reviewer is not selected, and the old JoinNode barrier — which declared
// both reviewers as predecessors — never fired, ending the run with no
// summary at all. The security findings must still reach the summary
// agent and produce a report.
func TestSingleReviewerRouteReachesSummary(t *testing.T) {
	m := &singleRouteModel{
		category: "security",
		findings: map[string]string{
			"static":   "- `a.go:1` [nit] static finding one",
			"security": "- `b.go:1` [major] security finding one",
		},
	}
	r, err := newRouteGraph(t, m, false)
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}

	userMsg := &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{Text: "Please review the following diff:\n\n```diff\n+package b\n```"}},
	}

	summary, runErr := drainSummary(t, r, "route-single", userMsg)
	if runErr != nil {
		t.Fatalf("run error: %v", runErr)
	}
	if summary == "" {
		t.Fatal("the security-only route produced no summary output; the reviewer orchestrator never completed")
	}
	if !strings.Contains(summary, "security finding one") {
		t.Errorf("summary does not contain the security findings:\n%s", summary)
	}
	if strings.Contains(summary, "static finding one") {
		t.Errorf("summary contains findings from the skipped static reviewer:\n%s", summary)
	}

	counts := m.callCounts()
	if counts["security"] != 1 {
		t.Errorf("security reviewer LLM calls = %d, want 1; calls: %v", counts["security"], m.recordedCalls())
	}
	if counts["static"] != 0 {
		t.Errorf("static reviewer LLM calls = %d, want 0 (triage routed it out); calls: %v", counts["static"], m.recordedCalls())
	}
	if counts["unknown"] != 0 {
		t.Errorf("unexpected LLM call markers: %v", m.recordedCalls())
	}
}

// TestTriageRoutesExpectedReviewers checks every triage category against
// the reviewer set the orchestrator actually runs: exactly the selected
// reviewers get LLM calls, and only their findings reach the summary.
func TestTriageRoutesExpectedReviewers(t *testing.T) {
	tests := []struct {
		name              string
		category          string
		wantStaticCalls   int
		wantSecurityCalls int
	}{
		{"static only", "static", 1, 0},
		{"security only", "security", 0, 1},
		{"both", "both", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &singleRouteModel{
				category: tt.category,
				findings: map[string]string{
					"static":   "- `a.go:1` [nit] static finding one",
					"security": "- `b.go:1` [major] security finding one",
				},
			}
			r, err := newRouteGraph(t, m, false)
			if err != nil {
				t.Fatalf("build graph: %v", err)
			}

			summary, runErr := drainSummary(t, r, "route-"+tt.name, &genai.Content{
				Role:  genai.RoleUser,
				Parts: []*genai.Part{{Text: "Please review the following diff:\n\n```diff\n+package a\n```"}},
			})
			if runErr != nil {
				t.Fatalf("run error: %v", runErr)
			}
			if summary == "" {
				t.Fatalf("%s route produced no summary output", tt.category)
			}

			wantStatic := tt.wantStaticCalls > 0
			if strings.Contains(summary, "static finding one") != wantStatic {
				t.Errorf("summary static-findings presence = %v, want %v:\n%s",
					strings.Contains(summary, "static finding one"), wantStatic, summary)
			}
			wantSecurity := tt.wantSecurityCalls > 0
			if strings.Contains(summary, "security finding one") != wantSecurity {
				t.Errorf("summary security-findings presence = %v, want %v:\n%s",
					strings.Contains(summary, "security finding one"), wantSecurity, summary)
			}

			counts := m.callCounts()
			if counts["static"] != tt.wantStaticCalls {
				t.Errorf("static reviewer LLM calls = %d, want %d; calls: %v",
					counts["static"], tt.wantStaticCalls, m.recordedCalls())
			}
			if counts["security"] != tt.wantSecurityCalls {
				t.Errorf("security reviewer LLM calls = %d, want %d; calls: %v",
					counts["security"], tt.wantSecurityCalls, m.recordedCalls())
			}
			if counts["triage"] != 1 {
				t.Errorf("triage LLM calls = %d, want 1; calls: %v", counts["triage"], m.recordedCalls())
			}
			if counts["summary"] != 1 {
				t.Errorf("summary LLM calls = %d, want 1; calls: %v", counts["summary"], m.recordedCalls())
			}
			if counts["unknown"] != 0 {
				t.Errorf("unexpected LLM call markers: %v", m.recordedCalls())
			}
		})
	}
}

// TestFindingsGateSingleReviewerReviseLoop exercises the live failure
// scenario: triage routes to a single reviewer and the findings gate then
// sends the findings back for revision. The revise round must re-run the
// same reviewer set triage selected (security only), with the human
// feedback in the prompt, and the revised findings must reach the
// summary after approval.
func TestFindingsGateSingleReviewerReviseLoop(t *testing.T) {
	m := &singleRouteModel{
		category: "security",
		findings: map[string]string{
			"static":   "- `a.go:1` [nit] static finding one",
			"security": "- `b.go:1` [major] security finding one",
		},
	}
	r, err := newRouteGraph(t, m, true)
	if err != nil {
		t.Fatalf("build gated graph: %v", err)
	}

	userMsg := &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{Text: "Please review the following diff:\n\n```diff\n+package b\n```"}},
	}

	// Turn 1: pipeline pauses on the gate with the first-round findings.
	_, pending := drainRun(t, r, "route-gate", userMsg)
	if pending == nil {
		t.Fatal("turn 1: expected a RequestInput pause on the findings gate")
	}
	if payload, ok := pending.Payload.(string); !ok || !strings.Contains(payload, "security finding one") {
		t.Fatalf("turn 1 payload = %#v, want the first-round security findings", pending.Payload)
	}

	// Turn 2: revise — the gate loops back to the reviewers, which re-run
	// the security reviewer only, and the gate pauses on the revised
	// findings.
	_, pending = drainRun(t, r, "route-gate",
		resumeContent(t, pending, map[string]any{"decision": "revise", "feedback": "dig into error handling"}))
	if pending == nil {
		t.Fatal("turn 2: expected a second RequestInput pause after the revision round")
	}
	if payload, ok := pending.Payload.(string); !ok || !strings.Contains(payload, revisedFinding) {
		t.Fatalf("turn 2 payload = %#v, want the revised findings", pending.Payload)
	}

	// Turn 3: approve — the summary consumes the revised findings and the
	// run completes without pausing again.
	summary, pending := drainRun(t, r, "route-gate",
		resumeContent(t, pending, map[string]any{"decision": "approve"}))
	if pending != nil {
		t.Fatalf("turn 3: unexpected pause after approval: %+v", pending)
	}
	if !strings.Contains(summary, revisedFinding) {
		t.Fatalf("summary output does not contain the revised findings:\n%s", summary)
	}
	if strings.Contains(summary, "security finding one") {
		t.Fatalf("summary output still contains the stale first-round findings:\n%s", summary)
	}

	counts := m.callCounts()
	if counts["security"] != 2 {
		t.Errorf("security reviewer LLM calls = %d, want 2 (first round + revision round); calls: %v",
			counts["security"], m.recordedCalls())
	}
	if counts["static"] != 0 {
		t.Errorf("static reviewer LLM calls = %d, want 0 (triage routed it out on every round); calls: %v",
			counts["static"], m.recordedCalls())
	}
	if counts["unknown"] != 0 {
		t.Errorf("unexpected LLM call markers: %v", m.recordedCalls())
	}
}

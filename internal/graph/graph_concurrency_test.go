package graph_test

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/genai"

	"github.com/geoffjay/agent-toolbox/internal/agents"
	"github.com/geoffjay/agent-toolbox/internal/graph"
	"github.com/geoffjay/agent-toolbox/internal/tools"
)

// rendezvous is a two-party barrier: the two parallel reviewers use one
// per script stage so their tool-call events interleave deterministically
// in the shared session — the condition under which a sibling's events
// steal the current-turn pivot and slice this agent's own history.
type rendezvous struct {
	mu     sync.Mutex
	n      int
	closed bool
	ch     chan struct{}
}

func newRendezvous() *rendezvous {
	return &rendezvous{ch: make(chan struct{})}
}

func (r *rendezvous) arriveAndWait() {
	r.mu.Lock()
	r.n++
	last := r.n >= 2 && !r.closed
	if r.n >= 2 {
		r.closed = true
	}
	r.mu.Unlock()
	if last {
		close(r.ch)
	}
	<-r.ch
}

// reqShape records what one reviewer's LLM request contained at one
// script stage: the reviewer's own tool calls / tool responses visible in
// the request, and whether the review request (the diff) was present.
type reqShape struct {
	stage   int
	which   string
	fcCount int
	frCount int
	hasDiff bool
}

// toolLoopModel plays a tool-using reviewer: each reviewer calls
// read_file twice, then reports. A stage barrier forces the two
// reviewers' events to interleave. The model records the request shape
// at every stage so the test can assert that a reviewer's own tool
// history survives the interleaving (the ADK v2.2.0 current-turn pivot
// scan drops it — a live run against claude-opus-5 looped for 53
// minutes on exactly this).
type toolLoopModel struct {
	mu sync.Mutex
	// shapes records one entry per reviewer LLM call, in call order.
	shapes []reqShape
	// stages[s] is the barrier for script stage s (1-based).
	stages map[int]*rendezvous
}

func newToolLoopModel() *toolLoopModel {
	return &toolLoopModel{stages: map[int]*rendezvous{
		1: newRendezvous(), 2: newRendezvous(),
	}}
}

func (m *toolLoopModel) Name() string { return "tool-loop" }

func (m *toolLoopModel) record(s reqShape) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shapes = append(m.shapes, s)
}

func (m *toolLoopModel) reviewerShapes() []reqShape {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []reqShape
	for _, s := range m.shapes {
		if s.which == agents.StaticAgentName || s.which == agents.SecurityAgentName {
			out = append(out, s)
		}
	}
	return out
}

func (m *toolLoopModel) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		// Classify by the system instruction only: the summary agent's
		// prompt embeds the reviewers' finding text, which would
		// otherwise re-match the reviewer markers.
		system := ""
		if req.Config != nil && req.Config.SystemInstruction != nil {
			system = contentText(req.Config.SystemInstruction)
		}
		prompt := system
		for _, c := range req.Contents {
			prompt += contentText(c)
		}
		fc, fr := 0, 0
		for _, c := range req.Contents {
			for _, p := range c.Parts {
				if p.FunctionCall != nil {
					fc++
				}
				if p.FunctionResponse != nil {
					fr++
				}
			}
		}
		hasDiff := strings.Contains(prompt, "Please review the following diff")

		var which, out string
		switch {
		case strings.Contains(system, "triage step"):
			which, out = "triage", "both"
		case strings.Contains(system, "static analysis reviewer"), strings.Contains(system, "security reviewer"):
			which = agents.StaticAgentName
			if strings.Contains(system, "security reviewer") {
				which = agents.SecurityAgentName
			}
			// Script: stage 1 and 2 call read_file (each followed by the
			// barrier so the sibling's events land in between), stage 3
			// reports the finding.
			m.mu.Lock()
			stage := 0
			for _, s := range m.shapes {
				if s.which == which {
					stage++
				}
			}
			m.mu.Unlock()
			m.record(reqShape{stage: stage + 1, which: which, fcCount: fc, frCount: fr, hasDiff: hasDiff})
			if stage < 2 {
				m.stages[stage+1].arriveAndWait()
				yield(&model.LLMResponse{Content: &genai.Content{
					Role: genai.RoleModel,
					Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{
						Name: "read_file",
						Args: map[string]any{"path": "graph.go"},
					}}},
				}}, nil)
				return
			}
			out = fmt.Sprintf("- `graph.go:%d` [minor] %s reviewer finished its tool pass", stage+1, which)
		case strings.Contains(system, "summary step"):
			which = "summary"
			out = "## Verdict\nRequest changes.\n\n## Findings\n" + extractScriptedFindings(prompt)
		default:
			which = "unknown"
			out = "unexpected prompt: " + prompt
		}
		yield(&model.LLMResponse{Content: &genai.Content{
			Role:  genai.RoleModel,
			Parts: []*genai.Part{{Text: out}},
		}}, nil)
	}
}

// TestParallelReviewersKeepOwnHistory reproduces the ADK v2.2.0
// current-turn pivot bug (contents_processor.go
// buildContentsCurrentTurnContextOnly checks isolation scope but not the
// event branch): with two reviewers running in parallel and calling
// tools, a sibling's events reset the turn boundary and the reviewer's
// own earlier tool calls vanish from its next request. A real model then
// re-explores from scratch forever — a live run against claude-opus-5
// looped for 53 minutes on this. The reviewer orchestrator must isolate
// each reviewer's history so the sibling cannot steal the pivot.
func TestParallelReviewersKeepOwnHistory(t *testing.T) {
	m := newToolLoopModel()

	repoTools, err := tools.NewTools()
	if err != nil {
		t.Fatalf("build tools: %v", err)
	}
	root, err := graph.New(t.Context(), graph.Config{Model: m, Tools: repoTools})
	if err != nil {
		t.Fatalf("build graph: %v", err)
	}
	r, err := runner.NewInMemory("concurrency-test", root)
	if err != nil {
		t.Fatalf("build runner: %v", err)
	}

	userMsg := &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{Text: "Please review the following diff:\n\n```diff\n+func New() {}\n```"}},
	}

	summary, runErr := drainSummary(t, r, "own-history", userMsg)
	if runErr != nil {
		t.Fatalf("run error: %v", runErr)
	}
	for _, reviewer := range []string{agents.StaticAgentName, agents.SecurityAgentName} {
		if !strings.Contains(summary, reviewer+" reviewer finished") {
			t.Errorf("summary missing the %s finding:\n%s", reviewer, summary)
		}
	}

	for _, reviewer := range []string{agents.StaticAgentName, agents.SecurityAgentName} {
		shapes := m.reviewerShapes()
		var own []reqShape
		for _, s := range shapes {
			if s.which == reviewer {
				own = append(own, s)
			}
		}
		if len(own) != 3 {
			t.Fatalf("%s: %d LLM calls, want 3 (two tool calls + report); shapes: %+v",
				reviewer, len(own), own)
		}
		// The reviewer's own first tool call must still be visible in
		// its second request, and both tool exchanges in its third:
		// the sibling's interleaved events must not slice them off.
		if own[1].frCount < 1 {
			t.Errorf("%s: second request carried %d own FunctionResponse(s), want ≥1 — "+
				"sibling events stole the current-turn pivot and wiped this reviewer's tool history",
				reviewer, own[1].frCount)
		}
		if own[2].frCount < 2 {
			t.Errorf("%s: third request carried %d own FunctionResponse(s), want 2 — "+
				"tool history truncated by sibling events", reviewer, own[2].frCount)
		}
		// The diff must reach every reviewer request.
		for _, s := range own {
			if !s.hasDiff {
				t.Errorf("%s: stage %d request was missing the diff", reviewer, s.stage)
			}
		}
	}
}

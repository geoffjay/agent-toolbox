package agents

import (
	"strings"
	"testing"
)

func TestParseGateDecision(t *testing.T) {
	tests := []struct {
		name         string
		reply        any
		wantDecision string
		wantFeedback string
		wantErr      bool
	}{
		{"map with decision and feedback",
			map[string]any{"decision": "revise", "feedback": "dig into the NPE"},
			DecisionRevise, "dig into the NPE", false},
		{"map with approve", map[string]any{"decision": "approve"}, DecisionApprove, "", false},
		{"bare string", "Approve\n", DecisionApprove, "", false},
		{"bare revise string", "revise", DecisionRevise, "", false},
		{"map with unknown decision",
			map[string]any{"decision": "maybe"}, "", "", true},
		{"unreadable type", 42, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, feedback, err := parseGateDecision(tt.reply)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseGateDecision() error = %v, wantErr %v", err, tt.wantErr)
			}
			if decision != tt.wantDecision {
				t.Errorf("decision = %q, want %q", decision, tt.wantDecision)
			}
			if feedback != tt.wantFeedback {
				t.Errorf("feedback = %q, want %q", feedback, tt.wantFeedback)
			}
		})
	}
}

func TestRevisePrompt(t *testing.T) {
	prompt := RevisePrompt("Please review the diff:\n```diff\n+hello\n```",
		"- `a.go:1` [nit] first-round finding", "focus on error handling")

	for _, want := range []string{
		"Please review the diff:",
		"## Prior review round",
		"- `a.go:1` [nit] first-round finding",
		"## Human feedback",
		"focus on error handling",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("revise prompt missing %q:\n%s", want, prompt)
		}
	}
	// The diff must come first: reviewers' instructions assume they receive
	// the diff up front.
	if strings.Index(prompt, "Please review the diff:") > strings.Index(prompt, "## Prior review round") {
		t.Error("revise prompt puts the findings before the diff; reviewers expect the diff first")
	}
}

package ui

import (
	"strings"
	"testing"

	"github.com/geoffjay/graph-review/internal/agents"
)

func TestPromptGateAnswer(t *testing.T) {
	req := GateRequest{
		Message: "Approve the reviewer findings?",
		Payload: "- `a.go:1` [nit] finding",
	}

	tests := []struct {
		name       string
		input      string
		want       map[string]any
		wantErrSub string
	}{
		{
			name:  "approve",
			input: "approve\n",
			want:  map[string]any{"decision": agents.DecisionApprove},
		},
		{
			name:  "revise with feedback",
			input: "revise\nfocus on error handling\n.\n",
			want: map[string]any{
				"decision": agents.DecisionRevise,
				"feedback": "focus on error handling\n",
			},
		},
		{
			name:       "revise with empty feedback fails",
			input:      "revise\n.\n",
			wantErrSub: "revise requires feedback",
		},
		{
			name:       "invalid decision fails",
			input:      "maybe\n",
			wantErrSub: "invalid gate decision",
		},
		{
			name:       "eof with no answer fails",
			input:      "",
			wantErrSub: "read gate decision",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			got, err := promptGateAnswer(strings.NewReader(tt.input), &out, req)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("promptGateAnswer() error = %v, want containing %q", err, tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("promptGateAnswer() error = %v", err)
			}
			if got["decision"] != tt.want["decision"] {
				t.Errorf("decision = %v, want %v", got["decision"], tt.want["decision"])
			}
			if got["feedback"] != tt.want["feedback"] {
				t.Errorf("feedback = %v, want %v", got["feedback"], tt.want["feedback"])
			}
			if !strings.Contains(out.String(), "a.go:1") {
				t.Errorf("prompt did not render the request payload:\n%s", out.String())
			}
			if !strings.Contains(out.String(), "approve/revise/abort") {
				t.Errorf("prompt did not show the decision choices:\n%s", out.String())
			}
		})
	}
}

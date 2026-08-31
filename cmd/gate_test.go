package cmd

import (
	"strings"
	"testing"

	"google.golang.org/adk/v2/workflow"
	"google.golang.org/genai"

	"github.com/geoffjay/graph-review/internal/agents"
	"github.com/geoffjay/graph-review/internal/ui"
)

func TestPromptGateAnswer(t *testing.T) {
	req := ui.GateRequest{
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

func TestResumeMessage(t *testing.T) {
	answer := map[string]any{"decision": "approve"}
	msg := resumeMessage("findings_gate-abc", answer)
	if msg.Role != genai.RoleUser || len(msg.Parts) != 1 {
		t.Fatalf("resumeMessage shape wrong: %+v", msg)
	}
	fr := msg.Parts[0].FunctionResponse
	if fr == nil {
		t.Fatal("resumeMessage part is not a FunctionResponse")
	}
	if fr.ID != "findings_gate-abc" {
		t.Errorf("FunctionResponse.ID = %q, want the interrupt ID", fr.ID)
	}
	if fr.Name != workflow.WorkflowInputFunctionCallName {
		t.Errorf("FunctionResponse.Name = %q, want %q", fr.Name, workflow.WorkflowInputFunctionCallName)
	}
	payload, ok := fr.Response["payload"].(map[string]any)
	if !ok || payload["decision"] != "approve" {
		t.Errorf("Response.payload = %#v, want the answer map", fr.Response["payload"])
	}
}

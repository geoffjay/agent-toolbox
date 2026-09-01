package cmd

import (
	"testing"

	"google.golang.org/adk/v2/workflow"
	"google.golang.org/genai"
)

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

package agents

import (
	"strings"
	"testing"
)

// TestDefaultInstructionsCarrySTEStyle guards the ASD-STE100 style block:
// every agent whose output a human reads must keep it appended to its
// default instruction. Dropping the append from any of them fails here.
func TestDefaultInstructionsCarrySTEStyle(t *testing.T) {
	for _, tc := range []struct {
		name        string
		instruction string
	}{
		{"static", DefaultStaticInstruction},
		{"security", DefaultSecurityInstruction},
		{"summary", DefaultSummaryInstruction},
	} {
		if !strings.HasSuffix(tc.instruction, StyleInstruction) {
			t.Errorf("%s default instruction lost the ASD-STE100 style block", tc.name)
		}
	}

	// Triage answers with a single routing word, so the style block must
	// stay off its instruction.
	if strings.Contains(DefaultTriageInstruction, StyleInstruction) {
		t.Error("triage instruction carries the style block; triage output is not human prose")
	}
}

package cmd

import (
	"strings"
	"testing"
)

func TestLoggingFlagsLevel(t *testing.T) {
	tests := []struct {
		name string
		lf   loggingFlags
		want string
	}{
		{"default", loggingFlags{}, "WARN"},
		{"verbose once", loggingFlags{verbose: 1}, "INFO"},
		{"verbose twice", loggingFlags{verbose: 2}, "DEBUG"},
		{"debug flag", loggingFlags{debug: true}, "DEBUG"},
		{"debug flag overrides verbosity", loggingFlags{verbose: 1, debug: true}, "DEBUG"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lf.level().String(); got != tt.want {
				t.Errorf("level() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDebugSnippet(t *testing.T) {
	if got := debugSnippet("short"); got != "short" {
		t.Errorf("debugSnippet(short) = %q, want unchanged", got)
	}
	big := strings.Repeat("x", debugPayloadMax+100)
	got := debugSnippet(big)
	if !strings.Contains(got, "bytes truncated") {
		t.Errorf("debugSnippet(long) not truncated: %d bytes", len(got))
	}
	if got[:debugPayloadMax] != big[:debugPayloadMax] {
		t.Errorf("debugSnippet(long) altered the prefix bytes")
	}
}

func TestPrintShallowDiagnostics(t *testing.T) {
	in := runPipelineInput{noClone: true}
	stats := &pipelineStats{
		events:    12,
		toolCalls: map[string]int{},
		agentText: map[string]int{"triage": 88, "summary": 0},
	}
	var sb strings.Builder
	printShallowDiagnostics(&sb, in, stats)

	out := sb.String()
	for _, want := range []string{
		"diagnostics: 12 events, 0 tool call(s)",
		"triage: 88 bytes of output",
		"summary: 0 bytes of output",
		"no reviewer tool calls were made",
		"--no-clone is set",
		"re-run with -vv",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("printShallowDiagnostics() missing %q in:\n%s", want, out)
		}
	}
}

func TestPrintShallowDiagnosticsNoTools(t *testing.T) {
	in := runPipelineInput{}
	in.noTools = true
	stats := &pipelineStats{toolCalls: map[string]int{}, agentText: map[string]int{}}
	var sb strings.Builder
	printShallowDiagnostics(&sb, in, stats)

	out := sb.String()
	if !strings.Contains(out, "tools were disabled with --no-tools") {
		t.Errorf("missing --no-tools note in:\n%s", out)
	}
	if strings.Contains(out, "--no-clone is set") {
		t.Errorf("unexpected --no-clone note in:\n%s", out)
	}
}

package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitFrontmatter(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantFM   string
		wantBody string
		wantErr  bool
	}{
		{
			name:     "no frontmatter",
			content:  "just a body\n",
			wantFM:   "",
			wantBody: "just a body\n",
		},
		{
			name:     "standard frontmatter",
			content:  "---\ntitle: x\n---\nBody text.\n",
			wantFM:   "title: x",
			wantBody: "Body text.\n",
		},
		{
			name:     "crlf delimiters",
			content:  "---\r\ntitle: x\r\n---\r\nBody text.\r\n",
			wantFM:   "title: x",
			wantBody: "Body text.\r\n",
		},
		{
			name:     "horizontal rule in body survives",
			content:  "---\ntitle: x\n---\nIntro.\n\n---\n\nMore.\n",
			wantFM:   "title: x",
			wantBody: "Intro.\n\n---\n\nMore.\n",
		},
		{
			name:    "unterminated frontmatter",
			content: "---\ntitle: x\nno closing delimiter\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, err := splitFrontmatter(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("splitFrontmatter() err = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("splitFrontmatter() err = %v", err)
			}
			if fm != tt.wantFM || body != tt.wantBody {
				t.Errorf("splitFrontmatter() = (%q, %q), want (%q, %q)", fm, body, tt.wantFM, tt.wantBody)
			}
		})
	}
}

func writeRule(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingDir(t *testing.T) {
	got, err := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Load() err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("Load() = %+v, want nil", got)
	}
}

func TestLoadValidRule(t *testing.T) {
	dir := t.TempDir()
	writeRule(t, dir, "context.md", "---\ntitle: Require context\nagents: [\"static_analysis\"]\nseverity: major\npriority: 10\ntags: [\"go\"]\n---\nUse context.Context.\n")

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	r := got[0]
	if r.Title != "Require context" || r.Severity != SeverityMajor || r.Priority != 10 {
		t.Errorf("frontmatter = %+v", r.Frontmatter)
	}
	if !r.HasAgent(AgentStatic) || r.HasAgent(AgentSecurity) {
		t.Errorf("agent scoping wrong: %v", r.Agents)
	}
	if r.Body != "Use context.Context." {
		t.Errorf("Body = %q", r.Body)
	}
}

func TestLoadDefaultsAndDisabled(t *testing.T) {
	dir := t.TempDir()
	writeRule(t, dir, "no-title.md", "---\nagents: [\"summary\"]\n---\nBody.\n")
	writeRule(t, dir, "off.md", "---\nagents: [\"summary\"]\nenabled: false\n---\nDisabled.\n")

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (disabled rules skipped)", len(got))
	}
	if got[0].Title != "no-title.md" {
		t.Errorf("Title = %q, want filename default", got[0].Title)
	}
	if got[0].Severity != SeverityMinor {
		t.Errorf("Severity = %q, want default minor", got[0].Severity)
	}
}

func TestLoadValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantIn  string
	}{
		{"no agents", "---\ntitle: x\n---\nBody.\n", "at least one agent"},
		{"unknown agent", "---\nagents: [\"bogus\"]\n---\nBody.\n", "unknown agent"},
		{"unknown severity", "---\nagents: [\"triage\"]\nseverity: catastrophic\n---\nBody.\n", "unknown severity"},
		{"empty frontmatter", "---\n---\nBody.\n", "at least one agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeRule(t, dir, "rule.md", tt.content)
			_, err := Load(dir)
			if err == nil {
				t.Fatal("Load() err = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantIn) {
				t.Errorf("err = %v, want containing %q", err, tt.wantIn)
			}
		})
	}
}

func TestLoadSorting(t *testing.T) {
	dir := t.TempDir()
	writeRule(t, dir, "a.md", "---\nagents: [\"static_analysis\"]\npriority: 1\nseverity: blocker\n---\nA.\n")
	writeRule(t, dir, "b.md", "---\nagents: [\"static_analysis\"]\npriority: 10\nseverity: nit\n---\nB.\n")
	writeRule(t, dir, "c.md", "---\nagents: [\"static_analysis\"]\npriority: 10\nseverity: major\n---\nC.\n")

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	var titles []string
	for _, r := range got {
		titles = append(titles, r.Title)
	}
	want := []string{"c.md", "b.md", "a.md"} // priority desc, then severity desc
	for i := range want {
		if titles[i] != want[i] {
			t.Fatalf("order = %v, want %v", titles, want)
		}
	}
}

func TestLoadNestedDirs(t *testing.T) {
	dir := t.TempDir()
	writeRule(t, dir, "top.md", "---\nagents: [\"triage\"]\n---\nTop.\n")
	sub := filepath.Join(dir, "go")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRule(t, sub, "nested.md", "---\nagents: [\"security\"]\n---\nNested.\n")

	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() err = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestForAgentWildcard(t *testing.T) {
	rules := []Rule{
		{Frontmatter: Frontmatter{Agents: []string{AgentAll}}},
		{Frontmatter: Frontmatter{Agents: []string{AgentSecurity}}},
	}
	matched := ForAgent(rules, AgentSecurity)
	if len(matched) != 2 {
		t.Errorf("ForAgent(security) matched %d, want 2 (specific + wildcard)", len(matched))
	}
	if len(ForAgent(rules, AgentTriage)) != 1 {
		t.Errorf("ForAgent(triage) matched %d, want 1 (wildcard only)", len(ForAgent(rules, AgentTriage)))
	}
}

func TestFormatInstruction(t *testing.T) {
	if got := FormatInstruction("base", nil); got != "base" {
		t.Errorf("FormatInstruction(base, nil) = %q, want unchanged", got)
	}

	rules := []Rule{
		{Frontmatter: Frontmatter{Title: "Wrap errors", Severity: SeverityMajor, Priority: 5}, Body: "Wrap all errors."},
	}
	got := FormatInstruction("base", rules)
	if !strings.HasPrefix(got, "base") {
		t.Errorf("formatted instruction must start with base, got %q", got)
	}
	for _, want := range []string{"## Repository rules", "Wrap errors [major, priority 5]", "Wrap all errors."} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted instruction missing %q:\n%s", want, got)
		}
	}
}

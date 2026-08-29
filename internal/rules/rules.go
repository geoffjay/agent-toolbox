// Package rules loads repository-specific review rules from .review/rules
// and compiles them into supplementary instructions for the review agents.
//
// Rule files are Markdown with YAML frontmatter. The frontmatter scopes
// each rule to one or more agents and controls severity, priority, and
// enablement. The Markdown body is the rule guidance text appended to
// matching agents' system instructions.
//
// Example rule file:
//
//	---
//	title: "Require context parameter"
//	agents: ["static_analysis", "security"]
//	severity: major
//	priority: 10
//	tags: ["go", "context"]
//	---
//	All functions that make network calls must accept a context.Context
//	as their first parameter.
package rules

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Agent name constants matching the agents package. Duplicated here to
// avoid an import cycle (agents imports rules indirectly via graph).
const (
	AgentTriage   = "triage"
	AgentStatic   = "static_analysis"
	AgentSecurity = "security"
	AgentSummary  = "summary"
	AgentAll      = "*"
)

// Severity levels ordered from most to least important.
const (
	SeverityBlocker = "blocker"
	SeverityMajor   = "major"
	SeverityMinor   = "minor"
	SeverityNit     = "nit"
)

// Frontmatter is the YAML metadata at the top of a rule file.
type Frontmatter struct {
	Title    string   `yaml:"title"`
	Agents   []string `yaml:"agents"`
	Severity string   `yaml:"severity"`
	Priority int      `yaml:"priority"`
	Enabled  *bool    `yaml:"enabled"`
	Tags     []string `yaml:"tags"`
}

// Rule is a parsed rule file: frontmatter plus the Markdown body.
type Rule struct {
	Frontmatter
	Body string
	File string
}

// HasAgent reports whether the rule applies to the given agent name.
func (r Rule) HasAgent(agent string) bool {
	for _, a := range r.Agents {
		if a == agent || a == AgentAll {
			return true
		}
	}
	return false
}

// Load reads all *.md files recursively from dir, parses frontmatter and
// body, and returns the rules sorted by priority (descending) then
// severity (blocker > major > minor > nit). Disabled rules are skipped.
// Returns nil if dir does not exist.
func Load(dir string) ([]Rule, error) {
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read rules dir %q: %w", dir, err)
	}

	var rules []Rule
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rule, err := loadRule(path)
		if err != nil {
			return fmt.Errorf("load rule %q: %w", path, err)
		}
		if rule != nil {
			rules = append(rules, *rule)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk rules directory: %w", err)
	}

	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority > rules[j].Priority
		}
		return severityRank(rules[i].Severity) > severityRank(rules[j].Severity)
	})
	return rules, nil
}

var validAgents = map[string]bool{
	AgentTriage:   true,
	AgentStatic:   true,
	AgentSecurity: true,
	AgentSummary:  true,
	AgentAll:      true,
}

var validSeverities = map[string]bool{
	SeverityBlocker: true,
	SeverityMajor:   true,
	SeverityMinor:   true,
	SeverityNit:     true,
}

func loadRule(path string) (*Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rule %s: %w", path, err)
	}

	fm, body, err := splitFrontmatter(string(data))
	if err != nil {
		return nil, err
	}

	rule := &Rule{File: path}
	if fm != "" {
		dec := yaml.NewDecoder(bytes.NewReader([]byte(fm)))
		dec.KnownFields(true)
		if err := dec.Decode(&rule.Frontmatter); err != nil {
			return nil, fmt.Errorf("parse frontmatter: %w", err)
		}
	}
	rule.Body = strings.TrimSpace(body)

	if rule.Enabled != nil && !*rule.Enabled {
		return nil, nil
	}
	if len(rule.Agents) == 0 {
		return nil, fmt.Errorf("rule %q: frontmatter must specify at least one agent", path)
	}
	for _, a := range rule.Agents {
		if !validAgents[a] {
			return nil, fmt.Errorf("rule %q: unknown agent %q (valid: triage, static_analysis, security, summary, *)", path, a)
		}
	}
	if rule.Severity == "" {
		rule.Severity = SeverityMinor
	}
	if !validSeverities[rule.Severity] {
		return nil, fmt.Errorf("rule %q: unknown severity %q (valid: blocker, major, minor, nit)", path, rule.Severity)
	}
	if rule.Title == "" {
		rule.Title = filepath.Base(path)
	}

	return rule, nil
}

var closeFrontmatterRe = regexp.MustCompile(`(?m)^---\s*$`)

// splitFrontmatter separates YAML frontmatter from the Markdown body.
// Frontmatter is delimited by --- at the start of the file and a closing
// --- on its own line. Returns empty frontmatter if no frontmatter is
// present.
func splitFrontmatter(content string) (frontmatter, body string, err error) {
	// Check for opening delimiter: --- at line start, followed by newline.
	var nlLen int
	if strings.HasPrefix(content, "---\r\n") {
		nlLen = 5
	} else if strings.HasPrefix(content, "---\n") {
		nlLen = 4
	} else {
		return "", content, nil
	}

	rest := content[nlLen:]

	// Find the closing --- on its own line (not mid-paragraph).
	loc := closeFrontmatterRe.FindStringIndex(rest)
	if loc == nil {
		return "", "", fmt.Errorf("unterminated frontmatter: missing closing ---")
	}

	frontmatter = strings.TrimSpace(rest[:loc[0]])
	// Skip past the closing --- and its trailing newline.
	afterClose := rest[loc[1]:]
	afterClose = strings.TrimLeft(afterClose, "\r\n")
	body = afterClose
	return frontmatter, body, nil
}

func severityRank(s string) int {
	switch strings.ToLower(s) {
	case SeverityBlocker:
		return 4
	case SeverityMajor:
		return 3
	case SeverityMinor:
		return 2
	case SeverityNit:
		return 1
	default:
		return 0
	}
}

// ForAgent returns the rules that apply to the given agent name.
func ForAgent(rules []Rule, agent string) []Rule {
	var matched []Rule
	for _, r := range rules {
		if r.HasAgent(agent) {
			matched = append(matched, r)
		}
	}
	return matched
}

// FormatInstruction appends matched rules to a base instruction string,
// producing a single instruction block for the agent. Rules are ordered
// by priority and severity (as returned by Load). If no rules match,
// the base instruction is returned unchanged.
func FormatInstruction(base string, rules []Rule) string {
	if len(rules) == 0 {
		return base
	}

	var sb strings.Builder
	sb.WriteString(base)
	sb.WriteString("\n\n## Repository rules\n\n")
	sb.WriteString("The following repository-specific rules apply to your review. ")
	sb.WriteString("Treat them as additional requirements alongside the instructions above.\n\n")

	for _, r := range rules {
		fmt.Fprintf(&sb, "### %s [%s, priority %d]\n\n", r.Title, r.Severity, r.Priority)
		sb.WriteString(r.Body)
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// DefaultRulesDir is the conventional location for repository rules.
const DefaultRulesDir = ".review/rules"

// FindRulesDir locates the rules directory relative to repoRoot. Returns
// the path if it exists, empty string otherwise.
func FindRulesDir(repoRoot string) string {
	dir := filepath.Join(repoRoot, DefaultRulesDir)
	if _, err := os.Stat(dir); err == nil {
		return dir
	}
	return ""
}

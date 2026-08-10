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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Agent name constants matching the agents package. Duplicated here to
// avoid an import cycle (agents imports rules indirectly via graph).
const (
	AgentTriage  = "triage"
	AgentStatic  = "static_analysis"
	AgentSecurity = "security"
	AgentSummary = "summary"
	AgentAll     = "*"
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
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read rules dir %q: %w", dir, err)
	}

	var rules []Rule
	for _, e := range entries {
		if err := walkDir(dir, e, &rules); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority > rules[j].Priority
		}
		return severityRank(rules[i].Severity) > severityRank(rules[j].Severity)
	})
	return rules, nil
}

func walkDir(base string, entry os.DirEntry, rules *[]Rule) error {
	full := filepath.Join(base, entry.Name())
	if entry.IsDir() {
		entries, err := os.ReadDir(full)
		if err != nil {
			return fmt.Errorf("read subdir %q: %w", full, err)
		}
		for _, e := range entries {
			if err := walkDir(full, e, rules); err != nil {
				return err
			}
		}
		return nil
	}
	if !strings.HasSuffix(entry.Name(), ".md") {
		return nil
	}
	rule, err := loadRule(full)
	if err != nil {
		return fmt.Errorf("load rule %q: %w", full, err)
	}
	if rule != nil {
		*rules = append(*rules, *rule)
	}
	return nil
}

func loadRule(path string) (*Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	fm, body, err := splitFrontmatter(string(data))
	if err != nil {
		return nil, err
	}

	rule := &Rule{File: path}
	if fm != "" {
		if err := yaml.Unmarshal([]byte(fm), &rule.Frontmatter); err != nil {
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
	if rule.Severity == "" {
		rule.Severity = SeverityMinor
	}
	if rule.Title == "" {
		rule.Title = filepath.Base(path)
	}

	return rule, nil
}

// splitFrontmatter separates YAML frontmatter from the Markdown body.
// Frontmatter is delimited by --- at the start of the file.
func splitFrontmatter(content string) (frontmatter, body string, err error) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return "", content, nil
	}

	rest := content[4:]
	if strings.HasPrefix(content, "---\r\n") {
		rest = content[5:]
	}

	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return "", "", fmt.Errorf("unterminated frontmatter: missing closing ---")
	}

	frontmatter = strings.TrimSpace(rest[:idx])
	body = rest[idx+4:]
	if len(body) > 0 && body[0] == '\r' {
		body = body[1:]
	}
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	}
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
		sb.WriteString(fmt.Sprintf("### %s [%s, priority %d]\n\n", r.Title, r.Severity, r.Priority))
		sb.WriteString(r.Body)
		sb.WriteString("\n\n")
	}

	return sb.String()
}

// LoadAndFormat is a convenience function that loads rules from dir and
// returns a map of agent name → formatted instruction, keyed by the
// agents that have matching rules.
func LoadAndFormat(dir string, baseInstructions map[string]string) (map[string]string, error) {
	rules, err := Load(dir)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return baseInstructions, nil
	}

	result := make(map[string]string, len(baseInstructions))
	for agent, base := range baseInstructions {
		result[agent] = FormatInstruction(base, ForAgent(rules, agent))
	}
	return result, nil
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
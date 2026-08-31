package cmd

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/workflow"
	"google.golang.org/genai"

	"github.com/geoffjay/graph-review/internal/agents"
	"github.com/geoffjay/graph-review/internal/graph"
	"github.com/geoffjay/graph-review/internal/review"
	"github.com/geoffjay/graph-review/internal/rules"
	"github.com/geoffjay/graph-review/internal/tools"
	"github.com/geoffjay/graph-review/internal/ui"
)

// modelFlags carries the model options shared by every review subcommand.
type modelFlags struct {
	provider  string
	modelName string
	apiKey    string
	authToken string
	baseURL   string
}

// loggingFlags carries the diagnostic log controls shared by every review
// subcommand. The plain surface logs to stderr; the TUI surface logs to a
// per-run file in the user cache dir because bubbletea owns the terminal.
type loggingFlags struct {
	verbose int  // -v count
	debug   bool // --debug
}

// level maps the logging flags onto slog levels: warnings only by
// default, pipeline flow with -v, and full model/tool payloads with
// -vv or --debug.
func (lf loggingFlags) level() slog.Level {
	switch {
	case lf.debug || lf.verbose >= 2:
		return slog.LevelDebug
	case lf.verbose == 1:
		return slog.LevelInfo
	default:
		return slog.LevelWarn
	}
}

func addLoggingFlags(cmd *cobra.Command, lf *loggingFlags) {
	cmd.Flags().CountVarP(&lf.verbose, "verbose", "v",
		"Increase log verbosity: -v shows pipeline flow, -vv adds raw agent output and tool activity")
	cmd.Flags().BoolVar(&lf.debug, "debug", false,
		"Log debug detail: every model event, tool call arguments, and tool results")
}

// debugPayloadMax bounds payload logging at debug level so a large diff
// response cannot flood the terminal.
const debugPayloadMax = 4 << 10 // 4 KiB

// debugSnippet truncates s for debug payload logs.
func debugSnippet(s string) string {
	if len(s) <= debugPayloadMax {
		return s
	}
	return s[:debugPayloadMax] + fmt.Sprintf("\n… (%d bytes truncated)", len(s)-debugPayloadMax)
}

// pipelineFlags carries the pipeline-level options shared by every review
// subcommand: per-agent instruction overrides and the no-tools toggle.
type pipelineFlags struct {
	triageInstruction   string
	staticInstruction   string
	securityInstruction string
	summaryInstruction  string
	noTools             bool
	rulesDir            string
	findingsGate        bool
}

func reviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Run the code review pipeline",
		Long: `Run the code review pipeline over a diff or a GitHub pull request.

Subcommands:
  review [file]              Review a unified diff from a file or stdin.
  review pr <owner/repo> <n> Review a GitHub pull request by number.`,
	}
	cmd.AddCommand(reviewDiffCmd())
	cmd.AddCommand(reviewPRCmd())
	return cmd
}

// reviewDiffCmd builds the `review [file]` subcommand.
func reviewDiffCmd() *cobra.Command {
	var (
		mf        modelFlags
		pf        pipelineFlags
		lf        loggingFlags
		repoPath  string
		sessionID string
		plain     bool
	)

	cmd := &cobra.Command{
		Use:   "diff [file]",
		Short: "Review a unified diff from a file or stdin",
		Long: `Run the code review pipeline over a unified diff.

The diff is read from the file given as an argument, or from stdin when
the argument is "-" or omitted. The reviewer agents can call
repo-inspection tools (read_file, list_files, git_blame, git_log) rooted
at --repo (default: working directory). Use --no-tools to disable them.

Progress is shown in a terminal interface when stdout is a terminal;
pass --plain for the classic streaming output.`,
		Aliases: []string{"file"},
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			diff, err := readDiff(args)
			if err != nil {
				return err
			}
			if strings.TrimSpace(diff) == "" {
				return fmt.Errorf("no diff input to review")
			}

			absRepo, err := filepath.Abs(repoPath)
			if err != nil {
				return fmt.Errorf("resolve repo path: %w", err)
			}

			state := map[string]any{
				tools.RepoPathStateKey: absRepo,
			}
			work := func(ctx context.Context, p Presenter) error {
				report, err := runPipeline(ctx, runPipelineInput{
					modelFlags:    mf,
					pipelineFlags: pf,
					diff:          diff,
					sessionID:     sessionID,
					state:         state,
					label:         fmt.Sprintf("reviewing %d bytes of diff with model %s", len(diff), mf.modelName),
					repoRoot:      absRepo,
				}, p)
				if err != nil {
					return fmt.Errorf("run pipeline: %w", err)
				}
				p.Finish(report)
				return nil
			}
			return dispatch(ctx, lf, plain, work)
		},
	}

	addModelFlags(cmd, &mf)
	addLoggingFlags(cmd, &lf)
	addPipelineFlags(cmd, &pf)
	addUIFlags(cmd, &plain)
	cmd.Flags().StringVar(&repoPath, "repo", ".", "Repository root the review tools operate in")
	cmd.Flags().StringVar(&sessionID, "session", "", "Session ID for the runner (random by default)")

	return cmd
}

// addUIFlags wires the presentation-surface flags onto a command.
func addUIFlags(cmd *cobra.Command, plain *bool) {
	cmd.Flags().BoolVar(plain, "plain", false, "Use classic stdout/stderr output instead of the terminal interface")
}

// payloadText renders a gate request payload for display: strings are
// kept verbatim, anything else falls back to JSON.
func payloadText(payload any) string {
	if payload == nil {
		return ""
	}
	if s, ok := payload.(string); ok {
		return s
	}
	return jsonString(payload)
}

// runPipelineInput bundles the inputs to runPipeline.
type runPipelineInput struct {
	modelFlags
	pipelineFlags
	diff      string
	sessionID string
	state     map[string]any
	label     string
	repoRoot  string
	noClone   bool
}

// runPipeline builds the model, tools, graph, and runner from the given
// input, sends the diff as a user message, and streams the agent output
// through the presenter. The state map is seeded into the runner via
// WithStateDelta so the tools can read repo_path / pr_ref at runtime. It
// returns the full text output of the pipeline (concatenated non-thought
// text parts).
func runPipeline(ctx context.Context, in runPipelineInput, p Presenter) (string, error) {
	m, err := agents.NewModel(ctx, agents.ModelConfig{
		Provider:  agents.Provider(in.provider),
		ModelName: in.modelName,
		APIKey:    in.apiKey,
		AuthToken: in.authToken,
		BaseURL:   in.baseURL,
	})
	if err != nil {
		return "", fmt.Errorf("build model: %w", err)
	}

	var reviewTools []tool.Tool
	if !in.noTools {
		reviewTools, err = tools.NewTools()
		if err != nil {
			return "", fmt.Errorf("build tools: %w", err)
		}
		prTools, err := tools.NewPRTools()
		if err != nil {
			return "", fmt.Errorf("build PR tools: %w", err)
		}
		reviewTools = append(reviewTools, prTools...)
	}

	rulesDir := in.rulesDir
	if rulesDir == "" && in.repoRoot != "" {
		rulesDir = rules.FindRulesDir(in.repoRoot)
	}

	root, err := graph.New(ctx, graph.Config{
		Model:               m,
		Tools:               reviewTools,
		TriageInstruction:   in.triageInstruction,
		StaticInstruction:   in.staticInstruction,
		SecurityInstruction: in.securityInstruction,
		SummaryInstruction:  in.summaryInstruction,
		RulesDir:            rulesDir,
		FindingsGate:        in.findingsGate,
	})
	if err != nil {
		return "", fmt.Errorf("build pipeline: %w", err)
	}

	r, err := runner.NewInMemory("graph-review", root)
	if err != nil {
		return "", fmt.Errorf("build runner: %w", err)
	}

	userID := "graph-review-cli"
	sessionID := in.sessionID
	if sessionID == "" {
		sessionID = randomSessionID()
	}

	slog.Info("pipeline starting",
		"label", in.label,
		"model", m.Name(),
		"tools", toolNames(reviewTools),
		"rules_dir", rulesDir,
		"repo_root", in.repoRoot,
		"state", in.state,
		"session", sessionID)

	msg := &genai.Content{
		Parts: []*genai.Part{
			{Text: "Please review the following diff:\n\n```diff\n" + in.diff + "\n```"},
		},
		Role: "user",
	}

	p.Start(in.label)

	var output strings.Builder
	stats := &pipelineStats{agentText: map[string]int{}, toolCalls: map[string]int{}}

	runOpts := []runner.RunOption{
		runner.WithStateDelta(in.state),
	}

	// The run loop supports gates: when a node pauses for human input
	// (ev.RequestedInput) the turn ends, the CLI asks the human through
	// the presenter, and a second Run turn on the same session delivers
	// the answer as an adk_request_input FunctionResponse. The loop
	// repeats until a turn completes without pausing.
	for {
		var pending *session.RequestInput
		for ev, err := range r.Run(ctx, userID, sessionID, msg, agentRunConfig(), runOpts...) {
			if err != nil {
				stats.logActivities()
				return output.String(), fmt.Errorf("run agents: %w", err)
			}
			if ev == nil {
				continue
			}
			if ev.RequestedInput != nil {
				pending = ev.RequestedInput
			}
			if ev.Content == nil {
				continue
			}
			stats.events++
			for _, part := range ev.Content.Parts {
				switch {
				case part.FunctionCall != nil:
					stats.toolCalls[ev.Author]++
					p.Activity(fmt.Sprintf("%s → %s", ev.Author, part.FunctionCall.Name))
					slog.Debug("tool call",
						"agent", ev.Author,
						"tool", part.FunctionCall.Name,
						"args", debugSnippet(jsonString(part.FunctionCall.Args)))
				case part.FunctionResponse != nil:
					slog.Debug("tool result",
						"agent", ev.Author,
						"tool", part.FunctionResponse.Name,
						"result", debugSnippet(jsonString(part.FunctionResponse.Response)))
				case part.Text != "":
					stats.agentText[ev.Author] += len(part.Text)
					if part.Thought {
						slog.Debug("model thought", "agent", ev.Author, "text", debugSnippet(part.Text))
						continue
					}
					slog.Debug("agent text", "agent", ev.Author, "text", debugSnippet(part.Text))
					p.Activity(ev.Author)
					p.Stream(ev.Author, part.Text)
					if ev.Author == agents.SummaryAgentName {
						output.WriteString(part.Text)
					}
				}
			}
		}
		if pending == nil {
			break
		}
		answer, err := p.Gate(ui.GateRequest{
			Message: pending.Message,
			Payload: payloadText(pending.Payload),
		})
		if err != nil {
			stats.logActivities()
			return output.String(), fmt.Errorf("collect gate decision: %w", err)
		}
		slog.Info("resuming pipeline", "interrupt", pending.InterruptID)
		msg = resumeMessage(pending.InterruptID, answer)
	}

	report := output.String()
	findings := review.ParseFindings(report)
	slog.Info("pipeline finished",
		"events", stats.events,
		"tool_calls", stats.totalToolCalls(),
		"report_bytes", len(report),
		"findings", len(findings))
	stats.logActivities()

	switch {
	case strings.TrimSpace(report) == "":
		p.Warn("WARNING: the review produced no report at all.\n" +
			"Every agent returned an empty response — the model likely failed.\n" +
			"Check the log for details; re-run with -vv to trace the failure.\n" +
			shallowDiagnostics(in, stats))
	case warnShallowReview(report, in.diff):
		p.Warn("WARNING: the review produced no findings for a non-trivial diff.\n" +
			"This may indicate the model did not thoroughly analyze the code.\n" +
			"Consider using a stronger model or reviewing manually.\n" +
			shallowDiagnostics(in, stats))
	}
	return report, nil
}

// promptGate renders a paused gate's request on the terminal and collects
// the human's decision (with feedback on revise). It fails closed when
// stdin is not an interactive terminal.
func promptGate(req ui.GateRequest) (map[string]any, error) {
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return nil, fmt.Errorf("pipeline paused for human input (%q) but stdin is not a terminal; re-run interactively", req.Message)
	}
	return promptGateAnswer(os.Stdin, os.Stderr, req)
}

// promptGateAnswer reads a gate decision (and revision feedback) from in,
// rendering the request on out. Split from promptGate for testability.
func promptGateAnswer(in io.Reader, out io.Writer, req ui.GateRequest) (map[string]any, error) {
	_, _ = fmt.Fprintln(out, "\n=== human gate ===")
	if req.Message != "" {
		_, _ = fmt.Fprintln(out, req.Message)
	}
	if req.Payload != "" {
		_, _ = fmt.Fprintln(out, req.Payload)
	}
	_, _ = fmt.Fprint(out, "decision [approve/revise/abort]: ")
	// One buffered reader for the whole interaction: a second reader over
	// the same stream would lose whatever the first had buffered ahead.
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return nil, fmt.Errorf("read gate decision: %w", err)
	}
	decision := strings.ToLower(strings.TrimSpace(line))
	switch decision {
	case agents.DecisionApprove, agents.DecisionRevise, agents.DecisionAbort:
	default:
		return nil, fmt.Errorf("invalid gate decision %q (want approve, revise, or abort)", decision)
	}
	answer := map[string]any{"decision": decision}
	if decision == agents.DecisionRevise {
		_, _ = fmt.Fprintln(out, "feedback for the reviewers (end with a single '.'):")
		var lines []string
		for {
			l, rerr := reader.ReadString('\n')
			if strings.TrimSpace(l) == "." {
				break
			}
			if l != "" {
				lines = append(lines, l)
			}
			if rerr != nil {
				break // EOF or failure: keep what was collected
			}
		}
		feedback := strings.Join(lines, "")
		if strings.TrimSpace(feedback) == "" {
			return nil, fmt.Errorf("revise requires feedback describing what to change")
		}
		answer["feedback"] = feedback
	}
	return answer, nil
}

// resumeMessage builds the user-side Content that resumes a paused
// workflow: a single FunctionResponse part whose ID/name match the
// gate's adk_request_input interrupt, with the answer wrapped under
// the "payload" key (the wire shape the workflow agent decodes).
func resumeMessage(interruptID string, answer any) *genai.Content {
	return &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{{
			FunctionResponse: &genai.FunctionResponse{
				ID:       interruptID,
				Name:     workflow.WorkflowInputFunctionCallName,
				Response: map[string]any{"payload": answer},
			},
		}},
	}
}

// shallowDiagnostics renders the diagnostics block as a string for the
// presenter surfaces.
func shallowDiagnostics(in runPipelineInput, stats *pipelineStats) string {
	var b strings.Builder
	printShallowDiagnostics(&b, in, stats)
	return b.String()
}

// printShallowDiagnostics explains what the pipeline actually observed,
// so an empty review can be traced to missing tool use, missing agent
// output, or a mis-rooted repo.
func printShallowDiagnostics(w io.Writer, in runPipelineInput, stats *pipelineStats) {
	_, _ = fmt.Fprintf(w, "\ndiagnostics: %d events, %d tool call(s)\n", stats.events, stats.totalToolCalls())
	for _, author := range sortedKeys(stats.agentText) {
		_, _ = fmt.Fprintf(w, "  %s: %d bytes of output, %d tool call(s)\n",
			author, stats.agentText[author], stats.toolCalls[author])
	}
	if in.noTools {
		_, _ = fmt.Fprintln(w, "  tools were disabled with --no-tools: reviewers saw only the diff")
	} else if stats.totalToolCalls() == 0 {
		_, _ = fmt.Fprintln(w, "  no reviewer tool calls were made: the diff was reviewed without any repo context")
	}
	if in.noClone {
		_, _ = fmt.Fprintln(w, "  --no-clone is set: repo-inspection tools ran against the current")
		_, _ = fmt.Fprintln(w, "  directory, which may not be the PR repository; prefer --clone-repo <path>")
	}
	_, _ = fmt.Fprintln(w, "re-run with -vv (or --debug) to trace every model event and tool payload on stderr")
}

// jsonString renders v as JSON for debug logs, falling back to %v.
func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// toolNames lists tool names for the pipeline-start log line.
func toolNames(tools []tool.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name())
	}
	return names
}

// pipelineStats records what the run actually did so shallow reviews can
// be diagnosed from observed behavior instead of guesses.
type pipelineStats struct {
	events    int
	toolCalls map[string]int // agent -> tool call count
	agentText map[string]int // agent -> non-thought text bytes
}

func (s *pipelineStats) totalToolCalls() int {
	n := 0
	for _, c := range s.toolCalls {
		n += c
	}
	return n
}

// logActivities emits per-agent activity at info level.
func (s *pipelineStats) logActivities() {
	for _, author := range sortedKeys(s.agentText) {
		slog.Info("agent activity",
			"agent", author,
			"text_bytes", s.agentText[author],
			"tool_calls", s.toolCalls[author])
	}
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// warnShallowReview returns true if the report has no findings and the
// diff is non-trivial (more than 10 changed lines, excluding file headers
// and index lines).
func warnShallowReview(report, diff string) bool {
	if countDiffLines(diff) <= 10 {
		return false
	}
	findings := review.Section(report, "## Findings")
	if findings == "" {
		return true
	}
	lower := strings.ToLower(findings)
	return strings.Contains(lower, "no findings") ||
		strings.Contains(lower, "none reported") ||
		strings.Contains(lower, "no issues")
}

func countDiffLines(diff string) int {
	count := 0
	for line := range strings.SplitSeq(diff, "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			count++
		} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
			count++
		}
	}
	return count
}

// addModelFlags wires the model flags onto a command.
func addModelFlags(cmd *cobra.Command, mf *modelFlags) {
	cmd.Flags().StringVar(&mf.provider, "provider", "", "Model provider: openai or anthropic (env OPENAI_API_KEY/ANTHROPIC_API_KEY)")
	cmd.Flags().StringVarP(&mf.modelName, "model", "m", "", "Model name (env OPENAI_MODEL or ANTHROPIC_MODEL)")
	cmd.Flags().StringVar(&mf.apiKey, "api-key", "", "API key for the model endpoint (env OPENAI_API_KEY or ANTHROPIC_API_KEY)")
	cmd.Flags().StringVar(&mf.authToken, "auth-token", "", "Authorization: Bearer token for the endpoint (Anthropic gateways/proxies; env ANTHROPIC_AUTH_TOKEN)")
	cmd.Flags().StringVar(&mf.baseURL, "base-url", "", "Base URL for the endpoint (env OPENAI_BASE_URL or ANTHROPIC_BASE_URL)")
}

// instructionOverrideHelp is the shared suffix for the agent-instruction
// override flags: an override replaces the built-in default, which
// carries the ASD-STE100 style rules.
const instructionOverrideHelp = " (replaces the built-in guidance, including its ASD-STE100 style rules)"

// addPipelineFlags wires the shared pipeline flags onto a command.
func addPipelineFlags(cmd *cobra.Command, pf *pipelineFlags) {
	cmd.Flags().BoolVar(&pf.noTools, "no-tools", false, "Disable repo-inspection and PR tools on the reviewer agents")
	cmd.Flags().StringVar(&pf.rulesDir, "rules-dir", "", "Path to repository rules directory (default: .review/rules relative to repo root)")
	cmd.Flags().BoolVar(&pf.findingsGate, "findings-gate", false, "Pause after the reviewers and require a human to approve the findings (revise loops the reviewers with your feedback)")
	cmd.Flags().StringVar(&pf.triageInstruction, "triage-instruction", "", "Override the triage agent instruction (replaces the built-in guidance)")
	cmd.Flags().StringVar(&pf.staticInstruction, "static-instruction", "", "Override the static analysis agent instruction"+instructionOverrideHelp)
	cmd.Flags().StringVar(&pf.securityInstruction, "security-instruction", "", "Override the security agent instruction"+instructionOverrideHelp)
	cmd.Flags().StringVar(&pf.summaryInstruction, "summary-instruction", "", "Override the summary agent instruction"+instructionOverrideHelp)
}

func readDiff(args []string) (string, error) {
	if len(args) == 0 || args[0] == "-" {
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := os.ReadFile(args[0])
	return string(b), err
}

func agentRunConfig() agent.RunConfig {
	return agent.RunConfig{}
}

// randomSessionID returns a sortable, collision-resistant session ID
// for ad-hoc runs: a timestamp plus random hex suffix.
func randomSessionID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "review-" + time.Now().Format("20060102-150405")
	}
	return "review-" + time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

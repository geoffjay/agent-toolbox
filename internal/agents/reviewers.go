package agents

import (
	"fmt"
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/workflow"
)

// ReviewersNodeName is the workflow node name for the dynamic reviewer
// orchestrator.
const ReviewersNodeName = "reviewers"

// reviewerTarget pairs an agent name with its workflow node for one
// fan-out invocation.
type reviewerTarget struct {
	name string
	node workflow.Node
}

// ReviewersNode returns the dynamic node that runs the reviewer agents
// triage selected, in parallel. It reads the category recorded by the
// triage step from session state (TriageCategoryStateKey) on every
// activation — including findings-gate revise rounds, reached through the
// gate's loop-back edge — so a revise round re-runs the same reviewer set
// the human saw, not both reviewers.
//
// Why a dynamic node instead of graph edges + a JoinNode: ADK's JoinNode
// waits for every declared predecessor, and a predecessor that conditional
// routing skipped never fires, so a single-reviewer triage verdict ("run
// security only") deadlocks the gather barrier — the scheduler drains
// without ever scheduling the join, the run ends with no summary output,
// and the pipeline's findings are lost. The orchestrator expresses
// "wait only for the reviewers that ran" as plain Go code, which is
// ADK's sanctioned alternative to conditional fan-in.
//
// The returned map is keyed by agent name (StaticAgentName /
// SecurityAgentName) — the same shape the old gather JoinNode emitted —
// so FormatFindings consumes it unchanged.
func ReviewersNode(static, security workflow.Node) workflow.Node {
	return workflow.NewDynamicNode[string, map[string]any](
		ReviewersNodeName,
		func(ctx agent.Context, reviewRequest string, _ func(*session.Event) error) (map[string]any, error) {
			targets := reviewerTargetsFor(categoryFromState(ctx), static, security)
			runID := fmt.Sprintf("r%d", revisionCount(ctx)+1)

			results := make([]string, len(targets))
			errs := make([]error, len(targets))
			var wg sync.WaitGroup
			for i, t := range targets {
				wg.Add(1)
				go func(i int, t reviewerTarget) {
					defer wg.Done()
					// WithUseSubBranch gives each reviewer its own
					// branch of the session so parallel LLM turns
					// don't pollute each other's request contents.
					// WithRunID scopes this invocation's cache so
					// revise rounds re-run the reviewers instead of
					// serving round-1 output from cache.
					out, err := workflow.RunNode[string](ctx, t.node, reviewRequest,
						workflow.WithUseSubBranch(), workflow.WithRunID(runID))
					results[i] = out
					errs[i] = err
				}(i, t)
			}
			wg.Wait()

			for i, err := range errs {
				if err != nil {
					return nil, fmt.Errorf("%s reviewer: %w", targets[i].name, err)
				}
			}
			gathered := map[string]any{}
			for i, t := range targets {
				gathered[t.name] = results[i]
			}
			return gathered, nil
		},
		workflow.NodeConfig{},
	)
}

// reviewerTargetsFor maps a triage category to the reviewer nodes to run.
func reviewerTargetsFor(category string, static, security workflow.Node) []reviewerTarget {
	var targets []reviewerTarget
	switch category {
	case RouteStatic:
		targets = append(targets, reviewerTarget{StaticAgentName, static})
	case RouteSecurity:
		targets = append(targets, reviewerTarget{SecurityAgentName, security})
	default: // RouteBoth, or unknown: run both so nothing is skipped
		targets = append(targets, reviewerTarget{StaticAgentName, static})
		targets = append(targets, reviewerTarget{SecurityAgentName, security})
	}
	return targets
}

// categoryFromState reads the triage category from session state,
// normalizing it (unknown or missing → both) so an off-script triage
// reply can't silently drop a reviewer.
func categoryFromState(ctx agent.Context) string {
	v, err := ctx.State().Get(TriageCategoryStateKey)
	if err != nil {
		return RouteBoth
	}
	return NormalizeTriageCategory(fmt.Sprint(v))
}

// revisionCount reads the findings-gate revision counter from session
// state. The value may round-trip through JSON as a float, so the stored
// type is normalized on read. Absent → 0 (first pass).
func revisionCount(ctx agent.Context) int {
	n := 0
	if v, err := ctx.State().Get(findingsRevisionsStateKey); err == nil {
		switch c := v.(type) {
		case int:
			n = c
		case int64:
			n = int(c)
		case float64:
			n = int(c)
		}
	}
	return n
}

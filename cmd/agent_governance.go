// Governance lines for astro agent inspect and logs (#113).
//
// When the working organization is connected to Zentinelle, inspect adds the
// task's agent's policies in effect and its last 24 hours of blocks,
// violations and approvals; logs adds the same events for the task's own time
// window. A server that does not offer the reads shows nothing new; one that
// is not connected, or refuses the caller, shows the reason in one line.

package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

const agentTaskWindowQuery = `query($id: ID!) {
  agentTask(id: $id) { agentSlug createdAt startedAt finishedAt }
}`

// agentGovernance is the governance field: a reason it is unavailable, or the report.
type agentGovernance struct {
	Unavailable string `json:"unavailable,omitempty"`
	*governanceReport
}

type governanceReport struct {
	AgentSlug  string                 `json:"agentSlug"`
	Since      string                 `json:"since"`
	Until      string                 `json:"until,omitempty"`
	Policies   []zentinellePolicy     `json:"policies"`
	Blocks     int                    `json:"blocks"`
	Violations int                    `json:"violations"`
	Approvals  int                    `json:"approvals"`
	Events     []zentinelleAuditEvent `json:"events"`
}

// governanceKind sorts an audit event into a block (an enforcement), a
// violation or an approval; anything else is not a governance line.
func governanceKind(e zentinelleAuditEvent) string {
	what := strings.ToLower(e.Action + " " + e.ResourceType)
	switch {
	case strings.Contains(what, "violation"):
		return "violation"
	case strings.Contains(what, "block"), strings.Contains(what, "enforcement"):
		return "block"
	case strings.Contains(what, "approval"):
		return "approval"
	}
	return ""
}

// readAgentGovernance reads governance for a task's agent. With taskWindow it
// covers the task's start to its finish (or now); otherwise the last 24 hours.
// It returns nil when the server does not offer the reads or cannot say.
func readAgentGovernance(ctx context.Context, client *api.Client, taskID string, taskWindow bool, now time.Time) *agentGovernance {
	state, err := zentinelleCapability(ctx, client, client.Org())
	if err != nil || state == zentinelleNotOffered {
		return nil
	}
	if err := zentinelleRefusal(ctx, client, client.Org(), client.BaseURL()); err != nil {
		return &agentGovernance{Unavailable: err.Error()}
	}
	var resp struct {
		Task *struct {
			AgentSlug  string  `json:"agentSlug"`
			CreatedAt  string  `json:"createdAt"`
			StartedAt  *string `json:"startedAt"`
			FinishedAt *string `json:"finishedAt"`
		} `json:"agentTask"`
	}
	if err := client.GraphQL(ctx, agentTaskWindowQuery, map[string]interface{}{"id": taskID}, &resp); err != nil || resp.Task == nil || resp.Task.AgentSlug == "" {
		return nil
	}
	report := &governanceReport{AgentSlug: resp.Task.AgentSlug, Since: now.Add(-24 * time.Hour).UTC().Format(time.RFC3339)}
	var until time.Time
	if taskWindow {
		report.Since = resp.Task.CreatedAt
		if resp.Task.StartedAt != nil && *resp.Task.StartedAt != "" {
			report.Since = *resp.Task.StartedAt
		}
		if resp.Task.FinishedAt != nil && *resp.Task.FinishedAt != "" {
			report.Until = *resp.Task.FinishedAt
			until, _ = time.Parse(time.RFC3339, report.Until)
		}
	}
	vars := map[string]interface{}{"agentSlug": report.AgentSlug}
	if err := client.GraphQL(ctx, zentinellePoliciesQuery, vars, &struct {
		Policies *[]zentinellePolicy `json:"astroliftZentinellePolicies"`
	}{&report.Policies}); err != nil {
		return &agentGovernance{Unavailable: fmt.Sprintf("reading Zentinelle policies: %v", err)}
	}
	vars["since"], vars["limit"] = report.Since, 200
	report.Events = []zentinelleAuditEvent{}
	var cursor *string
	for {
		vars["cursor"] = cursor
		events, next, err := auditPage(ctx, client, vars)
		if err != nil {
			return &agentGovernance{Unavailable: err.Error()}
		}
		for _, e := range events {
			if !until.IsZero() && e.OccurredAt != nil {
				if at, err := time.Parse(time.RFC3339, *e.OccurredAt); err == nil && at.After(until) {
					next = nil // pages run oldest first; the rest is after the task
					break
				}
			}
			switch governanceKind(e) {
			case "block":
				report.Blocks++
			case "violation":
				report.Violations++
			case "approval":
				report.Approvals++
			default:
				continue
			}
			report.Events = append(report.Events, e)
		}
		if next == nil {
			break
		}
		cursor = next
	}
	if report.Policies == nil {
		report.Policies = []zentinellePolicy{}
	}
	return &agentGovernance{governanceReport: report}
}

func (g *agentGovernance) counts() string {
	return fmt.Sprintf("%d %s, %d %s, %d %s",
		g.Blocks, plural(g.Blocks, "block"), g.Violations, plural(g.Violations, "violation"), g.Approvals, plural(g.Approvals, "approval"))
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// printInspectGovernance writes inspect's Governance and Policies lines.
func printInspectGovernance(out io.Writer, g *agentGovernance) {
	if g == nil {
		return
	}
	if g.governanceReport == nil {
		fmt.Fprintf(out, "Governance:    %s\n", g.Unavailable)
		return
	}
	fmt.Fprintf(out, "Governance:    %s, last 24h: %s\n", g.AgentSlug, g.counts())
	names := make([]string, len(g.Policies))
	for i, p := range g.Policies {
		names[i] = fmt.Sprintf("%s (%s)", p.Name, p.Enforcement)
	}
	if len(names) == 0 {
		names = []string{"none in effect"}
	}
	fmt.Fprintf(out, "Policies:      %s\n", strings.Join(names, ", "))
}

// printLogGovernance appends logs' governance lines, each marked so a reader
// can tell them from the agent's own output. A reason goes to errOut.
func printLogGovernance(out, errOut io.Writer, g *agentGovernance) {
	if g == nil {
		return
	}
	if g.governanceReport == nil {
		fmt.Fprintf(errOut, "governance: %s\n", g.Unavailable)
		return
	}
	until := g.Until
	if until == "" {
		until = "now"
	}
	fmt.Fprintf(out, "[governance] %s from %s to %s: %s\n", g.AgentSlug, g.Since, until, g.counts())
	for _, e := range g.Events {
		when := ""
		if e.OccurredAt != nil {
			when = *e.OccurredAt
		}
		fmt.Fprintf(out, "[governance] %s %-10s %s %s/%s %s\n", when, governanceKind(e), e.Action, e.ResourceType, e.ResourceID, e.Actor)
	}
}

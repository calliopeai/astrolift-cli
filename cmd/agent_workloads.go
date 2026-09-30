// Package cmd — `astro agent workloads ...` subcommand tree.
//
// Enumerates the org's registered `kind: agent` workloads. This is the
// discovery half of the agent surface: `astro agent ls` lists AgentTasks
// (the *runs*), and `astro agent register-repo` materializes workloads,
// but nothing listed the workloads themselves. Without it a caller has to
// know a slug or GUID out-of-band before it can:
//
//   - `astro agent dispatch <slug>`                    — needs the slug
//   - `astro workflow create --bind <stage>=<guid>`    — needs the GUID
//
// GraphQL operation (field names per backend/schema.graphql):
//   - ls → agentWorkloads(orgId, projectSlug) → [AstroliftAgentListItem]
//
// The resolver is org-scoped server-side (AGENT_READ + tenant check), so
// the only inputs are the active org and an optional project filter.
//
// Note: an agent's env-spec is a separate entity keyed by envSpecSlug and
// is not carried on the list row; use `astro agent envspec` for that.
//
// Issue: calliopeai/astrolift-cli#59
package cmd

import (
	"context"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	agentWorkloadsProject string
	agentWorkloadsLimit   int
	agentWorkloadsJSON    bool
)

// ---- GraphQL operation -----------------------------------------------------

// agentWorkloadsQuery lists the org's kind=agent workloads, newest first.
// `id` is the workload GUID that `workflow create --bind` consumes; `slug`
// is what `agent dispatch` takes.
const agentWorkloadsQuery = `query($orgId: ID!, $projectSlug: String) {
  agentWorkloads(orgId: $orgId, projectSlug: $projectSlug) {
    id
    name
    slug
    appSlug
    projectSlug
    sourceRepo
    sourceUrl
    runFamily
    runMode
    runPaused
    runCronExpression
    lastRunStatus
    lastRunAt
    runningCount
    runMaxParallel
    replicas
  }
}`

// agentWorkload mirrors the AstroliftAgentListItem GraphQL type.
type agentWorkload struct {
	ID                string  `json:"id"`
	Name              string  `json:"name"`
	Slug              string  `json:"slug"`
	AppSlug           string  `json:"appSlug"`
	ProjectSlug       string  `json:"projectSlug"`
	SourceRepo        string  `json:"sourceRepo"`
	SourceURL         string  `json:"sourceUrl"`
	RunFamily         string  `json:"runFamily"`
	RunMode           string  `json:"runMode"`
	RunPaused         bool    `json:"runPaused"`
	RunCronExpression string  `json:"runCronExpression"`
	LastRunStatus     *string `json:"lastRunStatus"`
	LastRunAt         *string `json:"lastRunAt"`
	RunningCount      int     `json:"runningCount"`
	RunMaxParallel    *int    `json:"runMaxParallel"`
	Replicas          int     `json:"replicas"`
}

// ---- astro agent workloads -------------------------------------------------

var agentWorkloadsCmd = &cobra.Command{
	Use:   "workloads",
	Short: "Discover the org's registered agent workloads",
	Long: `Inspect the kind=agent workloads registered in the working org.

These are the agents themselves, not their runs — use 'astro agent ls'
for AgentTasks.`,
}

var agentWorkloadsListCmd = &cobra.Command{
	Use:   "ls",
	Short: "List the org's registered agent workloads",
	Long: `Lists the working org's kind=agent workloads (newest first) via the
agentWorkloads GraphQL query. Use --project to narrow to one project.

Each row carries both identifiers the dispatch surface needs: the slug
that 'astro agent dispatch' takes and the GUID that
'astro workflow create --bind <stage>=<guid>' takes. Output is a table
unless --json is given.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentWorkloadsList(cmd, cmd.Context(), client, cfg)
	},
}

func runAgentWorkloadsList(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}

	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	vars := map[string]interface{}{"orgId": org.ID}
	if agentWorkloadsProject != "" {
		vars["projectSlug"] = agentWorkloadsProject
	}

	var resp struct {
		AgentWorkloads []agentWorkload `json:"agentWorkloads"`
	}
	if err := client.GraphQL(listCtx, agentWorkloadsQuery, vars, &resp); err != nil {
		return fmt.Errorf("listing agent workloads: %w", err)
	}

	workloads := resp.AgentWorkloads
	// The resolver has no limit arg; apply --limit client-side, as `agent ls` does.
	if agentWorkloadsLimit > 0 && len(workloads) > agentWorkloadsLimit {
		workloads = workloads[:agentWorkloadsLimit]
	}

	out := cmd.OutOrStdout()
	if agentWorkloadsJSON {
		return renderJSON(cmd, workloads)
	}

	if len(workloads) == 0 {
		if agentWorkloadsProject != "" {
			fmt.Fprintf(out, "No agent workloads found in project %q.\n", agentWorkloadsProject)
		} else {
			fmt.Fprintln(out, "No agent workloads found.")
		}
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tGUID\tPROJECT\tAPP\tMODE\tRUNNING\tLAST RUN\tSOURCE")
	for _, a := range workloads {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			a.Slug, a.ID, a.ProjectSlug, a.AppSlug,
			runModeLabel(a), a.RunningCount, lastRunLabel(a), a.SourceRepo,
		)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d agent workload(s) shown.\n", len(workloads))
	return nil
}

// runModeLabel renders the run mode, marking paused agents so a picker can
// show why a scheduled agent isn't firing.
func runModeLabel(a agentWorkload) string {
	if a.RunPaused {
		return a.RunMode + " (paused)"
	}
	return a.RunMode
}

// lastRunLabel collapses the last-run summary into one column: status when
// there is one, otherwise a dash for an agent that has never run.
func lastRunLabel(a agentWorkload) string {
	if a.LastRunStatus == nil || *a.LastRunStatus == "" {
		return "-"
	}
	if a.LastRunAt == nil {
		return *a.LastRunStatus
	}
	return fmt.Sprintf("%s %s", *a.LastRunStatus, shortTime(a.LastRunAt))
}

// ---- init ------------------------------------------------------------------

func init() {
	agentWorkloadsListCmd.Flags().StringVar(&agentWorkloadsProject, "project", "", "Only list agents in this project slug")
	agentWorkloadsListCmd.Flags().IntVar(&agentWorkloadsLimit, "limit", 50, "Maximum number of workloads to show")
	agentWorkloadsListCmd.Flags().BoolVar(&agentWorkloadsJSON, "json", false, "Output as JSON")

	agentWorkloadsCmd.AddCommand(agentWorkloadsListCmd)
	agentCmd.AddCommand(agentWorkloadsCmd)
}

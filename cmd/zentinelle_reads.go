// Zentinelle usage, policy and audit reads (#113, platform #1888).
//
// The commands light up only where they work: the server is asked once per
// run whether the working organization is connected to Zentinelle
// (astroliftZentinelleCapability). Until then they are hidden from help; a
// server without that field shows nothing new, and running one there, or on
// an organization that is not connected, says so in one line.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

type zentinelleUsageRow struct {
	AgentSlug    string  `json:"agentSlug"`
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	Requests     int64   `json:"requests"`
	InputTokens  int64   `json:"inputTokens"`
	OutputTokens int64   `json:"outputTokens"`
	CostUSD      float64 `json:"costUsd"`
}

type zentinelleUsage struct {
	Since string               `json:"since"`
	Rows  []zentinelleUsageRow `json:"rows"`
}

type zentinellePolicy struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	PolicyType  string `json:"policyType"`
	Enforcement string `json:"enforcement"`
	Scope       string `json:"scope"`
	Priority    int    `json:"priority"`
	Description string `json:"description"`
}

type zentinelleAuditEvent struct {
	ID           string  `json:"id"`
	OccurredAt   *string `json:"occurredAt"`
	Action       string  `json:"action"`
	ResourceType string  `json:"resourceType"`
	ResourceID   string  `json:"resourceId"`
	ResourceName string  `json:"resourceName"`
	AgentSlug    string  `json:"agentSlug"`
	AgentID      string  `json:"agentId"`
	Actor        string  `json:"actor"`
}

type zentinelleAuditExport struct {
	Filename    string `json:"filename"`
	Content     string `json:"content"`
	ContentType string `json:"contentType"`
	RowCount    int    `json:"rowCount"`
}

const (
	zentinelleCapabilityQuery = `query { astroliftZentinelleCapability { connected } }`
	zentinelleUsageQuery      = `query($since: DateTime, $agentSlug: String, $projectId: GUID, $teamId: GUID) {
  astroliftZentinelleUsage(since: $since, agentSlug: $agentSlug, projectId: $projectId, teamId: $teamId) {
    since rows { agentSlug provider model requests inputTokens outputTokens costUsd }
  }
}`
	zentinellePoliciesQuery = `query($agentSlug: String, $projectId: GUID) {
  astroliftZentinellePolicies(agentSlug: $agentSlug, projectId: $projectId) {
    id name policyType enforcement scope priority description
  }
}`
	zentinelleAuditQuery = `query($since: DateTime, $cursor: String, $limit: Int!, $agentSlug: String, $projectId: GUID, $teamId: GUID) {
  astroliftZentinelleAuditPage(since: $since, cursor: $cursor, limit: $limit, agentSlug: $agentSlug, projectId: $projectId, teamId: $teamId) {
    events { id occurredAt action resourceType resourceId resourceName agentSlug agentId actor }
    nextCursor
  }
}`
	zentinelleExportMutation = `mutation($input: ExportZentinelleAuditInput!) {
  exportZentinelleAudit(input: $input) {
    ok errors { code message field }
    data { filename content contentType rowCount }
  }
}`
)

var (
	zentinelleReadAgent      string
	zentinelleReadSince      string
	zentinelleReadUntil      string
	zentinelleTailFollow     bool
	zentinelleTailEvery      time.Duration
	zentinelleExportFmt      string
	zentinelleExportOut      string
	zentinelleCapabilityMemo = map[string]zentinelleState{}
)

// zentinelleState is what the server said about Zentinelle for one server and org.
type zentinelleState int

const (
	zentinelleNotOffered zentinelleState = iota
	zentinelleNotConnected
	zentinelleConnected
)

// zentinelleCapability asks the server once per run (per server and org).
// A server without the field is not offering the reads.
func zentinelleCapability(ctx context.Context, client *api.Client, org string) (zentinelleState, error) {
	key := client.BaseURL() + "|" + org
	if state, ok := zentinelleCapabilityMemo[key]; ok {
		return state, nil
	}
	var resp struct {
		Capability *struct {
			Connected bool `json:"connected"`
		} `json:"astroliftZentinelleCapability"`
	}
	state := zentinelleNotOffered
	if err := client.GraphQL(ctx, zentinelleCapabilityQuery, nil, &resp); err != nil {
		if !errors.Is(err, api.ErrSchemaMismatch) {
			return state, fmt.Errorf("asking the server about Zentinelle: %w", err)
		}
	} else if resp.Capability != nil && resp.Capability.Connected {
		state = zentinelleConnected
	} else if resp.Capability != nil {
		state = zentinelleNotConnected
	}
	zentinelleCapabilityMemo[key] = state
	return state, nil
}

// zentinelleReadCommands are hidden until the server says Zentinelle is connected.
func zentinelleReadCommands() []*cobra.Command {
	return []*cobra.Command{agentUsageCmd, policyCmd, auditCmd}
}

// revealZentinelleCommands unhides the read commands for help when the active
// server and org are connected. It never prompts and gives up quietly: no
// server, no single org, or no answer within a few seconds leaves them hidden.
func revealZentinelleCommands(c *cobra.Command) {
	if c != rootCmd && c != agentCmd && c != policyCmd && c != auditCmd {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, cfg, _, err := loadActiveClient(ctx, false)
	if err != nil {
		return
	}
	want := strings.TrimSpace(orgFlagValue(c))
	if want == "" {
		want = strings.TrimSpace(cfg.DefaultOrg)
	}
	orgs, err := listOrgs(ctx, client)
	if err != nil {
		return
	}
	org := ""
	for _, o := range orgs {
		if o.Slug == want || o.ID == want || (want == "" && len(orgs) == 1) {
			org = o.ID
		}
	}
	if org == "" {
		return
	}
	client.SetOrg(org)
	if state, err := zentinelleCapability(ctx, client, org); err == nil && state == zentinelleConnected {
		for _, hidden := range zentinelleReadCommands() {
			hidden.Hidden = false
		}
	}
}

// zentinelleReadRun loads the client and org, and refuses in one line unless
// the org is connected to Zentinelle on this server. Errors are reported as
// zentinelleReadErrors does.
func zentinelleReadRun(run func(*cobra.Command, context.Context, *api.Client, *config.Config, []string) error) func(*cobra.Command, []string) error {
	return zentinelleReadErrors(func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		client, cfg, entry, err := loadActiveClient(ctx, boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		server := client.BaseURL()
		if entry != nil && entry.APIURL != "" {
			server = entry.APIURL
		}
		if err := requireZentinelle(cmd, ctx, client, cfg, server); err != nil {
			return err
		}
		return run(cmd, ctx, client, cfg, args)
	})
}

func requireZentinelle(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, server string) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	return zentinelleRefusal(ctx, client, org.ID, server)
}

// zentinelleRefusal is the one-line reason the reads cannot run for org, or nil.
func zentinelleRefusal(ctx context.Context, client *api.Client, org, server string) error {
	state, err := zentinelleCapability(ctx, client, org)
	if err != nil {
		return err
	}
	switch state {
	case zentinelleNotOffered:
		return fmt.Errorf("%s does not offer Zentinelle reads; upgrade Astrolift there", server)
	case zentinelleNotConnected:
		return fmt.Errorf("Zentinelle is not connected on %s", server) //nolint:staticcheck // the product name leads the one-line refusal #113 specifies
	}
	return nil
}

// parseSince reads a Go duration back from now (24h, 30m), a number of days
// (7d), an RFC 3339 time or a date. Empty stays empty.
func parseSince(value string, now time.Time) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.HasSuffix(value, "d") {
		var days int
		if _, err := fmt.Sscanf(value, "%dd", &days); err == nil && days > 0 {
			return now.AddDate(0, 0, -days).UTC().Format(time.RFC3339), nil
		}
	}
	if d, err := time.ParseDuration(value); err == nil && d > 0 {
		return now.Add(-d).UTC().Format(time.RFC3339), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC().Format(time.RFC3339), nil
		}
	}
	return "", fmt.Errorf("%q is not a duration (24h, 7d), an RFC 3339 time or a date", value)
}

// zentinelleSelector reads --agent (or the agent argument), --project and
// --team into the query's variables, resolving slugs to ids.
func zentinelleSelector(cmd *cobra.Command, ctx context.Context, client *api.Client, agent string, team bool) (map[string]interface{}, error) {
	vars := map[string]interface{}{}
	project, _ := cmd.Flags().GetString("project")
	teamSlug, _ := cmd.Flags().GetString("team")
	if !team && strings.TrimSpace(teamSlug) != "" {
		return nil, errors.New("policies are read for an agent or a project, not a team")
	}
	project, teamSlug, agent = strings.TrimSpace(project), strings.TrimSpace(teamSlug), strings.TrimSpace(agent)
	named := 0
	for _, v := range []string{agent, project, teamSlug} {
		if v != "" {
			named++
		}
	}
	if named > 1 {
		if team {
			return nil, errors.New("name one of an agent, --project or --team")
		}
		return nil, errors.New("name an agent or --project, not both")
	}
	switch {
	case agent != "":
		vars["agentSlug"] = agent
	case project != "":
		var resp struct {
			Projects []projectRef `json:"astroliftProjects"`
		}
		if err := client.GraphQL(ctx, `query { astroliftProjects { id slug name team { slug name } } }`, nil, &resp); err != nil {
			return nil, fmt.Errorf("resolving project: %w", err)
		}
		for _, p := range resp.Projects {
			if p.ID == project || p.Slug == project {
				vars["projectId"] = p.ID
			}
		}
		if vars["projectId"] == nil {
			return nil, fmt.Errorf("project %q not found or not visible", project)
		}
	case teamSlug != "":
		teams, err := listTeams(ctx, client)
		if err != nil {
			return nil, fmt.Errorf("resolving team: %w", err)
		}
		for _, t := range teams {
			if t.ID == teamSlug || t.Slug == teamSlug {
				vars["teamId"] = t.ID
			}
		}
		if vars["teamId"] == nil {
			return nil, fmt.Errorf("team %q not found or not visible", teamSlug)
		}
	}
	return vars, nil
}

// ---- astro agent usage ---------------------------------------------------------

var agentUsageCmd = &cobra.Command{
	Use:   "usage [<agent>]",
	Short: "Model usage and cost from Zentinelle's gateway metering",
	Long: `Model usage and cost for an agent, a project's agents (--project) or a
team's (--team), or every agent you may see, per agent, provider and model.
--since takes 24h, 7d, an RFC 3339 time or a date; the server's default is
30 days. Needs zentinelle.usage_view.`,
	Args:   cobra.MaximumNArgs(1),
	Hidden: true,
	RunE:   zentinelleReadRun(runAgentUsage),
}

func runAgentUsage(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, args []string) error {
	agent := ""
	if len(args) == 1 {
		agent = args[0]
	}
	vars, err := zentinelleSelector(cmd, ctx, client, agent, true)
	if err != nil {
		return err
	}
	since, err := parseSince(zentinelleReadSince, time.Now())
	if err != nil {
		return err
	}
	if since != "" {
		vars["since"] = since
	}
	var resp struct {
		Usage zentinelleUsage `json:"astroliftZentinelleUsage"`
	}
	if err := client.GraphQL(ctx, zentinelleUsageQuery, vars, &resp); err != nil {
		return fmt.Errorf("reading Zentinelle usage: %w", err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Usage)
	}
	out := cmd.OutOrStdout()
	if len(resp.Usage.Rows) == 0 {
		fmt.Fprintf(out, "No model usage since %s.\n", resp.Usage.Since)
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tPROVIDER\tMODEL\tREQUESTS\tINPUT\tOUTPUT\tCOST (USD)")
	var requests, input, output int64
	var cost float64
	for _, r := range resp.Usage.Rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%.4f\n", r.AgentSlug, r.Provider, r.Model, r.Requests, r.InputTokens, r.OutputTokens, r.CostUSD)
		requests, input, output, cost = requests+r.Requests, input+r.InputTokens, output+r.OutputTokens, cost+r.CostUSD
	}
	fmt.Fprintf(tw, "TOTAL\t\t\t%d\t%d\t%d\t%.4f\n", requests, input, output, cost)
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "Since %s.\n", resp.Usage.Since)
	return nil
}

// ---- astro policy ls|show ---------------------------------------------------------

var policyCmd = &cobra.Command{
	Use:    "policy",
	Short:  "Zentinelle policies in effect",
	Hidden: true,
}

var policyLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List the policies in effect for an agent (--agent), a project (--project) or the organization",
	Args:  cobra.NoArgs,
	RunE:  zentinelleReadRun(runPolicyLs),
}

var policyShowCmd = &cobra.Command{
	Use:   "show <policy-id>",
	Short: "Show one policy in effect, as policy ls finds it",
	Args:  cobra.ExactArgs(1),
	RunE:  zentinelleReadRun(runPolicyShow),
}

func listPolicies(cmd *cobra.Command, ctx context.Context, client *api.Client) ([]zentinellePolicy, error) {
	vars, err := zentinelleSelector(cmd, ctx, client, zentinelleReadAgent, false)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Policies []zentinellePolicy `json:"astroliftZentinellePolicies"`
	}
	if err := client.GraphQL(ctx, zentinellePoliciesQuery, vars, &resp); err != nil {
		return nil, fmt.Errorf("reading Zentinelle policies: %w", err)
	}
	return resp.Policies, nil
}

func runPolicyLs(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, _ []string) error {
	policies, err := listPolicies(cmd, ctx, client)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, policies)
	}
	if len(policies) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No policies in effect.")
		return nil
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tTYPE\tENFORCEMENT\tSCOPE\tPRIORITY")
	for _, p := range policies {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n", p.ID, p.Name, p.PolicyType, p.Enforcement, p.Scope, p.Priority)
	}
	return tw.Flush()
}

func runPolicyShow(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, args []string) error {
	policies, err := listPolicies(cmd, ctx, client)
	if err != nil {
		return err
	}
	for _, p := range policies {
		if p.ID != args[0] {
			continue
		}
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, p)
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "ID:          %s\nName:        %s\nType:        %s\nEnforcement: %s\nScope:       %s\nPriority:    %d\n",
			p.ID, p.Name, p.PolicyType, p.Enforcement, p.Scope, p.Priority)
		if p.Description != "" {
			fmt.Fprintf(out, "Description: %s\n", p.Description)
		}
		return nil
	}
	return fmt.Errorf("policy %q is not in effect here (see astro policy ls)", args[0])
}

// ---- astro audit tail|export --------------------------------------------------------

var auditCmd = &cobra.Command{
	Use:    "audit",
	Short:  "Zentinelle's agent evidence and audit stream",
	Hidden: true,
}

var auditTailCmd = &cobra.Command{
	Use:   "tail",
	Short: "Print the evidence and audit stream since --since (default 1h), and with --follow keep printing",
	Args:  cobra.NoArgs,
	RunE:  zentinelleReadRun(runAuditTail),
}

var auditExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the evidence and audit stream between --since and --until as NDJSON or CSV",
	Long: `Export Zentinelle's evidence and audit stream for the agents you may see,
to --output or stdout. The server records the export in the audit log and
refuses one past 50,000 events. Needs zentinelle.audit_export.`,
	Args: cobra.NoArgs,
	RunE: zentinelleReadRun(runAuditExport),
}

func auditPage(ctx context.Context, client *api.Client, vars map[string]interface{}) ([]zentinelleAuditEvent, *string, error) {
	var resp struct {
		Page struct {
			Events     []zentinelleAuditEvent `json:"events"`
			NextCursor *string                `json:"nextCursor"`
		} `json:"astroliftZentinelleAuditPage"`
	}
	if err := client.GraphQL(ctx, zentinelleAuditQuery, vars, &resp); err != nil {
		return nil, nil, fmt.Errorf("reading the Zentinelle audit stream: %w", err)
	}
	return resp.Page.Events, resp.Page.NextCursor, nil
}

func runAuditTail(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, _ []string) error {
	vars, err := zentinelleSelector(cmd, ctx, client, zentinelleReadAgent, true)
	if err != nil {
		return err
	}
	sinceFlag := zentinelleReadSince
	if sinceFlag == "" {
		sinceFlag = "1h"
	}
	since, err := parseSince(sinceFlag, time.Now())
	if err != nil {
		return err
	}
	vars["since"], vars["limit"] = since, 200
	out := cmd.OutOrStdout()
	asJSON := boolFlag(cmd, "json")
	seen := map[string]bool{}
	for {
		var cursor *string
		for {
			vars["cursor"] = cursor
			events, next, err := auditPage(ctx, client, vars)
			if err != nil {
				return err
			}
			for _, e := range events {
				if seen[e.ID] {
					continue
				}
				seen[e.ID] = true
				if e.OccurredAt != nil {
					since = *e.OccurredAt
				}
				if asJSON {
					if err := renderJSONLine(cmd, e); err != nil {
						return err
					}
					continue
				}
				when := ""
				if e.OccurredAt != nil {
					when = *e.OccurredAt
				}
				fmt.Fprintf(out, "%s  %-24s %-16s %s/%s %s\n", when, e.Action, e.AgentSlug, e.ResourceType, e.ResourceID, e.Actor)
			}
			if next == nil {
				break
			}
			cursor = next
		}
		if !zentinelleTailFollow {
			return nil
		}
		// Pick up from the last event seen; its id keeps it from printing twice.
		vars["since"] = since
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(zentinelleTailEvery):
		}
	}
}

// renderJSONLine writes v as one compact JSON line, for streams.
func renderJSONLine(cmd *cobra.Command, v any) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
}

// exportRefused is the export's failure from its result envelope. Its code,
// not only the message, is what --json reports, as for the reads' refusals.
type exportRefused struct {
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}

func (e *exportRefused) Error() string { return e.Message }

func runAuditExport(cmd *cobra.Command, ctx context.Context, client *api.Client, _ *config.Config, _ []string) error {
	if strings.TrimSpace(zentinelleReadSince) == "" {
		return errors.New("export needs --since")
	}
	input, err := zentinelleSelector(cmd, ctx, client, zentinelleReadAgent, true)
	if err != nil {
		return err
	}
	now := time.Now()
	if input["since"], err = parseSince(zentinelleReadSince, now); err != nil {
		return err
	}
	if until, err := parseSince(zentinelleReadUntil, now); err != nil {
		return err
	} else if until != "" {
		input["until"] = until
	}
	input["format"] = zentinelleExportFmt
	var resp struct {
		Result struct {
			Ok     bool                   `json:"ok"`
			Errors []mutationError        `json:"errors"`
			Data   *zentinelleAuditExport `json:"data"`
		} `json:"exportZentinelleAudit"`
	}
	if err := client.GraphQL(ctx, zentinelleExportMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("exporting the Zentinelle audit stream: %w", err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		err := &exportRefused{Message: "export refused: " + firstDeployError(resp.Result.Errors)}
		if e := resp.Result.Errors; len(e) > 0 {
			err.Code = e[0].Code
			// A mutation's refusal comes back in its envelope, without the permission.
			if e[0].Code == "PERMISSION_DENIED" && e[0].Message == tokenScopeRefusal {
				return newCredentialScopeError("zentinelle.audit_export", err)
			}
		}
		return err
	}
	data := resp.Result.Data
	if zentinelleExportOut == "" {
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, data)
		}
		_, err := fmt.Fprint(cmd.OutOrStdout(), data.Content)
		return err
	}
	if err := privatefile.Write(zentinelleExportOut, []byte(data.Content)); err != nil {
		return fmt.Errorf("writing %s: %w", zentinelleExportOut, err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, struct {
			Path     string `json:"path"`
			RowCount int    `json:"rowCount"`
		}{zentinelleExportOut, data.RowCount})
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Wrote %d events to %s\n", data.RowCount, zentinelleExportOut)
	return nil
}

func init() {
	agentUsageCmd.Flags().StringVar(&zentinelleReadSince, "since", "", "How far back: 24h, 7d, an RFC 3339 time or a date (server default 30d)")
	for _, c := range []*cobra.Command{policyLsCmd, policyShowCmd} {
		c.Flags().StringVar(&zentinelleReadAgent, "agent", "", "Agent slug")
	}
	auditTailCmd.Flags().StringVar(&zentinelleReadAgent, "agent", "", "Agent slug")
	auditTailCmd.Flags().StringVar(&zentinelleReadSince, "since", "", "Start: 24h, 7d, an RFC 3339 time or a date (default 1h)")
	auditTailCmd.Flags().BoolVarP(&zentinelleTailFollow, "follow", "f", false, "Keep printing new events")
	auditTailCmd.Flags().DurationVar(&zentinelleTailEvery, "interval", 5*time.Second, "How often --follow asks for new events")
	auditExportCmd.Flags().StringVar(&zentinelleReadAgent, "agent", "", "Agent slug")
	auditExportCmd.Flags().StringVar(&zentinelleReadSince, "since", "", "Start: 24h, 7d, an RFC 3339 time or a date (required)")
	auditExportCmd.Flags().StringVar(&zentinelleReadUntil, "until", "", "End, in the same forms as --since (default now)")
	auditExportCmd.Flags().StringVar(&zentinelleExportFmt, "format", "ndjson", "ndjson or csv")
	auditExportCmd.Flags().StringVarP(&zentinelleExportOut, "output", "o", "", "Write the export to this file (mode 0600) instead of stdout")

	policyCmd.AddCommand(policyLsCmd, policyShowCmd)
	auditCmd.AddCommand(auditTailCmd, auditExportCmd)
	agentCmd.AddCommand(agentUsageCmd)
	rootCmd.AddCommand(policyCmd, auditCmd)

	help := rootCmd.HelpFunc()
	rootCmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		revealZentinelleCommands(c)
		help(c, args)
	})
}

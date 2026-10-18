// Package cmd — `astro agent show <agent-slug>`.
//
// One registered agent Workload(kind=agent) by slug, via agent(orgId, slug)
// → AstroliftAgentDetail. Shows the brains every run reads and the brains a
// launch may add with `astro agent dispatch --brain` (#2432).
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

var agentShowJSON bool

const agentShowQuery = `query($orgId: ID!, $slug: String!) {
  agent(orgId: $orgId, slug: $slug) {
    id
    name
    slug
    appSlug
    sourceRepo
    runMode
    runPaused
    imageRef
    brains
    brainsAllowed
  }
}`

// agentDetail mirrors the AstroliftAgentDetail fields agentShowQuery reads.
type agentDetail struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Slug          string    `json:"slug"`
	AppSlug       string    `json:"appSlug"`
	SourceRepo    string    `json:"sourceRepo"`
	RunMode       string    `json:"runMode"`
	RunPaused     bool      `json:"runPaused"`
	ImageRef      string    `json:"imageRef"`
	Brains        *[]string `json:"brains"`
	BrainsAllowed *[]string `json:"brainsAllowed"`
}

var agentShowCmd = &cobra.Command{
	Use:   "show <agent-slug>",
	Short: "Show a registered agent, with the brains its runs read",
	Long: `Shows one registered agent Workload(kind=agent) in the working org:
its app, run mode, source, image, the brains every run reads (from the
agent's manifest), and the brains a launch may add with
'astro agent dispatch --brain'. Use --json for the raw record.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentShow(cmd, cmd.Context(), client, cfg, args[0])
	},
}

func runAgentShow(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, slug string) error {
	org, err := resolveOrg(cmd, ctx, client, cfg)
	if err != nil {
		return err
	}
	showCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	vars := map[string]interface{}{"orgId": org.ID, "slug": slug}
	var resp struct {
		Agent json.RawMessage `json:"agent"`
	}
	brainsReported := true
	err = client.GraphQL(showCtx, agentShowQuery, vars, &resp)
	if err != nil && errors.Is(err, api.ErrSchemaMismatch) && strings.Contains(strings.ToLower(err.Error()), "brains") {
		brainsReported = false
		err = client.GraphQL(showCtx, strings.Replace(agentShowQuery, "    brains\n    brainsAllowed\n", "", 1), vars, &resp)
	}
	if err != nil {
		return fmt.Errorf("fetching agent: %w", err)
	}
	if len(resp.Agent) == 0 || string(resp.Agent) == "null" {
		return fmt.Errorf("agent %q not found", slug)
	}
	if agentShowJSON {
		return renderJSON(cmd, resp.Agent)
	}
	var a agentDetail
	if err := json.Unmarshal(resp.Agent, &a); err != nil {
		return fmt.Errorf("decoding agent: %w", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Agent:          %s (%s)\n", a.Slug, a.Name)
	fmt.Fprintf(out, "GUID:           %s\n", a.ID)
	fmt.Fprintf(out, "App:            %s\n", a.AppSlug)
	mode := a.RunMode
	if a.RunPaused {
		mode += " (paused)"
	}
	fmt.Fprintf(out, "Run mode:       %s\n", mode)
	if a.SourceRepo != "" {
		fmt.Fprintf(out, "Source:         %s\n", a.SourceRepo)
	}
	if a.ImageRef != "" {
		fmt.Fprintf(out, "Image:          %s\n", a.ImageRef)
	}
	if !brainsReported {
		fmt.Fprintln(out, "Brains:         not reported; this server is older than platform #2432")
		return nil
	}
	fmt.Fprintf(out, "Brains:         %s\n", brainList(a.Brains, "none"))
	fmt.Fprintf(out, "Brains allowed: %s\n", brainList(a.BrainsAllowed, "any brain the launcher can read"))
	return nil
}

// brainList renders a slug list; nil reads as whenNil, empty as none.
func brainList(slugs *[]string, whenNil string) string {
	if slugs == nil {
		return whenNil
	}
	if len(*slugs) == 0 {
		return "none"
	}
	return strings.Join(*slugs, ", ")
}

func init() {
	agentShowCmd.Flags().BoolVar(&agentShowJSON, "json", false, "Output the agent record as JSON")
	agentCmd.AddCommand(agentShowCmd)
}

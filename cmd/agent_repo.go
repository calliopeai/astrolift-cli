// Package cmd — `astro agent register-repo`: the agent-repo registration
// path (registerAgentRepo). Agent-kind workloads don't roll through the
// standard deploy pipeline (they spawn as Jobs on dispatch), so their
// Workload rows materialize here at registration time, not on deploy.
package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

var (
	agentRepoProjectID     string
	agentRepoRef           string
	agentRepoSourceKind    string
	agentRepoManifestPaths []string
)

const registerAgentRepoMutation = `mutation($input: RegisterAgentRepoInput!) {
  registerAgentRepo(input: $input) {
    ok
    errors { field message }
    data {
      agents { manifestPath slug appId workloadSlug created skillNotes }
    }
  }
}`

var agentRegisterRepoCmd = &cobra.Command{
	Use:   "register-repo <source-repo>",
	Short: "Register an agent repo (materializes kind=agent workloads)",
	Long: `Registers a repo containing agent manifests via registerAgentRepo.
Unlike app register + deploy, this creates the agent Workload rows directly —
agent workloads spawn as Jobs on dispatch and never roll through the deploy
pipeline.`,
	Args: cobra.ExactArgs(1),
	RunE: workflowOrgScopedRunE(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runAgentRegisterRepo(cmd, ctx, client, args[0])
	}),
}

func runAgentRegisterRepo(cmd *cobra.Command, ctx context.Context, client *api.Client, sourceRepo string) error {
	if agentRepoProjectID == "" {
		return fmt.Errorf("--project-id is required")
	}
	regCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	input := map[string]interface{}{
		"projectId":  agentRepoProjectID,
		"sourceRepo": sourceRepo,
		"sourceKind": agentRepoSourceKind,
		"ref":        agentRepoRef,
	}
	if len(agentRepoManifestPaths) > 0 {
		input["manifestPaths"] = agentRepoManifestPaths
	}

	var resp struct {
		Result struct {
			Ok     bool `json:"ok"`
			Errors []struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"errors"`
			Data *struct {
				Agents []struct {
					ManifestPath string   `json:"manifestPath"`
					Slug         string   `json:"slug"`
					AppID        string   `json:"appId"`
					WorkloadSlug string   `json:"workloadSlug"`
					Created      bool     `json:"created"`
					SkillNotes   []string `json:"skillNotes"`
				} `json:"agents"`
			} `json:"data"`
		} `json:"registerAgentRepo"`
	}
	if err := client.GraphQL(regCtx, registerAgentRepoMutation,
		map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("registering agent repo: %w", err)
	}
	if !resp.Result.Ok {
		msg := "unknown error"
		if len(resp.Result.Errors) > 0 {
			msg = resp.Result.Errors[0].Field + ": " + resp.Result.Errors[0].Message
		}
		return fmt.Errorf("register failed: %s", msg)
	}

	out := cmd.OutOrStdout()
	if resp.Result.Data == nil || len(resp.Result.Data.Agents) == 0 {
		// "No agent manifests registered." on its own reads as "the repo
		// has no agents", which is usually false and always unhelpful
		// (#1697). The commonest reason is a root manifest that declares
		// an agent beside other workloads: that agent is materialized by
		// app registration, and this command only walks standalone agent
		// packages.
		fmt.Fprintln(out, "No agent manifests registered.")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "This command registers standalone agent packages —")
		fmt.Fprintln(out, "  agents/<slug>/astrolift.toml, each declaring a single kind = \"agent\" workload.")
		fmt.Fprintln(out, "An agent declared alongside other workloads in the root astrolift.toml is")
		fmt.Fprintln(out, "registered with the app instead; use `astro app register` for that repo.")
		return nil
	}
	for _, a := range resp.Result.Data.Agents {
		state := "existing"
		if a.Created {
			state = "created"
		}
		fmt.Fprintf(out, "Agent %s (workload %s, %s) — %s\n", a.Slug, a.WorkloadSlug, state, a.ManifestPath)
		for _, n := range a.SkillNotes {
			fmt.Fprintf(out, "  note: %s\n", n)
		}
	}
	return nil
}

func init() {
	agentRegisterRepoCmd.Flags().StringVar(&agentRepoProjectID, "project-id", "", "Project GUID to register the agent app under (required)")
	agentRegisterRepoCmd.Flags().StringVar(&agentRepoRef, "ref", "main", "Git ref to read manifests from")
	agentRegisterRepoCmd.Flags().StringVar(&agentRepoSourceKind, "source-kind", "github", "Source kind")
	agentRegisterRepoCmd.Flags().StringSliceVar(&agentRepoManifestPaths, "manifest-path", nil, "Explicit manifest path(s); omit to scan")
	agentCmd.AddCommand(agentRegisterRepoCmd)
}

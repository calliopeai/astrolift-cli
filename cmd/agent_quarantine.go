package cmd

import (
	"context"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const agentQuarantinesQuery = `query {
 agentQuarantines { id targetKind targetId reason policyId evidenceUrl createdAt }
}`
const clearAgentQuarantineMutation = `mutation($id: ID!) {
 clearAgentQuarantine(id: $id) { ok errors { code message field } }
}`

type agentQuarantine struct {
	ID          string `json:"id"`
	TargetKind  string `json:"targetKind"`
	TargetID    string `json:"targetId"`
	Reason      string `json:"reason"`
	PolicyID    string `json:"policyId"`
	EvidenceURL string `json:"evidenceUrl"`
	CreatedAt   string `json:"createdAt"`
}

var agentQuarantineCmd = &cobra.Command{Use: "quarantine", Short: "Inspect and clear scoped agent dispatch quarantines"}
var agentQuarantineListCmd = &cobra.Command{
	Use: "ls", Short: "List active quarantines visible to the selected organization and credential", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, err := loadScopedAgentClient(cmd)
		if err != nil {
			return err
		}
		return runAgentQuarantineList(cmd, client)
	},
}
var agentQuarantineClearCmd = &cobra.Command{
	Use: "clear <quarantine-id>", Short: "Clear a quarantine with an audited, authorized mutation", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		yes, _ := cmd.Flags().GetBool("yes")
		if !yes {
			noPrompt, _ := cmd.Flags().GetBool("no-prompt")
			if !noPrompt {
				noPrompt, _ = cmd.Root().PersistentFlags().GetBool("no-prompt")
			}
			if noPrompt {
				return fmt.Errorf("clearing a quarantine requires --yes when prompts are disabled")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Clear quarantine %s? [y/N] ", args[0])
			var answer string
			_, _ = fmt.Fscan(cmd.InOrStdin(), &answer)
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				return fmt.Errorf("quarantine clear aborted")
			}
		}
		client, _, err := loadScopedAgentClient(cmd)
		if err != nil {
			return err
		}
		return runAgentQuarantineClear(cmd, client, args[0])
	},
}

func runAgentQuarantineList(cmd *cobra.Command, client *api.Client) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	var response struct {
		Rows []agentQuarantine `json:"agentQuarantines"`
	}
	if err := client.GraphQL(ctx, agentQuarantinesQuery, nil, &response); err != nil {
		return fmt.Errorf("listing quarantines: %w", err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, response.Rows)
	}
	out := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(out, "ID\tKIND\tTARGET\tPOLICY\tREASON")
	for _, row := range response.Rows {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", row.ID, row.TargetKind, row.TargetID, row.PolicyID, strings.Join(strings.Fields(row.Reason), " "))
	}
	return out.Flush()
}

func runAgentQuarantineClear(cmd *cobra.Command, client *api.Client, id string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()
	var response struct {
		Result noneMutationResult `json:"clearAgentQuarantine"`
	}
	if err := client.GraphQL(ctx, clearAgentQuarantineMutation, map[string]interface{}{"id": id}, &response); err != nil {
		return fmt.Errorf("clearing quarantine: %w", err)
	}
	if !response.Result.Ok {
		return fmt.Errorf("quarantine clear refused: %s", firstMutationError(response.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, map[string]interface{}{"id": id, "cleared": true})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Quarantine %s cleared.\n", id)
	return nil
}

func init() {
	agentQuarantineClearCmd.Flags().Bool("yes", false, "Confirm clearing the quarantine")
	agentQuarantineCmd.AddCommand(agentQuarantineListCmd, agentQuarantineClearCmd)
	agentCmd.AddCommand(agentQuarantineCmd)
}

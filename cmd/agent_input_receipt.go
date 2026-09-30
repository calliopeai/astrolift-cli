package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const agentInputReceiptQuery = `query($taskId: ID!, $requestId: String!) {
  agentTaskInputMessage(taskId: $taskId, clientRequestId: $requestId) {
    id clientRequestId message author createdAt deliveredAt
  }
}`

var agentInputReceiptCmd = &cobra.Command{
	Use:   "input-receipt <task-id>",
	Short: "Look up a queued input receipt without sending or consuming input",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		requestID, _ := cmd.Flags().GetString("request-id")
		parsed, err := uuid.Parse(requestID)
		if err != nil {
			return fmt.Errorf("--request-id must be a UUID")
		}
		client, _, err := loadScopedAgentClient(cmd)
		if err != nil {
			return err
		}
		return runAgentInputReceipt(cmd, cmd.Context(), client, args[0], parsed.String())
	},
}

func runAgentInputReceipt(cmd *cobra.Command, ctx context.Context, client *api.Client, taskID, requestID string) error {
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var resp struct {
		Receipt *agentTaskInputMessage `json:"agentTaskInputMessage"`
	}
	if err := client.GraphQL(readCtx, agentInputReceiptQuery, map[string]interface{}{"taskId": taskID, "requestId": requestID}, &resp); err != nil {
		return fmt.Errorf("reading input receipt: %w", err)
	}
	if resp.Receipt != nil && (resp.Receipt.ID == "" || resp.Receipt.ClientRequestID == nil || *resp.Receipt.ClientRequestID != requestID) {
		return fmt.Errorf("server returned a mismatched input receipt")
	}
	asJSON, _ := cmd.Flags().GetBool("json")
	if asJSON {
		return renderJSON(cmd, resp.Receipt)
	}
	if resp.Receipt == nil {
		fmt.Fprintln(cmd.OutOrStdout(), "No receipt found. An earlier send may still be in flight; retain its request ID.")
	} else if resp.Receipt.DeliveredAt == nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Input %s is queued.\n", resp.Receipt.ID)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Input %s was claimed for runner delivery; execution is not confirmed.\n", resp.Receipt.ID)
	}
	return nil
}

func init() {
	agentInputReceiptCmd.Flags().String("request-id", "", "Persisted request UUID from the original send")
	agentInputReceiptCmd.Flags().Bool("json", false, "Output the receipt, or null when not found")
	_ = agentInputReceiptCmd.MarkFlagRequired("request-id")
	agentCmd.AddCommand(agentInputReceiptCmd)
}

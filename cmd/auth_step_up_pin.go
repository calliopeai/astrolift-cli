package cmd

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

const stepUpPINPath = "/api/cli/v1/auth/step-up-pin"

type pinChangeRequest struct {
	ID          string `json:"id"`
	ApprovalURL string `json:"approvalUrl,omitempty"`
	ExpiresIn   int    `json:"expiresIn,omitempty"`
	Status      string `json:"status"`
}

func getPINChange(ctx context.Context, client *api.Client, id string) (pinChangeRequest, error) {
	var change pinChangeRequest
	parsed, err := uuid.Parse(id)
	if err != nil {
		return change, fmt.Errorf("invalid PIN request ID")
	}
	err = client.Get(ctx, stepUpPINPath+"/requests/"+parsed.String(), &change)
	return change, err
}

func waitForPINChange(ctx context.Context, client *api.Client, change pinChangeRequest) (pinChangeRequest, error) {
	if change.ExpiresIn < 1 || change.ExpiresIn > 300 {
		return change, fmt.Errorf("server returned an invalid PIN request expiry")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(change.ExpiresIn)*time.Second)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return change, fmt.Errorf("waiting for browser confirmation: %w", ctx.Err())
		case <-ticker.C:
			current, err := getPINChange(ctx, client, change.ID)
			if err != nil {
				return change, err
			}
			switch current.Status {
			case "completed":
				return current, nil
			case "expired":
				return current, fmt.Errorf("PIN request expired; start a new request")
			case "pending":
			default:
				return current, fmt.Errorf("server returned an unknown PIN request status")
			}
		}
	}
}

func startPINChange(cmd *cobra.Command, action string) error {
	client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	var change pinChangeRequest
	if err := client.Post(cmd.Context(), stepUpPINPath+"/requests", map[string]string{"action": action}, &change); err != nil {
		return err
	}
	approval, err := url.Parse(change.ApprovalURL)
	if err != nil || approval.Host == "" || approval.User != nil ||
		(approval.Scheme != "http" && approval.Scheme != "https") {
		return fmt.Errorf("server returned an invalid PIN approval URL")
	}
	if _, err := uuid.Parse(change.ID); err != nil || change.Status != "pending" {
		return fmt.Errorf("server returned an invalid PIN request")
	}

	if boolFlag(cmd, "no-wait") && boolFlag(cmd, "json") {
		return renderJSON(cmd, change)
	}
	out := cmd.OutOrStdout()
	if boolFlag(cmd, "json") {
		out = cmd.ErrOrStderr()
	}
	fmt.Fprintln(out, "Confirm the PIN change in your browser:")
	fmt.Fprintln(out, change.ApprovalURL)
	if boolFlag(cmd, "no-wait") {
		fmt.Fprintf(out, "Check the result with astro auth step-up-pin status %s\n", change.ID)
		return nil
	}
	if !boolFlag(cmd, "no-browser") {
		if err := openBrowser(change.ApprovalURL); err != nil {
			fmt.Fprintln(cmd.ErrOrStderr(), "Open the approval link manually.")
		}
	}
	confirmed, err := waitForPINChange(cmd.Context(), client, change)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, confirmed)
	}
	_, err = fmt.Fprintln(out, "PIN change confirmed.")
	return err
}

func newStepUpPINCommand() *cobra.Command {
	parent := &cobra.Command{Use: "step-up-pin", Short: "Manage your personal step-up PIN"}
	status := &cobra.Command{
		Use:   "status [request-id]",
		Short: "Show your PIN status or the result of a browser request",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
			if err != nil {
				return err
			}
			if len(args) == 1 {
				change, err := getPINChange(cmd.Context(), client, args[0])
				if err != nil {
					return err
				}
				if boolFlag(cmd, "json") {
					return renderJSON(cmd, change)
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), change.Status)
				return err
			}
			var current struct {
				IsSet     bool `json:"isSet"`
				Locked    bool `json:"locked"`
				OrgAllows bool `json:"orgAllows"`
			}
			if err := client.Get(cmd.Context(), stepUpPINPath, &current); err != nil {
				return err
			}
			if boolFlag(cmd, "json") {
				return renderJSON(cmd, current)
			}
			out := cmd.OutOrStdout()
			switch {
			case current.Locked:
				fmt.Fprintln(out, "Step-up PIN locked.")
			case current.IsSet:
				fmt.Fprintln(out, "Step-up PIN set.")
			default:
				fmt.Fprintln(out, "Step-up PIN unset.")
			}
			if !current.OrgAllows {
				fmt.Fprintln(out, "This organization does not allow step-up PINs.")
			}
			return nil
		},
	}
	parent.AddCommand(status)
	for _, action := range []string{"set", "clear"} {
		cmd := &cobra.Command{
			Use: action, Short: fmt.Sprintf("Request browser confirmation to %s your PIN", action),
			Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error { return startPINChange(cmd, action) },
		}
		cmd.Flags().Bool("no-browser", false, "Print the approval link without opening the browser")
		cmd.Flags().Bool("no-wait", false, "Print the approval link and request ID, then exit")
		parent.AddCommand(cmd)
	}
	return parent
}

func init() {
	authCmd.AddCommand(newStepUpPINCommand())
}

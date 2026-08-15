// Package cmd — `astro agent vnc` .
//
// Resolves an AgentTask to a URL a human (or an IDE) can actually open.
//
// The platform stores `vnc_url` as a root-relative path, `/app/vnc/<guid>`.
// That path is not openable: it is the raw RFB-over-WebSocket relay the
// noVNC client speaks to, not a page, and it is relative so a caller
// outside the browser has no host to resolve it against. Anything holding
// only that value can do nothing with it but print it.
//
// The console already ships the viewer that drives that relay, at
// /agents/runs/<task-id>/vnc. This command emits the absolute form of that
// page on the active server, so a "View VNC" action becomes a plain URL
// open. Auth is the relay's existing session-cookie flow — opening the page
// in the user's browser is what authenticates the socket — so no token is
// minted or embedded here.
//
// GraphQL operation (field names per backend/schema.graphql):
//   - agentTask(id) → AstroliftAgentTask  (status + vncEnabled preflight)
//
// Issue: calliopeai/astrolift-cli#59
package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	agentVNCURLOnly bool
	agentVNCJSON    bool
	agentVNCForce   bool
)

// agentVNCInfo is the --json shape. `relayPath` is reported as-is so a
// caller that wants to drive its own noVNC client still has the raw relay
// path, alongside the openable page.
type agentVNCInfo struct {
	TaskID     string `json:"taskId"`
	Status     string `json:"status"`
	VNCEnabled bool   `json:"vncEnabled"`
	ConsoleURL string `json:"consoleUrl"`
	RelayPath  string `json:"relayPath"`
}

// ---- astro agent vnc -------------------------------------------------------

var agentVNCCmd = &cobra.Command{
	Use:   "vnc <task-id>",
	Short: "Print an openable console URL for a task's VNC session",
	Long: `Resolves an AgentTask to the absolute console URL that serves its
live VNC viewer.

The task's stored vnc_url is a root-relative RFB-over-WebSocket relay
path, not something a browser or an editor can open. This prints the
console page that drives that relay, on the active server, so the URL
can be handed straight to a browser.

The session authenticates with the console's normal login, so open the
URL in a browser that is signed in to Astrolift.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentVNC(cmd, cmd.Context(), client, cfg, args[0])
	},
}

func runAgentVNC(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, taskID string) error {
	vncCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		AgentTask *agentTask `json:"agentTask"`
	}
	if err := client.GraphQL(vncCtx, agentTaskQuery, map[string]interface{}{"id": taskID}, &resp); err != nil {
		return fmt.Errorf("looking up task: %w", err)
	}
	if resp.AgentTask == nil {
		return fmt.Errorf("task %s not found", taskID)
	}
	task := resp.AgentTask

	consoleURL, err := vncConsoleURL(client.BaseURL(), task.ID)
	if err != nil {
		return err
	}

	// Preflight the two conditions the relay itself rejects (close 4410), so
	// a caller gets a reason here instead of a socket that closes on open.
	// --force still prints the URL, for a task that is about to start.
	if !agentVNCForce {
		if !task.VNCEnabled {
			return fmt.Errorf("task %s was not launched with VNC enabled; dispatch with --vnc to get a framebuffer", task.ID)
		}
		if !strings.EqualFold(task.Status, "running") {
			return fmt.Errorf("task %s is %s; a VNC session needs a running task (use --force to print the URL anyway)", task.ID, task.Status)
		}
	}

	out := cmd.OutOrStdout()
	if agentVNCJSON {
		return renderJSON(cmd, agentVNCInfo{
			TaskID:     task.ID,
			Status:     task.Status,
			VNCEnabled: task.VNCEnabled,
			ConsoleURL: consoleURL,
			RelayPath:  task.VNCURL,
		})
	}
	if agentVNCURLOnly {
		fmt.Fprintln(out, consoleURL)
		return nil
	}

	fmt.Fprintf(out, "Task:    %s (%s)\n", task.ID, task.Status)
	fmt.Fprintf(out, "Console: %s\n", consoleURL)
	fmt.Fprintln(out, "\nOpen the console URL in a browser signed in to Astrolift.")
	return nil
}

// vncConsoleURL joins the active server's base URL with the console viewer
// route. It builds the path with url.JoinPath rather than concatenation so a
// base carrying its own sub-path (a reverse-proxied install) is preserved,
// and escapes the task id since it lands in a path segment.
func vncConsoleURL(baseURL, taskID string) (string, error) {
	if baseURL == "" {
		return "", fmt.Errorf("no API URL configured for the active server; run 'astro auth login'")
	}
	joined, err := url.JoinPath(baseURL, "agents", "runs", url.PathEscape(taskID), "vnc")
	if err != nil {
		return "", fmt.Errorf("building console URL from %q: %w", baseURL, err)
	}
	return joined, nil
}

// ---- init ------------------------------------------------------------------

func init() {
	agentVNCCmd.Flags().BoolVar(&agentVNCURLOnly, "url", false, "Print only the console URL")
	agentVNCCmd.Flags().BoolVar(&agentVNCJSON, "json", false, "Output task/VNC details as JSON")
	agentVNCCmd.Flags().BoolVar(&agentVNCForce, "force", false, "Print the URL even if the task is not running or not VNC-enabled")

	agentCmd.AddCommand(agentVNCCmd)
}

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

var ciCmd = &cobra.Command{
	Use:   "ci",
	Short: "CI/CD-mode commands (deploy, status, render)",
	Long: `Designed to run on a CI runner with no interactive prompts.
Reads ASTROLIFT_API_URL, ASTROLIFT_DEPLOY_TOKEN, and
ASTROLIFT_APP_SLUG from the environment.

Spec ref: spec 14 §6 + backend ci_deploy.py (#17).`,
}

var ciDeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Deploy the current app from CI",
	Long: `POSTs to /api/cli/v1/apps/<slug>/deploy/ with the image tags,
commit SHA, and environment from CI env vars. Polls until terminal
state unless --no-wait is given.

Required env: ASTROLIFT_API_URL, ASTROLIFT_DEPLOY_TOKEN, ASTROLIFT_APP_SLUG.
Optional env: ASTROLIFT_ENVIRONMENT (default 'production'),
              ASTROLIFT_BRANCH (default 'main'),
              ASTROLIFT_IMAGE_TAGS (JSON map workload→tag).
Exit codes: 0 success, 1 deploy failure, 2 config error.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL := os.Getenv("ASTROLIFT_API_URL")
		token := os.Getenv("ASTROLIFT_DEPLOY_TOKEN")
		slug := os.Getenv("ASTROLIFT_APP_SLUG")
		if apiURL == "" || token == "" || slug == "" {
			return configErr(errors.New(
				"ASTROLIFT_API_URL, ASTROLIFT_DEPLOY_TOKEN, ASTROLIFT_APP_SLUG required",
			))
		}

		commitSHA := os.Getenv("ASTROLIFT_COMMIT_SHA")
		if commitSHA == "" {
			commitSHA = detectCommitSHA()
		}
		if commitSHA == "" {
			return configErr(errors.New(
				"ASTROLIFT_COMMIT_SHA not set and `git rev-parse HEAD` failed",
			))
		}

		environment := envOrDefault("ASTROLIFT_ENVIRONMENT", "production")
		branch := envOrDefault("ASTROLIFT_BRANCH", "main")

		imageTagsRaw := os.Getenv("ASTROLIFT_IMAGE_TAGS")
		var imageTags map[string]string
		if imageTagsRaw == "" {
			return configErr(errors.New(
				"ASTROLIFT_IMAGE_TAGS required (JSON map workload→tag)",
			))
		}
		if err := json.Unmarshal([]byte(imageTagsRaw), &imageTags); err != nil {
			return configErr(fmt.Errorf("ASTROLIFT_IMAGE_TAGS not valid JSON: %w", err))
		}

		body := map[string]interface{}{
			"image_tags":    imageTags,
			"commit_sha":    commitSHA,
			"branch":        branch,
			"environment":   environment,
			"trigger_kind":  "ci",
		}
		if idem := os.Getenv("ASTROLIFT_IDEMPOTENCY_KEY"); idem != "" {
			body["idempotency_key"] = idem
		}

		client := api.NewClient(apiURL, token, false)

		ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
		defer cancel()

		var resp struct {
			WorkflowID  string `json:"workflow_id"`
			PollingURL  string `json:"polling_url"`
		}
		path := fmt.Sprintf("/api/cli/v1/apps/%s/deploy/", slug)
		if err := client.Post(ctx, path, body, &resp); err != nil {
			return deployErr(fmt.Errorf("enqueueing deploy: %w", err))
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Deploy enqueued: %s\n", resp.WorkflowID)
		fmt.Fprintf(cmd.OutOrStdout(), "Polling URL:     %s\n", resp.PollingURL)

		noWait, _ := cmd.Flags().GetBool("no-wait")
		if noWait {
			return nil
		}

		// Poll for terminal state
		timeout := 30 * time.Minute
		if t := os.Getenv("ASTROLIFT_DEPLOY_TIMEOUT"); t != "" {
			parsed, err := time.ParseDuration(t)
			if err == nil {
				timeout = parsed
			}
		}

		pollCtx, pollCancel := context.WithTimeout(cmd.Context(), timeout)
		defer pollCancel()

		fmt.Fprintln(cmd.OutOrStdout(), "Polling for terminal state...")
		state, err := pollWorkflow(pollCtx, client, resp.PollingURL)
		if err != nil {
			return deployErr(err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Final state: %s\n", state)
		if state != "succeeded" && state != "completed" {
			return deployErr(fmt.Errorf("deploy ended in %q", state))
		}
		return nil
	},
}

var ciStatusCmd = &cobra.Command{
	Use:   "status <workflow-id>",
	Short: "Get the status of a deploy workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		apiURL := os.Getenv("ASTROLIFT_API_URL")
		token := os.Getenv("ASTROLIFT_DEPLOY_TOKEN")
		if apiURL == "" || token == "" {
			return configErr(errors.New(
				"ASTROLIFT_API_URL + ASTROLIFT_DEPLOY_TOKEN required",
			))
		}
		client := api.NewClient(apiURL, token, false)
		var status struct {
			State string `json:"state"`
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		if err := client.Get(ctx, fmt.Sprintf("/api/cli/v1/workflow_runs/%s", args[0]), &status); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), status.State)
		return nil
	},
}

var ciRenderCmd = &cobra.Command{
	Use:   "render",
	Short: "Render the local astrolift.toml as platform-side manifests for review",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Calls /api/cli/v1/apps/<slug>/render/ which executes
		// the manifest renderer server-side and returns the
		// k8s YAML the platform would commit. Useful for CI
		// pre-merge review.
		return notImplemented(cmd, "ci render")
	},
}

func init() {
	ciDeployCmd.Flags().Bool("no-wait", false, "Exit after enqueue, don't poll")
	ciCmd.AddCommand(ciDeployCmd, ciStatusCmd, ciRenderCmd)
	rootCmd.AddCommand(ciCmd)
}

// pollWorkflow polls the workflow status URL until terminal state.
func pollWorkflow(ctx context.Context, client *api.Client, pollingURL string) (string, error) {
	// pollingURL from #17's polling_url_for is path-only
	// (e.g. /api/cli/v1/workflow_runs/<id>); strip leading
	// slash for client.Get which prepends baseURL.
	path := pollingURL
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	terminal := map[string]bool{
		"succeeded": true, "completed": true,
		"failed":    true, "cancelled": true,
		"timed_out": true,
	}

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		var resp struct {
			State string `json:"state"`
		}
		if err := client.Get(ctx, path, &resp); err != nil {
			return "", err
		}
		if terminal[resp.State] {
			return resp.State, nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func detectCommitSHA() string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// configErr exits with code 2 (config error) per spec.
func configErr(err error) error {
	fmt.Fprintln(os.Stderr, "config error:", err)
	os.Exit(2)
	return err
}

// deployErr exits with code 1 per spec.
func deployErr(err error) error {
	return err // root.go's Execute returns 1 on non-nil
}

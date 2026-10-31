// Package cmd -- `astro app reprovision`: re-apply an app's platform
// resources without a build (astrolift-app#2573).
//
// reprovisionApp re-renders each environment from current state, pinned to
// the image that is already running, and applies it with server-side apply.
// --wait then polls the app's reachabilityStatus, the field `app list` and
// `app show` print next to the provisioning status.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	appReprovisionEnv     string
	appReprovisionWait    bool
	appReprovisionTimeout time.Duration
)

// appReprovisionPollInterval is the cadence --wait re-reads the app's
// reachability. A var so tests can shorten it.
var appReprovisionPollInterval = 5 * time.Second

// ---- GraphQL operations -----------------------------------------------------

const reprovisionAppMutation = `mutation($input: ReprovisionAppInput!) {
  reprovisionApp(input: $input) {
    ok
    errors { code message field }
    data {
      appSlug
      environments { environmentName deploymentId reachabilityStatus objectsCreated objectsUpdated unchanged }
    }
  }
}`

const appReachabilityQuery = `query($slug: String!) {
  astroliftApp(slug: $slug) {
    slug
    reachabilityStatus
  }
}`

// ---- response shapes (GraphQL camelCase) ------------------------------------

// reprovisionEnvironment is one ReprovisionEnvironmentResult.
type reprovisionEnvironment struct {
	EnvironmentName    string   `json:"environmentName"`
	DeploymentID       *string  `json:"deploymentId"`
	ReachabilityStatus string   `json:"reachabilityStatus"`
	ObjectsCreated     []string `json:"objectsCreated"`
	ObjectsUpdated     []string `json:"objectsUpdated"`
	Unchanged          bool     `json:"unchanged"`
}

// reprovisionResult is the ReprovisionAppPayload. ReachabilityStatus is not
// part of it: --wait sets it to the app's status when polling ended.
type reprovisionResult struct {
	AppSlug            string                   `json:"appSlug"`
	Environments       []reprovisionEnvironment `json:"environments"`
	ReachabilityStatus string                   `json:"reachabilityStatus,omitempty"`
}

// ---- astro app reprovision --------------------------------------------------

var appReprovisionCmd = &cobra.Command{
	Use:   "reprovision",
	Short: "Re-apply an app's platform resources without a build or image change",
	Long: `Re-renders the app's environments from current state, pinned to the image
that is already running, and re-applies their platform resources via the
reprovisionApp GraphQL mutation: Ingress and host rule, TLS certificate,
hostname claim, DNS record, edge-auth annotations, network policy,
managed-service bindings and the environment URL. Missing objects are created
and drifted ones updated, and no workload is deleted. There is no build and no
image change, and pods restart only when the rendered workload spec differs.
Use it when an app reads deployed but its URL does not answer.

The app slug comes from --app or the local astrolift.toml. --env limits it to
one environment; without it every environment is reprovisioned. Each
environment prints on one line with its reachability and the objects created
and updated, or "unchanged". Requires app.deploy and respects paused deploys.

With --wait, polls the app's overall reachability, not one environment's,
until it is reachable, and exits non-zero if the app needs a reprovision, has
no managed domain, or --timeout passes first.

Exit codes: 0 success, 1 failure.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, err := resolveAppSlug(cmd, "")
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppReprovision(cmd, cmd.Context(), client, slug)
	},
}

func runAppReprovision(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	if appReprovisionWait && appReprovisionTimeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	input := map[string]interface{}{"appSlug": slug}
	if env := strings.TrimSpace(appReprovisionEnv); env != "" {
		input["environmentName"] = env
	}

	reprovisionCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var resp struct {
		Result struct {
			Ok     bool               `json:"ok"`
			Errors []mutationError    `json:"errors"`
			Data   *reprovisionResult `json:"data"`
		} `json:"reprovisionApp"`
	}
	if err := client.GraphQL(reprovisionCtx, reprovisionAppMutation,
		map[string]interface{}{"input": input}, &resp); err != nil {
		// Validation rejects the whole document, so nothing was applied.
		if errors.Is(err, api.ErrSchemaMismatch) {
			return fmt.Errorf("reprovisioning %s: this server does not offer reprovision; it needs an Astrolift control plane with platform #2573", slug)
		}
		return fmt.Errorf("reprovisioning %s: %w", slug, err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("reprovision failed: %s", firstDeployError(resp.Result.Errors))
	}
	result := resp.Result.Data
	if result == nil {
		return fmt.Errorf("reprovision succeeded but the server returned no result")
	}

	jsonOut := boolFlag(cmd, "json")
	if !jsonOut {
		out := cmd.OutOrStdout()
		if len(result.Environments) == 0 {
			fmt.Fprintf(out, "No environments to reprovision for app %q.\n", slug)
		}
		for _, env := range result.Environments {
			fmt.Fprintln(out, reprovisionLine(env))
		}
	}
	if !appReprovisionWait {
		if jsonOut {
			return renderJSON(cmd, result)
		}
		return nil
	}

	status, timedOut, err := waitForReachability(cmd, ctx, client, slug)
	if err != nil {
		return err
	}
	result.ReachabilityStatus = status
	if jsonOut {
		if err := renderJSON(cmd, result); err != nil {
			return err
		}
	}
	switch {
	case timedOut:
		return fmt.Errorf("app %s still %s after %s", slug, reachabilityLabel(status), appReprovisionTimeout)
	case strings.EqualFold(status, "NEEDS_REPROVISION"):
		return fmt.Errorf("app %s still needs a reprovision", slug)
	case strings.EqualFold(status, "NO_MANAGED_DOMAIN"):
		return fmt.Errorf("app %s has no managed domain, so it cannot be reachable", slug)
	}
	return nil
}

// waitForReachability polls the app's reachabilityStatus until it is
// REACHABLE, NEEDS_REPROVISION or NO_MANAGED_DOMAIN, and returns the last
// status and whether --timeout passed first. PROVISIONING, and any value this
// build does not know, keeps polling. Changes print as they are seen, except
// under --json.
func waitForReachability(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) (string, bool, error) {
	jsonOut := boolFlag(cmd, "json")
	out := cmd.OutOrStdout()
	if !jsonOut {
		fmt.Fprintf(out, "Waiting for %s to be reachable...\n", slug)
	}
	deadline := time.Now().Add(appReprovisionTimeout)
	last := ""
	for {
		current, err := fetchAppReachability(ctx, client, slug)
		if err != nil {
			return last, false, err
		}
		if current != last && !jsonOut {
			fmt.Fprintf(out, "  → %s\n", reachabilityLabel(current))
		}
		last = current
		switch strings.ToUpper(current) {
		case "REACHABLE", "NEEDS_REPROVISION", "NO_MANAGED_DOMAIN":
			return current, false, nil
		}
		if !time.Now().Before(deadline) {
			return current, true, nil
		}
		select {
		case <-ctx.Done():
			return current, false, ctx.Err()
		case <-time.After(appReprovisionPollInterval):
		}
	}
}

func fetchAppReachability(ctx context.Context, client *api.Client, slug string) (string, error) {
	var resp struct {
		App *struct {
			ReachabilityStatus string `json:"reachabilityStatus"`
		} `json:"astroliftApp"`
	}
	if err := client.GraphQL(ctx, appReachabilityQuery, map[string]interface{}{"slug": slug}, &resp); err != nil {
		return "", fmt.Errorf("reading %s reachability: %w", slug, err)
	}
	if resp.App == nil {
		return "", fmt.Errorf("app %q not found", slug)
	}
	return resp.App.ReachabilityStatus, nil
}

// reprovisionLine renders one environment's result: its reachability, then
// the objects created and updated, or "unchanged".
func reprovisionLine(env reprovisionEnvironment) string {
	parts := []string{reachabilityLabel(env.ReachabilityStatus)}
	if env.Unchanged {
		parts = append(parts, "unchanged")
	}
	if len(env.ObjectsCreated) > 0 {
		parts = append(parts, "created "+strings.Join(env.ObjectsCreated, ", "))
	}
	if len(env.ObjectsUpdated) > 0 {
		parts = append(parts, "updated "+strings.Join(env.ObjectsUpdated, ", "))
	}
	return env.EnvironmentName + ": " + strings.Join(parts, "; ")
}

// reachabilityLabel renders a ReachabilityStatus for humans. The GraphQL enum
// is upper case (NEEDS_REPROVISION); the CLI prints it lower case, as it does
// provisioning status. --json keeps the server's value.
func reachabilityLabel(status string) string {
	return strings.ToLower(status)
}

// queryWithReachability reads an app list or detail with reachabilityStatus
// (astrolift-app#2573) beside provisioningStatus, and expiresAt. Schema
// validation rejects an unknown selection before executing it, so only that
// narrow error falls back to queryWithExpiry with the original query: the read
// the CLI made before reachability existed. Authorization and transport
// failures surface.
func queryWithReachability(cmd *cobra.Command, ctx context.Context, client *api.Client, query string, vars map[string]interface{}, target interface{}, expiryRequired bool) (bool, error) {
	enhanced := strings.Replace(query, "    slug\n", "    slug\n    expiresAt\n", 1)
	enhanced = strings.Replace(enhanced, "    provisioningStatus\n", "    provisioningStatus\n    reachabilityStatus\n", 1)
	err := client.GraphQL(ctx, enhanced, vars, target)
	if !errors.Is(err, api.ErrSchemaMismatch) {
		return err == nil, err
	}
	return queryWithExpiry(cmd, ctx, client, query, vars, target, expiryRequired)
}

func init() {
	appReprovisionCmd.Flags().StringVar(&appReprovisionEnv, "env", "", "Environment to reprovision (default: every environment)")
	appReprovisionCmd.Flags().BoolVar(&appReprovisionWait, "wait", false, "Poll until the app is reachable")
	appReprovisionCmd.Flags().DurationVar(&appReprovisionTimeout, "timeout", 5*time.Minute, "Maximum time --wait polls")
	appCmd.AddCommand(appReprovisionCmd)
}

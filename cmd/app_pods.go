// Package cmd — `astro app pods`.
//
// Enumerates an app's pods so a caller can pick an exec target instead of
// accepting the one `astro exec` auto-resolves. `exec` already resolves a
// pod internally (first ready/Running pod, optionally narrowed by
// --workload), but that resolution was not observable: for a multi-pod or
// multi-workload app there was no way to see the candidates, so a target
// picker had nothing to enumerate and `--pod` had to be guessed.
//
// GraphQL operation (field names per backend/schema.graphql):
//   - astroliftAppPods(appSlug, environmentName) → [AstroliftAppPod]
//
// Issue: calliopeai/astrolift-cli#59
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

// ---- flags -----------------------------------------------------------------

var (
	appPodsApp      string
	appPodsEnv      string
	appPodsWorkload string
	appPodsReady    bool
	appPodsJSON     bool
)

// ---- GraphQL operation -----------------------------------------------------

// appPodsQuery selects the full pod row. This is deliberately wider than
// exec.go's astroliftAppPodsQuery, which fetches only what target
// resolution needs; a picker also wants restarts/age/node to tell a
// healthy replica from a crash-looping one.
const appPodsQuery = `query($appSlug: String!, $environmentName: String) {
  astroliftAppPods(appSlug: $appSlug, environmentName: $environmentName) {
    name
    workload
    status
    phase
    ready
    restarts
    age
    node
    containerStatuses { name }
  }
}`

// appPod mirrors the AstroliftAppPod GraphQL type.
type appPod struct {
	Name              string  `json:"name"`
	Workload          string  `json:"workload"`
	Status            string  `json:"status"`
	Phase             string  `json:"phase"`
	Ready             bool    `json:"ready"`
	Restarts          int     `json:"restarts"`
	Age               *string `json:"age"`
	Node              string  `json:"node"`
	ContainerStatuses []struct {
		Name string `json:"name"`
	} `json:"containerStatuses"`
}

// containerNames flattens the container list for display; a multi-container
// pod is exactly the case where `exec -c` becomes necessary.
func (p appPod) containerNames() []string {
	names := make([]string, 0, len(p.ContainerStatuses))
	for _, c := range p.ContainerStatuses {
		names = append(names, c.Name)
	}
	return names
}

// ---- astro app pods --------------------------------------------------------

var appPodsCmd = &cobra.Command{
	Use:   "pods [app-slug]",
	Short: "List an app's pods (exec targets)",
	Long: `Lists the app's pods, the candidates 'astro exec' picks from.

The app slug comes from the positional argument, then --app, then the
local astrolift.toml. Use --workload to narrow to one workload and
--ready to show only pods that can actually accept an exec session.

Each row carries the pod name to pass to 'astro exec --pod' and its
container names for 'astro exec -c'.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		explicit := ""
		if len(args) > 0 {
			explicit = args[0]
		}
		if explicit == "" {
			explicit = appPodsApp
		}
		slug, err := resolveAppSlug(cmd, explicit)
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppPods(cmd, cmd.Context(), client, slug)
	},
}

func runAppPods(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string) error {
	podCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	vars := map[string]interface{}{"appSlug": appSlug}
	if appPodsEnv != "" {
		vars["environmentName"] = appPodsEnv
	}

	var resp struct {
		Pods []appPod `json:"astroliftAppPods"`
	}
	if err := client.GraphQL(podCtx, appPodsQuery, vars, &resp); err != nil {
		return fmt.Errorf("listing pods for %s: %w", appSlug, err)
	}

	pods := make([]appPod, 0, len(resp.Pods))
	for _, p := range resp.Pods {
		if appPodsWorkload != "" && p.Workload != appPodsWorkload {
			continue
		}
		if appPodsReady && !p.Ready {
			continue
		}
		pods = append(pods, p)
	}

	out := cmd.OutOrStdout()
	if appPodsJSON {
		return renderJSON(cmd, pods)
	}

	if len(pods) == 0 {
		fmt.Fprintf(out, "No pods found for app %q", appSlug)
		if appPodsWorkload != "" {
			fmt.Fprintf(out, " in workload %q", appPodsWorkload)
		}
		if appPodsReady {
			fmt.Fprint(out, " (--ready)")
		}
		fmt.Fprintln(out, ".")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "POD\tWORKLOAD\tSTATUS\tREADY\tRESTARTS\tNODE\tCONTAINERS")
	for _, p := range pods {
		containers := strings.Join(p.containerNames(), ",")
		if containers == "" {
			containers = "-"
		}
		node := p.Node
		if node == "" {
			node = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			p.Name, p.Workload, podStatusLabel(p), yesNo(p.Ready), p.Restarts, node, containers,
		)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d pod(s) shown.\n", len(pods))
	return nil
}

// podStatusLabel prefers the richer status string (CrashLoopBackOff and
// friends) and falls back to the coarse phase when it is empty.
func podStatusLabel(p appPod) string {
	if p.Status != "" {
		return p.Status
	}
	if p.Phase != "" {
		return p.Phase
	}
	return "-"
}

// ---- init ------------------------------------------------------------------

func init() {
	appPodsCmd.Flags().StringVar(&appPodsApp, "app-slug", "", "App slug (overrides --app and astrolift.toml)")
	appPodsCmd.Flags().StringVar(&appPodsEnv, "env", "", "Environment name (defaults to the app's default environment)")
	appPodsCmd.Flags().StringVar(&appPodsWorkload, "workload", "", "Only show pods for this workload")
	appPodsCmd.Flags().BoolVar(&appPodsReady, "ready", false, "Only show ready pods (valid exec targets)")
	appPodsCmd.Flags().BoolVar(&appPodsJSON, "json", false, "Output as JSON")

	appCmd.AddCommand(appPodsCmd)
}

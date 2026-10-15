// Package cmd -- `astro app environments ...` (alias `env`): list an app's
// environments and add one after registration (calliopeai/astrolift-app#2398).
//
// Before, an app's environments came only from registration and repo
// resync, so an app registered with only production could not gain staging
// while its manifest was a staged draft (SteadyMD DV-9250).
//
// GraphQL operations (field names per backend/schema.graphql):
//   - list   → astroliftEnvironments(appSlug)
//   - create → createAppEnvironment(input: CreateAppEnvironmentInput!)
package cmd

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

var appEnvCreateCluster string

// ---- GraphQL operations -----------------------------------------------------

const appEnvironmentsListQuery = `query($appSlug: String!) {
  astroliftEnvironments(appSlug: $appSlug) {
    name
    url
    deploysPaused
    clusterSlug
  }
}`

const createAppEnvironmentMutation = `mutation($input: CreateAppEnvironmentInput!) {
  createAppEnvironment(input: $input) {
    ok
    errors { code message field }
    data { name url deploysPaused clusterSlug }
  }
}`

// ---- astro app environments --------------------------------------------------

var appEnvironmentsCmd = &cobra.Command{
	Use:     "environments",
	Aliases: []string{"env", "envs"},
	Short:   "List an app's environments or add one",
}

var appEnvironmentsListCmd = &cobra.Command{
	Use:   "list [app]",
	Short: "List an app's environments",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppEnvironmentsList(cmd, cmd.Context(), client, slug)
	},
}

var appEnvironmentsCreateCmd = &cobra.Command{
	Use:   "create <name> [app]",
	Short: "Add an environment to an app",
	Long: `Adds an environment to a registered app, on the app's default cluster or
the one --cluster names. It is placed the way registration places one: its
own namespace and hostname when it shares a cluster with another
environment of the app. The applied manifest's managed services that target
it are reconciled, and its first deploy brings it up.

The name is one DNS label: lowercase letters, digits and hyphens.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		var appArg string
		if len(args) > 1 {
			appArg = args[1]
		}
		slug, err := resolveAppSlug(cmd, appArg)
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppEnvironmentsCreate(cmd, cmd.Context(), client, slug, args[0], appEnvCreateCluster)
	},
}

func runAppEnvironmentsList(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string) error {
	var resp struct {
		Environments []appEnvironment `json:"astroliftEnvironments"`
	}
	if err := client.GraphQL(ctx, appEnvironmentsListQuery, map[string]interface{}{"appSlug": appSlug}, &resp); err != nil {
		return fmt.Errorf("listing environments for %s: %w", appSlug, err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Environments)
	}
	out := cmd.OutOrStdout()
	if len(resp.Environments) == 0 {
		fmt.Fprintf(out, "No environments for app %q.\n", appSlug)
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tURL\tDEPLOYS\tCLUSTER")
	for _, e := range resp.Environments {
		fmt.Fprintln(w, environmentRow(e))
	}
	return w.Flush()
}

func runAppEnvironmentsCreate(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug, name, cluster string) error {
	input := map[string]interface{}{"appSlug": appSlug, "name": name}
	if cluster != "" {
		input["clusterSlug"] = cluster
	}
	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
			Data   *appEnvironment `json:"data"`
		} `json:"createAppEnvironment"`
	}
	if err := client.GraphQL(ctx, createAppEnvironmentMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("creating environment %s for %s: %w", name, appSlug, err)
	}
	if !resp.Result.Ok || resp.Result.Data == nil {
		return fmt.Errorf("creating environment %s for %s: %s", name, appSlug, firstDeployError(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	e := resp.Result.Data
	fmt.Fprintf(cmd.OutOrStdout(), "Created environment %s for %s on %s.\n", e.Name, appSlug, environmentCluster(*e))
	if e.URL != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Its first deploy serves it at %s.\n", e.URL)
	}
	return nil
}

func environmentCluster(e appEnvironment) string {
	if e.ClusterSlug != nil && *e.ClusterSlug != "" {
		return *e.ClusterSlug
	}
	return "-"
}

func environmentRow(e appEnvironment) string {
	deploys := "enabled"
	if e.DeploysPaused {
		deploys = "paused"
	}
	return fmt.Sprintf("%s\t%s\t%s\t%s", e.Name, dashIfEmpty(e.URL), deploys, environmentCluster(e))
}

func init() {
	appEnvironmentsCreateCmd.Flags().StringVar(&appEnvCreateCluster, "cluster", "", "Cluster slug to place the environment on (default: the app's default cluster)")
	appEnvironmentsCmd.AddCommand(appEnvironmentsListCmd, appEnvironmentsCreateCmd)
}

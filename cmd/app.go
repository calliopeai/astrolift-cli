package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "App lifecycle and sub-resource management",
	Long: `Commands for working with Astrolift apps: init, register, deploy,
rollback, promote, plus sub-resource management (secrets, services,
domains, tokens, members, jobs, events, audit).`,
}

// ---- lifecycle ----

var appInitCmd = &cobra.Command{
	Use:   "init [path]",
	Short: "Scaffold a new astrolift.toml in the current directory",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "astrolift.toml"
		if len(args) == 1 {
			path = args[0]
		}
		return scaffoldManifest(path, cmd)
	},
}

// ---- app register flags ----

var (
	appRegisterFile          string
	appRegisterProjectID     string
	appRegisterSourceRepo    string
	appRegisterSourceKind    string
	appRegisterDescription   string
	appRegisterManifestPath  string
	appRegisterDefaultBranch string
	appRegisterJSON          bool
)

// appManifest is the minimal shape of astrolift.toml that register reads.
type appManifest struct {
	App struct {
		Slug        string `toml:"slug"`
		DisplayName string `toml:"display_name"`
	} `toml:"app"`
}

// registeredApp is the data portion of AstroliftRegisteredAppMutationResult.
type registeredApp struct {
	ID               string `json:"id"`
	Slug             string `json:"slug"`
	Name             string `json:"name"`
	OrganizationSlug string `json:"organizationSlug"`
	ProjectSlug      string `json:"projectSlug"`
	K8sNamespace     string `json:"k8sNamespace"`
	Subdomain        string `json:"subdomain"`
}

type registerAppResult struct {
	RegisterApp struct {
		Ok     bool `json:"ok"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
		Data *registeredApp `json:"data"`
	} `json:"registerApp"`
}

var appRegisterCmd = &cobra.Command{
	Use:   "register",
	Short: "Register the local astrolift.toml as a new app on the platform",
	Long: `Reads the local astrolift.toml (or the file given by --file), extracts
the app slug and display name, and calls the registerApp GraphQL mutation.

--project-id and --source-repo are required; all other flags are optional.

Example:
  astro app register --project-id <uuid> --source-repo myorg/my-app`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), false)
		if err != nil {
			return err
		}

		raw, err := os.ReadFile(appRegisterFile)
		if err != nil {
			return fmt.Errorf("reading %s: %w", appRegisterFile, err)
		}
		var manifest appManifest
		if _, err := toml.Decode(string(raw), &manifest); err != nil {
			return fmt.Errorf("parsing %s: %w", appRegisterFile, err)
		}
		if manifest.App.Slug == "" {
			return fmt.Errorf("%s: [app] slug is required", appRegisterFile)
		}
		if manifest.App.DisplayName == "" {
			return fmt.Errorf("%s: [app] display_name is required", appRegisterFile)
		}

		if appRegisterProjectID == "" {
			return fmt.Errorf("--project-id is required")
		}
		if appRegisterSourceRepo == "" {
			return fmt.Errorf("--source-repo is required")
		}

		input := map[string]interface{}{
			"projectId":  appRegisterProjectID,
			"name":       manifest.App.DisplayName,
			"slug":       manifest.App.Slug,
			"sourceKind": appRegisterSourceKind,
			"sourceRepo": appRegisterSourceRepo,
		}
		if appRegisterDescription != "" {
			input["description"] = appRegisterDescription
		}
		if appRegisterManifestPath != "" {
			input["manifestPath"] = appRegisterManifestPath
		}
		if appRegisterDefaultBranch != "" {
			input["defaultBranch"] = appRegisterDefaultBranch
		}

		const m = `
mutation RegisterApp($input: RegisterAppInput!) {
  registerApp(input: $input) {
    ok
    errors { message }
    data {
      id slug name organizationSlug projectSlug
      k8sNamespace subdomain
    }
  }
}`
		vars := map[string]interface{}{"input": input}
		var result registerAppResult
		if err := client.GraphQL(cmd.Context(), m, vars, &result); err != nil {
			return fmt.Errorf("registerApp: %w", err)
		}
		if !result.RegisterApp.Ok {
			msgs := make([]string, 0, len(result.RegisterApp.Errors))
			for _, e := range result.RegisterApp.Errors {
				msgs = append(msgs, e.Message)
			}
			if len(msgs) > 0 {
				return fmt.Errorf("registerApp failed: %s", msgs[0])
			}
			return fmt.Errorf("registerApp failed (no error detail returned)")
		}

		app := result.RegisterApp.Data
		if appRegisterJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(app)
		}

		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "Registered app:   %s (%s)\n", app.Name, app.Slug)
		fmt.Fprintf(out, "ID:               %s\n", app.ID)
		fmt.Fprintf(out, "Org:              %s\n", app.OrganizationSlug)
		fmt.Fprintf(out, "Project:          %s\n", app.ProjectSlug)
		fmt.Fprintf(out, "Namespace:        %s\n", app.K8sNamespace)
		if app.Subdomain != "" {
			fmt.Fprintf(out, "Subdomain:        %s\n", app.Subdomain)
		}
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Next step: run `astro app deploy` to trigger the first deployment.")
		return nil
	},
}

// app deploy / rollback / promote / list / show are wired in
// cmd/app_lifecycle.go.

// ---- sub-resources ----
//
// Each sub-resource gets its own subcommand group. Bodies follow
// the pattern: load creds → call GraphQL → render.

func newSubResourceCmd(name, summary string) *cobra.Command {
	return &cobra.Command{
		Use:   name,
		Short: summary,
		Long: fmt.Sprintf(`%s.

Subcommands typically include: list, show, create, update, delete.`, summary),
	}
}

var appSecretsCmd = newSubResourceCmd("secrets", "Manage app secrets (#5)")
var appServicesCmd = newSubResourceCmd("services", "Manage bound managed services")
var appDomainsCmd = newSubResourceCmd("domains", "Manage custom domains")
var appTokensCmd = newSubResourceCmd("tokens", "Manage deploy tokens")
var appMembersCmd = newSubResourceCmd("members", "Manage app team members")
var appJobsCmd = newSubResourceCmd("jobs", "Manage scheduled jobs")
var appEventsCmd = newSubResourceCmd("events", "Show app event log (#12)")
var appAuditCmd = newSubResourceCmd("audit", "Show app audit log (#12)")

// app logs and app exec are wired in cmd/app_lifecycle.go.

var appPreviewsCmd = newSubResourceCmd("previews", "Manage preview environments (#95)")

func init() {
	appRegisterCmd.Flags().StringVarP(&appRegisterFile, "file", "f", "astrolift.toml", "Path to the app manifest (default: astrolift.toml)")
	appRegisterCmd.Flags().StringVar(&appRegisterProjectID, "project-id", "", "Project GUID to register the app under (required)")
	appRegisterCmd.Flags().StringVar(&appRegisterSourceRepo, "source-repo", "", "Source repository in owner/repo format (required)")
	appRegisterCmd.Flags().StringVar(&appRegisterSourceKind, "source-kind", "github", "SCM kind: github, gitlab, bitbucket (default: github)")
	appRegisterCmd.Flags().StringVar(&appRegisterDescription, "description", "", "Short description of the app")
	appRegisterCmd.Flags().StringVar(&appRegisterManifestPath, "manifest-path", "", "Path to the manifest file within the repo (default: astrolift.toml)")
	appRegisterCmd.Flags().StringVar(&appRegisterDefaultBranch, "default-branch", "", "Default branch for deploys (default: the repo default)")
	appRegisterCmd.Flags().BoolVar(&appRegisterJSON, "json", false, "Output the registered app as JSON")

	appCmd.AddCommand(
		appInitCmd, appRegisterCmd, appDeployCmd,
		appRollbackCmd, appPromoteCmd, appDeregisterCmd,
		appListCmd, appShowCmd,
		appSecretsCmd, appServicesCmd, appDomainsCmd,
		appTokensCmd, appMembersCmd, appJobsCmd,
		appEventsCmd, appAuditCmd,
		appLogsCmd, appExecCmd,
		appPreviewsCmd,
	)
	rootCmd.AddCommand(appCmd)
}

// notImplemented surfaces the gap rather than silently no-op'ing.
// The command tree is structured so adding the body is a focused
// change (see the GraphQL schema in astrolift-app for the queries).
func notImplemented(cmd *cobra.Command, name string) error {
	return fmt.Errorf(
		"`astro %s` is structured but not yet wired to the API; "+
			"see astrolift-cli/cmd for the command tree",
		name,
	)
}

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

var appCmd = &cobra.Command{
	Use:   "app",
	Short: "App lifecycle and sub-resource management",
	Long: `Commands for working with Astrolift apps: init, register, deploy,
rollback, promote, plus sub-resource management (secrets, services,
domains, tokens, members, jobs, events, audit, previews).`,
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
	appRegisterManifestRaw   bool
	appRegisterDefaultBranch string
	appRegisterBuildMode     string
	appRegisterBuildStrategy string
	appRegisterDockerfile    string
	appRegisterBuildContext  string
	appRegisterYes           bool
	appRegisterJSON          bool
)

// validBuildModes / validBuildStrategies mirror RegisteredApp.BuildMode and
// RegisteredApp.BuildStrategy on the platform. Validated client-side so a typo
// costs a round trip and a clear message rather than a GraphQL enum error.
var (
	validBuildModes      = []string{"ci_pushed", "platform_build", "none"}
	validBuildStrategies = []string{"off", "dockerfile", "buildpacks", "nixpacks"}
)

func oneOf(value string, allowed []string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}

// applyBuildFlags validates the build-related flags and writes them onto the
// registerApp input.
//
// Every field is omitted when empty rather than defaulted here: the platform
// owns the defaults, and sending one from the CLI would freeze it at whatever
// this binary was built against. The one exception is the strategy implied by
// platform_build, which is a client-side correction of a combination that is
// accepted by the API but can never build -- build_mode platform_build with
// build_strategy off selects no builder, so BuildImageActivity has nothing to
// invoke.
func applyBuildFlags(input map[string]interface{}, mode, strategy, dockerfile, context string) error {
	if mode != "" && !oneOf(mode, validBuildModes) {
		return fmt.Errorf("--build-mode must be one of %s", strings.Join(validBuildModes, ", "))
	}
	if strategy != "" && !oneOf(strategy, validBuildStrategies) {
		return fmt.Errorf("--build-strategy must be one of %s", strings.Join(validBuildStrategies, ", "))
	}
	if mode == "platform_build" && strategy == "" {
		strategy = "dockerfile"
	}
	if mode != "" {
		input["buildMode"] = mode
	}
	if strategy != "" {
		input["buildStrategy"] = strategy
	}
	if dockerfile != "" {
		input["dockerfilePath"] = dockerfile
	}
	if context != "" {
		input["buildContext"] = context
	}
	return nil
}

// appManifest is the minimal shape of astrolift.toml that register reads.
type appManifest struct {
	App struct {
		Slug        string `toml:"slug"`
		DisplayName string `toml:"display_name"`
		ExpiresIn   string `toml:"expires_in"`
	} `toml:"app"`
}

// registeredApp is the data portion of AstroliftRegisteredAppMutationResult.
type registeredApp struct {
	ExpiresAt        *string `json:"expiresAt"`
	ID               string  `json:"id"`
	Slug             string  `json:"slug"`
	Name             string  `json:"name"`
	OrganizationSlug string  `json:"organizationSlug"`
	ProjectSlug      string  `json:"projectSlug"`
	K8sNamespace     string  `json:"k8sNamespace"`
	Subdomain        string  `json:"subdomain"`
	BuildContext     string  `json:"buildContext"`
	DockerfilePath   string  `json:"dockerfilePath"`
	// SourceConnectionWarning is selected only from servers advertising
	// scm.registration_owner_warning; older schemas reject the field.
	SourceConnectionWarning string `json:"sourceConnectionWarning,omitempty"`
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
Use --manifest-raw to send the local manifest when the platform cannot fetch it
from the repository (for example, before configuring a source connection).

The build context is detected from --manifest-path: a manifest in a
sub-directory builds from that directory, with its Dockerfile
(src/web/astrolift.toml builds src/web). A manifest that sets build_context or
dockerfile_path itself keeps the repository root, and those paths apply.
register shows what it detected and asks "Use this? [Y/n/edit]": n registers
nothing, edit asks for both paths. --build-context and --dockerfile set them
without asking; --yes, --no-prompt and a non-interactive stdin take the
detected ones. Change them later with astro app set-build-context.

--build-mode selects who publishes the container image. The platform default
is ci_pushed, where your CI builds and pushes and the platform only rolls out
the tag. Use platform_build to hand the repo to the platform and have it build
in-cluster -- no CI to configure and nothing to build locally:

  astro app register --project-id <uuid> --source-repo myorg/my-app \
    --build-mode platform_build

which implies --build-strategy dockerfile unless you name another builder.

Examples:
  astro app register --project-id <uuid> --source-repo myorg/my-app
  astro app register -f src/web/astrolift.toml --manifest-path src/web/astrolift.toml \
    --project-id <uuid> --source-repo myorg/monorepo --yes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), false)
		if err != nil {
			return err
		}
		return runAppRegister(cmd, cmd.Context(), client)
	},
}

// runAppRegister registers the local manifest through registerApp.
func runAppRegister(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
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
	// Inline registration also works before a source connection is configured.
	if appRegisterManifestRaw {
		input["manifestRaw"] = string(raw)
	}
	if appRegisterDefaultBranch != "" {
		input["defaultBranch"] = appRegisterDefaultBranch
	}
	build, err := registerBuildPaths(cmd, string(raw))
	if errors.Is(err, errRegisterCancelled) {
		fmt.Fprintln(cmd.ErrOrStderr(), "Aborted; nothing was registered.")
		return nil
	}
	if err != nil {
		return err
	}
	if err := applyBuildFlags(input, appRegisterBuildMode, appRegisterBuildStrategy,
		build.Dockerfile, build.Context); err != nil {
		return err
	}

	if err := applyCreationExpiry(cmd, ctx, client, input, manifest.App.ExpiresIn); err != nil {
		return err
	}
	warningField := ""
	if serverAdvertises(ctx, client, "apps.temporary_expiry") {
		warningField = " expiresAt"
	}
	if serverAdvertises(ctx, client, "scm.registration_owner_warning") {
		warningField += " sourceConnectionWarning"
	}
	m := `
mutation RegisterApp($input: RegisterAppInput!) {
  registerApp(input: $input) {
    ok
    errors { message }
    data {
      id slug name organizationSlug projectSlug
      k8sNamespace subdomain buildContext dockerfilePath` + warningField + `
    }
  }
}`
	vars := map[string]interface{}{"input": input}
	var result registerAppResult
	if err := client.GraphQL(ctx, m, vars, &result); err != nil {
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
	if app.ExpiresAt != nil {
		fmt.Fprintf(out, "Expires:          %s\n", *app.ExpiresAt)
	}
	fmt.Fprintf(out, "Build context:    %s\n", app.BuildContext)
	fmt.Fprintf(out, "Dockerfile:       %s\n", app.DockerfilePath)
	if app.SourceConnectionWarning != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "\nWarning: %s\n", app.SourceConnectionWarning)
	}
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Next step: run `astro app deploy` to trigger the first deployment.")
	return nil
}

// registerBuildPaths decides the build context and Dockerfile to register
// with. The context is always sent, so an older platform stores the same
// one; the Dockerfile only when the operator named one, leaving the
// platform's Dockerfile default (and the manifest's own path) otherwise.
func registerBuildPaths(cmd *cobra.Command, raw string) (buildPaths, error) {
	manifestPath := appRegisterManifestPath
	if manifestPath == "" {
		manifestPath = "astrolift.toml"
	}
	detected, err := detectBuildPaths(manifestPath, raw)
	if err != nil {
		return buildPaths{}, fmt.Errorf("parsing %s: %w", appRegisterFile, err)
	}
	contextSet := cmd.Flags().Changed("build-context")
	dockerfileSet := cmd.Flags().Changed("dockerfile") || cmd.Flags().Changed("dockerfile-path")
	if contextSet || dockerfileSet {
		chosen := buildPaths{Context: detected.Context}
		if contextSet {
			chosen.Context = cleanBuildPath(appRegisterBuildContext)
			if chosen.Context == "" {
				return buildPaths{}, fmt.Errorf("--build-context is empty; use . for the repository root")
			}
		}
		if dockerfileSet {
			chosen.Dockerfile = cleanBuildPath(appRegisterDockerfile)
		}
		return chosen, nil
	}
	noPrompt, _ := cmd.Flags().GetBool("no-prompt")
	if !noPrompt {
		noPrompt, _ = cmd.Root().PersistentFlags().GetBool("no-prompt")
	}
	if appRegisterYes || noPrompt || !promptInteractive() {
		return buildPaths{Context: detected.Context}, nil
	}
	chosen, err := confirmBuildPaths(cmd, detected)
	if err != nil {
		return buildPaths{}, err
	}
	if chosen.Dockerfile == defaultDockerfile {
		chosen.Dockerfile = ""
	}
	return chosen, nil
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

var appTokensCmd = newSubResourceCmd("tokens", "Manage deploy tokens")
var appMembersCmd = newSubResourceCmd("members", "Manage app team members")
var appJobsCmd = newSubResourceCmd("jobs", "Manage scheduled jobs")
var appAuditCmd = newSubResourceCmd("audit", "Show app audit log (#12)")

// app logs and app exec are wired in cmd/app_lifecycle.go.
// app previews is a real command group, wired in cmd/app_previews.go.
// app secrets is a real command group, wired in cmd/app_secrets.go.
// app services is a real command group, wired in cmd/app_services.go.
// app domains is a real command group, wired in cmd/app_domains.go.
// app events is a real command group, wired in cmd/app_events.go.

func init() {
	appRegisterCmd.Flags().StringVarP(&appRegisterFile, "file", "f", "astrolift.toml", "Path to the app manifest (default: astrolift.toml)")
	appRegisterCmd.Flags().StringVar(&appRegisterProjectID, "project-id", "", "Project GUID to register the app under (required)")
	appRegisterCmd.Flags().StringVar(&appRegisterSourceRepo, "source-repo", "", "Source repository in owner/repo format (required)")
	appRegisterCmd.Flags().StringVar(&appRegisterSourceKind, "source-kind", "github", "SCM kind: github, gitlab, bitbucket (default: github)")
	appRegisterCmd.Flags().StringVar(&appRegisterDescription, "description", "", "Short description of the app")
	appRegisterCmd.Flags().StringVar(&appRegisterManifestPath, "manifest-path", "", "Path to the manifest file within the repo (default: astrolift.toml)")
	appRegisterCmd.Flags().BoolVar(&appRegisterManifestRaw, "manifest-raw", false, "Send the local manifest's contents with the registration, so workloads are created without the platform reading the repo (no source connection needed)")
	appRegisterCmd.Flags().StringVar(&appRegisterDefaultBranch, "default-branch", "", "Default branch for deploys (default: the repo default)")
	appRegisterCmd.Flags().StringVar(&appRegisterBuildMode, "build-mode", "", "Who publishes the image: ci_pushed (platform default), platform_build, none")
	appRegisterCmd.Flags().StringVar(&appRegisterBuildStrategy, "build-strategy", "", "Builder the platform invokes under --build-mode platform_build: dockerfile (default), buildpacks, nixpacks, off")
	appRegisterCmd.Flags().StringVar(&appRegisterBuildContext, "build-context", "", "Build context, relative to the repo root (default: the manifest's directory); used by platform builds and the managed GitHub CI workflow. Skips the prompt")
	appRegisterCmd.Flags().StringVar(&appRegisterDockerfile, "dockerfile", "", "Dockerfile path, relative to the build context (default: Dockerfile). Skips the prompt")
	// The flag's earlier name, kept working for existing scripts.
	appRegisterCmd.Flags().StringVar(&appRegisterDockerfile, "dockerfile-path", "", "Same as --dockerfile")
	_ = appRegisterCmd.Flags().MarkHidden("dockerfile-path")
	appRegisterCmd.Flags().BoolVarP(&appRegisterYes, "yes", "y", false, "Use the detected build context and Dockerfile without asking")
	appRegisterCmd.Flags().BoolVar(&appRegisterJSON, "json", false, "Output the registered app as JSON")

	appCmd.AddCommand(
		appInitCmd, appRegisterCmd, appDeployCmd,
		appRollbackCmd, appPromoteCmd, appDeregisterCmd,
		appListCmd, appShowCmd,
		appSecretsCmd, appServicesCmd, appDomainsCmd,
		appTokensCmd, appMembersCmd, appJobsCmd,
		appEventsCmd, appAuditCmd,
		appLogsCmd, appExecCmd,
		appPreviewsCmd, appEnvironmentsCmd, appManifestCmd,
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

// Package cmd — `astro app` lifecycle commands wired to the control-plane
// GraphQL API (the same surface cmd/agent.go and cmd/org.go use, posted to
// /app/gql/config/ via api.Client.GraphQL).
//
// GraphQL operations (field names per backend/schema.graphql):
//   - list     → astroliftApps(search, projectSlug) → [AstroliftRegisteredApp]
//   - show     → astroliftApp(slug) + astroliftWorkloads(appSlug)
//   - astroliftEnvironments(appSlug)
//   - deploy   → startDeployment(input: StartDeploymentInput!)
//     --wait polls astroliftDeployment(id) to a terminal state
//   - rollback → rollbackDeployment(input: {id})  (id = the running deployment
//     to roll back; the resolver rolls back to the prior superseded
//     one — backend astrolift_lifecycle/rollback.py + mutations.py)
//   - logs     → astroliftAppLogs(appSlug, since, until, …) paginated query
//     (items oldest-first, [since,until] inclusive); --follow polls
//     it (there is no SSE/subscription log stream the CLI drives)
//   - exec     → thin wrapper over runExec (cmd/exec.go, #1040)
//   - promote  → promoteDeployment(input: PromoteDeploymentInput!) (#1041;
//     --from/--to env names; reuses the #63 promotion policy)
//
// Issues: calliopeai/astrolift-cli#39, #40
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	appListSearch string

	appDeployEnv      string
	appDeployImageTag string
	appDeployWait     bool

	appRollbackEnv string
	appRollbackYes bool

	appLogsEnv    string
	appLogsSince  string
	appLogsTail   int
	appLogsFollow bool
	appLogsLevel  string
	appLogsSearch string
)

// appDeployPollInterval / appLogsPollInterval are the cadences --wait and
// --follow re-query the control plane. They are vars (not consts) so tests
// can shorten them.
var (
	appDeployPollInterval = 3 * time.Second
	appLogsPollInterval   = 3 * time.Second
)

// appLogsPageSize is the per-request page limit when paginating the log
// window; appLogsMaxPages caps the walk so a pathologically busy window can't
// fan out into unbounded requests. 500 * 40 = up to 20k lines scanned.
const (
	appLogsPageSize = 500
	appLogsMaxPages = 40
)

// ---- GraphQL operations ----------------------------------------------------

const astroliftAppsQuery = `query($search: String, $projectSlug: String) {
  astroliftApps(search: $search, projectSlug: $projectSlug) {
    slug
    name
    provisioningStatus
    isActive
    isArchived
    sourceKind
    sourceRepo
    lastDeployedAt
    latestDeployment { id status environmentName imageTag createdAt }
  }
}`

const astroliftAppShowQuery = `query($slug: String!) {
  astroliftApp(slug: $slug) {
    id
    slug
    name
    description
    organizationSlug
    projectSlug
    teamSlug
    sourceKind
    sourceRepo
    sourceUrl
    defaultBranch
    buildMode
    k8sNamespace
    subdomain
    managedHostname
    isActive
    isArchived
    provisioningStatus
    provisioningError
    lastDeployedAt
    latestDeployment { id status environmentName imageTag createdAt }
  }
  astroliftWorkloads(appSlug: $slug) {
    slug
    name
    kind
    replicas
    cpuLimit
    memoryLimit
    isPublic
  }
  astroliftEnvironments(appSlug: $slug) {
    name
    url
    deploysPaused
    clusterSlug
  }
}`

const startDeploymentMutation = `mutation($input: StartDeploymentInput!) {
  startDeployment(input: $input) {
    ok
    errors { code message field }
    data { id status environmentName imageTag registeredAppSlug triggerKind createdAt }
  }
}`

const rollbackDeploymentMutation = `mutation($input: DeploymentByIdInput!) {
  rollbackDeployment(input: $input) {
    ok
    errors { code message field }
    data { id status environmentName imageTag registeredAppSlug triggerKind createdAt }
  }
}`

const promoteDeploymentMutation = `mutation($input: PromoteDeploymentInput!) {
  promoteDeployment(input: $input) {
    ok
    errors { code message field }
    data { id status environmentName imageTag registeredAppSlug triggerKind createdAt }
  }
}`

// astroliftDeploymentQuery is the single-deployment status poll used by --wait.
const astroliftDeploymentQuery = `query($id: String!) {
  astroliftDeployment(id: $id) {
    id
    status
    environmentName
    imageTag
  }
}`

// astroliftDeploymentsQuery lists deployments newest-first; rollback uses it to
// find the currently-running deployment when no id is given.
const astroliftDeploymentsQuery = `query($appSlug: String, $environmentName: String, $limit: Int!) {
  astroliftDeployments(appSlug: $appSlug, environmentName: $environmentName, limit: $limit) {
    id
    status
    environmentName
    imageTag
    createdAt
  }
}`

const astroliftAppLogsQuery = `query($appSlug: String!, $since: DateTime!, $until: DateTime!, $environmentName: String, $workloadSlug: String, $level: String, $search: String, $limit: Int!, $cursor: String) {
  astroliftAppLogs(appSlug: $appSlug, since: $since, until: $until, environmentName: $environmentName, workloadSlug: $workloadSlug, level: $level, search: $search, limit: $limit, cursor: $cursor) {
    items { podName container timestamp message level stream }
    nextCursor
    reachedRetention
    historicalAvailable
    totalCount
  }
}`

// ---- response shapes (GraphQL camelCase) -----------------------------------

// mutationError mirrors the MutationError envelope error (code/message/field),
// shared by the deployment mutations here.
type mutationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field"`
}

// deploymentSummary is the AstroliftAppDeploymentSummary projection (the app's
// latest deployment) plus the subset the deployment mutations return.
type deploymentSummary struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	EnvironmentName string `json:"environmentName"`
	ImageTag        string `json:"imageTag"`
	CreatedAt       string `json:"createdAt"`
}

type appListItem struct {
	Slug               string             `json:"slug"`
	Name               string             `json:"name"`
	ProvisioningStatus string             `json:"provisioningStatus"`
	IsActive           bool               `json:"isActive"`
	IsArchived         bool               `json:"isArchived"`
	SourceKind         string             `json:"sourceKind"`
	SourceRepo         string             `json:"sourceRepo"`
	LastDeployedAt     *string            `json:"lastDeployedAt"`
	LatestDeployment   *deploymentSummary `json:"latestDeployment"`
}

type appDetail struct {
	ID                 string             `json:"id"`
	Slug               string             `json:"slug"`
	Name               string             `json:"name"`
	Description        string             `json:"description"`
	OrganizationSlug   string             `json:"organizationSlug"`
	ProjectSlug        string             `json:"projectSlug"`
	TeamSlug           string             `json:"teamSlug"`
	SourceKind         string             `json:"sourceKind"`
	SourceRepo         string             `json:"sourceRepo"`
	SourceURL          string             `json:"sourceUrl"`
	DefaultBranch      string             `json:"defaultBranch"`
	BuildMode          string             `json:"buildMode"`
	K8sNamespace       string             `json:"k8sNamespace"`
	Subdomain          string             `json:"subdomain"`
	ManagedHostname    string             `json:"managedHostname"`
	IsActive           bool               `json:"isActive"`
	IsArchived         bool               `json:"isArchived"`
	ProvisioningStatus string             `json:"provisioningStatus"`
	ProvisioningError  string             `json:"provisioningError"`
	LastDeployedAt     *string            `json:"lastDeployedAt"`
	LatestDeployment   *deploymentSummary `json:"latestDeployment"`
}

type appWorkload struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Replicas    int    `json:"replicas"`
	CPULimit    string `json:"cpuLimit"`
	MemoryLimit string `json:"memoryLimit"`
	IsPublic    bool   `json:"isPublic"`
}

type appEnvironment struct {
	Name          string  `json:"name"`
	URL           string  `json:"url"`
	DeploysPaused bool    `json:"deploysPaused"`
	ClusterSlug   *string `json:"clusterSlug"`
}

type appShowResult struct {
	App          *appDetail       `json:"astroliftApp"`
	Workloads    []appWorkload    `json:"astroliftWorkloads"`
	Environments []appEnvironment `json:"astroliftEnvironments"`
}

// deploymentMutationResult is the AstroliftDeploymentMutationResult envelope.
type deploymentMutationResult struct {
	Ok     bool               `json:"ok"`
	Errors []mutationError    `json:"errors"`
	Data   *deploymentSummary `json:"data"`
}

type appLogLine struct {
	PodName   string `json:"podName"`
	Container string `json:"container"`
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
	Level     string `json:"level"`
	Stream    string `json:"stream"`
}

// key uniquely identifies a log line for de-duplication across overlapping
// --follow polls (the query window is inclusive, so boundary lines repeat).
func (l appLogLine) key() string {
	return l.Timestamp + "\x00" + l.PodName + "\x00" + l.Container + "\x00" + l.Message
}

type appLogPage struct {
	Items               []appLogLine `json:"items"`
	NextCursor          string       `json:"nextCursor"`
	ReachedRetention    bool         `json:"reachedRetention"`
	HistoricalAvailable bool         `json:"historicalAvailable"`
	TotalCount          int          `json:"totalCount"`
}

// ---- shared helpers --------------------------------------------------------

// resolveAppSlug resolves the working app slug for a command: an explicit
// value wins, then the global --app flag, then the local astrolift.toml in the
// current directory. Returns a clear error when none resolve.
func resolveAppSlug(cmd *cobra.Command, explicit string) (string, error) {
	if s := strings.TrimSpace(explicit); s != "" {
		return s, nil
	}
	if s, _ := cmd.Flags().GetString("app"); strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s), nil
	}
	raw, err := os.ReadFile("astrolift.toml")
	if err != nil {
		return "", fmt.Errorf("no app specified: pass --app <slug> or run inside a directory with astrolift.toml")
	}
	var m appManifest
	if _, err := toml.Decode(string(raw), &m); err != nil {
		return "", fmt.Errorf("parsing astrolift.toml: %w", err)
	}
	if m.App.Slug == "" {
		return "", fmt.Errorf("astrolift.toml: [app] slug is required")
	}
	return m.App.Slug, nil
}

// firstDeployError renders the first MutationError for display.
func firstDeployError(errs []mutationError) string {
	if len(errs) == 0 {
		return "unknown error"
	}
	e := errs[0]
	if e.Field != "" {
		return fmt.Sprintf("%s: %s", e.Field, e.Message)
	}
	return e.Message
}

// deploymentTerminal classifies a deployment status for --wait. done=false
// means keep polling. When done, ok reports whether it ended successfully
// (status "running" — the live state). superseded/rolled_back/failed end the
// wait as non-success.
func deploymentTerminal(status string) (done bool, ok bool) {
	switch strings.ToLower(status) {
	case "running":
		return true, true
	case "failed", "superseded", "rolled_back":
		return true, false
	default:
		return false, false
	}
}

// ---- astro app list --------------------------------------------------------

var appListCmd = &cobra.Command{
	Use:   "list",
	Short: "List apps in the current org",
	Long: `Lists the organization's registered apps via the astroliftApps GraphQL
query (tenant-scoped server-side). Filter with --project (global) or --search.
Output is a table unless --json is given.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppList(cmd, cmd.Context(), client)
	},
}

func runAppList(cmd *cobra.Command, ctx context.Context, client *api.Client) error {
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	vars := map[string]interface{}{}
	if appListSearch != "" {
		vars["search"] = appListSearch
	}
	if project, _ := cmd.Flags().GetString("project"); strings.TrimSpace(project) != "" {
		vars["projectSlug"] = strings.TrimSpace(project)
	}

	var resp struct {
		Apps []appListItem `json:"astroliftApps"`
	}
	if err := client.GraphQL(listCtx, astroliftAppsQuery, vars, &resp); err != nil {
		return fmt.Errorf("listing apps: %w", err)
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Apps)
	}

	out := cmd.OutOrStdout()
	if len(resp.Apps) == 0 {
		fmt.Fprintln(out, "No apps found.")
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SLUG\tNAME\tSTATUS\tREPO\tLAST DEPLOYED")
	for _, a := range resp.Apps {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			a.Slug, a.Name, appStatusLabel(a.ProvisioningStatus, a.IsActive, a.IsArchived),
			dashIfEmpty(a.SourceRepo), shortTime(a.LastDeployedAt),
		)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d app(s).\n", len(resp.Apps))
	return nil
}

// ---- astro app show --------------------------------------------------------

var appShowCmd = &cobra.Command{
	Use:   "show [app-slug]",
	Short: "Show app details (status, source, workloads, environments)",
	Long: `Fetches one app via the astroliftApp GraphQL query plus its workloads
and environments, and prints a summary. The app slug comes from the positional
argument, then --app, then the local astrolift.toml. Use --json for the raw
combined record.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		explicit := ""
		if len(args) == 1 {
			explicit = args[0]
		}
		slug, err := resolveAppSlug(cmd, explicit)
		if err != nil {
			return err
		}
		return runAppShow(cmd, cmd.Context(), client, slug)
	},
}

func runAppShow(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	showCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var result appShowResult
	if err := client.GraphQL(showCtx, astroliftAppShowQuery,
		map[string]interface{}{"slug": slug}, &result); err != nil {
		return fmt.Errorf("fetching app: %w", err)
	}
	if result.App == nil {
		return fmt.Errorf("app %q not found", slug)
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, result)
	}

	a := result.App
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "App:          %s (%s)\n", a.Name, a.Slug)
	if a.Description != "" {
		fmt.Fprintf(out, "Description:  %s\n", a.Description)
	}
	fmt.Fprintf(out, "Status:       %s\n", appStatusLabel(a.ProvisioningStatus, a.IsActive, a.IsArchived))
	if a.ProvisioningError != "" {
		fmt.Fprintf(out, "Error:        %s\n", a.ProvisioningError)
	}
	fmt.Fprintf(out, "Org/Project:  %s / %s\n", a.OrganizationSlug, dashIfEmpty(a.ProjectSlug))
	source := dashIfEmpty(a.SourceRepo)
	if a.SourceKind != "" && a.SourceRepo != "" {
		source = fmt.Sprintf("%s %s", a.SourceKind, a.SourceRepo)
	}
	fmt.Fprintf(out, "Source:       %s\n", source)
	if a.DefaultBranch != "" {
		fmt.Fprintf(out, "Branch:       %s\n", a.DefaultBranch)
	}
	if a.BuildMode != "" {
		fmt.Fprintf(out, "Build mode:   %s\n", a.BuildMode)
	}
	if a.K8sNamespace != "" {
		fmt.Fprintf(out, "Namespace:    %s\n", a.K8sNamespace)
	}
	if host := a.ManagedHostname; host != "" {
		fmt.Fprintf(out, "Hostname:     %s\n", host)
	} else if a.Subdomain != "" {
		fmt.Fprintf(out, "Subdomain:    %s\n", a.Subdomain)
	}

	if d := a.LatestDeployment; d != nil {
		fmt.Fprintln(out, "\nLatest deployment:")
		fmt.Fprintf(out, "  %s  env=%s  image=%s  (%s)\n",
			d.Status, d.EnvironmentName, dashIfEmpty(d.ImageTag), shortTime(&d.CreatedAt))
	}

	fmt.Fprintln(out, "\nWorkloads:")
	if len(result.Workloads) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else {
		w := tabwriter.NewWriter(out, 2, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tKIND\tREPLICAS\tCPU\tMEMORY\tPUBLIC")
		for _, wl := range result.Workloads {
			fmt.Fprintf(w, "  %s\t%s\t%d\t%s\t%s\t%s\n",
				wl.Slug, wl.Kind, wl.Replicas,
				dashIfEmpty(wl.CPULimit), dashIfEmpty(wl.MemoryLimit), yesNo(wl.IsPublic))
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}

	fmt.Fprintln(out, "\nEnvironments:")
	if len(result.Environments) == 0 {
		fmt.Fprintln(out, "  (none)")
	} else {
		w := tabwriter.NewWriter(out, 2, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tURL\tDEPLOYS\tCLUSTER")
		for _, e := range result.Environments {
			cluster := "-"
			if e.ClusterSlug != nil && *e.ClusterSlug != "" {
				cluster = *e.ClusterSlug
			}
			deploys := "enabled"
			if e.DeploysPaused {
				deploys = "paused"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", e.Name, dashIfEmpty(e.URL), deploys, cluster)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return nil
}

// ---- astro app deploy ------------------------------------------------------

var appDeployCmd = &cobra.Command{
	Use:   "deploy",
	Short: "Trigger a deploy for the current app",
	Long: `Starts a deployment via the startDeployment GraphQL mutation. The app
slug comes from --app or the local astrolift.toml.

--image-tag is required (the container image tag or digest to deploy).
--env selects the target environment (default: production).
With --wait, blocks until the deployment reaches a terminal state, polling
astroliftDeployment and surfacing status transitions.

Exit codes: 0 success, 1 deploy failure.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		slug, err := resolveAppSlug(cmd, "")
		if err != nil {
			return err
		}
		return runAppDeploy(cmd, cmd.Context(), client, slug)
	},
}

func runAppDeploy(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	if strings.TrimSpace(appDeployImageTag) == "" {
		return fmt.Errorf("--image-tag is required (the image tag or digest to deploy)")
	}

	input := map[string]interface{}{
		"appSlug":         slug,
		"environmentName": appDeployEnv,
		"imageTag":        appDeployImageTag,
		"triggerKind":     "manual",
	}

	deployCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var resp struct {
		Result deploymentMutationResult `json:"startDeployment"`
	}
	if err := client.GraphQL(deployCtx, startDeploymentMutation,
		map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("starting deployment: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("deploy failed: %s", firstDeployError(resp.Result.Errors))
	}
	d := resp.Result.Data
	if d == nil {
		return fmt.Errorf("deploy succeeded but the server returned no deployment record")
	}

	if boolFlag(cmd, "json") && !appDeployWait {
		return renderJSON(cmd, d)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Deployment started: %s\n", d.ID)
	fmt.Fprintf(out, "Environment:        %s\n", d.EnvironmentName)
	fmt.Fprintf(out, "Image:              %s\n", d.ImageTag)
	fmt.Fprintf(out, "Status:             %s\n", d.Status)

	if !appDeployWait {
		return nil
	}
	return waitForDeployment(cmd, ctx, client, d.ID)
}

// waitForDeployment polls astroliftDeployment until the deployment reaches a
// terminal state, printing status transitions. Returns an error if it ends in
// a non-success state or the wait times out.
func waitForDeployment(cmd *cobra.Command, ctx context.Context, client *api.Client, id string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "Waiting for terminal state...")

	pollCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	last := ""
	for {
		var resp struct {
			Deployment *deploymentSummary `json:"astroliftDeployment"`
		}
		if err := client.GraphQL(pollCtx, astroliftDeploymentQuery,
			map[string]interface{}{"id": id}, &resp); err != nil {
			return fmt.Errorf("polling deployment: %w", err)
		}
		if resp.Deployment != nil {
			status := resp.Deployment.Status
			if status != last {
				fmt.Fprintf(out, "  → %s\n", status)
				last = status
			}
			if done, ok := deploymentTerminal(status); done {
				fmt.Fprintf(out, "Final status: %s\n", status)
				if !ok {
					return fmt.Errorf("deployment ended in %q", status)
				}
				return nil
			}
		}

		select {
		case <-pollCtx.Done():
			return fmt.Errorf("timed out waiting for terminal state (last status: %s)", last)
		case <-time.After(appDeployPollInterval):
		}
	}
}

// ---- astro app rollback ----------------------------------------------------

var appRollbackCmd = &cobra.Command{
	Use:   "rollback [deployment-id]",
	Short: "Roll back to the previous deployment",
	Long: `Rolls back via the rollbackDeployment GraphQL mutation. The argument is
the id of the currently-running deployment to roll back; the control plane
rolls forward to the prior superseded deployment's exact image.

With no argument, the running deployment is resolved automatically from
astroliftDeployments for the app (--app / astrolift.toml) and --env (default
production). Prompts for confirmation unless --yes is given.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		explicitID := ""
		if len(args) == 1 {
			explicitID = args[0]
		}
		return runAppRollback(cmd, cmd.Context(), client, explicitID)
	},
}

func runAppRollback(cmd *cobra.Command, ctx context.Context, client *api.Client, explicitID string) error {
	rollbackCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	id := strings.TrimSpace(explicitID)
	if id == "" {
		slug, err := resolveAppSlug(cmd, "")
		if err != nil {
			return err
		}
		running, err := findRunningDeployment(rollbackCtx, client, slug, appRollbackEnv)
		if err != nil {
			return err
		}
		id = running.ID
		fmt.Fprintf(cmd.OutOrStdout(), "Resolved running deployment for %s/%s: %s (image %s)\n",
			slug, appRollbackEnv, id, dashIfEmpty(running.ImageTag))
	}

	if !appRollbackYes {
		noPrompt, _ := cmd.Root().PersistentFlags().GetBool("no-prompt")
		if !noPrompt {
			fmt.Fprintf(cmd.OutOrStdout(), "Roll back deployment %s? [y/N] ", id)
			var answer string
			_, _ = fmt.Fscan(cmd.InOrStdin(), &answer)
			if strings.ToLower(strings.TrimSpace(answer)) != "y" {
				fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
				return nil
			}
		}
	}

	var resp struct {
		Result deploymentMutationResult `json:"rollbackDeployment"`
	}
	if err := client.GraphQL(rollbackCtx, rollbackDeploymentMutation,
		map[string]interface{}{"input": map[string]interface{}{"id": id}}, &resp); err != nil {
		return fmt.Errorf("rolling back: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("rollback failed: %s", firstDeployError(resp.Result.Errors))
	}
	d := resp.Result.Data
	if d == nil {
		return fmt.Errorf("rollback succeeded but the server returned no deployment record")
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, d)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Rollback started:   %s\n", d.ID)
	fmt.Fprintf(out, "Environment:        %s\n", d.EnvironmentName)
	fmt.Fprintf(out, "Image:              %s\n", d.ImageTag)
	fmt.Fprintf(out, "Status:             %s\n", d.Status)
	return nil
}

// findRunningDeployment returns the newest running deployment for an app/env,
// the rollback target the backend mutation expects.
func findRunningDeployment(ctx context.Context, client *api.Client, slug, env string) (deploymentSummary, error) {
	vars := map[string]interface{}{"appSlug": slug, "limit": 50}
	if env != "" {
		vars["environmentName"] = env
	}
	var resp struct {
		Deployments []deploymentSummary `json:"astroliftDeployments"`
	}
	if err := client.GraphQL(ctx, astroliftDeploymentsQuery, vars, &resp); err != nil {
		return deploymentSummary{}, fmt.Errorf("listing deployments: %w", err)
	}
	for _, d := range resp.Deployments {
		if strings.EqualFold(d.Status, "running") {
			return d, nil
		}
	}
	return deploymentSummary{}, fmt.Errorf(
		"no running deployment found for app %q (env %q) to roll back; pass an explicit deployment id",
		slug, env)
}

// ---- astro app promote -----------------------------------------------------

var (
	appPromoteFrom string
	appPromoteTo   string
)

var appPromoteCmd = &cobra.Command{
	Use:   "promote",
	Short: "Promote a deployment from one environment to another",
	Long: `Promote the source environment's currently-running deployment — its exact
image and config — into a target environment, via the promoteDeployment
mutation. The control plane records the lineage (promoted_from) and honors the
target environment's approval gate.

  astro app promote --from staging --to production [--app <slug>]`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		slug, err := resolveAppSlug(cmd, "")
		if err != nil {
			return err
		}
		return runAppPromote(cmd, cmd.Context(), client, slug, appPromoteFrom, appPromoteTo)
	},
}

func runAppPromote(cmd *cobra.Command, ctx context.Context, client *api.Client, slug, fromEnv, toEnv string) error {
	from := strings.TrimSpace(fromEnv)
	to := strings.TrimSpace(toEnv)
	if from == "" || to == "" {
		return fmt.Errorf("--from and --to are required (source and target environment names)")
	}

	promoteCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var resp struct {
		Result deploymentMutationResult `json:"promoteDeployment"`
	}
	if err := client.GraphQL(promoteCtx, promoteDeploymentMutation, map[string]interface{}{
		"input": map[string]interface{}{
			"appSlug":               slug,
			"sourceEnvironmentName": from,
			"targetEnvironmentName": to,
		},
	}, &resp); err != nil {
		return fmt.Errorf("promoting: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("promote failed: %s", firstDeployError(resp.Result.Errors))
	}
	d := resp.Result.Data
	if d == nil {
		return fmt.Errorf("promote succeeded but the server returned no deployment record")
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, d)
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Promotion started:  %s\n", d.ID)
	fmt.Fprintf(out, "From → To:          %s → %s\n", from, to)
	fmt.Fprintf(out, "Environment:        %s\n", d.EnvironmentName)
	fmt.Fprintf(out, "Image:              %s\n", d.ImageTag)
	fmt.Fprintf(out, "Status:             %s\n", d.Status)
	return nil
}

// ---- astro app logs --------------------------------------------------------

var appLogsCmd = &cobra.Command{
	Use:   "logs [workload]",
	Short: "Show logs for an app's workloads",
	Long: `Fetches historical log lines for the app via the astroliftAppLogs
GraphQL query, within the --since window (default 1h), oldest line last. The
app slug comes from --app or the local astrolift.toml; an optional [workload]
narrows to one workload.

The control plane has no SSE log stream, so --follow (-f) polls the query on a
fixed interval and prints only newly-appended lines until interrupted (Ctrl-C).
Filter with --level and --search; pick an environment with --env.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		slug, err := resolveAppSlug(cmd, "")
		if err != nil {
			return err
		}
		workload := ""
		if len(args) == 1 {
			workload = args[0]
		}
		return runAppLogs(cmd, cmd.Context(), client, slug, workload)
	},
}

func runAppLogs(cmd *cobra.Command, ctx context.Context, client *api.Client, slug, workload string) error {
	window, err := time.ParseDuration(appLogsSince)
	if err != nil {
		return fmt.Errorf("--since %q is not a valid duration (e.g. 30m, 1h, 24h): %w", appLogsSince, err)
	}
	if appLogsTail < 1 {
		return fmt.Errorf("--tail must be >= 1")
	}

	base := map[string]interface{}{"appSlug": slug}
	if appLogsEnv != "" {
		base["environmentName"] = appLogsEnv
	}
	if workload != "" {
		base["workloadSlug"] = workload
	}
	if appLogsLevel != "" {
		base["level"] = appLogsLevel
	}
	if appLogsSearch != "" {
		base["search"] = appLogsSearch
	}

	if boolFlag(cmd, "json") {
		// --json is a single snapshot of the tail (follow is a stream).
		lines, _, meta, err := fetchAppLogTail(ctx, client, base, window, appLogsTail)
		if err != nil {
			return err
		}
		logMetaNotes(cmd, meta)
		return renderJSON(cmd, lines)
	}

	out := cmd.OutOrStdout()
	lines, capped, meta, err := fetchAppLogTail(ctx, client, base, window, appLogsTail)
	if err != nil {
		return err
	}
	for _, l := range lines {
		printLogLine(out, l)
	}
	logMetaNotes(cmd, meta)
	if capped {
		fmt.Fprintln(cmd.ErrOrStderr(),
			"note: the log window has more lines than were scanned; narrow --since for older lines")
	}

	if !appLogsFollow {
		return nil
	}
	return followAppLogs(cmd, ctx, client, base, lines)
}

// fetchAppLogTail paginates the [now-window, now] log window forward
// (oldest-first) and returns at most `tail` lines — the most recent `tail`
// within the window. capped is true when the per-fetch page cap was hit before
// the window's end (so the result is an earlier slice, not the true tail).
func fetchAppLogTail(ctx context.Context, client *api.Client, base map[string]interface{}, window time.Duration, tail int) ([]appLogLine, bool, appLogPage, error) {
	until := time.Now().UTC()
	since := until.Add(-window)
	ring := make([]appLogLine, 0, tail)
	reachedEnd, meta, err := paginateAppLogs(ctx, client, base, since, until, func(items []appLogLine) {
		ring = append(ring, items...)
		if len(ring) > tail {
			ring = ring[len(ring)-tail:]
		}
	})
	if err != nil {
		return nil, false, meta, err
	}
	return ring, !reachedEnd, meta, nil
}

// followAppLogs polls a short trailing window and prints lines not already
// emitted. It tracks a per-poll seen-set keyed by line identity so the
// inclusive query boundary doesn't reprint lines; the window always covers a
// generous overlap so no line is missed between polls.
func followAppLogs(cmd *cobra.Command, ctx context.Context, client *api.Client, base map[string]interface{}, seeded []appLogLine) error {
	out := cmd.OutOrStdout()
	const overlap = 30 * time.Second

	seen := make(map[string]bool, len(seeded))
	for _, l := range seeded {
		seen[l.key()] = true
	}
	lastUntil := time.Now().UTC()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(appLogsPollInterval):
		}

		until := time.Now().UTC()
		since := lastUntil.Add(-overlap)

		var batch []appLogLine
		_, _, err := paginateAppLogs(ctx, client, base, since, until, func(items []appLogLine) {
			batch = append(batch, items...)
		})
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("polling logs: %w", err)
		}

		next := make(map[string]bool, len(batch))
		for _, l := range batch {
			k := l.key()
			next[k] = true
			if !seen[k] {
				printLogLine(out, l)
			}
		}
		seen = next
		lastUntil = until
	}
}

// paginateAppLogs drives the cursor pagination of one [since, until] window,
// invoking onPage with each page's items. Returns reachedEnd (the window was
// fully walked before the page cap) and the last page's metadata.
func paginateAppLogs(ctx context.Context, client *api.Client, base map[string]interface{}, since, until time.Time, onPage func([]appLogLine)) (bool, appLogPage, error) {
	var meta appLogPage
	cursor := ""
	for i := 0; i < appLogsMaxPages; i++ {
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		vars := make(map[string]interface{}, len(base)+4)
		for k, v := range base {
			vars[k] = v
		}
		vars["since"] = since.Format(time.RFC3339Nano)
		vars["until"] = until.Format(time.RFC3339Nano)
		vars["limit"] = appLogsPageSize
		if cursor != "" {
			vars["cursor"] = cursor
		}

		var resp struct {
			Page appLogPage `json:"astroliftAppLogs"`
		}
		err := client.GraphQL(fetchCtx, astroliftAppLogsQuery, vars, &resp)
		cancel()
		if err != nil {
			return false, meta, fmt.Errorf("fetching logs: %w", err)
		}
		meta = resp.Page
		onPage(resp.Page.Items)
		if resp.Page.NextCursor == "" {
			return true, meta, nil
		}
		cursor = resp.Page.NextCursor
	}
	return false, meta, nil
}

// printLogLine renders one log line as "<timestamp> <message>".
func printLogLine(out io.Writer, l appLogLine) {
	fmt.Fprintf(out, "%s %s\n", l.Timestamp, l.Message)
}

// logMetaNotes surfaces page-level caveats (no historical backend / retention)
// to stderr so they don't pollute the log stream on stdout.
func logMetaNotes(cmd *cobra.Command, meta appLogPage) {
	errOut := cmd.ErrOrStderr()
	if !meta.HistoricalAvailable {
		fmt.Fprintln(errOut,
			"note: this cluster has no historical log backend configured (live tail only)")
	}
	if meta.ReachedRetention {
		fmt.Fprintln(errOut,
			"note: earlier lines were discarded by the log backend (retention window)")
	}
}

// ---- astro app exec --------------------------------------------------------

var appExecCmd = &cobra.Command{
	Use:   "exec [workload] [-- command...]",
	Short: "Run a command or interactive shell in a workload's pod",
	Long: `Opens an exec session into a running pod for the current app. This is a
thin wrapper over 'astro exec' scoped to the app from --app or the local
astrolift.toml; an optional [workload] narrows pod selection.

With a '-- <command...>' suffix runs that command; otherwise an interactive
shell. Requires the app.exec_pod permission; every session is audited. See
'astro exec --help' for --pod / --container / --no-tty details.

Examples:
  astro app exec -- bash
  astro app exec web -- python manage.py migrate
  astro app exec --app api worker -- sh`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		slug, err := resolveAppSlug(cmd, "")
		if err != nil {
			return err
		}

		// Split the optional [workload] positional from the post-`--` command.
		workload, command, err := splitExecArgs(args, cmd.ArgsLenAtDash())
		if err != nil {
			return err
		}

		// Populate the shared exec inputs and reuse runExec verbatim — no
		// duplicated WebSocket logic (cmd/exec.go).
		execApp = slug
		execWorkload = workload
		return runExec(cmd, cmd.Context(), client, command)
	},
}

// splitExecArgs separates the optional [workload] positional from the
// post-`--` command for `app exec`. dash is cobra's ArgsLenAtDash() (-1 when
// no `--` was given, in which case all args are positional and the command is
// empty → interactive shell).
func splitExecArgs(args []string, dash int) (workload string, command []string, err error) {
	pre := args
	if dash >= 0 {
		pre = args[:dash]
		command = args[dash:]
	}
	if len(pre) > 1 {
		return "", nil, fmt.Errorf("unexpected arguments before '--': %v (usage: app exec [workload] -- <command...>)", pre[1:])
	}
	if len(pre) == 1 {
		workload = pre[0]
	}
	return workload, command, nil
}

// ---- small render helpers --------------------------------------------------

// appStatusLabel renders an app's lifecycle state for tables/show output.
func appStatusLabel(provisioning string, active, archived bool) string {
	if archived {
		return "archived"
	}
	if provisioning != "" && !strings.EqualFold(provisioning, "ready") && !strings.EqualFold(provisioning, "provisioned") {
		return provisioning
	}
	if active {
		return "active"
	}
	return "inactive"
}

// dashIfEmpty renders "-" for an empty string (table readability).
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// ---- init ------------------------------------------------------------------

func init() {
	// list
	appListCmd.Flags().StringVar(&appListSearch, "search", "", "Filter apps by name/slug substring")

	// deploy
	appDeployCmd.Flags().StringVar(&appDeployEnv, "env", "production", "Target environment")
	appDeployCmd.Flags().StringVar(&appDeployImageTag, "image-tag", "", "Container image tag or digest to deploy (required)")
	appDeployCmd.Flags().BoolVar(&appDeployWait, "wait", false, "Block until the deployment reaches a terminal state")

	// rollback
	appRollbackCmd.Flags().StringVar(&appRollbackEnv, "env", "production", "Environment whose running deployment to roll back (when no id given)")
	appRollbackCmd.Flags().BoolVarP(&appRollbackYes, "yes", "y", false, "Skip confirmation prompt")

	// promote
	appPromoteCmd.Flags().StringVar(&appPromoteFrom, "from", "", "Source environment whose running deployment to promote (required)")
	appPromoteCmd.Flags().StringVar(&appPromoteTo, "to", "", "Target environment to promote into (required)")

	// logs
	appLogsCmd.Flags().StringVar(&appLogsEnv, "env", "", "Environment to read logs from (default: app default)")
	appLogsCmd.Flags().StringVar(&appLogsSince, "since", "1h", "How far back to read (Go duration, e.g. 30m, 1h, 24h)")
	appLogsCmd.Flags().IntVar(&appLogsTail, "tail", 200, "Maximum number of recent lines to show")
	appLogsCmd.Flags().BoolVarP(&appLogsFollow, "follow", "f", false, "Poll for and stream new log lines")
	appLogsCmd.Flags().StringVar(&appLogsLevel, "level", "", "Filter by structured log level")
	appLogsCmd.Flags().StringVar(&appLogsSearch, "search", "", "Filter to lines matching a substring")

	// exec — reuse the same target vars cmd/exec.go binds, so runExec sees them
	appExecCmd.Flags().StringVar(&execPod, "pod", "", "Exact pod name (skips auto-resolution)")
	appExecCmd.Flags().StringVarP(&execContainer, "container", "c", "", "Container name (multi-container pods)")
	appExecCmd.Flags().BoolVar(&execNoTTY, "no-tty", false, "Force non-interactive (no TTY) even on a terminal")
}

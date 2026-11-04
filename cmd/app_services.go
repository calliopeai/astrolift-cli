// Package cmd -- `astro app services ...`: read-only view of the managed
// services bound to one app.
//
// The group existed as an empty placeholder (calliopeai/astrolift-cli#91):
// `astro app services list` printed the generic sub-resource blurb and did
// nothing, even though it was "the natural way to check whether a managed
// service had provisioned" during a live demo.
//
// This is deliberately read-only. Provisioning, attaching, detaching, and
// reconfiguring managed services is already a real command group at
// `astro project resources` (aliased `astro project services`), which
// drives provisionProjectManagedService / attachProjectManagedService /
// detachProjectManagedService / updateProjectManagedService against the
// project's shared-service catalogue (cmd/project_resources.go). `app
// services list` complements it with the answer to "what is bound to
// *this* app right now", scoped by astroliftManagedServicesPage(appSlug).
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var appServicesListEnv string

// appServicesPageSize / appServicesMaxPages bound the walk, mirroring the
// previews page walk (cmd/app_previews.go): a handful of managed services
// per app is the common case, but the query is paginated server-side, so
// the walk stays bounded rather than looping without limit.
const (
	appServicesPageSize = 50
	appServicesMaxPages = resourceScanMaxPages
)

// ---- GraphQL operations -----------------------------------------------------

const appManagedServicesPageQuery = `query($appSlug: String!, $environmentName: String, $limit: Int!, $after: String) {
  astroliftManagedServicesPage(appSlug: $appSlug, environmentName: $environmentName, limit: $limit, after: $after) {
    items {
      id
      name
      kind
      variant
      isolation
      status
      environmentName
      clusterSlug
      projectSlug
      bindingReady
      createdAt
      updatedAt
    }
    nextCursor
    totalCount
  }
}`

// ---- response shapes (GraphQL camelCase) ------------------------------------

type appManagedServiceAttachment struct {
	ID              string `json:"id"`
	ConsumerKind    string `json:"consumerKind"`
	ConsumerSlug    string `json:"consumerSlug"`
	EnvironmentName string `json:"environmentName"`
}

// appManagedService mirrors the subset of AstroliftManagedService a
// per-app read view needs.
type appManagedService struct {
	ID              string                        `json:"id"`
	Name            string                        `json:"name"`
	Kind            string                        `json:"kind"`
	Variant         string                        `json:"variant"`
	Isolation       string                        `json:"isolation"`
	Status          string                        `json:"status"`
	StatusError     string                        `json:"-"`
	EnvironmentName string                        `json:"environmentName"`
	ClusterSlug     string                        `json:"clusterSlug"`
	ProjectSlug     string                        `json:"projectSlug"`
	BindingReady    bool                          `json:"bindingReady"`
	GrantState      string                        `json:"grantState"`
	CreatedAt       string                        `json:"createdAt"`
	UpdatedAt       string                        `json:"updatedAt"`
	Attachments     []appManagedServiceAttachment `json:"-"`
}

type appServicePage struct {
	Items      []appManagedService `json:"items"`
	NextCursor *string             `json:"nextCursor"`
	TotalCount int                 `json:"totalCount"`
}

func fetchAppServicePage(ctx context.Context, client *api.Client, variables map[string]interface{}) (*appServicePage, error) {
	var response struct {
		Page *appServicePage `json:"astroliftManagedServicesPage"`
	}
	if err := client.GraphQL(ctx, appManagedServicesPageQuery, variables, &response); err != nil {
		return nil, fmt.Errorf("managed service page unavailable; restart the page or review current permissions")
	}
	if response.Page == nil {
		return nil, fmt.Errorf("managed service page unavailable")
	}
	return response.Page, nil
}

// ---- astro app services ------------------------------------------------------

var appServicesCmd = &cobra.Command{
	Use:   "services",
	Short: "List the managed services bound to an app",
	Long: `Lists the managed services (databases, caches, queues, object stores, and
the rest of the provider catalogue) bound to an app, via
astroliftManagedServicesPage.

This is a read-only view. Provision, attach, detach, and reconfigure a
managed service with "astro project resources" (aliased "astro project
services"), which operates on the project's shared-service catalogue rather
than one app's view of it.`,
}

var appServicesListCmd = &cobra.Command{
	Use:   "list [app]",
	Short: "List the managed services bound to an app",
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
		return runAppServicesList(cmd, cmd.Context(), client, slug)
	},
}

func runAppServicesList(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string) error {
	limit := appServicesPageSize
	if cmd.Flags().Lookup("limit") != nil {
		limit, _ = cmd.Flags().GetInt("limit")
	}
	if limit < 1 || limit > 200 {
		return fmt.Errorf("--limit must be between 1 and 200")
	}
	vars := map[string]interface{}{"appSlug": appSlug, "limit": limit}
	if strings.TrimSpace(appServicesListEnv) != "" {
		vars["environmentName"] = strings.TrimSpace(appServicesListEnv)
	}
	paged := boolFlag(cmd, "page")
	if after := resourceStringFlag(cmd, "after"); after != "" {
		if !paged {
			return fmt.Errorf("--after requires --page")
		}
		vars["after"] = after
	}
	page, err := fetchAppServicePage(ctx, client, vars)
	if err != nil {
		return err
	}
	if paged && boolFlag(cmd, "json") {
		return renderJSON(cmd, page)
	}
	rows := page.Items
	if !paged {
		seen := map[string]bool{}
		for count := 1; page.NextCursor != nil; count++ {
			if count >= appServicesMaxPages || seen[*page.NextCursor] {
				return fmt.Errorf("managed service walk incomplete; use --page with explicit continuation")
			}
			seen[*page.NextCursor] = true
			vars["after"] = *page.NextCursor
			page, err = fetchAppServicePage(ctx, client, vars)
			if err != nil {
				return err
			}
			rows = append(rows, page.Items...)
		}
		if rows == nil {
			rows = []appManagedService{}
		}
		if boolFlag(cmd, "json") {
			return renderJSON(cmd, rows)
		}
	}
	out := cmd.OutOrStdout()
	if len(rows) == 0 {
		fmt.Fprintf(out, "No managed services bound to app %q.\n", appSlug)
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tVARIANT\tENVIRONMENT\tSTATUS\tBINDING READY\tID\tCREATED")
	for _, row := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", row.Name, row.Kind, dashIfEmpty(row.Variant), dashIfEmpty(row.EnvironmentName), dashIfEmpty(row.Status), yesNo(row.BindingReady), row.ID, shortTime(&row.CreatedAt))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d managed service(s) shown.\n", len(rows))
	if paged && page.NextCursor != nil {
		fmt.Fprintf(out, "Next cursor: %s\n", *page.NextCursor)
	}
	return nil
}

var appServicesShowCmd = &cobra.Command{Use: "show <resource-GUID>", Short: "Review exact app-owned managed service metadata", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	app, err := resolveAppSlug(cmd, "")
	if err != nil {
		return err
	}
	client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	return runAppServiceShow(cmd, cmd.Context(), client, app, args[0])
}}

func runAppServiceShow(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug, id string) error {
	row, err := fetchAppOwnedService(cmd, ctx, client, appSlug, id)
	if err != nil {
		return err
	}
	return renderProjectResource(cmd, row)
}

// fetchAppOwnedService reads one managed service and refuses anything that is
// not owned by appSlug in the active organization, or that changed from
// --expected-context-revision.
func fetchAppOwnedService(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug, id string) (*projectResource, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, fmt.Errorf("an exact resource GUID is required")
	}
	var response struct {
		Row *projectResource `json:"astroliftManagedService"`
	}
	query := `query($id:GUID!,$expectedContextRevision:String){astroliftManagedService(id:$id,expectedContextRevision:$expectedContextRevision){` + resourceContextFields + `}}`
	vars := map[string]interface{}{"id": id}
	revision := resourceStringFlag(cmd, "expected-context-revision")
	if revision != "" {
		vars["expectedContextRevision"] = revision
	}
	if err := client.GraphQL(ctx, query, vars, &response); err != nil {
		return nil, fmt.Errorf("resource context unavailable or changed")
	}
	row := response.Row
	if row == nil || row.ID != id || row.OwnerScope != "app" || row.RegisteredAppSlug != appSlug || row.RegisteredAppID == "" || row.ContextRevision == "" || (client.Org() != "" && row.OrganizationID != client.Org()) || (revision != "" && row.ContextRevision != revision) {
		return nil, fmt.Errorf("resource context unavailable or changed")
	}
	return row, nil
}

// ---- astro app services match / reprovision ---------------------------------

const previewManagedServiceMatchMutation = `mutation($input: MatchManagedServicesInput!) {
  previewManagedServiceMatch(input: $input) {
    ok
    errors { code message field }
    data {
      appSlug
      digest
      blocked
      changes { action ownerScope environment kind name reason }
    }
  }
}`

const applyManagedServiceMatchMutation = `mutation($input: ApplyManagedServiceMatchInput!) {
  applyManagedServiceMatch(input: $input) {
    ok
    errors { code message field }
    data {
      appSlug
      digest
      changes { action ownerScope environment kind name reason }
    }
  }
}`

const reprovisionManagedServiceMutation = `mutation($input: ReprovisionManagedServiceInput!) {
  reprovisionManagedService(input: $input) {
    ok
    errors { code message field }
    data { id name kind variant status environmentName bindingReady }
  }
}`

// managedServiceMatchChange is one ManagedServiceMatchChange: what the match
// would do to one declared [[managed_services]] entry.
type managedServiceMatchChange struct {
	Action      string `json:"action"`
	OwnerScope  string `json:"ownerScope"`
	Environment string `json:"environment"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Reason      string `json:"reason"`
}

// managedServiceMatchPlan is the ManagedServiceMatchPlanPayload from the dry
// run. Digest pins the plan that apply must still match.
type managedServiceMatchPlan struct {
	AppSlug string                      `json:"appSlug"`
	Digest  string                      `json:"digest"`
	Blocked bool                        `json:"blocked"`
	Changes []managedServiceMatchChange `json:"changes"`
}

// managedServiceMatchApplied is the ManagedServiceMatchAppliedPayload.
type managedServiceMatchApplied struct {
	AppSlug string                      `json:"appSlug"`
	Digest  string                      `json:"digest"`
	Changes []managedServiceMatchChange `json:"changes"`
}

var appServicesMatchYes bool

var appServicesMatchCmd = &cobra.Command{
	Use:   "match [app]",
	Short: "Match an app's managed services to its manifest",
	Long: `Compares each declared [[managed_services]] entry in the app's manifest with
the platform record and lists what would change: create, reconfigure, bind,
retry, remove, detach, or blocked. The plan is printed first. Nothing changes
until you confirm; --yes skips the prompt. With prompts disabled, --yes is
required.

Apply sends the plan's digest back, so a plan that changed since it was
previewed is refused with CONFLICT and nothing is applied. A blocked plan is
printed and exits non-zero without applying.

Exit codes: 0 applied or nothing to do, 1 failure or blocked plan.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppServicesMatch(cmd, cmd.Context(), client, slug)
	},
}

func runAppServicesMatch(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string) error {
	input := map[string]interface{}{"appSlug": appSlug}
	var preview struct {
		Result struct {
			Ok     bool                     `json:"ok"`
			Errors []mutationError          `json:"errors"`
			Data   *managedServiceMatchPlan `json:"data"`
		} `json:"previewManagedServiceMatch"`
	}
	if err := client.GraphQL(ctx, previewManagedServiceMatchMutation, map[string]interface{}{"input": input}, &preview); err != nil {
		if errors.Is(err, api.ErrSchemaMismatch) {
			return fmt.Errorf("matching %s: this server does not offer service match; it needs an Astrolift control plane with astrolift-app#2606", appSlug)
		}
		return fmt.Errorf("previewing match for %s: %w", appSlug, err)
	}
	if !preview.Result.Ok {
		return fmt.Errorf("match preview failed: %s", managedServiceMatchError(preview.Result.Errors))
	}
	plan := preview.Result.Data
	if plan == nil {
		return fmt.Errorf("match preview returned no plan")
	}

	jsonOut := boolFlag(cmd, "json")
	out := cmd.OutOrStdout()
	if plan.Blocked {
		if jsonOut {
			if err := renderJSON(cmd, plan); err != nil {
				return err
			}
		} else {
			printManagedServiceChanges(out, plan.Changes)
		}
		return fmt.Errorf("match for %s is blocked; resolve the blocked change(s) and run it again (nothing was changed)", appSlug)
	}
	if len(plan.Changes) == 0 {
		if jsonOut {
			return renderJSON(cmd, plan)
		}
		fmt.Fprintf(out, "Managed services for app %q already match the manifest.\n", appSlug)
		return nil
	}
	if !jsonOut {
		printManagedServiceChanges(out, plan.Changes)
	}

	if !appServicesMatchYes {
		if jsonOut {
			return fmt.Errorf("pass --yes with --json to apply the match (nothing was changed)")
		}
		noPrompt, _ := cmd.Root().PersistentFlags().GetBool("no-prompt")
		if noPrompt {
			return fmt.Errorf("match needs confirmation: pass --yes (--no-prompt is set; nothing was changed)")
		}
		fmt.Fprintf(out, "\nApply %d change(s) to app %s? [y/N] ", len(plan.Changes), appSlug)
		var answer string
		_, _ = fmt.Fscan(cmd.InOrStdin(), &answer)
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
	}

	var applied struct {
		Result struct {
			Ok     bool                        `json:"ok"`
			Errors []mutationError             `json:"errors"`
			Data   *managedServiceMatchApplied `json:"data"`
		} `json:"applyManagedServiceMatch"`
	}
	applyInput := map[string]interface{}{"appSlug": appSlug, "expectedDigest": plan.Digest}
	if err := client.GraphQL(ctx, applyManagedServiceMatchMutation, map[string]interface{}{"input": applyInput}, &applied); err != nil {
		return fmt.Errorf("applying match for %s: %w", appSlug, err)
	}
	if !applied.Result.Ok {
		return fmt.Errorf("match for %s failed: %s", appSlug, managedServiceMatchError(applied.Result.Errors))
	}
	result := applied.Result.Data
	if result == nil {
		return fmt.Errorf("match applied but the server returned no result")
	}
	if jsonOut {
		return renderJSON(cmd, result)
	}
	fmt.Fprintf(out, "Applied %d change(s) to app %q.\n", len(result.Changes), appSlug)
	return nil
}

// printManagedServiceChanges prints the plan, one change per row.
func printManagedServiceChanges(out io.Writer, changes []managedServiceMatchChange) {
	if len(changes) == 0 {
		fmt.Fprintln(out, "No changes.")
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ACTION\tOWNER\tENVIRONMENT\tKIND\tNAME\tREASON")
	for _, c := range changes {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", c.Action, dashIfEmpty(c.OwnerScope), dashIfEmpty(c.Environment), c.Kind, c.Name, dashIfEmpty(c.Reason))
	}
	_ = w.Flush()
}

// managedServiceMatchError renders the first mutation error. CONFLICT means
// the plan moved since the preview, so the user reruns match to review it.
func managedServiceMatchError(errs []mutationError) string {
	if len(errs) > 0 && errs[0].Code == "CONFLICT" {
		return "the plan changed since it was previewed; run match again to review it (nothing was applied)"
	}
	if len(errs) > 0 && errs[0].Code != "" {
		return errs[0].Code + ": " + firstDeployError(errs)
	}
	return firstDeployError(errs)
}

var appServicesReprovisionCmd = &cobra.Command{
	Use:   "reprovision <resource-GUID>",
	Short: "Re-apply one app-owned managed service",
	Long: `Re-applies one app-owned managed service from current state via the
reprovisionManagedService mutation. The service must belong to --app; a
project-owned service is refused. Use "astro app services list" to find its
GUID.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		app, err := resolveAppSlug(cmd, "")
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppServicesReprovision(cmd, cmd.Context(), client, app, args[0])
	},
}

func runAppServicesReprovision(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug, id string) error {
	row, err := fetchAppOwnedService(cmd, ctx, client, appSlug, id)
	if err != nil {
		return err
	}
	input := map[string]interface{}{"managedServiceId": id}
	if revision := resourceStringFlag(cmd, "expected-context-revision"); revision != "" {
		input["expectedContextRevision"] = revision
	}
	var resp struct {
		Result struct {
			Ok     bool               `json:"ok"`
			Errors []mutationError    `json:"errors"`
			Data   *appManagedService `json:"data"`
		} `json:"reprovisionManagedService"`
	}
	if err := client.GraphQL(ctx, reprovisionManagedServiceMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		if errors.Is(err, api.ErrSchemaMismatch) {
			return fmt.Errorf("reprovisioning %s: this server does not offer managed service reprovision; it needs an Astrolift control plane with astrolift-app#2606", row.Name)
		}
		return fmt.Errorf("reprovisioning %s: %w", row.Name, err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("reprovision of %s failed: %s", row.Name, managedServiceMatchError(resp.Result.Errors))
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Reprovisioned %s (%s) on app %q.\n", row.Name, row.Kind, appSlug)
	return nil
}

func init() {
	appServicesMatchCmd.Flags().BoolVarP(&appServicesMatchYes, "yes", "y", false, "Apply the planned changes without the confirmation prompt")
	appServicesReprovisionCmd.Flags().String("expected-context-revision", "", "Refuse a changed resource context")
	appServicesCmd.AddCommand(appServicesMatchCmd, appServicesReprovisionCmd)

	appServicesListCmd.Flags().StringVar(&appServicesListEnv, "environment", "", "Filter to one environment")
	appServicesListCmd.Flags().Bool("page", false, "Return one page with items, totalCount and nextCursor")
	appServicesListCmd.Flags().Int("limit", 50, "Server page size (1-200)")
	appServicesListCmd.Flags().String("after", "", "Continuation cursor; requires --page")
	appServicesShowCmd.Flags().String("expected-context-revision", "", "Refuse a changed resource context")
	appServicesCmd.AddCommand(appServicesListCmd, appServicesShowCmd)
}

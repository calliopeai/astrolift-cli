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
	"fmt"
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
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("an exact resource GUID is required")
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
		return fmt.Errorf("resource context unavailable or changed")
	}
	row := response.Row
	if row == nil || row.ID != id || row.OwnerScope != "app" || row.RegisteredAppSlug != appSlug || row.RegisteredAppID == "" || row.ContextRevision == "" || (client.Org() != "" && row.OrganizationID != client.Org()) || (revision != "" && row.ContextRevision != revision) {
		return fmt.Errorf("resource context unavailable or changed")
	}
	return renderProjectResource(cmd, row)
}

func init() {
	appServicesListCmd.Flags().StringVar(&appServicesListEnv, "environment", "", "Filter to one environment")
	appServicesListCmd.Flags().Bool("page", false, "Return one page with items, totalCount and nextCursor")
	appServicesListCmd.Flags().Int("limit", 50, "Server page size (1-200)")
	appServicesListCmd.Flags().String("after", "", "Continuation cursor; requires --page")
	appServicesShowCmd.Flags().String("expected-context-revision", "", "Refuse a changed resource context")
	appServicesCmd.AddCommand(appServicesListCmd, appServicesShowCmd)
}

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
	appServicesMaxPages = 4
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
      statusError
      environmentName
      clusterSlug
      projectSlug
      bindingReady
      grantState
      createdAt
      updatedAt
      attachments { id consumerKind consumerSlug environmentName }
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
	StatusError     string                        `json:"statusError"`
	EnvironmentName string                        `json:"environmentName"`
	ClusterSlug     string                        `json:"clusterSlug"`
	ProjectSlug     string                        `json:"projectSlug"`
	BindingReady    bool                          `json:"bindingReady"`
	GrantState      string                        `json:"grantState"`
	CreatedAt       string                        `json:"createdAt"`
	UpdatedAt       string                        `json:"updatedAt"`
	Attachments     []appManagedServiceAttachment `json:"attachments"`
}

// fetchAppManagedServices walks astroliftManagedServicesPage for one app.
// The second return reports that the page cap cut the walk short.
func fetchAppManagedServices(ctx context.Context, client *api.Client, appSlug, environmentName string) ([]appManagedService, bool, error) {
	all := make([]appManagedService, 0, appServicesPageSize)
	cursor := ""
	for page := 0; page < appServicesMaxPages; page++ {
		vars := map[string]interface{}{"appSlug": appSlug, "limit": appServicesPageSize}
		if environmentName != "" {
			vars["environmentName"] = environmentName
		}
		if cursor != "" {
			vars["after"] = cursor
		}

		var resp struct {
			Page struct {
				Items      []appManagedService `json:"items"`
				NextCursor string              `json:"nextCursor"`
			} `json:"astroliftManagedServicesPage"`
		}
		if err := client.GraphQL(ctx, appManagedServicesPageQuery, vars, &resp); err != nil {
			return nil, false, fmt.Errorf("listing managed services for %s: %w", appSlug, err)
		}
		all = append(all, resp.Page.Items...)
		if resp.Page.NextCursor == "" {
			return all, false, nil
		}
		cursor = resp.Page.NextCursor
	}
	return all, true, nil
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
	rows, truncated, err := fetchAppManagedServices(ctx, client, appSlug, strings.TrimSpace(appServicesListEnv))
	if err != nil {
		return err
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, rows)
	}

	out := cmd.OutOrStdout()
	if len(rows) == 0 {
		fmt.Fprintf(out, "No managed services bound to app %q.\n", appSlug)
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tVARIANT\tENVIRONMENT\tSTATUS\tBINDING READY\tCREATED")
	for _, s := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Name, s.Kind, dashIfEmpty(s.Variant), dashIfEmpty(s.EnvironmentName),
			appManagedServiceStatusLabel(s), yesNo(s.BindingReady), shortTime(&s.CreatedAt))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d managed service(s) shown.\n", len(rows))
	if truncated {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: only the %d most recently created managed services were scanned\n", appServicesPageSize*appServicesMaxPages)
	}
	return nil
}

// appManagedServiceStatusLabel appends the error when a service is in a
// failed state, so "list" surfaces why without a separate "show".
func appManagedServiceStatusLabel(s appManagedService) string {
	if strings.TrimSpace(s.StatusError) != "" {
		return fmt.Sprintf("%s (%s)", s.Status, s.StatusError)
	}
	return dashIfEmpty(s.Status)
}

func init() {
	appServicesListCmd.Flags().StringVar(&appServicesListEnv, "environment", "", "Filter to one environment")
	appServicesCmd.AddCommand(appServicesListCmd)
}

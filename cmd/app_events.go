// Package cmd -- `astro app events ...`: an app's platform event feed.
//
// The group existed as an empty placeholder (calliopeai/astrolift-cli#91 /
// #99): `astro app events --help` printed the generic sub-resource blurb
// and produced nothing, even though the README's quick-start already
// documented `astro app events` as the way to see platform events.
//
// Backed by astroliftEventsPage(appSlug, limit, eventType, severity,
// search) -- the paginated field; the unpaged astroliftEvents is
// deprecated (caps at 500 rows with no way to reach the 501st). Only the
// first page is fetched: an operator checking recent activity for one app
// wants the newest --limit rows, and nothing in either reported issue asked
// for a --after cursor.
//
// Runs both bare ("astro app events") and as an explicit subcommand
// ("astro app events list"): the README already documents the bare form,
// and calliopeai/astrolift-cli#91 was filed against the explicit one.
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

var (
	appEventsLimit    int
	appEventsType     string
	appEventsSeverity string
	appEventsSearch   string
)

// ---- GraphQL operations -----------------------------------------------------

const appEventsPageQuery = `query($appSlug: String, $limit: Int!, $eventType: String, $severity: String, $search: String) {
  astroliftEventsPage(appSlug: $appSlug, limit: $limit, eventType: $eventType, severity: $severity, search: $search) {
    items {
      id
      eventType
      payload
      occurredAt
      resourceKind
      resourceId
      severity
    }
    nextCursor
    reason
  }
}`

// ---- response shapes (GraphQL camelCase) ------------------------------------

// appEvent mirrors the AstroliftEvent GraphQL type.
type appEvent struct {
	ID           string                 `json:"id"`
	EventType    string                 `json:"eventType"`
	Payload      map[string]interface{} `json:"payload"`
	OccurredAt   string                 `json:"occurredAt"`
	ResourceKind string                 `json:"resourceKind"`
	ResourceID   string                 `json:"resourceId"`
	Severity     string                 `json:"severity"`
}

// ---- astro app events -------------------------------------------------------

var appEventsCmd = &cobra.Command{
	Use:   "events [app]",
	Short: "Show an app's platform event log",
	Long: `Lists recent platform events for an app -- deploys, secret writes, scale
operations, workload health transitions, and the rest of the activity
astroliftEventsPage records.

Runs the listing directly, or via the explicit "list" subcommand -- both
take the same flags and print the same table. --json includes each event's
full payload; the table shows only time, type, severity, and resource.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAppEventsFromArgs(cmd, args)
	},
}

var appEventsListCmd = &cobra.Command{
	Use:   "list [app]",
	Short: "Show an app's platform event log",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runAppEventsFromArgs(cmd, args)
	},
}

func runAppEventsFromArgs(cmd *cobra.Command, args []string) error {
	slug, err := resolveAppSlug(cmd, firstArg(args))
	if err != nil {
		return err
	}
	client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	return runAppEventsList(cmd, cmd.Context(), client, slug)
}

func runAppEventsList(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string) error {
	if appEventsLimit < 1 {
		return fmt.Errorf("--limit must be >= 1")
	}
	vars := map[string]interface{}{"appSlug": appSlug, "limit": appEventsLimit}
	if v := strings.TrimSpace(appEventsType); v != "" {
		vars["eventType"] = v
	}
	if v := strings.TrimSpace(appEventsSeverity); v != "" {
		vars["severity"] = v
	}
	if v := strings.TrimSpace(appEventsSearch); v != "" {
		vars["search"] = v
	}

	var resp struct {
		Page struct {
			Items      []appEvent `json:"items"`
			NextCursor string     `json:"nextCursor"`
			Reason     string     `json:"reason"`
		} `json:"astroliftEventsPage"`
	}
	if err := client.GraphQL(ctx, appEventsPageQuery, vars, &resp); err != nil {
		return fmt.Errorf("listing events for %s: %w", appSlug, err)
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Page.Items)
	}

	out := cmd.OutOrStdout()
	if len(resp.Page.Items) == 0 {
		if resp.Page.Reason == "NO_DATA_YET" {
			fmt.Fprintf(out, "No events recorded yet for app %q.\n", appSlug)
		} else {
			fmt.Fprintf(out, "No events matched for app %q.\n", appSlug)
		}
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tTYPE\tSEVERITY\tRESOURCE")
	for _, e := range resp.Page.Items {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			shortTime(&e.OccurredAt), e.EventType, dashIfEmpty(e.Severity), appEventResourceLabel(e))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d event(s) shown. Use --json for each event's full payload.\n", len(resp.Page.Items))
	if resp.Page.NextCursor != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: more events are available; raise --limit to see them\n")
	}
	return nil
}

// appEventResourceLabel renders an event's resource as "<kind>/<id>",
// falling back to whichever half is present.
func appEventResourceLabel(e appEvent) string {
	kind := strings.TrimSpace(e.ResourceKind)
	id := strings.TrimSpace(e.ResourceID)
	switch {
	case kind != "" && id != "":
		return kind + "/" + id
	case kind != "":
		return kind
	case id != "":
		return id
	default:
		return "-"
	}
}

func init() {
	for _, c := range []*cobra.Command{appEventsCmd, appEventsListCmd} {
		c.Flags().IntVar(&appEventsLimit, "limit", 100, "Maximum number of events to show")
		c.Flags().StringVar(&appEventsType, "type", "", "Filter by event type")
		c.Flags().StringVar(&appEventsSeverity, "severity", "", "Filter by severity (info, warn, error)")
		c.Flags().StringVar(&appEventsSearch, "search", "", "Filter to events matching a substring")
	}

	appEventsCmd.AddCommand(appEventsListCmd)
}

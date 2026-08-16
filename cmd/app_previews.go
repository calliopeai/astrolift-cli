// Package cmd — `astro app previews ...` subcommand tree.
//
// The group existed as an empty placeholder: `astro app previews --help`
// printed the generic sub-resource blurb and registered nothing, so per-PR
// preview environments were visible in the console but unreachable from the
// CLI. The control-plane surface has been shipped for a while; this is the
// client half.
//
// GraphQL operations (field names per backend/schema.graphql):
//   - list/show/logs/open/teardown all resolve rows from
//     astroliftPreviewEnvironmentsPage(appSlug, limit, after)
//     → Page{ items: [AstroliftPreviewEnvironment], nextCursor, totalCount }.
//     The unpaged astroliftPreviewEnvironments is deprecated (200-row cap,
//     and it prices every row on read).
//   - logs     → astroliftEnvironments(appSlug) to map the preview onto the
//     AppEnvironment the platform synthesized for it, then reuses runAppLogs
//     (cmd/app_lifecycle.go) verbatim rather than duplicating the log
//     pagination / --follow plumbing.
//   - teardown → tearDownPreview(input: TearDownPreviewInputGql!)
//   - open     → no call; the URL is https://<hostname> off the row.
//   - pin/unpin → setPreviewPinned(input: SetPreviewPinnedInput!), one setter
//     behind two verbs: pin sends pinned: true, unpin sends pinned: false.
//
// The pin is the operator's exemption from garbage collection, and it covers
// *both* rules the scheduled sweep applies (astrolift_workflows/preview_gc.py):
// is_eligible_for_gc short-circuits on it so TTL expiry never fires, and
// max-active eviction filters it out of the candidate list so a newer PR
// cannot push it out. extendPreviewTtl is not the same thing: it only moves
// ttl_until, in capped 1/7/30-day steps, on the TTL axis alone, and it has no
// inverse. Unpin clears the whole pinned-at/by/reason trail so a stale
// justification never reads as the current one; re-pinning refreshes it.
//
// Issues: calliopeai/astrolift-cli#64, calliopeai/astrolift-cli#67
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

// previewSelector is the --pr / --branch pair every single-preview verb takes.
//
// --branch exists because manual previews (created via
// createPreviewEnvironment rather than a PR webhook) carry pr_number = NULL,
// which the resolver coerces to prNumber: 0. They appear in `list` but no PR
// number addresses them, so branch is the only selector that reaches them.
type previewSelector struct {
	pr int
	// prSet distinguishes "--pr not given" from an explicit "--pr 0", which
	// would otherwise silently match every manual preview at once.
	prSet  bool
	branch string
}

var (
	previewsListAll   bool
	previewsListLimit int

	previewsShowSel previewSelector

	previewsLogsSel      previewSelector
	previewsLogsWorkload string

	previewsOpenSel     previewSelector
	previewsOpenURLOnly bool

	previewsTeardownSel previewSelector
	previewsTeardownYes bool

	previewsPinSel    previewSelector
	previewsPinReason string

	previewsUnpinSel previewSelector
)

// ---- GraphQL operations -----------------------------------------------------

// previewsPageSize / previewsMaxPages bound the walk. Every returned row costs
// the server one live pod listing plus a pricing lookup, which is why the
// deprecated unpaged field capped at 200; the same ceiling is kept here.
const (
	previewsPageSize = 50
	previewsMaxPages = 4
)

const previewEnvironmentsPageQuery = `query($appSlug: String, $limit: Int!, $after: String) {
  astroliftPreviewEnvironmentsPage(appSlug: $appSlug, limit: $limit, after: $after) {
    items {
      id
      registeredAppSlug
      prNumber
      isManual
      branch
      commitSha
      status
      hostname
      namespace
      lastDeployedAt
      tornDownAt
      ttlUntil
      isPinned
      pinnedAt
      pinnedByEmail
      pinReason
      sourceUrl
      prUrl
      aggregateResources { cpuCores memoryBytes podCount }
      estimatedDailyCostUsd
    }
    nextCursor
    totalCount
  }
}`

// previewAppEnvironmentsQuery selects only what environment resolution needs.
const previewAppEnvironmentsQuery = `query($appSlug: String) {
  astroliftEnvironments(appSlug: $appSlug) {
    name
    url
  }
}`

const tearDownPreviewMutation = `mutation($input: TearDownPreviewInputGql!) {
  tearDownPreview(input: $input) {
    ok
    errors { code message field }
  }
}`

// setPreviewPinnedMutation is the single setter behind both pin and unpin.
// The full row comes back so the reported reason is the stored, truncated one
// rather than the flag text, and so --json hands back a record identical in
// shape to what `previews show` emits.
const setPreviewPinnedMutation = `mutation($input: SetPreviewPinnedInput!) {
  setPreviewPinned(input: $input) {
    ok
    errors { code message field }
    data {
      id
      registeredAppSlug
      prNumber
      isManual
      branch
      commitSha
      status
      hostname
      namespace
      lastDeployedAt
      tornDownAt
      ttlUntil
      isPinned
      pinnedAt
      pinnedByEmail
      pinReason
      sourceUrl
      prUrl
      aggregateResources { cpuCores memoryBytes podCount }
      estimatedDailyCostUsd
    }
  }
}`

// ---- response shapes (GraphQL camelCase) -----------------------------------

// previewAggregateResources mirrors AstroliftPreviewAggregateResources.
type previewAggregateResources struct {
	CPUCores    float64 `json:"cpuCores"`
	MemoryBytes float64 `json:"memoryBytes"`
	PodCount    int     `json:"podCount"`
}

// previewEnvironment mirrors the AstroliftPreviewEnvironment GraphQL type.
// This is the shape --json emits.
type previewEnvironment struct {
	ID                    string                    `json:"id"`
	RegisteredAppSlug     string                    `json:"registeredAppSlug"`
	PRNumber              int                       `json:"prNumber"`
	IsManual              bool                      `json:"isManual"`
	Branch                string                    `json:"branch"`
	CommitSha             string                    `json:"commitSha"`
	Status                string                    `json:"status"`
	Hostname              string                    `json:"hostname"`
	Namespace             string                    `json:"namespace"`
	LastDeployedAt        *string                   `json:"lastDeployedAt"`
	TornDownAt            *string                   `json:"tornDownAt"`
	TTLUntil              string                    `json:"ttlUntil"`
	IsPinned              bool                      `json:"isPinned"`
	PinnedAt              *string                   `json:"pinnedAt"`
	PinnedByEmail         *string                   `json:"pinnedByEmail"`
	PinReason             string                    `json:"pinReason"`
	SourceURL             string                    `json:"sourceUrl"`
	PRURL                 string                    `json:"prUrl"`
	AggregateResources    previewAggregateResources `json:"aggregateResources"`
	EstimatedDailyCostUSD *float64                  `json:"estimatedDailyCostUsd"`
}

// previewAppEnvironment is the AstroliftAppEnvironment subset used to map a
// preview onto the environment its workloads actually run in.
type previewAppEnvironment struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// previewOpenInfo is the --json shape for `open`. hostname is reported
// alongside url so a caller that builds its own links still has the raw value.
type previewOpenInfo struct {
	PRNumber int    `json:"prNumber"`
	Branch   string `json:"branch"`
	Status   string `json:"status"`
	Hostname string `json:"hostname"`
	URL      string `json:"url"`
}

// previewTeardownResult is the --json shape for `teardown`. The teardown runs
// as an async workflow, so the only honest report is which preview was asked
// to go away, not a terminal state.
type previewTeardownResult struct {
	ID                string `json:"id"`
	PRNumber          int    `json:"prNumber"`
	Branch            string `json:"branch"`
	PreviousStatus    string `json:"previousStatus"`
	TeardownRequested bool   `json:"teardownRequested"`
}

// ---- pure helpers -----------------------------------------------------------

// validate rejects selector combinations that cannot name exactly one preview.
func (s previewSelector) validate() error {
	branch := strings.TrimSpace(s.branch)
	switch {
	case s.prSet && branch != "":
		return errors.New("--pr and --branch are mutually exclusive; pass one")
	case s.prSet && s.pr < 1:
		return errors.New(
			"--pr must be a positive PR number; manual previews have no PR number, select them with --branch")
	case !s.prSet && branch == "":
		return errors.New("one of --pr <n> or --branch <name> is required")
	}
	return nil
}

// label names what the selector asked for, for error and prompt text.
func (s previewSelector) label() string {
	if branch := strings.TrimSpace(s.branch); branch != "" {
		return fmt.Sprintf("branch %q", branch)
	}
	return fmt.Sprintf("PR #%d", s.pr)
}

// matches reports whether one row satisfies the selector.
func (s previewSelector) matches(p previewEnvironment) bool {
	if branch := strings.TrimSpace(s.branch); branch != "" {
		return p.Branch == branch
	}
	return s.pr > 0 && p.PRNumber == s.pr
}

// previewIsTornDown reports whether a row is already gone. Torn-down previews
// keep their hostname and namespace, so status is the signal, not emptiness.
func previewIsTornDown(p previewEnvironment) bool {
	if strings.EqualFold(strings.TrimSpace(p.Status), "torn_down") {
		return true
	}
	return p.TornDownAt != nil && strings.TrimSpace(*p.TornDownAt) != ""
}

// livePreviews drops torn-down rows. `list` hides them by default so the
// table shows what is currently costing money; --all brings back the history.
func livePreviews(items []previewEnvironment) []previewEnvironment {
	live := make([]previewEnvironment, 0, len(items))
	for _, p := range items {
		if !previewIsTornDown(p) {
			live = append(live, p)
		}
	}
	return live
}

// previewPRLabel renders the PR column. Manual previews report prNumber 0
// because the column is NULL on the row; showing "0" would read as a real PR.
func previewPRLabel(p previewEnvironment) string {
	if p.PRNumber > 0 {
		return fmt.Sprintf("#%d", p.PRNumber)
	}
	return "manual"
}

// previewURL is the browsable URL for a preview, empty until the platform has
// assigned a hostname (a preview is created in status=building).
func previewURL(p previewEnvironment) string {
	host := strings.TrimSpace(p.Hostname)
	if host == "" {
		return ""
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		return host
	}
	return "https://" + host
}

// hostFromURL extracts the lowercase host from an AppEnvironment.url, which
// the backend writes as "https://<hostname>". Tolerates a bare hostname.
func hostFromURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if u, err := url.Parse(s); err == nil && u.Host != "" {
		return strings.ToLower(u.Hostname())
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// resolvePreview picks the single row a selector names.
//
// Ambiguity is real for --branch: the unique-active constraint is per
// (app, pr_number) for PR previews and per (app, branch) for manual ones, so
// a PR preview and a manual preview can share a branch. Picking one silently
// would tear down or tail the wrong environment.
func resolvePreview(items []previewEnvironment, sel previewSelector) (previewEnvironment, error) {
	if err := sel.validate(); err != nil {
		return previewEnvironment{}, err
	}
	var matches []previewEnvironment
	for _, p := range items {
		if sel.matches(p) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return previewEnvironment{}, fmt.Errorf(
			"no preview environment for %s%s", sel.label(), previewCandidateHint(items))
	default:
		return previewEnvironment{}, fmt.Errorf(
			"%s matches %d preview environments (%s); select one with --pr",
			sel.label(), len(matches), strings.Join(previewLabels(matches), ", "))
	}
}

// previewLabels renders rows as "#12 (feat/x)" for disambiguation messages.
func previewLabels(items []previewEnvironment) []string {
	out := make([]string, 0, len(items))
	for _, p := range items {
		out = append(out, fmt.Sprintf("%s (%s)", previewPRLabel(p), dashIfEmpty(p.Branch)))
	}
	return out
}

// previewCandidateHint lists what the app does have, so a typo'd --pr is
// self-correcting instead of a bare "not found".
func previewCandidateHint(items []previewEnvironment) string {
	if len(items) == 0 {
		return "; the app has no preview environments"
	}
	return fmt.Sprintf("; the app has %d: %s", len(items), strings.Join(previewLabels(items), ", "))
}

// previewEnvironmentName maps a preview onto the AppEnvironment the platform
// synthesized for it. The GraphQL type does not carry the environment name,
// but both backend creation paths write AppEnvironment.url as
// "https://" + preview.hostname, so the host is an exact join key.
//
// The name-convention fallback (preview-pr-<n>, per spec 18 §5) is only used
// when an environment by that name actually exists; guessing a name the
// platform does not have would turn a resolution failure into an empty log
// tail.
func previewEnvironmentName(envs []previewAppEnvironment, p previewEnvironment) string {
	if host := strings.ToLower(strings.TrimSpace(p.Hostname)); host != "" {
		for _, e := range envs {
			if hostFromURL(e.URL) == host {
				return e.Name
			}
		}
	}
	if p.PRNumber > 0 {
		want := fmt.Sprintf("preview-pr-%d", p.PRNumber)
		for _, e := range envs {
			if strings.EqualFold(e.Name, want) {
				return e.Name
			}
		}
	}
	return ""
}

// previewMemoryLabel renders aggregate memory in the largest readable unit.
func previewMemoryLabel(bytes float64) string {
	switch {
	case bytes >= float64(1<<30):
		return fmt.Sprintf("%.1f GiB", bytes/float64(1<<30))
	case bytes >= float64(1<<20):
		return fmt.Sprintf("%.0f MiB", bytes/float64(1<<20))
	case bytes > 0:
		return fmt.Sprintf("%.0f B", bytes)
	default:
		return "0"
	}
}

// previewResourcesLabel collapses the aggregate resource block into one line.
func previewResourcesLabel(r previewAggregateResources) string {
	return fmt.Sprintf("%d pod(s), %.2f vCPU, %s", r.PodCount, r.CPUCores, previewMemoryLabel(r.MemoryBytes))
}

// previewCostLabel renders the estimated daily cost; null means the cluster
// has no pricing wired up, which is not the same as zero.
func previewCostLabel(usd *float64) string {
	if usd == nil {
		return "-"
	}
	return fmt.Sprintf("$%.2f/day", *usd)
}

// writePreviewPinDetail renders the pin block for `show`.
//
// The pin is what decides whether the TTL line above it means anything, so it
// is always printed, and the who/when/why lines only when a pin is actually in
// force — unpin clears all three server-side, so rendering them unconditionally
// would print a cleared trail as though it were current.
func writePreviewPinDetail(out io.Writer, p previewEnvironment) {
	if !p.IsPinned {
		fmt.Fprintf(out, "Pinned:           %s\n", yesNo(false))
		return
	}
	// pinnedByEmail is null when the account that set the pin has since been
	// removed, which is not the same as a preview that was never pinned.
	pinnedBy := ""
	if p.PinnedByEmail != nil {
		pinnedBy = *p.PinnedByEmail
	}
	fmt.Fprintf(out, "Pinned:           yes (exempt from TTL expiry and max-active eviction)\n")
	fmt.Fprintf(out, "Pinned at:        %s\n", shortTime(p.PinnedAt))
	fmt.Fprintf(out, "Pinned by:        %s\n", dashIfEmpty(pinnedBy))
	fmt.Fprintf(out, "Pin reason:       %s\n", dashIfEmpty(p.PinReason))
}

// ---- transport --------------------------------------------------------------

// fetchAppPreviews walks astroliftPreviewEnvironmentsPage for one app. The
// second return reports that the page cap cut the walk short, so a "not
// found" can say it only scanned a prefix instead of asserting absence.
func fetchAppPreviews(ctx context.Context, client *api.Client, appSlug string) ([]previewEnvironment, bool, error) {
	all := make([]previewEnvironment, 0, previewsPageSize)
	cursor := ""
	for page := 0; page < previewsMaxPages; page++ {
		vars := map[string]interface{}{"appSlug": appSlug, "limit": previewsPageSize}
		if cursor != "" {
			vars["after"] = cursor
		}

		var resp struct {
			Page struct {
				Items      []previewEnvironment `json:"items"`
				NextCursor string               `json:"nextCursor"`
				TotalCount *int                 `json:"totalCount"`
			} `json:"astroliftPreviewEnvironmentsPage"`
		}

		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := client.GraphQL(fetchCtx, previewEnvironmentsPageQuery, vars, &resp)
		cancel()
		if err != nil {
			return nil, false, fmt.Errorf("listing preview environments for %s: %w", appSlug, err)
		}

		all = append(all, resp.Page.Items...)
		if resp.Page.NextCursor == "" {
			return all, false, nil
		}
		cursor = resp.Page.NextCursor
	}
	return all, true, nil
}

// loadPreview fetches the app's previews and resolves the one a selector names.
func loadPreview(ctx context.Context, client *api.Client, appSlug string, sel previewSelector) (previewEnvironment, error) {
	if err := sel.validate(); err != nil {
		return previewEnvironment{}, err
	}
	rows, truncated, err := fetchAppPreviews(ctx, client, appSlug)
	if err != nil {
		return previewEnvironment{}, err
	}
	p, err := resolvePreview(rows, sel)
	if err != nil {
		if truncated {
			return previewEnvironment{}, fmt.Errorf(
				"%w (only the %d most recent previews for %q were scanned)", err, len(rows), appSlug)
		}
		return previewEnvironment{}, err
	}
	return p, nil
}

// fetchAppEnvironments reads the app's environments, previews included.
func fetchAppEnvironments(ctx context.Context, client *api.Client, appSlug string) ([]previewAppEnvironment, error) {
	envCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Environments []previewAppEnvironment `json:"astroliftEnvironments"`
	}
	if err := client.GraphQL(envCtx, previewAppEnvironmentsQuery,
		map[string]interface{}{"appSlug": appSlug}, &resp); err != nil {
		return nil, fmt.Errorf("listing environments for %s: %w", appSlug, err)
	}
	return resp.Environments, nil
}

// ---- astro app previews -----------------------------------------------------

var appPreviewsCmd = &cobra.Command{
	Use:   "previews",
	Short: "Inspect and control an app's preview environments",
	Long: `Work with the per-PR preview environments the platform builds for an app.

A preview is created when a PR opens on a preview-enabled app (or by hand via
the console), gets its own namespace, hostname and synthesized environment,
and is torn down when the PR closes or the GC reaps it.

The app slug comes from the positional argument, then --app, then the local
astrolift.toml. Single-preview verbs take --pr <n>; manual previews have no
PR number, so select those with --branch <name>.

'pin' exempts a preview from garbage collection entirely — both from TTL
expiry and from max-active eviction — until an operator runs 'unpin'. That
is the difference from extending a TTL, which only defers the TTL clock and
still leaves the preview evictable when a newer PR needs the slot.`,
}

// ---- astro app previews list ------------------------------------------------

var appPreviewsListCmd = &cobra.Command{
	Use:   "list [app]",
	Short: "List an app's preview environments",
	Long: `Lists the app's preview environments, newest first, via the
astroliftPreviewEnvironmentsPage GraphQL query.

Each row carries the PR number (or 'manual'), branch, status, hostname, when
it last deployed, the TTL the garbage collector reaps it at, and whether an
operator has pinned it. A pinned preview is exempt from collection, so its
TTL column is advisory. Torn-down previews are hidden by default; pass --all
to include them.`,
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
		return runAppPreviewsList(cmd, cmd.Context(), client, slug)
	},
}

func runAppPreviewsList(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string) error {
	if previewsListLimit < 1 {
		return fmt.Errorf("--limit must be >= 1")
	}

	rows, truncated, err := fetchAppPreviews(ctx, client, appSlug)
	if err != nil {
		return err
	}
	if !previewsListAll {
		rows = livePreviews(rows)
	}
	if len(rows) > previewsListLimit {
		rows = rows[:previewsListLimit]
	}

	out := cmd.OutOrStdout()
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, rows)
	}

	if len(rows) == 0 {
		if previewsListAll {
			fmt.Fprintf(out, "No preview environments for app %q.\n", appSlug)
		} else {
			fmt.Fprintf(out, "No active preview environments for app %q (--all includes torn-down ones).\n", appSlug)
		}
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PR\tBRANCH\tSTATUS\tHOSTNAME\tLAST DEPLOYED\tTTL UNTIL\tPINNED")
	for _, p := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			previewPRLabel(p), dashIfEmpty(p.Branch), dashIfEmpty(p.Status),
			dashIfEmpty(p.Hostname), shortTime(p.LastDeployedAt), shortTime(&p.TTLUntil),
			yesNo(p.IsPinned),
		)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d preview(s) shown.\n", len(rows))
	if truncated {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: only the %d most recent previews were scanned\n", previewsPageSize*previewsMaxPages)
	}
	return nil
}

// ---- astro app previews show ------------------------------------------------

var appPreviewsShowCmd = &cobra.Command{
	Use:   "show [app] --pr <n>",
	Short: "Show one preview environment in detail",
	Long: `Prints one preview environment's full record: its URL, status, branch and
commit, the namespace it runs in, its TTL, its garbage-collection pin, the
pull request that created it, and the aggregate resources and estimated
daily cost it is consuming.

Select the preview with --pr <n>, or --branch <name> for a manual preview.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sel := readPreviewSelector(cmd, previewsShowSel)
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppPreviewsShow(cmd, cmd.Context(), client, slug, sel)
	},
}

func runAppPreviewsShow(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string, sel previewSelector) error {
	p, err := loadPreview(ctx, client, appSlug, sel)
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, p)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "App:              %s\n", dashIfEmpty(p.RegisteredAppSlug))
	fmt.Fprintf(out, "PR:               %s\n", previewPRLabel(p))
	fmt.Fprintf(out, "Branch:           %s\n", dashIfEmpty(p.Branch))
	fmt.Fprintf(out, "Status:           %s\n", dashIfEmpty(p.Status))
	if u := previewURL(p); u != "" {
		fmt.Fprintf(out, "URL:              %s\n", u)
	} else {
		fmt.Fprintln(out, "URL:              - (no hostname assigned yet)")
	}
	fmt.Fprintf(out, "Namespace:        %s\n", dashIfEmpty(p.Namespace))
	fmt.Fprintf(out, "Commit:           %s\n", dashIfEmpty(p.CommitSha))
	fmt.Fprintf(out, "Last deployed:    %s\n", shortTime(p.LastDeployedAt))
	fmt.Fprintf(out, "TTL until:        %s\n", shortTime(&p.TTLUntil))
	writePreviewPinDetail(out, p)
	if previewIsTornDown(p) {
		fmt.Fprintf(out, "Torn down:        %s\n", shortTime(p.TornDownAt))
	}
	if p.PRURL != "" {
		fmt.Fprintf(out, "Pull request:     %s\n", p.PRURL)
	}
	fmt.Fprintf(out, "Resources:        %s\n", previewResourcesLabel(p.AggregateResources))
	fmt.Fprintf(out, "Estimated cost:   %s\n", previewCostLabel(p.EstimatedDailyCostUSD))
	fmt.Fprintf(out, "ID:               %s\n", p.ID)
	return nil
}

// ---- astro app previews logs ------------------------------------------------

var appPreviewsLogsCmd = &cobra.Command{
	Use:   "logs [app] --pr <n>",
	Short: "Show logs for a preview environment's workloads",
	Long: `Tails the log lines for one preview environment.

This is 'astro app logs' pointed at the environment the platform synthesized
for the preview, so every log flag behaves identically: --since bounds the
window, --tail caps the lines, --follow (-f) polls for new ones, and --level
/ --search filter. Use --workload to narrow to one workload.

Select the preview with --pr <n>, or --branch <name> for a manual preview.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sel := readPreviewSelector(cmd, previewsLogsSel)
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppPreviewsLogs(cmd, cmd.Context(), client, slug, sel)
	},
}

func runAppPreviewsLogs(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string, sel previewSelector) error {
	p, err := loadPreview(ctx, client, appSlug, sel)
	if err != nil {
		return err
	}
	if previewIsTornDown(p) {
		return fmt.Errorf("%s on app %q is torn down; its workloads and namespace are gone",
			sel.label(), appSlug)
	}

	envs, err := fetchAppEnvironments(ctx, client, appSlug)
	if err != nil {
		return err
	}
	envName := previewEnvironmentName(envs, p)
	if envName == "" {
		return fmt.Errorf(
			"could not resolve the environment for %s on app %q: no environment matches hostname %q (the preview may still be building)",
			sel.label(), appSlug, dashIfEmpty(p.Hostname))
	}

	// Reuse the app-logs plumbing verbatim: same query, same pagination, same
	// --follow loop, just scoped to the preview's environment.
	appLogsEnv = envName
	return runAppLogs(cmd, ctx, client, appSlug, previewsLogsWorkload)
}

// ---- astro app previews open ------------------------------------------------

var appPreviewsOpenCmd = &cobra.Command{
	Use:   "open [app] --pr <n>",
	Short: "Open a preview environment's URL in a browser",
	Long: `Resolves a preview to its public URL and opens it in the default browser.

Pass --url to print the URL instead of opening it, which is what a script or
an editor integration wants. --json prints the URL alongside the preview's
status and hostname.

Select the preview with --pr <n>, or --branch <name> for a manual preview.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sel := readPreviewSelector(cmd, previewsOpenSel)
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppPreviewsOpen(cmd, cmd.Context(), client, slug, sel)
	},
}

func runAppPreviewsOpen(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string, sel previewSelector) error {
	p, err := loadPreview(ctx, client, appSlug, sel)
	if err != nil {
		return err
	}
	target := previewURL(p)
	if target == "" {
		return fmt.Errorf("%s on app %q has no hostname assigned yet (status: %s)",
			sel.label(), appSlug, dashIfEmpty(p.Status))
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, previewOpenInfo{
			PRNumber: p.PRNumber,
			Branch:   p.Branch,
			Status:   p.Status,
			Hostname: p.Hostname,
			URL:      target,
		})
	}

	// A building or failed preview still has a hostname; say so rather than
	// silently opening a URL that will not resolve.
	if !strings.EqualFold(p.Status, "running") {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"note: preview is %s, not running; the URL may not resolve yet\n", dashIfEmpty(p.Status))
	}

	out := cmd.OutOrStdout()
	if previewsOpenURLOnly {
		fmt.Fprintln(out, target)
		return nil
	}
	fmt.Fprintln(out, target)
	if err := openBrowser(target); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "note: could not open a browser (%v); the URL is above\n", err)
	}
	return nil
}

// ---- astro app previews teardown --------------------------------------------

var appPreviewsTeardownCmd = &cobra.Command{
	Use:   "teardown [app] --pr <n>",
	Short: "Force teardown of a preview environment",
	Long: `Tears down a preview environment now, without waiting for its PR to close
or for the garbage collector's TTL to expire.

Calls the tearDownPreview mutation, which enqueues TeardownPreviewWorkflow:
the namespace, DNS record and any dedicated managed services go away
asynchronously. Prompts for confirmation unless --yes is given.

Select the preview with --pr <n>, or --branch <name> for a manual preview.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sel := readPreviewSelector(cmd, previewsTeardownSel)
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppPreviewsTeardown(cmd, cmd.Context(), client, slug, sel)
	},
}

func runAppPreviewsTeardown(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string, sel previewSelector) error {
	p, err := loadPreview(ctx, client, appSlug, sel)
	if err != nil {
		return err
	}
	if previewIsTornDown(p) {
		return fmt.Errorf("%s on app %q is already torn down", sel.label(), appSlug)
	}

	out := cmd.OutOrStdout()
	if !previewsTeardownYes {
		noPrompt, _ := cmd.Root().PersistentFlags().GetBool("no-prompt")
		if noPrompt {
			return fmt.Errorf("teardown needs confirmation: pass --yes (--no-prompt is set)")
		}
		fmt.Fprintf(out, "Tear down preview %s for %s (%s)? [y/N] ",
			previewPRLabel(p), appSlug, dashIfEmpty(p.Hostname))
		var answer string
		_, _ = fmt.Fscan(cmd.InOrStdin(), &answer)
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
	}

	tearCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var resp struct {
		Result struct {
			Ok     bool            `json:"ok"`
			Errors []mutationError `json:"errors"`
		} `json:"tearDownPreview"`
	}
	vars := map[string]interface{}{"input": map[string]interface{}{"id": p.ID}}
	if err := client.GraphQL(tearCtx, tearDownPreviewMutation, vars, &resp); err != nil {
		return fmt.Errorf("tearing down %s: %w", sel.label(), err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("teardown failed: %s", firstDeployError(resp.Result.Errors))
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, previewTeardownResult{
			ID:                p.ID,
			PRNumber:          p.PRNumber,
			Branch:            p.Branch,
			PreviousStatus:    p.Status,
			TeardownRequested: true,
		})
	}
	fmt.Fprintf(out, "Teardown requested for preview %s (%s).\n", previewPRLabel(p), dashIfEmpty(p.Hostname))
	fmt.Fprintln(out, "The namespace, DNS record and dedicated services are removed asynchronously.")
	return nil
}

// ---- astro app previews pin / unpin ------------------------------------------

var appPreviewsPinCmd = &cobra.Command{
	Use:   "pin [app] --pr <n>",
	Short: "Exempt a preview environment from garbage collection",
	Long: `Pins a preview environment so the garbage collector leaves it alone.

A pin covers both collection rules: the TTL never expires the preview, and
max-active eviction skips it, so a newer PR cannot claim its slot. That is
what makes this different from extending a TTL, which only defers the TTL
clock and leaves the preview evictable under pressure. The pin holds until
someone runs 'unpin' — nothing expires it — so it keeps costing money.

Pass --reason <text> to record why. The reason is stored with the pin and is
shown by 'previews show'; it is truncated to 512 characters. Re-pinning an
already-pinned preview replaces the recorded actor, timestamp and reason, so
what is on the record is always the justification currently in force — which
means re-pinning without --reason clears the previous one.

A torn-down preview cannot be pinned: its namespace is already gone, so the
pin would protect nothing.

Select the preview with --pr <n>, or --branch <name> for a manual preview.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sel := readPreviewSelector(cmd, previewsPinSel)
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppPreviewsSetPinned(cmd, cmd.Context(), client, slug, sel, true, previewsPinReason)
	},
}

var appPreviewsUnpinCmd = &cobra.Command{
	Use:   "unpin [app] --pr <n>",
	Short: "Return a preview environment to garbage collection",
	Long: `Removes a preview environment's pin, putting it back under the garbage
collector: its TTL applies again, and max-active eviction can reclaim its
slot for a newer PR.

Unpinning clears the whole record of the pin — who set it, when, and why —
so a justification that no longer applies never reads as the current one.
Unpinning a preview that is not pinned succeeds and changes nothing.

Unlike 'pin', this works on a torn-down preview, so stale state can always
be cleared.

There is no --reason: the platform ignores a reason on an unpin, and a flag
that silently does nothing is worse than no flag.

Select the preview with --pr <n>, or --branch <name> for a manual preview.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sel := readPreviewSelector(cmd, previewsUnpinSel)
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppPreviewsSetPinned(cmd, cmd.Context(), client, slug, sel, false, "")
	},
}

// runAppPreviewsSetPinned drives setPreviewPinned for both verbs.
//
// Deliberately no local torn-down guard on the pin path, unlike teardown: the
// resolver already refuses it with a PRECONDITION whose wording is the
// platform's, and a second local wording would drift from it the first time
// the rule changes. Unpin on a torn-down preview is legal server-side and
// stays legal here.
func runAppPreviewsSetPinned(
	cmd *cobra.Command,
	ctx context.Context,
	client *api.Client,
	appSlug string,
	sel previewSelector,
	pinned bool,
	reason string,
) error {
	p, err := loadPreview(ctx, client, appSlug, sel)
	if err != nil {
		return err
	}

	input := map[string]interface{}{"id": p.ID, "pinned": pinned}
	// Omit the variable rather than sending "": the input defaults reason to
	// null, and an empty string would read as "clear the recorded reason".
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		input["reason"] = trimmed
	}

	pinCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Result struct {
			Ok     bool                `json:"ok"`
			Errors []mutationError     `json:"errors"`
			Data   *previewEnvironment `json:"data"`
		} `json:"setPreviewPinned"`
	}
	if err := client.GraphQL(pinCtx, setPreviewPinnedMutation,
		map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("%s %s: %w", previewPinVerb(pinned), sel.label(), err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("%s failed: %s", previewPinVerb(pinned), firstDeployError(resp.Result.Errors))
	}
	if resp.Result.Data == nil {
		return fmt.Errorf("%s %s: the server returned no preview", previewPinVerb(pinned), sel.label())
	}

	updated := *resp.Result.Data
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, updated)
	}

	out := cmd.OutOrStdout()
	if pinned {
		fmt.Fprintf(out, "Pinned preview %s for %s (%s).\n",
			previewPRLabel(updated), appSlug, dashIfEmpty(updated.Hostname))
		// The reason comes off the returned row, not off the flag: the server
		// truncates it to 512 characters, and echoing the flag would report a
		// justification longer than the one on the record.
		if updated.PinReason != "" {
			fmt.Fprintf(out, "Reason: %s\n", updated.PinReason)
		}
		fmt.Fprintln(out, "It is exempt from TTL expiry and max-active eviction until it is unpinned.")
		return nil
	}
	// Unpinning something that was never pinned is a no-op success server-side.
	// The pre-mutation row is the only thing that distinguishes it from a real
	// unpin, and claiming a state change that never happened is worse than
	// saying nothing changed.
	if !p.IsPinned {
		fmt.Fprintf(out, "Preview %s for %s (%s) was not pinned; nothing changed.\n",
			previewPRLabel(updated), appSlug, dashIfEmpty(updated.Hostname))
		return nil
	}
	fmt.Fprintf(out, "Unpinned preview %s for %s (%s).\n",
		previewPRLabel(updated), appSlug, dashIfEmpty(updated.Hostname))
	fmt.Fprintf(out, "The garbage collector can reclaim it again; its TTL is %s.\n", shortTime(&updated.TTLUntil))
	return nil
}

// previewPinVerb names the operation for error text, so a failure reads as
// the verb the operator typed rather than as the shared setter.
func previewPinVerb(pinned bool) string {
	if pinned {
		return "pin"
	}
	return "unpin"
}

// ---- wiring -----------------------------------------------------------------

// firstArg returns the leading positional argument, or "" when none was given.
func firstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// readPreviewSelector fills in whether --pr was actually typed, which the
// flag value alone cannot tell apart from an explicit --pr 0.
func readPreviewSelector(cmd *cobra.Command, sel previewSelector) previewSelector {
	sel.prSet = cmd.Flags().Changed("pr")
	return sel
}

// addPreviewSelectorFlags registers the --pr / --branch pair the four
// single-preview verbs share.
func addPreviewSelectorFlags(c *cobra.Command, sel *previewSelector) {
	c.Flags().IntVar(&sel.pr, "pr", 0, "PR number of the preview")
	c.Flags().StringVar(&sel.branch, "branch", "", "Branch name (selects manual previews, which have no PR number)")
}

func init() {
	appPreviewsListCmd.Flags().BoolVar(&previewsListAll, "all", false, "Include torn-down previews")
	appPreviewsListCmd.Flags().IntVar(&previewsListLimit, "limit", 50, "Maximum number of previews to show")

	addPreviewSelectorFlags(appPreviewsShowCmd, &previewsShowSel)
	addPreviewSelectorFlags(appPreviewsLogsCmd, &previewsLogsSel)
	addPreviewSelectorFlags(appPreviewsOpenCmd, &previewsOpenSel)
	addPreviewSelectorFlags(appPreviewsTeardownCmd, &previewsTeardownSel)
	addPreviewSelectorFlags(appPreviewsPinCmd, &previewsPinSel)
	addPreviewSelectorFlags(appPreviewsUnpinCmd, &previewsUnpinSel)

	// logs — bind the same vars runAppLogs reads, as `app exec` does with the
	// exec target vars, so the shared implementation needs no changes.
	appPreviewsLogsCmd.Flags().StringVar(&previewsLogsWorkload, "workload", "", "Only show logs for this workload")
	appPreviewsLogsCmd.Flags().StringVar(&appLogsSince, "since", "1h", "How far back to read (Go duration, e.g. 30m, 1h, 24h)")
	appPreviewsLogsCmd.Flags().IntVar(&appLogsTail, "tail", 200, "Maximum number of recent lines to show")
	appPreviewsLogsCmd.Flags().BoolVarP(&appLogsFollow, "follow", "f", false, "Poll for and stream new log lines")
	appPreviewsLogsCmd.Flags().StringVar(&appLogsLevel, "level", "", "Filter by structured log level")
	appPreviewsLogsCmd.Flags().StringVar(&appLogsSearch, "search", "", "Filter to lines matching a substring")

	appPreviewsOpenCmd.Flags().BoolVar(&previewsOpenURLOnly, "url", false, "Print the URL instead of opening a browser")

	appPreviewsTeardownCmd.Flags().BoolVarP(&previewsTeardownYes, "yes", "y", false, "Skip the confirmation prompt")

	// unpin takes no --reason: the platform ignores a reason when clearing a
	// pin, so the flag would accept text and drop it.
	appPreviewsPinCmd.Flags().StringVar(&previewsPinReason, "reason", "",
		"Why the preview is pinned (recorded with the pin, truncated to 512 characters)")

	appPreviewsCmd.AddCommand(
		appPreviewsListCmd,
		appPreviewsShowCmd,
		appPreviewsLogsCmd,
		appPreviewsOpenCmd,
		appPreviewsTeardownCmd,
		appPreviewsPinCmd,
		appPreviewsUnpinCmd,
	)
}

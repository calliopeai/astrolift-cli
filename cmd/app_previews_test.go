package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// ---- fixtures ---------------------------------------------------------------

func resetPreviewsFlags() {
	previewsListAll = false
	previewsListLimit = 50
	previewsShowSel = previewSelector{}
	previewsLogsSel = previewSelector{}
	previewsLogsWorkload = ""
	previewsOpenSel = previewSelector{}
	previewsOpenURLOnly = false
	previewsTeardownSel = previewSelector{}
	previewsTeardownYes = false
}

func strptr(s string) *string { return &s }

func f64ptr(f float64) *float64 { return &f }

// prPreview is a healthy running PR-triggered preview.
func prPreview(pr int, branch string) previewEnvironment {
	n := strconv.Itoa(pr)
	return previewEnvironment{
		ID:                 "guid-pr-" + branch,
		RegisteredAppSlug:  "web",
		PRNumber:           pr,
		Branch:             branch,
		CommitSha:          "deadbeef",
		Status:             "running",
		Hostname:           "pr-" + n + ".web.acme.example.com",
		Namespace:          "acme-web-pr-" + n,
		LastDeployedAt:     strptr("2026-08-14T10:00:00.123456+00:00"),
		TTLUntil:           "2026-08-21T10:00:00+00:00",
		SourceURL:          "https://github.com/acme/web",
		PRURL:              "https://github.com/acme/web/pull/" + n,
		AggregateResources: previewAggregateResources{CPUCores: 0.5, MemoryBytes: 536870912, PodCount: 1},
	}
}

// previewRow renders a preview as the JSON map the GraphQL server returns.
func previewRow(p previewEnvironment) map[string]interface{} {
	raw, err := json.Marshal(p)
	if err != nil {
		panic(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	return m
}

func previewPage(items []previewEnvironment, nextCursor string) map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(items))
	for _, p := range items {
		rows = append(rows, previewRow(p))
	}
	page := map[string]interface{}{"items": rows, "totalCount": len(rows)}
	if nextCursor == "" {
		page["nextCursor"] = nil
	} else {
		page["nextCursor"] = nextCursor
	}
	return map[string]interface{}{"astroliftPreviewEnvironmentsPage": page}
}

// previewsServer routes by operation text so one command run can drive the
// previews page, the environments query and the log query in sequence. Every
// request is appended to captured in order.
func previewsServer(t *testing.T, route func(req gqlRequest) map[string]interface{}, captured *[]gqlRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req gqlRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		if captured != nil {
			*captured = append(*captured, req)
		}
		data := route(req)
		if data == nil {
			t.Errorf("unrouted operation:\n%s", req.Query)
			data = map[string]interface{}{}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}))
}

// ---- previewSelector --------------------------------------------------------

func TestPreviewSelectorValidate(t *testing.T) {
	cases := []struct {
		name    string
		sel     previewSelector
		wantErr string
	}{
		{"pr only", previewSelector{pr: 12, prSet: true}, ""},
		{"branch only", previewSelector{branch: "feat/x"}, ""},
		{"neither", previewSelector{}, "one of --pr"},
		{"both", previewSelector{pr: 12, prSet: true, branch: "feat/x"}, "mutually exclusive"},
		// An explicit --pr 0 must not be read as "unset": manual previews all
		// report prNumber 0, so it would otherwise match every one of them.
		{"explicit zero", previewSelector{pr: 0, prSet: true}, "positive PR number"},
		{"negative", previewSelector{pr: -3, prSet: true}, "positive PR number"},
		// A blank --branch is the same as not passing it.
		{"blank branch", previewSelector{branch: "   "}, "one of --pr"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.sel.validate()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("validate() = %v, want error containing %q", err, c.wantErr)
			}
		})
	}
}

// prNumber 0 is the NULL column of a manual preview. matches must never
// treat it as a real PR: teardown is destructive, and a selector that
// matched every manual preview at once would tear one down by accident if a
// caller ever reached matches without validating first.
func TestPreviewSelectorMatchesNeverMatchesPRZero(t *testing.T) {
	manual := previewEnvironment{IsManual: true, PRNumber: 0, Branch: "spike"}
	if (previewSelector{pr: 0, prSet: true}).matches(manual) {
		t.Error("pr 0 must not match a manual preview")
	}
	if (previewSelector{}).matches(manual) {
		t.Error("an empty selector must not match anything")
	}
	if !(previewSelector{pr: 12, prSet: true}).matches(prPreview(12, "feat/b")) {
		t.Error("a real PR number must match its preview")
	}
	// Branch wins when set; the pr field is then irrelevant.
	if !(previewSelector{branch: "spike"}).matches(manual) {
		t.Error("branch selector must match on branch")
	}
}

// ---- resolvePreview ---------------------------------------------------------

func TestResolvePreviewByPRNumber(t *testing.T) {
	rows := []previewEnvironment{prPreview(7, "feat/a"), prPreview(12, "feat/b"), prPreview(31, "feat/c")}
	got, err := resolvePreview(rows, previewSelector{pr: 12, prSet: true})
	if err != nil {
		t.Fatalf("resolvePreview: %v", err)
	}
	if got.Branch != "feat/b" {
		t.Errorf("resolved the wrong preview: %+v", got)
	}
}

// A manual preview reports prNumber 0. It must be unreachable through --pr —
// otherwise `--pr 0` (or a mis-parsed flag) would silently select one.
func TestResolvePreviewByPRSkipsManualPreviews(t *testing.T) {
	manual := previewEnvironment{ID: "guid-manual", IsManual: true, PRNumber: 0, Branch: "spike", Status: "running"}
	rows := []previewEnvironment{manual, prPreview(12, "feat/b")}

	if _, err := resolvePreview(rows, previewSelector{pr: 0, prSet: true}); err == nil {
		t.Fatal("--pr 0 must be rejected, not matched against manual previews")
	}
	got, err := resolvePreview(rows, previewSelector{branch: "spike"})
	if err != nil {
		t.Fatalf("resolving a manual preview by branch: %v", err)
	}
	if got.ID != "guid-manual" {
		t.Errorf("branch selector resolved %q, want guid-manual", got.ID)
	}
}

// The unique-active constraint is per (app, pr_number) for PR previews and per
// (app, branch) for manual ones, so a branch can carry both. Picking one
// silently would tail or tear down the wrong environment.
func TestResolvePreviewByBranchRejectsAmbiguity(t *testing.T) {
	shared := "feat/shared"
	rows := []previewEnvironment{
		prPreview(12, shared),
		{ID: "guid-manual", IsManual: true, PRNumber: 0, Branch: shared, Status: "running"},
	}
	_, err := resolvePreview(rows, previewSelector{branch: shared})
	if err == nil {
		t.Fatal("an ambiguous branch must error, not pick one")
	}
	for _, want := range []string{"matches 2", "#12", "manual", "--pr"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ambiguity error missing %q: %v", want, err)
		}
	}
}

func TestResolvePreviewNotFoundNamesTheCandidates(t *testing.T) {
	rows := []previewEnvironment{prPreview(7, "feat/a"), prPreview(12, "feat/b")}
	_, err := resolvePreview(rows, previewSelector{pr: 99, prSet: true})
	if err == nil {
		t.Fatal("expected an error for an unknown PR")
	}
	// A bare "not found" makes a typo'd --pr a guessing game.
	for _, want := range []string{"PR #99", "#7", "#12"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("not-found error missing %q: %v", want, err)
		}
	}
}

func TestResolvePreviewOnAppWithNoPreviews(t *testing.T) {
	_, err := resolvePreview(nil, previewSelector{pr: 3, prSet: true})
	if err == nil || !strings.Contains(err.Error(), "no preview environments") {
		t.Fatalf("err = %v, want it to say the app has no previews", err)
	}
}

// ---- torn-down classification -----------------------------------------------

func TestPreviewIsTornDown(t *testing.T) {
	cases := []struct {
		name string
		p    previewEnvironment
		want bool
	}{
		{"running", previewEnvironment{Status: "running"}, false},
		{"building", previewEnvironment{Status: "building"}, false},
		{"failed is not torn down", previewEnvironment{Status: "failed"}, false},
		{"status", previewEnvironment{Status: "torn_down"}, true},
		{"status uppercase", previewEnvironment{Status: "TORN_DOWN"}, true},
		// The workflow stamps torn_down_at before the status settles.
		{"timestamp", previewEnvironment{Status: "running", TornDownAt: strptr("2026-08-01T00:00:00Z")}, true},
		{"empty timestamp", previewEnvironment{Status: "running", TornDownAt: strptr("")}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := previewIsTornDown(c.p); got != c.want {
				t.Errorf("previewIsTornDown(%+v) = %v, want %v", c.p, got, c.want)
			}
		})
	}
}

// ---- formatting helpers ------------------------------------------------------

func TestPreviewPRLabel(t *testing.T) {
	if got := previewPRLabel(prPreview(12, "b")); got != "#12" {
		t.Errorf("PR label = %q, want #12", got)
	}
	// prNumber 0 is a NULL column, not pull request zero.
	if got := previewPRLabel(previewEnvironment{IsManual: true}); got != "manual" {
		t.Errorf("manual label = %q, want manual", got)
	}
}

func TestPreviewURL(t *testing.T) {
	cases := []struct {
		name, hostname, want string
	}{
		{"bare hostname", "pr-12.web.acme.example.com", "https://pr-12.web.acme.example.com"},
		{"not assigned yet", "", ""},
		{"whitespace only", "   ", ""},
		{"already absolute", "https://pr-12.web.acme.example.com", "https://pr-12.web.acme.example.com"},
		{"plain http kept", "http://localhost:8080", "http://localhost:8080"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := previewURL(previewEnvironment{Hostname: c.hostname}); got != c.want {
				t.Errorf("previewURL(%q) = %q, want %q", c.hostname, got, c.want)
			}
		})
	}
}

func TestHostFromURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://pr-12.web.acme.example.com", "pr-12.web.acme.example.com"},
		{"https://PR-12.Web.Acme.Example.com/", "pr-12.web.acme.example.com"},
		{"http://localhost:8080/path", "localhost"},
		{"pr-12.web.acme.example.com", "pr-12.web.acme.example.com"},
		{"", ""},
	}
	for _, c := range cases {
		if got := hostFromURL(c.in); got != c.want {
			t.Errorf("hostFromURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPreviewMemoryLabel(t *testing.T) {
	cases := []struct {
		bytes float64
		want  string
	}{
		{0, "0"},
		{512, "512 B"},
		{536870912, "512 MiB"},
		{1610612736, "1.5 GiB"},
	}
	for _, c := range cases {
		if got := previewMemoryLabel(c.bytes); got != c.want {
			t.Errorf("previewMemoryLabel(%v) = %q, want %q", c.bytes, got, c.want)
		}
	}
}

func TestPreviewResourcesLabel(t *testing.T) {
	got := previewResourcesLabel(previewAggregateResources{CPUCores: 0.5, MemoryBytes: 536870912, PodCount: 2})
	if got != "2 pod(s), 0.50 vCPU, 512 MiB" {
		t.Errorf("resources label = %q", got)
	}
}

// A null cost means the cluster has no pricing wired up. Rendering it as
// $0.00 would read as "this preview is free".
func TestPreviewCostLabelDistinguishesNullFromZero(t *testing.T) {
	if got := previewCostLabel(nil); got != "-" {
		t.Errorf("nil cost = %q, want -", got)
	}
	if got := previewCostLabel(f64ptr(0)); got != "$0.00/day" {
		t.Errorf("zero cost = %q, want $0.00/day", got)
	}
	if got := previewCostLabel(f64ptr(1.234)); got != "$1.23/day" {
		t.Errorf("cost = %q, want $1.23/day", got)
	}
}

// ---- previewEnvironmentName --------------------------------------------------

// Both backend creation paths write AppEnvironment.url as
// "https://" + preview.hostname, which is the only exact join key available.
func TestPreviewEnvironmentNameMatchesOnHostname(t *testing.T) {
	envs := []previewAppEnvironment{
		{Name: "production", URL: "https://web.acme.example.com"},
		{Name: "preview-pr-12", URL: "https://PR-12.web.acme.example.com/"},
	}
	got := previewEnvironmentName(envs, prPreview(12, "feat/b"))
	if got != "preview-pr-12" {
		t.Errorf("environment = %q, want preview-pr-12", got)
	}
}

// A manual preview's environment is named after its branch, not its PR, so
// the host match is what reaches it at all.
func TestPreviewEnvironmentNameResolvesManualPreview(t *testing.T) {
	manual := previewEnvironment{
		IsManual: true, PRNumber: 0, Branch: "spike/thing",
		Hostname: "preview-spike-thing.web.acme.example.com",
	}
	envs := []previewAppEnvironment{
		{Name: "production", URL: "https://web.acme.example.com"},
		{Name: "preview-spike-thing", URL: "https://preview-spike-thing.web.acme.example.com"},
	}
	if got := previewEnvironmentName(envs, manual); got != "preview-spike-thing" {
		t.Errorf("environment = %q, want preview-spike-thing", got)
	}
}

func TestPreviewEnvironmentNameFallsBackToConvention(t *testing.T) {
	// The url is not the preview hostname (an install without a managed
	// zone rewrites it), but the conventional environment exists.
	envs := []previewAppEnvironment{{Name: "preview-pr-12", URL: "https://something-else.example.com"}}
	if got := previewEnvironmentName(envs, prPreview(12, "feat/b")); got != "preview-pr-12" {
		t.Errorf("environment = %q, want the conventional preview-pr-12", got)
	}
}

// Guessing "preview-pr-N" when the platform has no such environment turns a
// resolution failure into a silently empty log tail.
func TestPreviewEnvironmentNameDoesNotInventAName(t *testing.T) {
	envs := []previewAppEnvironment{{Name: "production", URL: "https://web.acme.example.com"}}
	if got := previewEnvironmentName(envs, prPreview(12, "feat/b")); got != "" {
		t.Errorf("environment = %q, want empty for an unresolvable preview", got)
	}
}

// ---- astro app previews list -------------------------------------------------

func TestAppPreviewsListRendersRows(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	srv := gqlServer(t, previewPage([]previewEnvironment{prPreview(12, "feat/b")}, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppPreviewsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPreviewsList: %v", err)
	}

	got := out.String()
	for _, want := range []string{"#12", "feat/b", "running", "pr-12.web.acme.example.com", "2026-08-14T10:00:00", "1 preview(s) shown."} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
}

func TestAppPreviewsListHidesTornDownByDefault(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	gone := prPreview(7, "feat/a")
	gone.Status = "torn_down"
	gone.TornDownAt = strptr("2026-08-10T00:00:00+00:00")
	rows := []previewEnvironment{gone, prPreview(12, "feat/b")}

	srv := gqlServer(t, previewPage(rows, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppPreviewsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPreviewsList: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "feat/a") {
		t.Errorf("torn-down preview should be hidden by default:\n%s", got)
	}
	if !strings.Contains(got, "1 preview(s) shown.") {
		t.Errorf("count should reflect the filter:\n%s", got)
	}

	previewsListAll = true
	cmd2, out2 := appTestCmd()
	if err := runAppPreviewsList(cmd2, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPreviewsList --all: %v", err)
	}
	if !strings.Contains(out2.String(), "feat/a") {
		t.Errorf("--all should include torn-down previews:\n%s", out2.String())
	}
}

// An app with no previews is the common first-run case; the message has to
// distinguish "none active" from "none at all".
func TestAppPreviewsListEmptyMentionsAll(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	srv := gqlServer(t, previewPage(nil, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppPreviewsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPreviewsList: %v", err)
	}
	got := out.String()
	for _, want := range []string{`"web"`, "--all"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty message should mention %s, got: %s", want, got)
		}
	}
}

// The resolver pages at 50; an app past that must not lose its older rows.
func TestAppPreviewsListWalksCursorPages(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	var captured []gqlRequest
	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		if req.Variables["after"] == nil {
			return previewPage([]previewEnvironment{prPreview(12, "feat/b")}, "cursor-2")
		}
		return previewPage([]previewEnvironment{prPreview(7, "feat/a")}, "")
	}, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppPreviewsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPreviewsList: %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("expected 2 page requests, got %d", len(captured))
	}
	if got := captured[1].Variables["after"]; got != "cursor-2" {
		t.Errorf("second page after = %v, want cursor-2", got)
	}
	got := out.String()
	if !strings.Contains(got, "feat/a") || !strings.Contains(got, "feat/b") {
		t.Errorf("both pages should be rendered:\n%s", got)
	}
	if !strings.Contains(got, "2 preview(s) shown.") {
		t.Errorf("count should span pages:\n%s", got)
	}
}

func TestAppPreviewsListLimitTruncates(t *testing.T) {
	resetPreviewsFlags()
	previewsListLimit = 1
	defer resetPreviewsFlags()

	rows := []previewEnvironment{prPreview(12, "feat/b"), prPreview(7, "feat/a")}
	srv := gqlServer(t, previewPage(rows, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppPreviewsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPreviewsList: %v", err)
	}
	if !strings.Contains(out.String(), "1 preview(s) shown.") {
		t.Errorf("--limit should truncate:\n%s", out.String())
	}
}

// The IDE integration this unblocks reads the JSON, so the field set is a
// contract: the URL is derived from hostname, and cost/resources ride along.
func TestAppPreviewsListJSONShape(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	p := prPreview(12, "feat/b")
	p.EstimatedDailyCostUSD = f64ptr(1.25)
	srv := gqlServer(t, previewPage([]previewEnvironment{p}, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppPreviewsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPreviewsList --json: %v", err)
	}

	var rows []previewEnvironment
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decoding JSON: %v\n%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	got := rows[0]
	if got.PRNumber != 12 || got.Branch != "feat/b" || got.Hostname == "" {
		t.Errorf("identity fields lost: %+v", got)
	}
	if got.AggregateResources.PodCount != 1 || got.AggregateResources.CPUCores != 0.5 {
		t.Errorf("aggregateResources lost: %+v", got.AggregateResources)
	}
	if got.EstimatedDailyCostUSD == nil || *got.EstimatedDailyCostUSD != 1.25 {
		t.Errorf("estimatedDailyCostUsd lost: %v", got.EstimatedDailyCostUSD)
	}
	if previewURL(got) != "https://pr-12.web.acme.example.com" {
		t.Errorf("URL not derivable from the JSON row: %q", previewURL(got))
	}
}

// ---- astro app previews show -------------------------------------------------

func TestAppPreviewsShowRendersDetail(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	p := prPreview(12, "feat/b")
	p.EstimatedDailyCostUSD = f64ptr(1.25)
	srv := gqlServer(t, previewPage([]previewEnvironment{prPreview(7, "feat/a"), p}, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	err := runAppPreviewsShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err != nil {
		t.Fatalf("runAppPreviewsShow: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"https://pr-12.web.acme.example.com",
		"feat/b",
		"acme-web-pr-12",
		"deadbeef",
		"https://github.com/acme/web/pull/12",
		"1 pod(s), 0.50 vCPU, 512 MiB",
		"$1.25/day",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("detail missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "feat/a") {
		t.Errorf("show must render only the selected preview:\n%s", got)
	}
}

// A preview is created in status=building with no hostname; the detail view
// has to say so rather than printing "URL: https://".
func TestAppPreviewsShowWithoutHostname(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	building := prPreview(12, "feat/b")
	building.Status = "building"
	building.Hostname = ""
	building.LastDeployedAt = nil
	srv := gqlServer(t, previewPage([]previewEnvironment{building}, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	err := runAppPreviewsShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err != nil {
		t.Fatalf("runAppPreviewsShow: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "no hostname assigned yet") {
		t.Errorf("expected an explanation for the missing URL:\n%s", got)
	}
	if strings.Contains(got, "https://\n") {
		t.Errorf("rendered a bare scheme as the URL:\n%s", got)
	}
}

func TestAppPreviewsShowUnknownPRErrors(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	srv := gqlServer(t, previewPage([]previewEnvironment{prPreview(12, "feat/b")}, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	err := runAppPreviewsShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 99, prSet: true})
	if err == nil {
		t.Fatal("expected an error for an unknown PR")
	}
	if !strings.Contains(err.Error(), "PR #99") || !strings.Contains(err.Error(), "#12") {
		t.Errorf("error should name the request and the candidates: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("nothing should be printed on the error path: %s", out.String())
	}
}

func TestAppPreviewsShowRejectsMissingSelector(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	// No server call should happen: the selector is invalid before transport.
	srv := previewsServer(t, func(gqlRequest) map[string]interface{} {
		t.Error("no GraphQL call should be made for an invalid selector")
		return map[string]interface{}{}
	}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppPreviewsShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", previewSelector{})
	if err == nil || !strings.Contains(err.Error(), "one of --pr") {
		t.Fatalf("err = %v, want a selector-required error", err)
	}
}

func TestAppPreviewsShowJSONEmitsTheRow(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	srv := gqlServer(t, previewPage([]previewEnvironment{prPreview(12, "feat/b")}, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	err := runAppPreviewsShow(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err != nil {
		t.Fatalf("runAppPreviewsShow --json: %v", err)
	}
	var p previewEnvironment
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatalf("decoding JSON: %v\n%s", err, out.String())
	}
	if p.PRNumber != 12 || p.Namespace != "acme-web-pr-12" {
		t.Errorf("unexpected row: %+v", p)
	}
}

// ---- astro app previews open -------------------------------------------------

func TestAppPreviewsOpenPrintsURL(t *testing.T) {
	resetPreviewsFlags()
	previewsOpenURLOnly = true // never shell out to a browser in tests
	defer resetPreviewsFlags()

	srv := gqlServer(t, previewPage([]previewEnvironment{prPreview(12, "feat/b")}, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	err := runAppPreviewsOpen(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err != nil {
		t.Fatalf("runAppPreviewsOpen: %v", err)
	}
	if strings.TrimSpace(out.String()) != "https://pr-12.web.acme.example.com" {
		t.Errorf("--url should print exactly the URL, got %q", out.String())
	}
}

func TestAppPreviewsOpenJSONCarriesURL(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	srv := gqlServer(t, previewPage([]previewEnvironment{prPreview(12, "feat/b")}, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	err := runAppPreviewsOpen(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err != nil {
		t.Fatalf("runAppPreviewsOpen --json: %v", err)
	}
	var info previewOpenInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("decoding JSON: %v\n%s", err, out.String())
	}
	if info.URL != "https://pr-12.web.acme.example.com" || info.PRNumber != 12 || info.Status != "running" {
		t.Errorf("unexpected open info: %+v", info)
	}
}

// Opening a preview that has not been assigned a hostname would hand the
// browser "https://".
func TestAppPreviewsOpenWithoutHostnameErrors(t *testing.T) {
	resetPreviewsFlags()
	previewsOpenURLOnly = true
	defer resetPreviewsFlags()

	building := prPreview(12, "feat/b")
	building.Status = "building"
	building.Hostname = ""
	srv := gqlServer(t, previewPage([]previewEnvironment{building}, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	err := runAppPreviewsOpen(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err == nil || !strings.Contains(err.Error(), "no hostname") {
		t.Fatalf("err = %v, want a no-hostname error", err)
	}
	if !strings.Contains(err.Error(), "building") {
		t.Errorf("error should carry the status that explains it: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("nothing should be printed: %s", out.String())
	}
}

// ---- astro app previews teardown ---------------------------------------------

// tearDownPreview keys on the preview's GUID. Sending the PR number, or the
// app's guid, would tear down nothing (or something else).
func TestAppPreviewsTeardownSendsPreviewGUID(t *testing.T) {
	resetPreviewsFlags()
	previewsTeardownYes = true
	defer resetPreviewsFlags()

	var captured []gqlRequest
	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "tearDownPreview") {
			return map[string]interface{}{"tearDownPreview": map[string]interface{}{
				"ok": true, "errors": []interface{}{},
			}}
		}
		return previewPage([]previewEnvironment{prPreview(7, "feat/a"), prPreview(12, "feat/b")}, "")
	}, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	err := runAppPreviewsTeardown(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err != nil {
		t.Fatalf("runAppPreviewsTeardown: %v", err)
	}

	if len(captured) != 2 {
		t.Fatalf("expected a list then a mutation, got %d requests", len(captured))
	}
	input, ok := captured[1].Variables["input"].(map[string]interface{})
	if !ok {
		t.Fatalf("mutation input missing: %#v", captured[1].Variables)
	}
	if input["id"] != "guid-pr-feat/b" {
		t.Errorf("teardown id = %v, want the selected preview's guid", input["id"])
	}
	if !strings.Contains(out.String(), "Teardown requested") {
		t.Errorf("expected a confirmation line:\n%s", out.String())
	}
}

func TestAppPreviewsTeardownRefusesAlreadyTornDown(t *testing.T) {
	resetPreviewsFlags()
	previewsTeardownYes = true
	defer resetPreviewsFlags()

	gone := prPreview(12, "feat/b")
	gone.Status = "torn_down"
	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "tearDownPreview") {
			t.Error("no teardown mutation should be sent for an already torn-down preview")
			return map[string]interface{}{"tearDownPreview": map[string]interface{}{"ok": true, "errors": []interface{}{}}}
		}
		return previewPage([]previewEnvironment{gone}, "")
	}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppPreviewsTeardown(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err == nil || !strings.Contains(err.Error(), "already torn down") {
		t.Fatalf("err = %v, want an already-torn-down error", err)
	}
}

func TestAppPreviewsTeardownSurfacesMutationError(t *testing.T) {
	resetPreviewsFlags()
	previewsTeardownYes = true
	defer resetPreviewsFlags()

	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "tearDownPreview") {
			return map[string]interface{}{"tearDownPreview": map[string]interface{}{
				"ok": false,
				"errors": []map[string]interface{}{
					{"code": "PRECONDITION", "message": "no deployment exists for this app yet", "field": ""},
				},
			}}
		}
		return previewPage([]previewEnvironment{prPreview(12, "feat/b")}, "")
	}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppPreviewsTeardown(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err == nil || !strings.Contains(err.Error(), "no deployment exists") {
		t.Fatalf("err = %v, want the server's message surfaced verbatim", err)
	}
}

// A "n" at the prompt must not send the mutation.
func TestAppPreviewsTeardownAbortsOnDecline(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "tearDownPreview") {
			t.Error("declining the prompt must not send the mutation")
			return map[string]interface{}{"tearDownPreview": map[string]interface{}{"ok": true, "errors": []interface{}{}}}
		}
		return previewPage([]previewEnvironment{prPreview(12, "feat/b")}, "")
	}, nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	cmd.SetIn(strings.NewReader("n\n"))
	err := runAppPreviewsTeardown(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err != nil {
		t.Fatalf("declining should not be an error: %v", err)
	}
	if !strings.Contains(out.String(), "Aborted.") {
		t.Errorf("expected an abort line:\n%s", out.String())
	}
}

// Under --no-prompt there is no one to answer, so the command must fail
// loudly instead of blocking a CI job on stdin.
func TestAppPreviewsTeardownRequiresYesUnderNoPrompt(t *testing.T) {
	resetPreviewsFlags()
	defer resetPreviewsFlags()

	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "tearDownPreview") {
			t.Error("no mutation should be sent without confirmation")
			return map[string]interface{}{"tearDownPreview": map[string]interface{}{"ok": true, "errors": []interface{}{}}}
		}
		return previewPage([]previewEnvironment{prPreview(12, "feat/b")}, "")
	}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	_ = cmd.Root().PersistentFlags().Set("no-prompt", "true")
	err := runAppPreviewsTeardown(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want a --yes requirement", err)
	}
}

// ---- astro app previews logs -------------------------------------------------

// previewLogsServer answers the three operations `previews logs` drives.
func previewLogsServer(t *testing.T, previews []previewEnvironment, envs []map[string]interface{}, captured *[]gqlRequest) *httptest.Server {
	t.Helper()
	return previewsServer(t, func(req gqlRequest) map[string]interface{} {
		switch {
		case strings.Contains(req.Query, "astroliftPreviewEnvironmentsPage"):
			return previewPage(previews, "")
		case strings.Contains(req.Query, "astroliftEnvironments"):
			return map[string]interface{}{"astroliftEnvironments": envs}
		case strings.Contains(req.Query, "astroliftAppLogs"):
			return map[string]interface{}{"astroliftAppLogs": map[string]interface{}{
				"items": []map[string]interface{}{{
					"podName": "web-1", "container": "web",
					"timestamp": "2026-08-14T10:00:00Z", "message": "preview line",
					"level": "info", "stream": "stdout",
				}},
				"nextCursor": nil, "reachedRetention": false,
				"historicalAvailable": true, "totalCount": 1,
			}}
		}
		return nil
	}, captured)
}

// The whole point of the verb: the log query must be scoped to the preview's
// synthesized environment, not the app's default one.
func TestAppPreviewsLogsTargetsThePreviewEnvironment(t *testing.T) {
	resetPreviewsFlags()
	appLogsEnv, appLogsSince, appLogsTail, appLogsFollow = "", "1h", 200, false
	defer func() { resetPreviewsFlags(); appLogsEnv = "" }()

	var captured []gqlRequest
	srv := previewLogsServer(t,
		[]previewEnvironment{prPreview(12, "feat/b")},
		[]map[string]interface{}{
			{"name": "production", "url": "https://web.acme.example.com"},
			{"name": "preview-pr-12", "url": "https://pr-12.web.acme.example.com"},
		}, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	err := runAppPreviewsLogs(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err != nil {
		t.Fatalf("runAppPreviewsLogs: %v", err)
	}

	var logReq *gqlRequest
	for i := range captured {
		if strings.Contains(captured[i].Query, "astroliftAppLogs") {
			logReq = &captured[i]
		}
	}
	if logReq == nil {
		t.Fatal("no log query was sent")
	}
	if got := logReq.Variables["environmentName"]; got != "preview-pr-12" {
		t.Errorf("environmentName = %v, want preview-pr-12", got)
	}
	if got := logReq.Variables["appSlug"]; got != "web" {
		t.Errorf("appSlug = %v, want web", got)
	}
	if !strings.Contains(out.String(), "preview line") {
		t.Errorf("log lines should reach stdout:\n%s", out.String())
	}
}

func TestAppPreviewsLogsPassesWorkloadThrough(t *testing.T) {
	resetPreviewsFlags()
	previewsLogsWorkload = "worker"
	appLogsEnv, appLogsSince, appLogsTail, appLogsFollow = "", "1h", 200, false
	defer func() { resetPreviewsFlags(); appLogsEnv = "" }()

	var captured []gqlRequest
	srv := previewLogsServer(t,
		[]previewEnvironment{prPreview(12, "feat/b")},
		[]map[string]interface{}{{"name": "preview-pr-12", "url": "https://pr-12.web.acme.example.com"}},
		&captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	if err := runAppPreviewsLogs(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true}); err != nil {
		t.Fatalf("runAppPreviewsLogs: %v", err)
	}
	for i := range captured {
		if strings.Contains(captured[i].Query, "astroliftAppLogs") {
			if got := captured[i].Variables["workloadSlug"]; got != "worker" {
				t.Errorf("workloadSlug = %v, want worker", got)
			}
			return
		}
	}
	t.Fatal("no log query was sent")
}

// Without an environment match the log query would silently read the app's
// default environment — production, in the worst case.
func TestAppPreviewsLogsErrorsWhenEnvironmentUnresolvable(t *testing.T) {
	resetPreviewsFlags()
	appLogsEnv, appLogsSince, appLogsTail, appLogsFollow = "", "1h", 200, false
	defer func() { resetPreviewsFlags(); appLogsEnv = "" }()

	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		switch {
		case strings.Contains(req.Query, "astroliftPreviewEnvironmentsPage"):
			return previewPage([]previewEnvironment{prPreview(12, "feat/b")}, "")
		case strings.Contains(req.Query, "astroliftEnvironments"):
			return map[string]interface{}{"astroliftEnvironments": []map[string]interface{}{
				{"name": "production", "url": "https://web.acme.example.com"},
			}}
		case strings.Contains(req.Query, "astroliftAppLogs"):
			t.Error("logs must not fall back to the app's default environment")
			return map[string]interface{}{"astroliftAppLogs": map[string]interface{}{"items": []interface{}{}}}
		}
		return nil
	}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppPreviewsLogs(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err == nil || !strings.Contains(err.Error(), "could not resolve the environment") {
		t.Fatalf("err = %v, want an unresolved-environment error", err)
	}
}

func TestAppPreviewsLogsRefusesTornDownPreview(t *testing.T) {
	resetPreviewsFlags()
	appLogsEnv, appLogsSince, appLogsTail, appLogsFollow = "", "1h", 200, false
	defer func() { resetPreviewsFlags(); appLogsEnv = "" }()

	gone := prPreview(12, "feat/b")
	gone.Status = "torn_down"
	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "astroliftPreviewEnvironmentsPage") {
			return previewPage([]previewEnvironment{gone}, "")
		}
		t.Errorf("no further calls expected, got:\n%s", req.Query)
		return map[string]interface{}{}
	}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppPreviewsLogs(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err == nil || !strings.Contains(err.Error(), "torn down") {
		t.Fatalf("err = %v, want a torn-down error", err)
	}
}

// ---- command wiring ----------------------------------------------------------

// The group was a stub that registered nothing; regressing to that is the
// exact failure this issue exists for.
func TestAppPreviewsGroupRegistersVerbs(t *testing.T) {
	want := map[string]bool{"list": false, "show": false, "logs": false, "open": false, "teardown": false}
	for _, sub := range appPreviewsCmd.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("astro app previews %s is not registered", name)
		}
	}
	if strings.Contains(appPreviewsCmd.Long, "Subcommands typically include") {
		t.Error("the placeholder sub-resource help text is still in place")
	}
	// pin/unpin have no backing mutation; the help has to say why rather than
	// leaving a caller to wonder.
	if !strings.Contains(appPreviewsCmd.Long, "astrolift-app#1399") {
		t.Error("the pin/unpin gap should cite its tracking issue")
	}
	for _, sub := range appPreviewsCmd.Commands() {
		if sub.Name() == "pin" || sub.Name() == "unpin" {
			t.Errorf("%s must not ship without a backing mutation", sub.Name())
		}
	}
}

func TestAppPreviewsTeardownJSONReportsTheRequest(t *testing.T) {
	resetPreviewsFlags()
	previewsTeardownYes = true
	defer resetPreviewsFlags()

	srv := previewsServer(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "tearDownPreview") {
			return map[string]interface{}{"tearDownPreview": map[string]interface{}{
				"ok": true, "errors": []interface{}{},
			}}
		}
		return previewPage([]previewEnvironment{prPreview(12, "feat/b")}, "")
	}, nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	err := runAppPreviewsTeardown(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web",
		previewSelector{pr: 12, prSet: true})
	if err != nil {
		t.Fatalf("runAppPreviewsTeardown --json: %v", err)
	}
	var result previewTeardownResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("decoding JSON: %v\n%s", err, out.String())
	}
	if result.ID != "guid-pr-feat/b" || result.PRNumber != 12 || !result.TeardownRequested {
		t.Errorf("unexpected teardown result: %+v", result)
	}
	// The teardown is asynchronous, so reporting the pre-teardown status is
	// the only honest thing to say about state.
	if result.PreviousStatus != "running" {
		t.Errorf("previousStatus = %q, want running", result.PreviousStatus)
	}
}

func TestAppPreviewsSelectorFlagsOnEveryVerb(t *testing.T) {
	for _, name := range []string{"show", "logs", "open", "teardown"} {
		var found bool
		for _, sub := range appPreviewsCmd.Commands() {
			if sub.Name() != name {
				continue
			}
			found = true
			for _, flag := range []string{"pr", "branch"} {
				if sub.Flags().Lookup(flag) == nil {
					t.Errorf("astro app previews %s is missing --%s", name, flag)
				}
			}
		}
		if !found {
			t.Errorf("astro app previews %s is not registered", name)
		}
	}
}

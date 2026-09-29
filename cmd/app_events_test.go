package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// ---- fixtures ---------------------------------------------------------------

func resetAppEventsFlags() {
	appEventsLimit = 100
	appEventsType = ""
	appEventsSeverity = ""
	appEventsSearch = ""
}

func eventsPageData(items []appEvent, nextCursor, reason string) map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(items))
	for _, e := range items {
		raw, err := json.Marshal(e)
		if err != nil {
			panic(err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			panic(err)
		}
		rows = append(rows, m)
	}
	page := map[string]interface{}{"items": rows, "reason": reason}
	if nextCursor == "" {
		page["nextCursor"] = nil
	} else {
		page["nextCursor"] = nextCursor
	}
	return map[string]interface{}{"astroliftEventsPage": page}
}

// ---- list --------------------------------------------------------------------

func TestAppEventsListSendsAppSlugAndDefaultLimit(t *testing.T) {
	resetAppEventsFlags()
	defer resetAppEventsFlags()

	var captured gqlRequest
	srv := gqlServer(t, eventsPageData(nil, "", "NO_DATA_YET"), &captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	if err := runAppEventsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppEventsList: %v", err)
	}
	if captured.Variables["appSlug"] != "web" {
		t.Errorf("appSlug = %v, want web", captured.Variables["appSlug"])
	}
	if captured.Variables["limit"] != float64(100) {
		t.Errorf("limit = %v, want 100", captured.Variables["limit"])
	}
	for _, key := range []string{"eventType", "severity", "search"} {
		if _, ok := captured.Variables[key]; ok {
			t.Errorf("%s should be omitted when unset, got %v", key, captured.Variables)
		}
	}
}

func TestAppEventsListSendsFilters(t *testing.T) {
	resetAppEventsFlags()
	defer resetAppEventsFlags()
	appEventsType = "deploy.completed"
	appEventsSeverity = "error"
	appEventsSearch = "timeout"
	appEventsLimit = 10

	var captured gqlRequest
	srv := gqlServer(t, eventsPageData(nil, "", "OK"), &captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	if err := runAppEventsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppEventsList: %v", err)
	}
	if captured.Variables["eventType"] != "deploy.completed" {
		t.Errorf("eventType = %v", captured.Variables["eventType"])
	}
	if captured.Variables["severity"] != "error" {
		t.Errorf("severity = %v", captured.Variables["severity"])
	}
	if captured.Variables["search"] != "timeout" {
		t.Errorf("search = %v", captured.Variables["search"])
	}
	if captured.Variables["limit"] != float64(10) {
		t.Errorf("limit = %v, want 10", captured.Variables["limit"])
	}
}

func TestAppEventsListRejectsNonPositiveLimit(t *testing.T) {
	resetAppEventsFlags()
	defer resetAppEventsFlags()
	appEventsLimit = 0

	cmd, _ := appTestCmd()
	err := runAppEventsList(cmd, context.Background(), api.NewClient("http://unused.invalid", "tok", false), "web")
	if err == nil || !strings.Contains(err.Error(), "--limit") {
		t.Fatalf("err = %v, want a --limit complaint", err)
	}
}

func TestAppEventsListRendersTable(t *testing.T) {
	resetAppEventsFlags()
	defer resetAppEventsFlags()

	rows := []appEvent{
		{
			ID: "evt-1", EventType: "deploy.completed", Severity: "info",
			OccurredAt: "2026-08-14T10:00:00+00:00", ResourceKind: "workload", ResourceID: "web-abc",
			Payload: map[string]interface{}{"imageTag": "sha-deadbeef"},
		},
		{ID: "evt-2", EventType: "workload.unhealthy", Severity: "error", OccurredAt: "2026-08-14T09:00:00+00:00"},
	}
	srv := gqlServer(t, eventsPageData(rows, "", "OK"), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppEventsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppEventsList: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"deploy.completed", "info", "workload/web-abc",
		"workload.unhealthy", "error", "2 event(s) shown.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// A resource with neither kind nor id must render as a dash, not a bare
	// or trailing slash (covered precisely in TestAppEventResourceLabel;
	// this just checks the table actually uses that renderer).
	if strings.Contains(got, "workload.unhealthy/") {
		t.Errorf("expected a dash for the missing resource, not a dangling slash:\n%s", got)
	}
}

func TestAppEventsListEmptyNoDataYet(t *testing.T) {
	resetAppEventsFlags()
	defer resetAppEventsFlags()

	srv := gqlServer(t, eventsPageData(nil, "", "NO_DATA_YET"), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppEventsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppEventsList: %v", err)
	}
	if !strings.Contains(out.String(), "No events recorded yet") {
		t.Errorf("expected the never-had-events message: %s", out.String())
	}
}

func TestAppEventsListEmptyFilteredOut(t *testing.T) {
	resetAppEventsFlags()
	defer resetAppEventsFlags()
	appEventsSeverity = "error"

	srv := gqlServer(t, eventsPageData(nil, "", "OK"), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppEventsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppEventsList: %v", err)
	}
	if !strings.Contains(out.String(), "No events matched") {
		t.Errorf("expected the filtered-empty message: %s", out.String())
	}
}

func TestAppEventsListJSONIncludesPayload(t *testing.T) {
	resetAppEventsFlags()
	defer resetAppEventsFlags()

	rows := []appEvent{{ID: "evt-1", EventType: "deploy.completed", OccurredAt: "2026-08-14T10:00:00Z", Payload: map[string]interface{}{"imageTag": "sha-deadbeef"}}}
	srv := gqlServer(t, eventsPageData(rows, "", "OK"), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppEventsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppEventsList --json: %v", err)
	}
	var decoded []appEvent
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding json output: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Payload["imageTag"] != "sha-deadbeef" {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestAppEventsListNotesMoreAvailable(t *testing.T) {
	resetAppEventsFlags()
	defer resetAppEventsFlags()

	rows := []appEvent{{ID: "evt-1", EventType: "x", OccurredAt: "2026-08-14T10:00:00Z"}}
	srv := gqlServer(t, eventsPageData(rows, "cursor-1", "OK"), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	errBuf := &strings.Builder{}
	cmd.SetErr(errBuf)
	if err := runAppEventsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppEventsList: %v", err)
	}
	_ = out
	if !strings.Contains(errBuf.String(), "raise --limit") {
		t.Errorf("expected a note about more events, got: %s", errBuf.String())
	}
}

func TestAppEventsListSurfacesGraphQLError(t *testing.T) {
	resetAppEventsFlags()
	defer resetAppEventsFlags()

	srv := graphQLErrorServer(t, "permission denied: audit_log.read")
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppEventsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v, want the server's message surfaced", err)
	}
}

func TestAppEventResourceLabel(t *testing.T) {
	cases := []struct {
		name string
		e    appEvent
		want string
	}{
		{"kind and id", appEvent{ResourceKind: "workload", ResourceID: "web-abc"}, "workload/web-abc"},
		{"kind only", appEvent{ResourceKind: "workload"}, "workload"},
		{"id only", appEvent{ResourceID: "web-abc"}, "web-abc"},
		{"neither", appEvent{}, "-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := appEventResourceLabel(c.e); got != c.want {
				t.Errorf("appEventResourceLabel(%+v) = %q, want %q", c.e, got, c.want)
			}
		})
	}
}

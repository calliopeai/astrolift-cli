package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// ---- fixtures ---------------------------------------------------------------

func resetAppServicesFlags() {
	appServicesListEnv = ""
}

func managedServiceRow(s appManagedService) map[string]interface{} {
	raw, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		panic(err)
	}
	return m
}

func managedServicesPageData(items []appManagedService, nextCursor string) map[string]interface{} {
	rows := make([]map[string]interface{}, 0, len(items))
	for _, s := range items {
		rows = append(rows, managedServiceRow(s))
	}
	page := map[string]interface{}{"items": rows, "totalCount": len(rows)}
	if nextCursor == "" {
		page["nextCursor"] = nil
	} else {
		page["nextCursor"] = nextCursor
	}
	return map[string]interface{}{"astroliftManagedServicesPage": page}
}

// ---- list --------------------------------------------------------------------

func TestAppServicesListSendsAppSlugAndPageSize(t *testing.T) {
	resetAppServicesFlags()
	defer resetAppServicesFlags()

	var captured gqlRequest
	srv := gqlServer(t, managedServicesPageData(nil, ""), &captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	if err := runAppServicesList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppServicesList: %v", err)
	}
	if captured.Variables["appSlug"] != "web" {
		t.Errorf("appSlug = %v, want web", captured.Variables["appSlug"])
	}
	if captured.Variables["limit"] != float64(appServicesPageSize) {
		t.Errorf("limit = %v, want %d", captured.Variables["limit"], appServicesPageSize)
	}
	if _, ok := captured.Variables["environmentName"]; ok {
		t.Errorf("environmentName should be omitted when --environment is unset, got %v", captured.Variables)
	}
}

func TestAppServicesListSendsEnvironmentFilter(t *testing.T) {
	resetAppServicesFlags()
	defer resetAppServicesFlags()
	appServicesListEnv = "production"

	var captured gqlRequest
	srv := gqlServer(t, managedServicesPageData(nil, ""), &captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	if err := runAppServicesList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppServicesList: %v", err)
	}
	if captured.Variables["environmentName"] != "production" {
		t.Errorf("environmentName = %v, want production", captured.Variables["environmentName"])
	}
}

func TestAppServicesListRendersTable(t *testing.T) {
	resetAppServicesFlags()
	defer resetAppServicesFlags()

	rows := []appManagedService{
		{Name: "web-db", Kind: "postgres", Variant: "rds", EnvironmentName: "production", Status: "active", BindingReady: true, CreatedAt: "2026-08-14T10:00:00+00:00"},
		{Name: "web-cache", Kind: "redis", Status: "failed", StatusError: "quota exceeded", CreatedAt: "2026-08-10T00:00:00+00:00"},
	}
	srv := gqlServer(t, managedServicesPageData(rows, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppServicesList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppServicesList: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"web-db", "postgres", "rds", "production", "active", "yes",
		"web-cache", "redis", "failed (quota exceeded)", "2 managed service(s) shown.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestAppServicesListWalksCursorPages(t *testing.T) {
	resetAppServicesFlags()
	defer resetAppServicesFlags()

	var captured []gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		captured = append(captured, req)
		if req.Variables["after"] == nil {
			return managedServicesPageData([]appManagedService{{Name: "svc-a"}}, "cursor-2")
		}
		return managedServicesPageData([]appManagedService{{Name: "svc-b"}}, "")
	})
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppServicesList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppServicesList: %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("expected 2 page requests, got %d", len(captured))
	}
	if got := captured[1].Variables["after"]; got != "cursor-2" {
		t.Errorf("second page after = %v, want cursor-2", got)
	}
	got := out.String()
	if !strings.Contains(got, "svc-a") || !strings.Contains(got, "svc-b") {
		t.Errorf("both pages should be rendered:\n%s", got)
	}
	if !strings.Contains(got, "2 managed service(s) shown.") {
		t.Errorf("count should span pages:\n%s", got)
	}
}

func TestAppServicesListNotesTruncationAtPageCap(t *testing.T) {
	resetAppServicesFlags()
	defer resetAppServicesFlags()

	page := 0
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		page++
		return managedServicesPageData([]appManagedService{{Name: "svc"}}, "more")
	})
	defer srv.Close()

	cmd, _ := appTestCmd()
	errBuf := &strings.Builder{}
	cmd.SetErr(errBuf)
	if err := runAppServicesList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppServicesList: %v", err)
	}
	if page != appServicesMaxPages {
		t.Errorf("walked %d pages, want the bounded max of %d", page, appServicesMaxPages)
	}
	if !strings.Contains(errBuf.String(), "note:") {
		t.Errorf("expected a truncation note, got: %s", errBuf.String())
	}
}

func TestAppServicesListEmptyNamesTheApp(t *testing.T) {
	resetAppServicesFlags()
	defer resetAppServicesFlags()

	srv := gqlServer(t, managedServicesPageData(nil, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppServicesList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppServicesList: %v", err)
	}
	if !strings.Contains(out.String(), `"web"`) {
		t.Errorf("empty message should name the app: %s", out.String())
	}
}

func TestAppServicesListJSONShape(t *testing.T) {
	resetAppServicesFlags()
	defer resetAppServicesFlags()

	rows := []appManagedService{{
		ID: "svc-1", Name: "web-db", Kind: "postgres", EnvironmentName: "production",
		Attachments: []appManagedServiceAttachment{{ID: "att-1", ConsumerKind: "app", ConsumerSlug: "web", EnvironmentName: "production"}},
	}}
	srv := gqlServer(t, managedServicesPageData(rows, ""), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppServicesList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppServicesList --json: %v", err)
	}
	var decoded []appManagedService
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding json output: %v", err)
	}
	if len(decoded) != 1 || decoded[0].ID != "svc-1" || len(decoded[0].Attachments) != 1 {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestAppServicesListSurfacesGraphQLError(t *testing.T) {
	resetAppServicesFlags()
	defer resetAppServicesFlags()

	srv := graphQLErrorServer(t, "permission denied: app.read")
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppServicesList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v, want the server's message surfaced", err)
	}
}

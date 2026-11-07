package cmd

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
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
		"web-cache", "redis", "failed", "2 managed service(s) shown.",
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

func TestAppServicesListRefusesCursorCycleWithoutPartialOutput(t *testing.T) {
	resetAppServicesFlags()
	defer resetAppServicesFlags()
	page := 0
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		page++
		return managedServicesPageData([]appManagedService{{Name: "svc"}}, "more")
	})
	defer srv.Close()
	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	err := runAppServicesList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web")
	if err == nil || !strings.Contains(err.Error(), "incomplete") || out.Len() != 0 || page != 2 {
		t.Fatalf("expected explicit incomplete walk, requests=%d out=%s err=%v", page, out, err)
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
	if len(decoded) != 1 || decoded[0].ID != "svc-1" || len(decoded[0].Attachments) != 0 {
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
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("err = %v, want a static refusal without server diagnostics", err)
	}
}

// ---- match -------------------------------------------------------------------

func matchPlanData(blocked bool, changes []map[string]interface{}) map[string]interface{} {
	if changes == nil {
		changes = []map[string]interface{}{}
	}
	return map[string]interface{}{"appSlug": "web", "digest": "d1", "blocked": blocked, "changes": changes}
}

var testCreateChange = map[string]interface{}{"action": "create", "ownerScope": "app", "environment": "production", "kind": "postgres", "name": "web-db", "reason": "declared but not provisioned"}

// matchServer answers the preview with plan and the apply with applyResult.
func matchServer(t *testing.T, plan map[string]interface{}, applyResult map[string]interface{}, captured *[]gqlRequest) *httptest.Server {
	t.Helper()
	return gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		*captured = append(*captured, req)
		if strings.Contains(req.Query, "applyManagedServiceMatch(") {
			return map[string]interface{}{"applyManagedServiceMatch": applyResult}
		}
		return map[string]interface{}{"previewManagedServiceMatch": map[string]interface{}{"ok": true, "errors": []interface{}{}, "data": plan}}
	})
}

func resetAppServicesMatchFlags() { appServicesMatchYes = false }

func TestAppServicesMatchYesAppliesWithPreviewDigest(t *testing.T) {
	resetAppServicesMatchFlags()
	defer resetAppServicesMatchFlags()
	appServicesMatchYes = true

	var captured []gqlRequest
	applied := map[string]interface{}{"ok": true, "errors": []interface{}{}, "data": map[string]interface{}{"appSlug": "web", "digest": "d2", "changes": []interface{}{testCreateChange}}}
	srv := matchServer(t, matchPlanData(false, []map[string]interface{}{testCreateChange}), applied, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppServicesMatch(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppServicesMatch: %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("expected preview then apply, got %d requests", len(captured))
	}
	if captured[0].Variables["input"].(map[string]interface{})["appSlug"] != "web" {
		t.Errorf("preview input = %v", captured[0].Variables)
	}
	input := captured[1].Variables["input"].(map[string]interface{})
	if input["appSlug"] != "web" || input["expectedDigest"] != "d1" {
		t.Errorf("apply input = %v, want appSlug web and expectedDigest d1", input)
	}
	got := out.String()
	for _, want := range []string{"ACTION", "create", "web-db", "postgres", "Applied 1 change(s) to app \"web\"."} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestAppServicesMatchBlockedPrintsPlanAndDoesNotApply(t *testing.T) {
	resetAppServicesMatchFlags()
	defer resetAppServicesMatchFlags()
	appServicesMatchYes = true

	var captured []gqlRequest
	srv := matchServer(t, matchPlanData(true, []map[string]interface{}{{"action": "blocked", "ownerScope": "project", "environment": "production", "kind": "redis", "name": "shared-cache", "reason": "project service not attached"}}), nil, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	err := runAppServicesMatch(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web")
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected blocked error, got %v", err)
	}
	if len(captured) != 1 {
		t.Errorf("blocked plan must not apply; got %d requests", len(captured))
	}
	if !strings.Contains(out.String(), "shared-cache") {
		t.Errorf("blocked plan should still list changes:\n%s", out.String())
	}
}

func TestAppServicesMatchNoChangesDoesNotApply(t *testing.T) {
	resetAppServicesMatchFlags()
	defer resetAppServicesMatchFlags()

	var captured []gqlRequest
	srv := matchServer(t, matchPlanData(false, nil), nil, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppServicesMatch(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppServicesMatch: %v", err)
	}
	if len(captured) != 1 {
		t.Errorf("nothing to apply; got %d requests", len(captured))
	}
	if !strings.Contains(out.String(), "already match") {
		t.Errorf("output = %q", out.String())
	}
}

func TestAppServicesMatchNoPromptRequiresYes(t *testing.T) {
	resetAppServicesMatchFlags()
	defer resetAppServicesMatchFlags()

	var captured []gqlRequest
	srv := matchServer(t, matchPlanData(false, []map[string]interface{}{testCreateChange}), nil, &captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	if err := cmd.Root().PersistentFlags().Set("no-prompt", "true"); err != nil {
		t.Fatal(err)
	}
	err := runAppServicesMatch(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web")
	if err == nil || !strings.Contains(err.Error(), "pass --yes") {
		t.Fatalf("expected --yes requirement, got %v", err)
	}
	if len(captured) != 1 {
		t.Errorf("must not apply without confirmation; got %d requests", len(captured))
	}
}

func TestAppServicesMatchDeclinedPromptDoesNotApply(t *testing.T) {
	resetAppServicesMatchFlags()
	defer resetAppServicesMatchFlags()

	var captured []gqlRequest
	srv := matchServer(t, matchPlanData(false, []map[string]interface{}{testCreateChange}), nil, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	cmd.SetIn(strings.NewReader("n\n"))
	if err := runAppServicesMatch(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppServicesMatch: %v", err)
	}
	if len(captured) != 1 {
		t.Errorf("declined prompt must not apply; got %d requests", len(captured))
	}
	if !strings.Contains(out.String(), "Aborted.") {
		t.Errorf("output = %q", out.String())
	}
}

func TestAppServicesMatchConflictIsExplained(t *testing.T) {
	resetAppServicesMatchFlags()
	defer resetAppServicesMatchFlags()
	appServicesMatchYes = true

	var captured []gqlRequest
	applied := map[string]interface{}{"ok": false, "errors": []interface{}{map[string]interface{}{"code": "CONFLICT", "message": "digest moved", "field": "expectedDigest"}}, "data": nil}
	srv := matchServer(t, matchPlanData(false, []map[string]interface{}{testCreateChange}), applied, &captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppServicesMatch(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web")
	if err == nil || !strings.Contains(err.Error(), "changed since it was previewed") {
		t.Fatalf("expected CONFLICT explanation, got %v", err)
	}
}

// ---- reprovision -------------------------------------------------------------

const testServiceID = "7d6e2c1a-4b5f-4c3d-8e9f-0a1b2c3d4e5f"

func serviceRowData(ownerScope, appSlug string) map[string]interface{} {
	return map[string]interface{}{
		"id": testServiceID, "name": "web-db", "kind": "postgres", "ownerScope": ownerScope,
		"registeredAppSlug": appSlug, "registeredAppId": "app-1", "contextRevision": "rev-1",
	}
}

func reprovisionCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd, _ := appTestCmd()
	cmd.Flags().String("expected-context-revision", "", "")
	return cmd
}

func TestAppServicesReprovisionSendsManagedServiceID(t *testing.T) {
	var captured []gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		captured = append(captured, req)
		if strings.Contains(req.Query, "reprovisionManagedService(") {
			return map[string]interface{}{"reprovisionManagedService": map[string]interface{}{"ok": true, "errors": []interface{}{}, "data": map[string]interface{}{"id": testServiceID, "name": "web-db", "kind": "postgres", "status": "active"}}}
		}
		return map[string]interface{}{"astroliftManagedService": serviceRowData("app", "web")}
	})
	defer srv.Close()

	cmd, out := appTestCmd()
	cmd.Flags().String("expected-context-revision", "", "")
	if err := runAppServicesReprovision(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", testServiceID); err != nil {
		t.Fatalf("runAppServicesReprovision: %v", err)
	}
	if len(captured) != 2 {
		t.Fatalf("expected ownership read then reprovision, got %d requests", len(captured))
	}
	input := captured[1].Variables["input"].(map[string]interface{})
	if input["managedServiceId"] != testServiceID {
		t.Errorf("managedServiceId = %v", input["managedServiceId"])
	}
	if _, ok := input["expectedContextRevision"]; ok {
		t.Errorf("expectedContextRevision should be omitted when the flag is unset")
	}
	if !strings.Contains(out.String(), "Reprovisioned web-db (postgres) on app \"web\".") {
		t.Errorf("output = %q", out.String())
	}
}

func TestAppServicesReprovisionRefusesProjectOwnedService(t *testing.T) {
	var captured []gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		captured = append(captured, req)
		return map[string]interface{}{"astroliftManagedService": serviceRowData("project", "web")}
	})
	defer srv.Close()

	cmd := reprovisionCmd(t)
	err := runAppServicesReprovision(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", testServiceID)
	if err == nil || !strings.Contains(err.Error(), "resource context unavailable") {
		t.Fatalf("expected ownership refusal, got %v", err)
	}
	if len(captured) != 1 {
		t.Errorf("must not reprovision a project-owned service; got %d requests", len(captured))
	}
}

func TestAppServicesReprovisionRejectsNonGUID(t *testing.T) {
	cmd := reprovisionCmd(t)
	err := runAppServicesReprovision(cmd, context.Background(), api.NewClient("http://127.0.0.1:1", "tok", false), "web", "web-db")
	if err == nil || !strings.Contains(err.Error(), "exact resource GUID") {
		t.Fatalf("expected GUID refusal, got %v", err)
	}
}

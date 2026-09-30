package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const permissionTestAppID = "3f1c25ab-8bc1-4d98-bb8c-7a42d67a0cb0"

func inspectionCommand() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	command, out := agentTestCmd()
	errOut := &bytes.Buffer{}
	command.SetErr(errOut)
	command.SetContext(context.Background())
	command.Flags().Bool("permissions", false, "")
	for _, flag := range []string{"permission", "scope-type", "scope-id"} {
		command.Flags().String(flag, "", "")
	}
	return command, out, errOut
}

// The fixture checks requests at the HTTP boundary, not merely query substrings
// in output. Identity, summaries and traces must all use the verified tenant.
func inspectionServer(t *testing.T, handle func(*gqlRequest) map[string]any) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/gql/config/" || r.Header.Get("Authorization") != "Bearer selected-token" {
			t.Errorf("wrong request boundary: %s %s", r.Method, r.URL.Path)
		}
		var request gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if strings.Contains(request.Query, "astroliftOrganizations") {
			if r.Header.Get("X-Astrolift-Organization") != "" {
				t.Error("org selection used a guessed tenant")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"astroliftOrganizations": []map[string]string{{"id": "org-id", "slug": "org", "name": "Default"}, {"id": "sibling-id", "slug": "sibling", "name": "Sibling"}}}})
			return
		}
		request.Organization = r.Header.Get("X-Astrolift-Organization")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": handle(&request)})
	}))
	t.Cleanup(server.Close)
	serverSelectionFixture(t, server.URL)
	return server
}

func inspectionData(t *testing.T, request *gqlRequest) map[string]any {
	t.Helper()
	if strings.Contains(request.Query, "astroliftMyProfile") {
		return map[string]any{"astroliftMyProfile": map[string]any{"userId": 42, "username": "leo", "email": "leo@example.test"}}
	}
	if strings.Contains(request.Query, "effectivePermissions") {
		if request.Variables["userId"] != "42" {
			t.Errorf("did not propagate actual self ID: %v", request.Variables)
		}
		return map[string]any{"astroliftMyPermissions": []string{"app.deploy"}, "effectivePermissions": []any{map[string]any{"slug": "app.deploy", "resource": "app", "action": "deploy", "grantedVia": []string{"Operator@PROJECT:sibling-project"}}}}
	}
	if strings.Contains(request.Query, "astroliftMyPermissions") {
		return map[string]any{"astroliftMyPermissions": []string{"app.deploy"}}
	}
	if strings.Contains(request.Query, "astroliftApp(") {
		return map[string]any{"astroliftApp": map[string]any{"id": permissionTestAppID, "slug": "demo", "viewerPermissions": []string{}}}
	}
	if strings.Contains(request.Query, "permissionDiagnose(") {
		if request.Variables["userId"] != "42" {
			t.Errorf("diagnosed a different user: %v", request.Variables)
		}
		return map[string]any{"permissionDiagnose": map[string]any{"userId": "42", "username": "leo", "permission": request.Variables["permission"], "granted": false, "isSuperuser": false, "steps": []any{
			map[string]any{"check": "rbac", "result": true, "detail": "granted by project operator"},
			map[string]any{"check": "abac_policies", "result": false, "detail": "production policy needs two approvals"},
			map[string]any{"check": "resolver_verdict", "result": false, "detail": "production policy needs two approvals"},
		}}}
	}
	t.Errorf("unexpected document: %s", request.Query)
	return nil
}

func TestWhoamiUsesVerifiedSelectedServerOrgAndUser(t *testing.T) {
	for _, permissions := range []bool{false, true} {
		t.Run(fmt.Sprint(permissions), func(t *testing.T) {
			grants := 0
			server := inspectionServer(t, func(request *gqlRequest) map[string]any {
				if request.Organization != "sibling-id" {
					t.Error("identity read escaped selected org")
				}
				if strings.Contains(request.Query, "astroliftMyPermissions") {
					grants++
				}
				return inspectionData(t, request)
			})
			command, out, errOut := inspectionCommand()
			_ = command.Flags().Set("org", "sibling")
			_ = command.Flags().Set("json", "true")
			_ = command.Flags().Set("permissions", fmt.Sprint(permissions))
			if err := whoamiCmd.RunE(command, nil); err != nil {
				t.Fatal(err)
			}
			var result whoamiResult
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.User.UserID != 42 || result.Organization.ID != "sibling-id" || result.Server.Name != "selected" || result.Server.APIURL != server.URL || errOut.Len() != 0 {
				t.Fatalf("wrong verified identity: %+v %s", result, errOut)
			}
			if permissions {
				if grants != 1 || result.AccountGrants == nil || !strings.Contains(result.AccountGrants.Interpretation, "not permission to act on a target") || len(result.AccountGrants.Permissions) != 1 {
					t.Fatalf("misleading grant summary: %+v", result.AccountGrants)
				}
			} else if grants != 0 || result.AccountGrants != nil {
				t.Fatal("plain whoami requested grants")
			}
			for _, secret := range []string{"selected-token", "selected-refresh", "Authorization"} {
				if strings.Contains(out.String()+errOut.String(), secret) {
					t.Fatal("output contained credentials")
				}
			}
		})
	}
}

func TestPermissionInspectionUnknownOrgStopsBeforeIdentity(t *testing.T) {
	for _, run := range []func(*cobra.Command, []string) error{whoamiCmd.RunE, permsDiagnoseCmd.RunE} {
		queries := 0
		inspectionServer(t, func(request *gqlRequest) map[string]any { queries++; return inspectionData(t, request) })
		command, out, _ := inspectionCommand()
		_ = command.Flags().Set("org", "foreign-org")
		if err := run(command, nil); err == nil || queries != 0 || out.Len() != 0 {
			t.Fatalf("unknown tenant read identity: %v queries=%d", err, queries)
		}
	}
}

func TestPermissionInspectionRejectsMissingOrInvalidIdentity(t *testing.T) {
	for _, profile := range []any{nil, map[string]any{"userId": 0, "username": "leo"}, map[string]any{"userId": 42, "username": ""}} {
		t.Run(fmt.Sprint(profile), func(t *testing.T) {
			subsequent := 0
			inspectionServer(t, func(request *gqlRequest) map[string]any {
				if strings.Contains(request.Query, "astroliftMyProfile") {
					return map[string]any{"astroliftMyProfile": profile}
				}
				subsequent++
				return inspectionData(t, request)
			})
			command, out, _ := inspectionCommand()
			_ = command.Flags().Set("permissions", "true")
			if err := whoamiCmd.RunE(command, nil); err == nil || subsequent != 0 || out.Len() != 0 {
				t.Fatalf("unverified identity proceeded: %v", err)
			}
		})
	}
}

func TestPermsDiagnoseShowsInformationalSourcesWithoutDiagnosisOrWrite(t *testing.T) {
	inspectionServer(t, func(request *gqlRequest) map[string]any {
		if request.Organization != "org-id" || strings.Contains(request.Query, "mutation") || strings.Contains(request.Query, "permissionDiagnose(") {
			t.Errorf("unrequested action: %s", request.Query)
		}
		return inspectionData(t, request)
	})
	command, out, _ := inspectionCommand()
	_ = command.Flags().Set("json", "true")
	if err := permsDiagnoseCmd.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	var result PermsDiagnoseResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Diagnosis != nil || result.AccountGrants.Interpretation != accountGrantNotice || result.GrantSources[0].GrantedVia[0] != "Operator@PROJECT:sibling-project" {
		t.Fatalf("incorrect account summary: %+v", result)
	}
}

func TestPermsDiagnoseResolvesActualAppGuidAndPreservesDeniedTrace(t *testing.T) {
	diagnosisRequests := 0
	inspectionServer(t, func(request *gqlRequest) map[string]any {
		if strings.Contains(request.Query, "permissionDiagnose(") {
			diagnosisRequests++
			if request.Variables["scopeType"] != "APP" || request.Variables["scopeId"] != permissionTestAppID || request.Variables["permission"] != "app.deploy" {
				t.Errorf("diagnostic did not name actual app: %v", request.Variables)
			}
		}
		return inspectionData(t, request)
	})
	command, out, _ := inspectionCommand()
	_ = command.Flags().Set("permission", "app.deploy")
	_ = command.Flags().Set("json", "true")
	if err := permsDiagnoseCmd.RunE(command, []string{"demo"}); err != nil {
		t.Fatal(err)
	}
	var result PermsDiagnoseResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if diagnosisRequests != 1 || result.AppContext.ID != permissionTestAppID || result.Diagnosis.Granted || result.Diagnosis.Interpretation != diagnosisNotice || result.Diagnosis.Steps[1].Detail != "production policy needs two approvals" {
		t.Fatalf("trace changed: %+v", result)
	}
}

func TestPermsDiagnoseMissingAppNeverRequestsTrace(t *testing.T) {
	diagnosisRequests := 0
	inspectionServer(t, func(request *gqlRequest) map[string]any {
		if strings.Contains(request.Query, "astroliftApp(") {
			return map[string]any{"astroliftApp": nil}
		}
		if strings.Contains(request.Query, "permissionDiagnose(") {
			diagnosisRequests++
		}
		return inspectionData(t, request)
	})
	command, out, _ := inspectionCommand()
	_ = command.Flags().Set("permission", "app.deploy")
	if err := permsDiagnoseCmd.RunE(command, []string{"foreign"}); err == nil || diagnosisRequests != 0 || out.Len() != 0 {
		t.Fatalf("missing app treated as scope: %v", err)
	}
}

func TestPermsDiagnoseExplicitScopeIsSelfServiceAndInformational(t *testing.T) {
	inspectionServer(t, func(request *gqlRequest) map[string]any {
		if strings.Contains(request.Query, "permissionDiagnose(") && (request.Variables["scopeType"] != "PROJECT" || request.Variables["scopeId"] != "14458f25-0cd2-46b7-b6fc-94b353bfc7b4") {
			t.Errorf("explicit target lost: %v", request.Variables)
		}
		return inspectionData(t, request)
	})
	command, out, _ := inspectionCommand()
	_ = command.Flags().Set("permission", "app.deploy")
	_ = command.Flags().Set("scope-type", "project")
	_ = command.Flags().Set("scope-id", "14458f25-0cd2-46b7-b6fc-94b353bfc7b4")
	if err := permsDiagnoseCmd.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), diagnosisNotice) || !strings.Contains(out.String(), "granted=false") || !strings.Contains(out.String(), "production policy needs two approvals") {
		t.Fatalf("human trace unclear: %s", out)
	}
}

func TestPermsDiagnoseInvalidScopeFlagsMakeNoHTTPRequest(t *testing.T) {
	for _, fixture := range []struct {
		permission, kind, id string
		args                 []string
	}{{"app.deploy", "APP", "", nil}, {"app.deploy", "", "guid", nil}, {"app.deploy", "HOST", "guid", nil}, {"", "APP", "guid", nil}, {"app.deploy", "APP", "guid", []string{"demo"}}} {
		t.Run(fmt.Sprint(fixture), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++ }))
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command, out, _ := inspectionCommand()
			_ = command.Flags().Set("permission", fixture.permission)
			_ = command.Flags().Set("scope-type", fixture.kind)
			_ = command.Flags().Set("scope-id", fixture.id)
			if err := permsDiagnoseCmd.RunE(command, fixture.args); err == nil || requests != 0 || out.Len() != 0 {
				t.Fatalf("invalid flags reached server: %v count=%d", err, requests)
			}
		})
	}
}

func TestInspectionStructuredDenialStaysNonzeroAndOnStderr(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(request.Query, "astroliftOrganizations") {
			_, _ = fmt.Fprint(w, `{"data":{"astroliftOrganizations":[{"id":"org-id","slug":"org"}]}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"errors":[{"message":"denied selected-token","extensions":{"code":"PERMISSION_DENIED","reason":"outside credential team","authorization":"hidden-secret"}}]}`)
	}))
	defer server.Close()
	serverSelectionFixture(t, server.URL)
	command, out, errOut := inspectionCommand()
	_ = command.Flags().Set("json", "true")
	err := whoamiCmd.RunE(command, nil)
	var failure *api.GraphQLResponseError
	if !errors.As(err, &failure) || requests != 2 || out.Len() != 0 {
		t.Fatalf("denial suppressed: %v %s", err, out)
	}
	var report struct {
		Error api.GraphQLResponseError `json:"error"`
	}
	if err := json.Unmarshal(errOut.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Error.Errors[0].Code != "PERMISSION_DENIED" || report.Error.Errors[0].Reason != "outside credential team" {
		t.Fatalf("lost denial: %s", errOut)
	}
	if strings.Contains(errOut.String()+err.Error(), "selected-token") || strings.Contains(errOut.String(), "hidden-secret") {
		t.Fatal("denial leaked credentials")
	}
}

func TestPermsTraceRemovesReflectedCredentialWithoutChangingVerdict(t *testing.T) {
	inspectionServer(t, func(request *gqlRequest) map[string]any {
		data := inspectionData(t, request)
		if strings.Contains(request.Query, "permissionDiagnose(") {
			diagnosis := data["permissionDiagnose"].(map[string]any)
			diagnosis["steps"] = []any{map[string]any{"check": "resolver_verdict", "result": false, "detail": "refused selected-token"}}
		}
		return data
	})
	command, out, _ := inspectionCommand()
	_ = command.Flags().Set("permission", "app.deploy")
	if err := permsDiagnoseCmd.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "selected-token") || !strings.Contains(out.String(), "refused [redacted]") || !strings.Contains(out.String(), "granted=false") {
		t.Fatalf("unsafe trace: %s", out)
	}
}

func TestServerDisplayRemovesURLCredentials(t *testing.T) {
	if got := publicServerURL("https://user:password@example.test/prefix/?token=secret#secret"); got != "https://example.test/prefix/" {
		t.Fatalf("unsafe server URL: %s", got)
	}
}

func TestInspectionNonJSONHTTPDenialIsStructuredWithoutRawBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&request)
		if strings.Contains(request.Query, "astroliftOrganizations") {
			_, _ = fmt.Fprint(w, `{"data":{"astroliftOrganizations":[{"id":"org-id","slug":"org"}]}}`)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, "<html>selected-token arbitrary-secret</html>")
	}))
	defer server.Close()
	serverSelectionFixture(t, server.URL)
	command, out, errOut := inspectionCommand()
	_ = command.Flags().Set("json", "true")
	err := permsDiagnoseCmd.RunE(command, nil)
	var failure *api.HTTPError
	if !errors.As(err, &failure) || failure.Status != 403 || out.Len() != 0 {
		t.Fatalf("not an HTTP refusal: %v", err)
	}
	var report struct {
		Error api.HTTPError `json:"error"`
	}
	if err := json.Unmarshal(errOut.Bytes(), &report); err != nil || report.Error.Status != 403 {
		t.Fatalf("no structured HTTP status: %s %v", errOut, err)
	}
	if strings.Contains(errOut.String()+err.Error(), "selected-token") || strings.Contains(errOut.String()+err.Error(), "arbitrary-secret") {
		t.Fatal("raw body leaked")
	}
}

func TestWhoamiHumanSummaryDoesNotApproveSiblingGrant(t *testing.T) {
	inspectionServer(t, func(request *gqlRequest) map[string]any { return inspectionData(t, request) })
	command, out, _ := inspectionCommand()
	_ = command.Flags().Set("permissions", "true")
	if err := whoamiCmd.RunE(command, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), accountGrantNotice) || !strings.Contains(out.String(), "app.deploy") || !strings.Contains(out.String(), "User:   leo") {
		t.Fatalf("summary misrepresented: %s", out)
	}
}

func TestPermsDiagnoseRejectsAbsentOrMismatchedTrace(t *testing.T) {
	for _, fixture := range []any{nil, map[string]any{"userId": "foreign", "permission": "app.deploy", "steps": []any{}}, map[string]any{"userId": "42", "permission": "other.permission", "steps": []any{}}} {
		t.Run(fmt.Sprint(fixture), func(t *testing.T) {
			inspectionServer(t, func(request *gqlRequest) map[string]any {
				if strings.Contains(request.Query, "permissionDiagnose(") {
					return map[string]any{"permissionDiagnose": fixture}
				}
				return inspectionData(t, request)
			})
			command, out, _ := inspectionCommand()
			_ = command.Flags().Set("permission", "app.deploy")
			if err := permsDiagnoseCmd.RunE(command, nil); err == nil || out.Len() != 0 {
				t.Fatalf("mismatched diagnostic printed: %v %s", err, out)
			}
		})
	}
}

func TestReportedInspectionErrorPreservesWrappedClassification(t *testing.T) {
	for _, failure := range []*api.GraphQLResponseError{
		{Status: 200, Errors: []api.GraphQLError{{Message: `Cannot query field "missing" on type "Query".`}}},
		{Status: 200, Errors: []api.GraphQLError{{Message: `Cannot query field "missing" on type "Query".`}, {Message: "denied", Code: "PERMISSION_DENIED"}}},
	} {
		command, out, errOut := inspectionCommand()
		_ = command.Flags().Set("json", "true")
		original := fmt.Errorf("verifying identity: %w", failure)
		err := permissionCommandError(command, original)
		var typed *api.GraphQLResponseError
		var reported interface{ AlreadyReported() bool }
		if !errors.As(err, &typed) || typed != failure || !errors.As(err, &reported) || !reported.AlreadyReported() || errors.Is(err, api.ErrSchemaMismatch) != (len(failure.Errors) == 1) {
			t.Fatalf("wrapped classification lost: %v", err)
		}
		if out.Len() != 0 || !json.Valid(errOut.Bytes()) {
			t.Fatalf("error output invalid: %s %s", out, errOut)
		}
	}
}

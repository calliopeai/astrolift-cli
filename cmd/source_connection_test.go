package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// ---- app show: the source connection for the repository's owner (astrolift-app#2579) ----

// sourceConnectionServer serves app show reads. connection is what a current
// server reports for the app; nil is a server without the field, which refuses
// a selection of it at validation as GraphQL does.
func sourceConnectionServer(t *testing.T, connection map[string]interface{}, sent *[]gqlRequest) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		*sent = append(*sent, req)
		if connection == nil && strings.Contains(req.Query, "sourceConnection") {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]string{
				{"message": "Cannot query field 'sourceConnection' on type 'AstroliftRegisteredApp'."},
			}})
			return
		}
		app := map[string]interface{}{
			"id": "app-1", "slug": "web", "name": "Web App", "organizationSlug": "acme",
			"provisioningStatus": "ready", "isActive": true, "sourceKind": "github",
			"sourceRepo": "calliopeai/cykick", "reachabilityStatus": "REACHABLE",
		}
		if connection != nil && strings.Contains(req.Query, "sourceConnection") {
			app["sourceConnection"] = connection
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
			"astroliftApp": app, "astroliftWorkloads": []interface{}{}, "astroliftEnvironments": []interface{}{},
		}})
	}))
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "tok", false)
}

func TestAppShowPrintsTheSourceConnectionForTheRepoOwner(t *testing.T) {
	notOnOwner := "No GitHub App installation in this organization is recorded on calliopeai."
	for _, tc := range []struct {
		name       string
		connection map[string]interface{}
		want       string
	}{
		{
			"connected",
			map[string]interface{}{"state": "CONNECTED", "owner": "calliopeai", "detail": ""},
			"Source:       github calliopeai/cykick\nConnection:   connected (calliopeai)\n\nWorkloads:",
		},
		{
			"not connected",
			map[string]interface{}{"state": "NOT_CONNECTED", "owner": "calliopeai", "detail": notOnOwner},
			"Connection:   not connected (calliopeai)\n              " + notOnOwner + "\n",
		},
		{
			"unknown",
			map[string]interface{}{"state": "UNKNOWN", "owner": "", "detail": "The app has no source repository."},
			"Connection:   unknown\n              The app has no source repository.\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent []gqlRequest
			client := sourceConnectionServer(t, tc.connection, &sent)
			cmd, out, errOut := listShowTestCmd()

			if err := runAppShow(cmd, context.Background(), client, "web"); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("show must print the connection under the source:\n%s", out.String())
			}
			if !strings.Contains(out.String(), "Reachability: reachable\n") {
				t.Fatalf("the reachability read must not be lost:\n%s", out.String())
			}
			if len(sent) != 1 || !strings.Contains(sent[0].Query, "sourceConnection { state owner detail }") || errOut.Len() != 0 {
				t.Fatalf("a current server answers in one read: requests=%d stderr=%q", len(sent), errOut.String())
			}

			out.Reset()
			if err := cmd.Flags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
			if err := runAppShow(cmd, context.Background(), client, "web"); err != nil {
				t.Fatal(err)
			}
			var detail struct {
				App struct {
					SourceConnection map[string]interface{} `json:"sourceConnection"`
				} `json:"astroliftApp"`
			}
			if err := json.Unmarshal(out.Bytes(), &detail); err != nil || detail.App.SourceConnection["state"] != tc.connection["state"] {
				t.Fatalf("show JSON must keep the server's value: %v\n%s", err, out.String())
			}
		})
	}
}

// A server older than the field refuses its selection. The read falls back to
// the one the CLI made before, keeping reachability and expiry, and the source
// connection reads as unknown instead of disappearing.
func TestAppShowReadsAnUnreportedSourceConnectionAsUnknown(t *testing.T) {
	var sent []gqlRequest
	client := sourceConnectionServer(t, nil, &sent)
	cmd, out, errOut := listShowTestCmd()

	if err := runAppShow(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("show on an older server: %v", err)
	}
	if !strings.Contains(out.String(), "Connection:   unknown\n              "+sourceConnectionUnreported+"\n") {
		t.Fatalf("an unreported source connection must read unknown:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Reachability: reachable\n") {
		t.Fatalf("the fallback must keep reachability:\n%s", out.String())
	}
	if len(sent) != 2 || strings.Contains(sent[1].Query, "sourceConnection") || errOut.Len() != 0 {
		t.Fatalf("one refused read, then the earlier one, and no expiry note: requests=%d stderr=%q", len(sent), errOut.String())
	}

	out.Reset()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runAppShow(cmd, context.Background(), client, "web"); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		App struct {
			ExpiryAvailable  bool                 `json:"expiryAvailable"`
			SourceConnection *appSourceConnection `json:"sourceConnection"`
		} `json:"astroliftApp"`
	}
	if err := json.Unmarshal(out.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	got := detail.App.SourceConnection
	if got == nil || got.State != "UNKNOWN" || got.Detail != sourceConnectionUnreported || !detail.App.ExpiryAvailable {
		t.Fatalf("show JSON must carry UNKNOWN, not omit the field:\n%s", out.String())
	}
}

// ---- scm list: the scope it needs and how to get it (astrolift-app#2579) ----

// scmRefusalServer refuses astroliftSourceConnections the way the server does:
// the reason says whether the credential's scopes or the account's roles refused.
// The client's credential must not occur in the reason, which the client redacts.
func scmRefusalServer(t *testing.T, reason string) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": nil, "errors": []interface{}{map[string]interface{}{
			"message": reason + ": scm.read", "path": []string{"astroliftSourceConnections"},
			"extensions": map[string]string{"code": "PERMISSION_DENIED", "reason": reason, "permission": "scm.read"},
		}}})
	}))
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "cli-credential", false)
}

func TestScmListSaysWhichScopeItNeedsAndHowToGetIt(t *testing.T) {
	client := scmRefusalServer(t, tokenScopeRefusal)
	cmd, _ := groupsTestCmd()

	err := runScmList(cmd, context.Background(), client)
	if err == nil {
		t.Fatal("a refused read must fail")
	}
	for _, want := range []string{
		"api token scope does not allow this permission: scm.read",
		"This credential's scopes do not allow scm.read",
		"astro auth login --scope admin",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal must say %q:\n%v", want, err)
		}
	}

	errOut := &bytes.Buffer{}
	cmd.SetErr(errOut)
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := permissionCommandError(cmd, runScmList(cmd, context.Background(), client)); err == nil {
		t.Fatal("a refused read must fail under --json too")
	}
	var report struct {
		Credential *credentialAdvice `json:"credential"`
	}
	if err := json.Unmarshal(errOut.Bytes(), &report); err != nil || report.Credential == nil ||
		report.Credential.Permission != "scm.read" || report.Credential.LoginScope != "admin" {
		t.Fatalf("--json must carry the sign-in advice: %v\n%s", err, errOut.String())
	}
}

func TestScmListLeavesAnAccountRefusalAsTheServerSaidIt(t *testing.T) {
	client := scmRefusalServer(t, "no grant for this permission at any scope")
	cmd, _ := groupsTestCmd()

	err := runScmList(cmd, context.Background(), client)
	if err == nil || !strings.Contains(err.Error(), "no grant for this permission at any scope") {
		t.Fatalf("the account refusal must surface: %v", err)
	}
	if strings.Contains(err.Error(), "astro auth login") {
		t.Fatalf("another sign-in cannot fix an account refusal:\n%v", err)
	}
}

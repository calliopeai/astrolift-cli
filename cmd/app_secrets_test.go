package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// ---- fixtures ---------------------------------------------------------------

func resetAppSecretsFlags() {
	appSecretsListEnv = ""
	appSecretsCreateValue = ""
	appSecretsCreateStdin = false
	appSecretsCreateScope = "all"
	appSecretsCreateSetVia = "cli"
	appSecretsDeleteYes = false
}

func secretRow(s appSecret) map[string]interface{} {
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

func secretsListData(rows []appSecret) map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(rows))
	for _, r := range rows {
		items = append(items, secretRow(r))
	}
	return map[string]interface{}{"astroliftAppSecrets": items}
}

// graphQLErrorServer replies with a transport-level GraphQL error envelope
// (no "data" key), for testing query-side error surfacing.
func graphQLErrorServer(t *testing.T, message string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errors": []map[string]interface{}{{"message": message}},
		})
	}))
}

// ---- list --------------------------------------------------------------------

func TestAppSecretsListSendsAppSlugAndRendersTable(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	rows := []appSecret{{
		Key: "DATABASE_URL", EnvironmentName: "production", Source: "literal",
		Scope: "all", SetVia: "cli", LastEditedAt: strptr("2026-08-14T10:00:00+00:00"),
	}}

	var captured gqlRequest
	srv := gqlServer(t, secretsListData(rows), &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppSecretsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppSecretsList: %v", err)
	}

	if captured.Variables["appSlug"] != "web" {
		t.Errorf("appSlug = %v, want web", captured.Variables["appSlug"])
	}
	if _, ok := captured.Variables["environmentName"]; ok {
		t.Errorf("environmentName should be omitted when --environment is unset, got %v", captured.Variables)
	}
	if strings.Contains(captured.Query, "value") {
		t.Error("the list query must never select a value field")
	}

	got := out.String()
	for _, want := range []string{"DATABASE_URL", "production", "literal", "all", "cli", "1 secret(s) shown."} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestAppSecretsListSendsEnvironmentFilter(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()
	appSecretsListEnv = "production"

	var captured gqlRequest
	srv := gqlServer(t, secretsListData(nil), &captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	if err := runAppSecretsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppSecretsList: %v", err)
	}
	if captured.Variables["environmentName"] != "production" {
		t.Errorf("environmentName = %v, want production", captured.Variables["environmentName"])
	}
}

func TestAppSecretsListEmptyNamesTheApp(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	srv := gqlServer(t, secretsListData(nil), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppSecretsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppSecretsList: %v", err)
	}
	if !strings.Contains(out.String(), `"web"`) {
		t.Errorf("empty message should name the app: %s", out.String())
	}
}

func TestAppSecretsListJSONNeverIncludesAValue(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	rows := []appSecret{{Key: "API_KEY", EnvironmentName: "production", Scope: "all", SetVia: "cli"}}
	srv := gqlServer(t, secretsListData(rows), nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppSecretsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppSecretsList --json: %v", err)
	}

	var decoded []appSecret
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding json output: %v", err)
	}
	if len(decoded) != 1 || decoded[0].Key != "API_KEY" {
		t.Errorf("decoded = %+v", decoded)
	}
	if strings.Contains(out.String(), `"value"`) {
		t.Error("json output must never contain a value field")
	}
}

func TestAppSecretsListSurfacesGraphQLError(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	srv := graphQLErrorServer(t, "permission denied: app.read")
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppSecretsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web")
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("err = %v, want the server's message surfaced", err)
	}
}

// ---- create --------------------------------------------------------------------

func TestAppSecretsCreateSendsSetAppSecretWithDefaults(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"setAppSecret": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"appSlug": "web", "key": "API_KEY", "rawManifestStaged": "API_KEY=***", "pendingProposalId": nil},
		},
	}, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	err := runAppSecretsCreate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "API_KEY", "s3cr3t")
	if err != nil {
		t.Fatalf("runAppSecretsCreate: %v", err)
	}

	input, ok := captured.Variables["input"].(map[string]interface{})
	if !ok {
		t.Fatalf("mutation input missing: %#v", captured.Variables)
	}
	if input["appSlug"] != "web" || input["key"] != "API_KEY" || input["value"] != "s3cr3t" {
		t.Errorf("input = %#v", input)
	}
	if input["scope"] != "all" {
		t.Errorf("scope default = %v, want all", input["scope"])
	}
	if input["setVia"] != "cli" {
		t.Errorf("setVia default = %v, want cli", input["setVia"])
	}
	if strings.Contains(out.String(), "s3cr3t") {
		t.Error("the value must never be echoed back")
	}
	if !strings.Contains(out.String(), "Set secret API_KEY on app web.") {
		t.Errorf("unexpected confirmation: %s", out.String())
	}
}

func TestAppSecretsCreateHonorsScopeAndSetViaFlags(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()
	appSecretsCreateScope = "preview:feat/x"
	appSecretsCreateSetVia = "web"

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"setAppSecret": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"appSlug": "web", "key": "K", "rawManifestStaged": ""},
		},
	}, &captured)
	defer srv.Close()

	cmd, _ := appTestCmd()
	if err := runAppSecretsCreate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "K", "v"); err != nil {
		t.Fatalf("runAppSecretsCreate: %v", err)
	}
	input := captured.Variables["input"].(map[string]interface{})
	if input["scope"] != "preview:feat/x" || input["setVia"] != "web" {
		t.Errorf("input = %#v", input)
	}
}

func TestAppSecretsCreateReportsPendingProposal(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	srv := gqlServer(t, map[string]interface{}{
		"setAppSecret": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"appSlug": "web", "key": "API_KEY", "rawManifestStaged": "",
				"pendingProposalId": "proposal-123",
			},
		},
	}, nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppSecretsCreate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "API_KEY", "s3cr3t"); err != nil {
		t.Fatalf("runAppSecretsCreate: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "requires approval") || !strings.Contains(got, "proposal-123") {
		t.Errorf("expected the pending-proposal message, got: %s", got)
	}
	if strings.Contains(got, "Set secret API_KEY on app web.") {
		t.Error("must not claim the write applied when it is only queued")
	}
}

func TestAppSecretsCreateJSONShape(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	srv := gqlServer(t, map[string]interface{}{
		"setAppSecret": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"appSlug": "web", "key": "API_KEY", "rawManifestStaged": "x"},
		},
	}, nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppSecretsCreate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "API_KEY", "s3cr3t"); err != nil {
		t.Fatalf("runAppSecretsCreate --json: %v", err)
	}
	var decoded appSecretWritePayload
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("decoding json output: %v", err)
	}
	if decoded.Key != "API_KEY" || decoded.AppSlug != "web" {
		t.Errorf("decoded = %+v", decoded)
	}
}

func TestAppSecretsCreateSurfacesMutationError(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	srv := gqlServer(t, map[string]interface{}{
		"setAppSecret": map[string]interface{}{
			"ok": false,
			"errors": []map[string]interface{}{
				{"code": "VALIDATION", "message": "not a valid POSIX env var name", "field": "key"},
			},
		},
	}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppSecretsCreate(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "bad key", "v")
	if err == nil || !strings.Contains(err.Error(), "not a valid POSIX env var name") {
		t.Fatalf("err = %v, want the server's message surfaced", err)
	}
}

func TestReadAppSecretValuePrefersValueFlag(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()
	appSecretsCreateValue = "from-flag"

	cmd, _ := appTestCmd()
	v, err := readAppSecretValue(cmd)
	if err != nil {
		t.Fatalf("readAppSecretValue: %v", err)
	}
	if v != "from-flag" {
		t.Errorf("value = %q, want from-flag", v)
	}
}

func TestReadAppSecretValueReadsStdin(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()
	appSecretsCreateStdin = true

	cmd, _ := appTestCmd()
	cmd.SetIn(strings.NewReader("from-stdin\n"))
	v, err := readAppSecretValue(cmd)
	if err != nil {
		t.Fatalf("readAppSecretValue: %v", err)
	}
	if v != "from-stdin" {
		t.Errorf("value = %q, want from-stdin", v)
	}
}

func TestReadAppSecretValueRejectsEmptyStdin(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()
	appSecretsCreateStdin = true

	cmd, _ := appTestCmd()
	cmd.SetIn(strings.NewReader("\n"))
	if _, err := readAppSecretValue(cmd); err == nil {
		t.Fatal("expected an error for an empty stdin value")
	}
}

// ---- delete --------------------------------------------------------------------

func TestAppSecretsDeleteSendsMutationWhenConfirmed(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"deleteAppSecret": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"appSlug": "web", "key": "API_KEY", "rawManifestStaged": ""},
		},
	}, &captured)
	defer srv.Close()

	cmd, out := appTestCmd()
	cmd.SetIn(strings.NewReader("y\n"))
	if err := runAppSecretsDelete(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "API_KEY"); err != nil {
		t.Fatalf("runAppSecretsDelete: %v", err)
	}
	input := captured.Variables["input"].(map[string]interface{})
	if input["appSlug"] != "web" || input["key"] != "API_KEY" {
		t.Errorf("input = %#v", input)
	}
	if !strings.Contains(out.String(), "Deleted secret API_KEY on app web.") {
		t.Errorf("unexpected confirmation: %s", out.String())
	}
}

func TestAppSecretsDeleteSkipsPromptWithYesFlag(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()
	appSecretsDeleteYes = true

	srv := gqlServer(t, map[string]interface{}{
		"deleteAppSecret": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"appSlug": "web", "key": "API_KEY", "rawManifestStaged": ""},
		},
	}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	// No stdin provided: if the command tried to prompt, Fscan would block/err.
	cmd.SetIn(strings.NewReader(""))
	if err := runAppSecretsDelete(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "API_KEY"); err != nil {
		t.Fatalf("runAppSecretsDelete --yes: %v", err)
	}
}

func TestAppSecretsDeleteAbortsOnDecline(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("declining the prompt must not send any request")
	}))
	defer srv.Close()

	cmd, out := appTestCmd()
	cmd.SetIn(strings.NewReader("n\n"))
	if err := runAppSecretsDelete(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "API_KEY"); err != nil {
		t.Fatalf("runAppSecretsDelete: %v", err)
	}
	if !strings.Contains(out.String(), "Aborted.") {
		t.Errorf("expected an abort message: %s", out.String())
	}
}

func TestAppSecretsDeleteRequiresYesUnderNoPrompt(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no mutation should be sent without confirmation")
	}))
	defer srv.Close()

	cmd, _ := appTestCmd()
	_ = cmd.Root().PersistentFlags().Set("no-prompt", "true")
	err := runAppSecretsDelete(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "API_KEY")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want a --yes requirement", err)
	}
}

func TestAppSecretsDeleteSurfacesNotFoundError(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()
	appSecretsDeleteYes = true

	srv := gqlServer(t, map[string]interface{}{
		"deleteAppSecret": map[string]interface{}{
			"ok": false,
			"errors": []map[string]interface{}{
				{"code": "NOT_FOUND", "message": "key 'MISSING' not present in [env]", "field": "key"},
			},
		},
	}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppSecretsDelete(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "MISSING")
	if err == nil || !strings.Contains(err.Error(), "not present in [env]") {
		t.Fatalf("err = %v, want the server's message surfaced", err)
	}
}

func TestAppSecretsDeleteReportsPendingProposal(t *testing.T) {
	resetAppSecretsFlags()
	defer resetAppSecretsFlags()
	appSecretsDeleteYes = true

	srv := gqlServer(t, map[string]interface{}{
		"deleteAppSecret": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"appSlug": "web", "key": "API_KEY", "rawManifestStaged": "",
				"pendingProposalId": "proposal-9",
			},
		},
	}, nil)
	defer srv.Close()

	cmd, out := appTestCmd()
	if err := runAppSecretsDelete(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web", "API_KEY"); err != nil {
		t.Fatalf("runAppSecretsDelete: %v", err)
	}
	if !strings.Contains(out.String(), "requires approval") || !strings.Contains(out.String(), "proposal-9") {
		t.Errorf("expected the pending-proposal message, got: %s", out.String())
	}
}

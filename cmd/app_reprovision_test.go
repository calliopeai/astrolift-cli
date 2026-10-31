package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

func resetAppReprovisionFlags() {
	appReprovisionEnv = ""
	appReprovisionWait = false
	appReprovisionTimeout = 5 * time.Minute
	appReprovisionPollInterval = time.Millisecond
}

// reprovisionAccepted is a reprovisionApp envelope: production gained its
// host objects, staging was already in place.
func reprovisionAccepted() map[string]interface{} {
	return map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{
			"appSlug": "web",
			"environments": []map[string]interface{}{
				{
					"environmentName": "production", "deploymentId": "dep-7", "reachabilityStatus": "PROVISIONING",
					"objectsCreated": []string{"Ingress/web", "Certificate/web-tls"},
					"objectsUpdated": []string{"NetworkPolicy/web"}, "unchanged": false,
				},
				{
					"environmentName": "staging", "deploymentId": nil, "reachabilityStatus": "REACHABLE",
					"objectsCreated": []string{}, "objectsUpdated": []string{}, "unchanged": true,
				},
			},
		},
	}
}

// reprovisionServer answers reprovisionApp with envelope and serves statuses
// in order on each astroliftApp reachability read, repeating the last.
func reprovisionServer(t *testing.T, envelope map[string]interface{}, statuses []string, sent *[]gqlRequest) *api.Client {
	t.Helper()
	reads := 0
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		*sent = append(*sent, req)
		if strings.Contains(req.Query, "reprovisionApp") {
			return map[string]interface{}{"reprovisionApp": envelope}
		}
		status := statuses[min(reads, len(statuses)-1)]
		reads++
		return map[string]interface{}{"astroliftApp": map[string]interface{}{"slug": "web", "reachabilityStatus": status}}
	})
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "tok", false)
}

func TestAppReprovisionSendsTheInputAndPrintsEachEnvironment(t *testing.T) {
	resetAppReprovisionFlags()
	t.Cleanup(resetAppReprovisionFlags)
	var sent []gqlRequest
	client := reprovisionServer(t, reprovisionAccepted(), []string{"REACHABLE"}, &sent)
	cmd, out := appTestCmd()

	if err := runAppReprovision(cmd, context.Background(), client, "web"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 {
		t.Fatalf("without --wait only the mutation is sent, got %d requests", len(sent))
	}
	for _, want := range []string{
		"$input: ReprovisionAppInput!", "reprovisionApp(input: $input)", "errors { code message field }", "appSlug",
		"environmentName", "deploymentId", "reachabilityStatus", "objectsCreated", "objectsUpdated", "unchanged",
	} {
		if !strings.Contains(sent[0].Query, want) {
			t.Errorf("mutation lacks %q:\n%s", want, sent[0].Query)
		}
	}
	input := sent[0].Variables["input"].(map[string]interface{})
	if input["appSlug"] != "web" {
		t.Fatalf("reprovisionApp input: %#v", input)
	}
	if _, ok := input["environmentName"]; ok {
		t.Error("environmentName must be omitted without --env")
	}
	want := "production: provisioning; created Ingress/web, Certificate/web-tls; updated NetworkPolicy/web\n" +
		"staging: reachable; unchanged\n"
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestAppReprovisionForwardsTheEnvironment(t *testing.T) {
	resetAppReprovisionFlags()
	t.Cleanup(resetAppReprovisionFlags)
	appReprovisionEnv = " staging "
	var sent []gqlRequest
	client := reprovisionServer(t, reprovisionAccepted(), []string{"REACHABLE"}, &sent)
	cmd, _ := appTestCmd()

	if err := runAppReprovision(cmd, context.Background(), client, "web"); err != nil {
		t.Fatal(err)
	}
	input := sent[0].Variables["input"].(map[string]interface{})
	if input["appSlug"] != "web" || input["environmentName"] != "staging" {
		t.Fatalf("reprovisionApp input: %#v", input)
	}
}

func TestAppReprovisionJSONPrintsThePayload(t *testing.T) {
	resetAppReprovisionFlags()
	t.Cleanup(resetAppReprovisionFlags)
	var sent []gqlRequest
	client := reprovisionServer(t, reprovisionAccepted(), []string{"REACHABLE"}, &sent)
	cmd, out := appTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}

	if err := runAppReprovision(cmd, context.Background(), client, "web"); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out.String())
	}
	if got["appSlug"] != "web" {
		t.Fatalf("json: %#v", got)
	}
	if _, ok := got["reachabilityStatus"]; ok {
		t.Error("the app's reachability is reported only after --wait")
	}
	envs := got["environments"].([]interface{})
	production, staging := envs[0].(map[string]interface{}), envs[1].(map[string]interface{})
	if production["deploymentId"] != "dep-7" || production["reachabilityStatus"] != "PROVISIONING" ||
		len(production["objectsCreated"].([]interface{})) != 2 || len(production["objectsUpdated"].([]interface{})) != 1 {
		t.Fatalf("production: %#v", production)
	}
	if id, ok := staging["deploymentId"]; !ok || id != nil || staging["unchanged"] != true {
		t.Fatalf("staging: %#v", staging)
	}
}

func TestAppReprovisionSurfacesTheRefusalWithoutPolling(t *testing.T) {
	resetAppReprovisionFlags()
	t.Cleanup(resetAppReprovisionFlags)
	appReprovisionWait = true
	refusal := map[string]interface{}{
		"ok": false, "data": nil,
		"errors": []map[string]string{{"code": "PERMISSION_DENIED", "message": "app.deploy is required", "field": ""}},
	}
	var sent []gqlRequest
	client := reprovisionServer(t, refusal, []string{"REACHABLE"}, &sent)
	cmd, out := appTestCmd()

	err := runAppReprovision(cmd, context.Background(), client, "web")
	if err == nil || !strings.Contains(err.Error(), "app.deploy is required") {
		t.Fatalf("expected the server's refusal, got %v", err)
	}
	if len(sent) != 1 || out.Len() != 0 {
		t.Fatalf("a refusal must not poll or print: requests=%d output=%s", len(sent), out.String())
	}
}

func TestAppReprovisionTransportFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
		want  string
		not   string
	}{
		{"server without reprovision", `{"errors":[{"message":"Cannot query field \"reprovisionApp\" on type \"Mutation\"."}]}`,
			"this server does not offer reprovision; it needs an Astrolift control plane with platform #2573", ""},
		{"server failure", `{"errors":[{"message":"apply failed","extensions":{"code":"INTERNAL"}}]}`,
			"reprovisioning web: graphql errors: INTERNAL: apply failed", "does not offer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetAppReprovisionFlags()
			t.Cleanup(resetAppReprovisionFlags)
			appReprovisionWait = true
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				_, _ = fmt.Fprint(w, tc.reply)
			}))
			defer srv.Close()
			cmd, _ := appTestCmd()

			err := runAppReprovision(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web")
			if err == nil || !strings.Contains(err.Error(), tc.want) || (tc.not != "" && strings.Contains(err.Error(), tc.not)) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
			if requests != 1 {
				t.Fatalf("expected the mutation only, got %d requests", requests)
			}
		})
	}
}

func TestAppReprovisionWaitPollsUntilReachable(t *testing.T) {
	resetAppReprovisionFlags()
	t.Cleanup(resetAppReprovisionFlags)
	appReprovisionWait = true
	var sent []gqlRequest
	client := reprovisionServer(t, reprovisionAccepted(), []string{"PROVISIONING", "PROVISIONING", "REACHABLE"}, &sent)
	cmd, out := appTestCmd()

	if err := runAppReprovision(cmd, context.Background(), client, "web"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 4 {
		t.Fatalf("expected the mutation plus three reads, got %d requests", len(sent))
	}
	for _, read := range sent[1:] {
		if read.Variables["slug"] != "web" || !strings.Contains(read.Query, "astroliftApp(slug: $slug)") ||
			!strings.Contains(read.Query, "reachabilityStatus") {
			t.Fatalf("reachability read: %#v", read)
		}
	}
	got := out.String()
	if !strings.HasPrefix(got, "production: provisioning;") || strings.Count(got, "→ provisioning") != 1 ||
		!strings.HasSuffix(got, "Waiting for web to be reachable...\n  → provisioning\n  → reachable\n") {
		t.Fatalf("each status change prints once:\n%s", got)
	}
}

func TestAppReprovisionWaitFailsWhenTheAppIsNotReachable(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []string
		timeout  time.Duration
		reads    int
		want     string
	}{
		{"needs reprovision", []string{"NEEDS_REPROVISION", "REACHABLE"}, time.Minute, 1, "app web still needs a reprovision"},
		{"no managed domain", []string{"PROVISIONING", "NO_MANAGED_DOMAIN", "REACHABLE"}, time.Minute, 2, "app web has no managed domain"},
		{"timeout", []string{"PROVISIONING"}, 5 * time.Millisecond, -1, "app web still provisioning after 5ms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetAppReprovisionFlags()
			t.Cleanup(resetAppReprovisionFlags)
			appReprovisionWait, appReprovisionTimeout = true, tc.timeout
			var sent []gqlRequest
			client := reprovisionServer(t, reprovisionAccepted(), tc.statuses, &sent)
			cmd, _ := appTestCmd()

			err := runAppReprovision(cmd, context.Background(), client, "web")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error containing %q, got %v", tc.want, err)
			}
			if tc.reads >= 0 && len(sent)-1 != tc.reads {
				t.Fatalf("expected the wait to stop after %d reads, got %d", tc.reads, len(sent)-1)
			}
		})
	}
}

func TestAppReprovisionWaitJSONReportsTheFinalStatus(t *testing.T) {
	for _, tc := range []struct {
		statuses []string
		final    string
		fails    bool
	}{
		{[]string{"PROVISIONING", "REACHABLE"}, "REACHABLE", false},
		{[]string{"NO_MANAGED_DOMAIN"}, "NO_MANAGED_DOMAIN", true},
	} {
		t.Run(tc.final, func(t *testing.T) {
			resetAppReprovisionFlags()
			t.Cleanup(resetAppReprovisionFlags)
			appReprovisionWait = true
			var sent []gqlRequest
			client := reprovisionServer(t, reprovisionAccepted(), tc.statuses, &sent)
			cmd, out := appTestCmd()
			if err := cmd.Flags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}

			err := runAppReprovision(cmd, context.Background(), client, "web")
			if (err != nil) != tc.fails {
				t.Fatalf("error = %v, want failure %t", err, tc.fails)
			}
			var got reprovisionResult
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("stdout is not one JSON object: %v\n%s", err, out.String())
			}
			if got.ReachabilityStatus != tc.final || got.AppSlug != "web" || len(got.Environments) != 2 {
				t.Fatalf("json result: %+v", got)
			}
		})
	}
}

func TestAppReprovisionWaitStopsOnAFailedRead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply string
		want  string
	}{
		{"refused", `{"errors":[{"message":"app.read is required","extensions":{"code":"PERMISSION_DENIED"}}]}`, "reading web reachability: graphql errors: PERMISSION_DENIED: app.read is required"},
		{"gone", `{"data":{"astroliftApp":null}}`, `app "web" not found`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetAppReprovisionFlags()
			t.Cleanup(resetAppReprovisionFlags)
			appReprovisionWait = true
			reads := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req gqlRequest
				_ = json.NewDecoder(r.Body).Decode(&req)
				if strings.Contains(req.Query, "reprovisionApp") {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"reprovisionApp": reprovisionAccepted()}})
					return
				}
				reads++
				_, _ = fmt.Fprint(w, tc.reply)
			}))
			defer srv.Close()
			cmd, _ := appTestCmd()

			err := runAppReprovision(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web")
			if err == nil || !strings.Contains(err.Error(), tc.want) || reads != 1 {
				t.Fatalf("expected one read failing with %q, got %v after %d reads", tc.want, err, reads)
			}
		})
	}
}

func TestAppReprovisionRejectsANonPositiveTimeout(t *testing.T) {
	resetAppReprovisionFlags()
	t.Cleanup(resetAppReprovisionFlags)
	appReprovisionWait, appReprovisionTimeout = true, 0
	cmd, _ := appTestCmd()

	err := runAppReprovision(cmd, context.Background(), api.NewClient("http://127.0.0.1:1", "tok", false), "web")
	if err == nil || err.Error() != "--timeout must be positive" {
		t.Fatalf("expected a --timeout refusal before any request, got %v", err)
	}
}

// A positional slug would otherwise be ignored and the local astrolift.toml's
// app reprovisioned instead.
func TestAppReprovisionRefusesAPositionalSlug(t *testing.T) {
	if err := appReprovisionCmd.Args(appReprovisionCmd, []string{"web"}); err == nil {
		t.Fatal("app reprovision accepted a positional argument; the slug goes in --app")
	}
	if err := appReprovisionCmd.Args(appReprovisionCmd, nil); err != nil {
		t.Fatal(err)
	}
}

// ---- reachability in app list / app show -------------------------------------

// reachabilityServer serves app list and detail reads. missing names the
// fields this server generation lacks: a query selecting any of them fails
// validation with one error per field, as GraphQL reports them.
func reachabilityServer(t *testing.T, missing []string, sent *[]gqlRequest) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		*sent = append(*sent, req)
		var refused []map[string]string
		for _, field := range missing {
			if strings.Contains(req.Query, field) {
				refused = append(refused, map[string]string{"message": "Cannot query field '" + field + "' on type 'AstroliftRegisteredApp'."})
			}
		}
		if len(refused) > 0 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": refused})
			return
		}
		app := map[string]interface{}{
			"id": "app-1", "slug": "web", "name": "Web App", "organizationSlug": "acme",
			"provisioningStatus": "ready", "isActive": true, "sourceRepo": "acme/web",
		}
		if strings.Contains(req.Query, "reachabilityStatus") {
			app["reachabilityStatus"] = "NEEDS_REPROVISION"
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
			"astroliftApps": []interface{}{app}, "astroliftApp": app,
			"astroliftWorkloads": []interface{}{}, "astroliftEnvironments": []interface{}{},
		}})
	}))
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "tok", false)
}

// listShowTestCmd is appTestCmd with app list's --temporary flag and a
// separate stderr buffer.
func listShowTestCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd, out := appTestCmd()
	cmd.Flags().Bool("temporary", false, "")
	errOut := &bytes.Buffer{}
	cmd.SetErr(errOut)
	return cmd, out, errOut
}

func TestAppListAndShowPrintReachabilityNextToStatus(t *testing.T) {
	var sent []gqlRequest
	client := reachabilityServer(t, nil, &sent)
	cmd, out, errOut := listShowTestCmd()

	if err := runAppList(cmd, context.Background(), client); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	header, row := lines[0], lines[1]
	status, column, repo := strings.Index(header, "STATUS"), strings.Index(header, "REACHABILITY"), strings.Index(header, "REPO")
	if status >= column || column >= repo {
		t.Fatalf("REACHABILITY must follow STATUS:\n%s", out.String())
	}
	if strings.Index(row, "needs_reprovision") != column || !strings.Contains(row, "active") {
		t.Fatalf("row lacks the reachability under its column:\n%s", out.String())
	}

	out.Reset()
	if err := runAppShow(cmd, context.Background(), client, "web"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Status:       active\nReachability: needs_reprovision\n") {
		t.Fatalf("show must print reachability right after the status:\n%s", out.String())
	}

	out.Reset()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runAppList(cmd, context.Background(), client); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil || len(rows) != 1 || rows[0]["reachabilityStatus"] != "NEEDS_REPROVISION" {
		t.Fatalf("list JSON must keep the server's value: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := runAppShow(cmd, context.Background(), client, "web"); err != nil {
		t.Fatal(err)
	}
	var detail struct {
		App map[string]interface{} `json:"astroliftApp"`
	}
	if err := json.Unmarshal(out.Bytes(), &detail); err != nil || detail.App["reachabilityStatus"] != "NEEDS_REPROVISION" {
		t.Fatalf("show JSON must keep the server's value: %v\n%s", err, out.String())
	}
	if len(sent) != 4 || errOut.Len() != 0 {
		t.Fatalf("a current server answers each read once: requests=%d stderr=%s", len(sent), errOut.String())
	}
}

// An older server rejects reachabilityStatus at validation. The reads fall
// back to what the CLI asked before, without a false note about expiry when
// the server has it, and refuse --temporary only when it does not.
func TestAppListAndShowTolerateServersWithoutReachability(t *testing.T) {
	for _, tc := range []struct {
		name    string
		missing []string
		expiry  bool
		reads   int
	}{
		{"without reachability", []string{"reachabilityStatus"}, true, 2},
		{"without reachability or expiry", []string{"reachabilityStatus", "expiresAt"}, false, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent []gqlRequest
			client := reachabilityServer(t, tc.missing, &sent)
			cmd, out, errOut := listShowTestCmd()

			if err := runAppList(cmd, context.Background(), client); err != nil {
				t.Fatalf("list on an older server: %v", err)
			}
			lines := strings.Split(out.String(), "\n")
			if column := strings.Index(lines[0], "REACHABILITY"); column < 0 || lines[1][column] != '-' {
				t.Fatalf("an unreported reachability reads -:\n%s", out.String())
			}
			notes := 0
			if !tc.expiry {
				notes = 1
			}
			if len(sent) != tc.reads || strings.Count(errOut.String(), expiryUnavailable) != notes {
				t.Fatalf("requests=%d stderr=%q", len(sent), errOut.String())
			}

			if err := cmd.Flags().Set("temporary", "true"); err != nil {
				t.Fatal(err)
			}
			if err := runAppList(cmd, context.Background(), client); (err == nil) != tc.expiry {
				t.Fatalf("--temporary: %v (server has expiry: %t)", err, tc.expiry)
			}
			if err := cmd.Flags().Set("temporary", "false"); err != nil {
				t.Fatal(err)
			}

			out.Reset()
			if err := runAppShow(cmd, context.Background(), client, "web"); err != nil {
				t.Fatalf("show on an older server: %v", err)
			}
			if strings.Contains(out.String(), "Reachability:") || !strings.Contains(out.String(), "Status:       active") {
				t.Fatalf("show output:\n%s", out.String())
			}
			out.Reset()
			if err := cmd.Flags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
			if err := runAppShow(cmd, context.Background(), client, "web"); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out.String(), "reachabilityStatus") ||
				!strings.Contains(out.String(), fmt.Sprintf(`"expiryAvailable": %t`, tc.expiry)) {
				t.Fatalf("show JSON:\n%s", out.String())
			}
		})
	}
}

func TestAppReadsWithReachabilityNeverFallBackOnARealFailure(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]interface{}{
			{"message": "Cannot query field 'reachabilityStatus' on type 'AstroliftRegisteredApp'.", "extensions": map[string]string{"code": "PERMISSION_DENIED"}},
		}})
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "tok", false)
	cmd, _, _ := listShowTestCmd()

	if err := runAppList(cmd, context.Background(), client); err == nil {
		t.Fatal("list swallowed a refusal")
	}
	if err := runAppShow(cmd, context.Background(), client, "web"); err == nil {
		t.Fatal("show swallowed a refusal")
	}
	if calls != 2 {
		t.Fatalf("a refusal must not be retried: %d requests", calls)
	}
}

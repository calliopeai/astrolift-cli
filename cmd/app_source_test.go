package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

// ConflictHQ/fossilhub became ConflictHQ/fossilrepos on GitHub (platform #2478).
const (
	renamedFrom = "ConflictHQ/fossilhub"
	renamedTo   = "ConflictHQ/fossilrepos"
)

// setSourceServer answers the app read with app and setAppSource with result.
func setSourceServer(t *testing.T, app, result map[string]interface{}, seen *[]gqlRequest) *api.Client {
	t.Helper()
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		*seen = append(*seen, req)
		if strings.Contains(req.Query, "astroliftApp(") {
			return map[string]interface{}{"astroliftApp": app}
		}
		return map[string]interface{}{"setAppSource": result}
	})
	t.Cleanup(srv.Close)
	// Not "tok": the client redacts its credential from responses, and the
	// server's messages say "token".
	return api.NewClient(srv.URL, "cli-credential", false)
}

func fossilApp() map[string]interface{} {
	return map[string]interface{}{"id": testAppGUID, "slug": "fossil", "version": 4, "sourceRepo": renamedFrom}
}

func sourceChanged(previous, now string, autowire map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{
			"previousSourceRepo": previous,
			"app": map[string]interface{}{
				"id": testAppGUID, "slug": "fossil", "sourceRepo": now,
				"sourceUrl": "https://github.com/" + now, "autowire": autowire,
			},
		},
	}
}

func wired(secrets, detail string) map[string]interface{} {
	return map[string]interface{}{
		"connected": true, "ciWorkflow": "ok", "webhook": "ok", "secrets": secrets, "detail": detail,
	}
}

func setSourceRequest(t *testing.T, seen []gqlRequest) gqlRequest {
	t.Helper()
	for _, req := range seen {
		if strings.Contains(req.Query, "setAppSource(") {
			return req
		}
	}
	t.Fatal("setAppSource was not sent")
	return gqlRequest{}
}

func TestSetSourceSendsTheRepositoryAgainstTheReadVersion(t *testing.T) {
	var seen []gqlRequest
	client := setSourceServer(t, fossilApp(), sourceChanged(renamedFrom, renamedTo, wired("ok", "")), &seen)

	cmd, out := appTestCmd()
	if err := runAppSetSource(cmd, context.Background(), client, "fossil", "  conflicthq/FossilRepos "); err != nil {
		t.Fatalf("runAppSetSource: %v", err)
	}
	req := setSourceRequest(t, seen)
	input, _ := req.Variables["input"].(map[string]interface{})
	if input["id"] != testAppGUID || input["sourceRepo"] != "conflicthq/FossilRepos" || input["ifMatchVersion"] != float64(4) {
		t.Errorf("setAppSource input = %v", input)
	}
	for _, field := range []string{"previousSourceRepo", "autowire { connected ciWorkflow webhook secrets detail }"} {
		if !strings.Contains(req.Query, field) {
			t.Errorf("setAppSource does not select %q:\n%s", field, req.Query)
		}
	}
	got := out.String()
	for _, want := range []string{
		"fossil: source ConflictHQ/fossilhub -> ConflictHQ/fossilrepos",
		"Autowire: CI workflow ok, webhook ok, secrets ok",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "astro ci setup") {
		t.Errorf("a fully wired app was told to retry:\n%s", got)
	}
}

func TestSetSourceNamesAFailedStepAndTheRetry(t *testing.T) {
	var seen []gqlRequest
	detail := "secrets: GitHub returned 403"
	client := setSourceServer(t, fossilApp(), sourceChanged(renamedFrom, renamedTo, wired("error", detail)), &seen)

	cmd, out := appTestCmd()
	if err := runAppSetSource(cmd, context.Background(), client, "fossil", renamedTo); err != nil {
		t.Fatalf("runAppSetSource: %v", err)
	}
	got := out.String()
	for _, want := range []string{"secrets error", detail, "Retry the wiring with: astro ci setup fossil"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestSetSourceSaysWhenNothingCouldBeWired(t *testing.T) {
	var seen []gqlRequest
	unconnected := map[string]interface{}{
		"connected": false, "ciWorkflow": "missing", "webhook": "missing", "secrets": "missing", "detail": "",
	}
	client := setSourceServer(t, fossilApp(), sourceChanged(renamedFrom, renamedTo, unconnected), &seen)

	cmd, out := appTestCmd()
	if err := runAppSetSource(cmd, context.Background(), client, "fossil", renamedTo); err != nil {
		t.Fatalf("runAppSetSource: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Nothing was wired") || !strings.Contains(got, "astro ci setup fossil") {
		t.Errorf("output:\n%s", got)
	}
	if strings.Contains(got, "Autowire:") {
		t.Errorf("reported steps that never ran:\n%s", got)
	}
}

func TestSetSourceUnchangedSaysSo(t *testing.T) {
	var seen []gqlRequest
	client := setSourceServer(t, fossilApp(), sourceChanged(renamedFrom, renamedFrom, wired("ok", "")), &seen)

	cmd, out := appTestCmd()
	if err := runAppSetSource(cmd, context.Background(), client, "fossil", renamedFrom); err != nil {
		t.Fatalf("runAppSetSource: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "fossil already points at ConflictHQ/fossilhub; nothing changed.") || strings.Contains(got, "Autowire:") {
		t.Errorf("output:\n%s", got)
	}
}

func TestSetSourceStaleVersionSavesNothing(t *testing.T) {
	var seen []gqlRequest
	client := setSourceServer(t, fossilApp(), map[string]interface{}{
		"ok": false, "data": nil,
		"errors": []map[string]interface{}{{"code": "VERSION_MISMATCH", "message": ""}},
	}, &seen)

	cmd, _ := appTestCmd()
	err := runAppSetSource(cmd, context.Background(), client, "fossil", renamedTo)
	if err == nil || !strings.Contains(err.Error(), "fossil changed while this command ran") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetSourceSurfacesARefusal(t *testing.T) {
	var seen []gqlRequest
	refusal := renamedTo + " is not in the GitHub App installation on ConflictHQ. " +
		"Give the App access to the repository on GitHub, then retry."
	client := setSourceServer(t, fossilApp(), map[string]interface{}{
		"ok": false, "data": nil,
		"errors": []map[string]interface{}{{"code": "PRECONDITION", "field": "sourceRepo", "message": refusal}},
	}, &seen)

	cmd, out := appTestCmd()
	err := runAppSetSource(cmd, context.Background(), client, "fossil", renamedTo)
	if err == nil || err.Error() != "setAppSource failed: sourceRepo: "+refusal {
		t.Fatalf("err = %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("printed on a refusal:\n%s", out.String())
	}
}

func TestSetSourceUnknownApp(t *testing.T) {
	var seen []gqlRequest
	client := setSourceServer(t, nil, nil, &seen)

	cmd, _ := appTestCmd()
	err := runAppSetSource(cmd, context.Background(), client, "nope", renamedTo)
	if err == nil || !strings.Contains(err.Error(), `app "nope" not found`) {
		t.Fatalf("err = %v", err)
	}
	for _, req := range seen {
		if strings.Contains(req.Query, "setAppSource(") {
			t.Fatal("setAppSource sent for an app that was not found")
		}
	}
}

func TestSetSourceRefusesAnEmptyRepository(t *testing.T) {
	cmd, _ := appTestCmd()
	err := runAppSetSource(cmd, context.Background(), nil, "fossil", " ")
	if err == nil || !strings.Contains(err.Error(), "--source-repo owner/name") {
		t.Fatalf("err = %v", err)
	}
}

func TestSetSourceJSON(t *testing.T) {
	var seen []gqlRequest
	client := setSourceServer(t, fossilApp(), sourceChanged(renamedFrom, renamedTo, wired("ok", "")), &seen)

	cmd, out := appTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runAppSetSource(cmd, context.Background(), client, "fossil", renamedTo); err != nil {
		t.Fatalf("runAppSetSource: %v", err)
	}
	var got appSourceChange
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	if got.PreviousSourceRepo != renamedFrom || got.App.SourceRepo != renamedTo ||
		got.App.Autowire == nil || got.App.Autowire.Secrets != "ok" {
		t.Errorf("JSON = %+v", got)
	}
	if strings.Contains(out.String(), "Autowire:") {
		t.Errorf("--json printed text:\n%s", out.String())
	}
}

func TestSetSourceNeedsTheSlugAndTheRepository(t *testing.T) {
	if err := appSetSourceCmd.Args(appSetSourceCmd, []string{"fossil"}); err != nil {
		t.Fatal(err)
	}
	if err := appSetSourceCmd.Args(appSetSourceCmd, nil); err == nil {
		t.Error("app set-source accepted no app slug")
	}
	flag := appSetSourceCmd.Flags().Lookup("source-repo")
	if flag == nil || flag.Annotations["cobra_annotation_bash_completion_one_required_flag"] == nil {
		t.Error("--source-repo is not a required flag")
	}
}

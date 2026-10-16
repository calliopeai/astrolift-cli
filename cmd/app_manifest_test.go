package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

const testAppGUID = "7f3c1a52-0d4e-4b8a-9a61-2f0c5e8b1d23"

// manifestServer answers the slug lookup with testAppGUID and the named
// mutation with result, recording every request it sees.
func manifestServer(t *testing.T, mutation string, result map[string]interface{}, seen *[]gqlRequest) *api.Client {
	t.Helper()
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		*seen = append(*seen, req)
		if strings.Contains(req.Query, "astroliftApp(") {
			return map[string]interface{}{"astroliftApp": map[string]interface{}{"id": testAppGUID}}
		}
		return map[string]interface{}{mutation: result}
	})
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "tok", false)
}

func withManifestYes(t *testing.T, yes bool) {
	t.Helper()
	prev := appManifestYes
	appManifestYes = yes
	t.Cleanup(func() { appManifestYes = prev })
}

func mutationInputID(t *testing.T, seen []gqlRequest, mutation string) string {
	t.Helper()
	for _, req := range seen {
		if strings.Contains(req.Query, mutation+"(") {
			input, _ := req.Variables["input"].(map[string]interface{})
			id, _ := input["id"].(string)
			return id
		}
	}
	t.Fatalf("no %s request was sent", mutation)
	return ""
}

func TestAppManifestPushPrintsPullRequest(t *testing.T) {
	withManifestYes(t, true)
	var seen []gqlRequest
	client := manifestServer(t, "pushManifestToRepo", map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{
			"id": testAppGUID, "prUrl": "https://github.com/acme/web/pull/7",
			"branchName": "astrolift/manifest-web", "note": "pushed",
		},
	}, &seen)

	cmd, out := appTestCmd()
	if err := runAppManifestPush(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppManifestPush: %v", err)
	}
	if got := mutationInputID(t, seen, "pushManifestToRepo"); got != testAppGUID {
		t.Errorf("input id = %q, want the looked-up GUID", got)
	}
	got := out.String()
	for _, want := range []string{
		"Opened a pull request for web: https://github.com/acme/web/pull/7",
		"branch: astrolift/manifest-web",
		"[env] table are not in it",
		"astro app manifest pull web",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestAppManifestPushEnvOnlyPushesNothing(t *testing.T) {
	withManifestYes(t, true)
	var seen []gqlRequest
	client := manifestServer(t, "pushManifestToRepo", map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{"id": testAppGUID, "prUrl": "", "branchName": "", "note": "env_only"},
	}, &seen)

	cmd, out := appTestCmd()
	if err := runAppManifestPush(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppManifestPush: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "Nothing to push") || !strings.Contains(got, "never written to the repo") {
		t.Errorf("output:\n%s", got)
	}
	if strings.Contains(got, "Opened a pull request") {
		t.Errorf("env_only reported a pull request:\n%s", got)
	}
}

func TestAppManifestPushNothingStaged(t *testing.T) {
	withManifestYes(t, true)
	var seen []gqlRequest
	client := manifestServer(t, "pushManifestToRepo", map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{"id": testAppGUID, "prUrl": "", "branchName": "", "note": "nothing_to_push"},
	}, &seen)

	cmd, out := appTestCmd()
	if err := runAppManifestPush(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppManifestPush: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "web has no staged manifest draft") {
		t.Errorf("output:\n%s", got)
	}
}

func TestAppManifestPushRequiresYesWithoutPrompts(t *testing.T) {
	withManifestYes(t, false)
	var seen []gqlRequest
	client := manifestServer(t, "pushManifestToRepo", map[string]interface{}{"ok": true}, &seen)

	cmd, _ := appTestCmd()
	if err := cmd.Flags().Set("no-prompt", "true"); err != nil {
		t.Fatal(err)
	}
	err := runAppManifestPush(cmd, context.Background(), client, "web")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("err = %v, want a --yes refusal", err)
	}
	for _, req := range seen {
		if strings.Contains(req.Query, "pushManifestToRepo(") {
			t.Fatal("the mutation was sent without confirmation")
		}
	}
}

func TestAppManifestPullDeclinedPromptSendsNothing(t *testing.T) {
	withManifestYes(t, false)
	var seen []gqlRequest
	client := manifestServer(t, "syncManifestFromRepo", map[string]interface{}{"ok": true}, &seen)

	cmd, out := appTestCmd()
	cmd.SetIn(strings.NewReader("n\n"))
	if err := runAppManifestPull(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppManifestPull: %v", err)
	}
	if !strings.Contains(out.String(), "Aborted.") {
		t.Errorf("output:\n%s", out.String())
	}
	for _, req := range seen {
		if strings.Contains(req.Query, "syncManifestFromRepo(") {
			t.Fatal("the mutation was sent after the prompt was declined")
		}
	}
}

func TestAppManifestPullReportsKeptSecrets(t *testing.T) {
	withManifestYes(t, true)
	var seen []gqlRequest
	client := manifestServer(t, "syncManifestFromRepo", map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{
			"id": testAppGUID, "syncState": "in_sync", "rawManifest": "[app]\n",
			"rawManifestStaged": "[app]\n[env]\nAPI_KEY = \"s3cret\"\n",
		},
	}, &seen)

	cmd, out := appTestCmd()
	if err := runAppManifestPull(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppManifestPull: %v", err)
	}
	if got := mutationInputID(t, seen, "syncManifestFromRepo"); got != testAppGUID {
		t.Errorf("input id = %q, want the looked-up GUID", got)
	}
	got := out.String()
	for _, want := range []string{"Pulled the manifest for web from its repo (sync state: in_sync).", "were kept"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "s3cret") {
		t.Errorf("output prints a staged value:\n%s", got)
	}
}

func TestAppManifestPullJSONLeavesManifestOut(t *testing.T) {
	withManifestYes(t, true)
	var seen []gqlRequest
	client := manifestServer(t, "syncManifestFromRepo", map[string]interface{}{
		"ok": true, "errors": []interface{}{},
		"data": map[string]interface{}{
			"id": testAppGUID, "syncState": "in_sync",
			"rawManifestStaged": "[env]\nAPI_KEY = \"s3cret\"\n",
		},
	}, &seen)

	cmd, out := appTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runAppManifestPull(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppManifestPull: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"syncState": "in_sync"`) || strings.Contains(got, "s3cret") {
		t.Errorf("JSON output:\n%s", got)
	}
}

func TestAppManifestPullSurfacesDiverged(t *testing.T) {
	withManifestYes(t, true)
	var seen []gqlRequest
	client := manifestServer(t, "syncManifestFromRepo", map[string]interface{}{
		"ok": false, "data": nil,
		"errors": []map[string]interface{}{{"code": "SCM_DIVERGED", "message": "staged drafts would be clobbered by repo content"}},
	}, &seen)

	cmd, _ := appTestCmd()
	err := runAppManifestPull(cmd, context.Background(), client, "web")
	if err == nil || !strings.Contains(err.Error(), "clobbered") {
		t.Fatalf("err = %v, want the server's refusal", err)
	}
}

func TestAppManifestUnknownApp(t *testing.T) {
	withManifestYes(t, true)
	srv := gqlServer(t, map[string]interface{}{"astroliftApp": nil}, nil)
	defer srv.Close()

	cmd, _ := appTestCmd()
	err := runAppManifestPush(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "nope")
	if err == nil || !strings.Contains(err.Error(), `app "nope" not found`) {
		t.Fatalf("err = %v, want not found", err)
	}
}

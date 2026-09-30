package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// chdir changes into dir for the duration of the test (go1.23-safe; the
// stdlib t.Chdir is go1.24+ and the module targets go1.23).
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// appTestCmd builds a command carrying the persistent + local flags the app
// run-funcs and resolveAppSlug inspect, with a buffer captured for stdout.
func appTestCmd() (*cobra.Command, *bytes.Buffer) {
	root := &cobra.Command{}
	root.PersistentFlags().Bool("no-prompt", false, "")
	c := &cobra.Command{}
	c.Flags().String("app", "", "")
	c.Flags().String("project", "", "")
	c.Flags().String("org", "", "")
	c.Flags().Bool("no-prompt", false, "")
	c.Flags().Bool("debug", false, "")
	c.Flags().Bool("json", false, "")
	root.AddCommand(c)
	out := &bytes.Buffer{}
	c.SetOut(out)
	return c, out
}

// ---- splitExecArgs ---------------------------------------------------------

func TestSplitExecArgs(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		dash     int
		workload string
		command  []string
		wantErr  bool
	}{
		{"workload + command", []string{"web", "python", "manage.py"}, 1, "web", []string{"python", "manage.py"}, false},
		{"command only (no workload)", []string{"sh"}, 0, "", []string{"sh"}, false},
		{"workload only, no dash", []string{"web"}, -1, "web", nil, false},
		{"nothing", []string{}, -1, "", nil, false},
		{"too many before dash", []string{"web", "extra", "ls"}, 2, "", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, cmd, err := splitExecArgs(c.args, c.dash)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %v", c.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if w != c.workload {
				t.Errorf("workload = %q, want %q", w, c.workload)
			}
			if strings.Join(cmd, " ") != strings.Join(c.command, " ") {
				t.Errorf("command = %v, want %v", cmd, c.command)
			}
		})
	}
}

// ---- resolveAppSlug --------------------------------------------------------

func TestResolveAppSlugExplicitWins(t *testing.T) {
	cmd, _ := appTestCmd()
	_ = cmd.Flags().Set("app", "from-flag")
	got, err := resolveAppSlug(cmd, "explicit")
	if err != nil {
		t.Fatalf("resolveAppSlug: %v", err)
	}
	if got != "explicit" {
		t.Errorf("got %q, want explicit (explicit must win over --app)", got)
	}
}

func TestResolveAppSlugFromFlag(t *testing.T) {
	cmd, _ := appTestCmd()
	_ = cmd.Flags().Set("app", "from-flag")
	got, err := resolveAppSlug(cmd, "")
	if err != nil {
		t.Fatalf("resolveAppSlug: %v", err)
	}
	if got != "from-flag" {
		t.Errorf("got %q, want from-flag", got)
	}
}

func TestResolveAppSlugFromManifest(t *testing.T) {
	dir := t.TempDir()
	manifest := "[app]\nslug = \"manifest-app\"\ndisplay_name = \"Manifest App\"\n"
	if err := os.WriteFile(filepath.Join(dir, "astrolift.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	cmd, _ := appTestCmd()
	got, err := resolveAppSlug(cmd, "")
	if err != nil {
		t.Fatalf("resolveAppSlug: %v", err)
	}
	if got != "manifest-app" {
		t.Errorf("got %q, want manifest-app", got)
	}
}

func TestResolveAppSlugNoneErrors(t *testing.T) {
	chdir(t, t.TempDir()) // no astrolift.toml here
	cmd, _ := appTestCmd()
	if _, err := resolveAppSlug(cmd, ""); err == nil {
		t.Fatal("expected error when no app can be resolved")
	}
}

// ---- app list --------------------------------------------------------------

func appListData() map[string]interface{} {
	return map[string]interface{}{
		"astroliftApps": []map[string]interface{}{
			{
				"slug": "web", "name": "Web App", "provisioningStatus": "ready",
				"isActive": true, "isArchived": false,
				"sourceKind": "github", "sourceRepo": "acme/web",
				"lastDeployedAt": "2026-06-20T10:00:00+00:00",
				"latestDeployment": map[string]interface{}{
					"id": "dep-1", "status": "running", "environmentName": "production",
					"imageTag": "sha-abc", "createdAt": "2026-06-20T10:00:00+00:00",
				},
			},
			{
				"slug": "api", "name": "API", "provisioningStatus": "provisioning",
				"isActive": false, "isArchived": false,
				"sourceKind": "github", "sourceRepo": "acme/api",
				"lastDeployedAt": nil, "latestDeployment": nil,
			},
		},
	}
}

func TestAppListRendersTable(t *testing.T) {
	srv := gqlServer(t, appListData(), nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runAppList: %v", err)
	}
	got := out.String()
	for _, want := range []string{"web", "Web App", "active", "acme/web", "2026-06-20T10:00:00", "api", "provisioning", "2 app(s)."} {
		if !strings.Contains(got, want) {
			t.Errorf("list output missing %q:\n%s", want, got)
		}
	}
}

func TestAppListJSON(t *testing.T) {
	srv := gqlServer(t, appListData(), nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runAppList: %v", err)
	}
	var rows []appListItem
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out.String())
	}
	if len(rows) != 2 || rows[0].Slug != "web" || rows[0].LatestDeployment == nil || rows[0].LatestDeployment.Status != "running" {
		t.Errorf("decoded rows wrong: %+v", rows)
	}
}

func TestAppListForwardsFilters(t *testing.T) {
	appListSearch = "we"
	defer func() { appListSearch = "" }()

	var captured gqlRequest
	srv := gqlServer(t, appListData(), &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := appTestCmd()
	_ = cmd.Flags().Set("project", "platform")
	if err := runAppList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runAppList: %v", err)
	}
	if captured.Variables["search"] != "we" {
		t.Errorf("search var = %v, want we", captured.Variables["search"])
	}
	if captured.Variables["projectSlug"] != "platform" {
		t.Errorf("projectSlug var = %v, want platform", captured.Variables["projectSlug"])
	}
}

func TestAppListEmpty(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{"astroliftApps": []map[string]interface{}{}}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runAppList: %v", err)
	}
	if !strings.Contains(out.String(), "No apps found.") {
		t.Errorf("expected empty message, got:\n%s", out.String())
	}
}

// ---- app show --------------------------------------------------------------

func appShowData() map[string]interface{} {
	return map[string]interface{}{
		"astroliftApp": map[string]interface{}{
			"id": "app-1", "slug": "web", "name": "Web App", "description": "the web tier",
			"organizationSlug": "acme", "projectSlug": "platform", "teamSlug": "core",
			"sourceKind": "github", "sourceRepo": "acme/web", "sourceUrl": "https://github.com/acme/web",
			"defaultBranch": "main", "buildMode": "dockerfile",
			"k8sNamespace": "acme-web", "subdomain": "web", "managedHostname": "web.acme.example.com",
			"isActive": true, "isArchived": false, "provisioningStatus": "ready", "provisioningError": "",
			"lastDeployedAt": "2026-06-20T10:00:00+00:00",
			"latestDeployment": map[string]interface{}{
				"id": "dep-1", "status": "running", "environmentName": "production",
				"imageTag": "sha-abc", "createdAt": "2026-06-20T10:00:00+00:00",
			},
		},
		"astroliftWorkloads": []map[string]interface{}{
			{"slug": "web", "name": "web", "kind": "deployment", "replicas": 2,
				"cpuLimit": "500m", "memoryLimit": "512Mi", "isPublic": true},
		},
		"astroliftEnvironments": []map[string]interface{}{
			{"name": "production", "url": "https://web.acme.example.com", "deploysPaused": false, "clusterSlug": "prod-us"},
		},
	}
}

func TestAppShowRenders(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, appShowData(), &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppShow(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppShow: %v", err)
	}
	if captured.Variables["slug"] != "web" {
		t.Errorf("slug var = %v, want web", captured.Variables["slug"])
	}
	got := out.String()
	for _, want := range []string{
		"Web App (web)", "the web tier", "active", "acme / platform",
		"github acme/web", "main", "dockerfile", "acme-web", "web.acme.example.com",
		"Latest deployment:", "running", "image=sha-abc",
		"Workloads:", "deployment", "Environments:", "production", "prod-us",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("show output missing %q:\n%s", want, got)
		}
	}
}

func TestAppShowNotFound(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftApp": nil, "astroliftWorkloads": []interface{}{}, "astroliftEnvironments": []interface{}{},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := appTestCmd()
	err := runAppShow(cmd, context.Background(), client, "ghost")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestAppShowJSON(t *testing.T) {
	srv := gqlServer(t, appShowData(), nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppShow(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppShow: %v", err)
	}
	var result appShowResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if result.App == nil || result.App.Slug != "web" || len(result.Workloads) != 1 || len(result.Environments) != 1 {
		t.Errorf("decoded show result wrong: %+v", result)
	}
}

// ---- app deploy ------------------------------------------------------------

func resetDeployFlags() {
	appDeployEnv = "production"
	appDeployImageTag = ""
	appDeployRef = ""
	appDeployWait = false
}

func TestAppDeployDefersImageRequirementToServer(t *testing.T) {
	resetDeployFlags()
	defer resetDeployFlags()
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"startDeployment": map[string]interface{}{
			"ok":     false,
			"errors": []interface{}{map[string]interface{}{"code": "VALIDATION", "message": "image_tag is required for ci_pushed apps", "field": "imageTag"}},
		},
	}, &captured)
	defer srv.Close()
	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := appTestCmd()
	err := runAppDeploy(cmd, context.Background(), client, "web")
	if err == nil || !strings.Contains(err.Error(), "image_tag is required for ci_pushed") {
		t.Fatalf("expected server image-tag requirement, got %v", err)
	}
	input := captured.Variables["input"].(map[string]interface{})
	if _, exists := input["imageTag"]; exists {
		t.Fatalf("must not invent a tag: %+v", input)
	}
}

func TestAppDeployWithoutImageTag(t *testing.T) {
	for _, ref := range []string{"", "release/v2", "v2.0.0", strings.Repeat("a", 40)} {
		t.Run(ref, func(t *testing.T) {
			resetDeployFlags()
			defer resetDeployFlags()
			appDeployRef = ref
			var captured gqlRequest
			sha := strings.Repeat("a", 40)
			srv := gqlServer(t, map[string]interface{}{
				"startDeployment": map[string]interface{}{
					"ok": true, "errors": []interface{}{},
					"data": map[string]interface{}{"id": "dep-built", "status": "pending", "environmentName": "production", "imageTag": sha, "commitSha": sha},
				},
			}, &captured)
			defer srv.Close()
			cmd, out := appTestCmd()
			if err := runAppDeploy(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
				t.Fatal(err)
			}
			input := captured.Variables["input"].(map[string]interface{})
			if _, exists := input["imageTag"]; exists {
				t.Fatalf("omitted image tag sent as an override: %+v", input)
			}
			if ref == "" {
				if _, exists := input["sourceRef"]; exists {
					t.Fatal("default ref must be resolved by the server")
				}
			} else if input["sourceRef"] != ref {
				t.Fatalf("source ref missing: %+v", input)
			}
			if !strings.Contains(out.String(), "Source commit:      "+sha) {
				t.Fatalf("resolved source missing: %s", out.String())
			}
		})
	}
}

func TestAppDeploySendsMutation(t *testing.T) {
	resetDeployFlags()
	appDeployImageTag = "sha-deadbeef"
	appDeployEnv = "staging"
	defer resetDeployFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"startDeployment": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"id": "dep-99", "status": "pending", "environmentName": "staging",
				"imageTag": "sha-deadbeef", "registeredAppSlug": "web",
				"triggerKind": "manual", "createdAt": "2026-06-20T10:00:00+00:00",
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppDeploy(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppDeploy: %v", err)
	}

	input, ok := captured.Variables["input"].(map[string]interface{})
	if !ok {
		t.Fatalf("input var missing/not an object: %v", captured.Variables["input"])
	}
	if input["appSlug"] != "web" || input["environmentName"] != "staging" ||
		input["imageTag"] != "sha-deadbeef" || input["triggerKind"] != "manual" {
		t.Errorf("deploy input wrong: %+v", input)
	}
	got := out.String()
	if !strings.Contains(got, "Deployment started: dep-99") || !strings.Contains(got, "staging") {
		t.Errorf("deploy output wrong:\n%s", got)
	}
}

func TestAppDeployExplicitTagAndRef(t *testing.T) {
	resetDeployFlags()
	defer resetDeployFlags()
	if err := appDeployCmd.Flags().Set("ref", " release/v2 "); err != nil {
		t.Fatal(err)
	}
	appDeployImageTag = " custom-tag "
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"startDeployment": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"id": "dep-1", "status": "pending", "imageTag": "custom-tag", "commitSha": strings.Repeat("a", 40), "branch": "release/v2"},
		},
	}, &captured)
	defer srv.Close()
	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppDeploy(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatal(err)
	}
	input := captured.Variables["input"].(map[string]interface{})
	if input["imageTag"] != "custom-tag" || input["sourceRef"] != "release/v2" {
		t.Fatalf("explicit inputs lost: %+v", input)
	}
	var result deploymentSummary
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.CommitSHA != strings.Repeat("a", 40) || result.Branch != "release/v2" {
		t.Fatalf("resolved revision lost in JSON: %+v", result)
	}
}

func TestAppDeployManifestImagesWithoutTag(t *testing.T) {
	resetDeployFlags()
	defer resetDeployFlags()
	appDeployImageTag = "  "
	appDeployRef = "  "
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"startDeployment": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{"id": "dep-1", "status": "pending", "imageTag": "", "commitSha": ""},
		},
	}, &captured)
	defer srv.Close()
	cmd, out := appTestCmd()
	if err := runAppDeploy(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatal(err)
	}
	input := captured.Variables["input"].(map[string]interface{})
	for _, key := range []string{"imageTag", "sourceRef"} {
		if _, exists := input[key]; exists {
			t.Fatalf("empty override sent: %+v", input)
		}
	}
	if strings.Contains(out.String(), "Source commit:") || !strings.Contains(out.String(), "Image:              -") {
		t.Fatalf("unexpected manifest image output: %s", out.String())
	}
}

func TestAppDeploySurfacesMutationError(t *testing.T) {
	resetDeployFlags()
	appDeployImageTag = "sha-1"
	defer resetDeployFlags()

	srv := gqlServer(t, map[string]interface{}{
		"startDeployment": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"code": "precondition", "message": "environment is locked", "field": "environmentName"},
			},
			"data": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := appTestCmd()
	err := runAppDeploy(cmd, context.Background(), client, "web")
	if err == nil || !strings.Contains(err.Error(), "environment is locked") {
		t.Fatalf("expected mutation error surfaced, got %v", err)
	}
}

// deployWaitServer replies to startDeployment then walks astroliftDeployment
// through the given status sequence on successive polls.
func deployWaitServer(t *testing.T, statuses []string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	i := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req gqlRequest
		_ = json.Unmarshal(body, &req)
		var data map[string]interface{}
		switch {
		case strings.Contains(req.Query, "startDeployment"):
			data = map[string]interface{}{"startDeployment": map[string]interface{}{
				"ok": true, "errors": []interface{}{},
				"data": map[string]interface{}{
					"id": "dep-1", "status": "pending", "environmentName": "production",
					"imageTag": "sha-1", "registeredAppSlug": "web", "triggerKind": "manual",
					"createdAt": "2026-06-20T10:00:00+00:00",
				},
			}}
		case strings.Contains(req.Query, "astroliftDeployment("):
			mu.Lock()
			status := statuses[i]
			if i < len(statuses)-1 {
				i++
			}
			mu.Unlock()
			data = map[string]interface{}{"astroliftDeployment": map[string]interface{}{
				"id": "dep-1", "status": status, "environmentName": "production", "imageTag": "sha-1",
			}}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}))
}

func TestAppDeployWaitSucceeds(t *testing.T) {
	resetDeployFlags()
	appDeployImageTag = "sha-1"
	appDeployWait = true
	defer resetDeployFlags()

	prev := appDeployPollInterval
	appDeployPollInterval = 5 * time.Millisecond
	defer func() { appDeployPollInterval = prev }()

	srv := deployWaitServer(t, []string{"deploying", "running"})
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppDeploy(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppDeploy --wait: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "→ deploying") || !strings.Contains(got, "Final status: running") {
		t.Errorf("wait output wrong:\n%s", got)
	}
}

func TestAppDeployWaitFails(t *testing.T) {
	resetDeployFlags()
	appDeployImageTag = "sha-1"
	appDeployWait = true
	defer resetDeployFlags()

	prev := appDeployPollInterval
	appDeployPollInterval = 5 * time.Millisecond
	defer func() { appDeployPollInterval = prev }()

	srv := deployWaitServer(t, []string{"deploying", "failed"})
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := appTestCmd()
	err := runAppDeploy(cmd, context.Background(), client, "web")
	if err == nil || !strings.Contains(err.Error(), `ended in "failed"`) {
		t.Fatalf("expected failed-deploy error, got %v", err)
	}
}

// ---- app rollback ----------------------------------------------------------

func TestAppRollbackExplicitID(t *testing.T) {
	appRollbackYes = true
	defer func() { appRollbackYes = false }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"rollbackDeployment": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"id": "dep-new", "status": "pending", "environmentName": "production",
				"imageTag": "sha-prior", "registeredAppSlug": "web", "triggerKind": "rollback",
				"createdAt": "2026-06-20T11:00:00+00:00",
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppRollback(cmd, context.Background(), client, "dep-running"); err != nil {
		t.Fatalf("runAppRollback: %v", err)
	}
	input, _ := captured.Variables["input"].(map[string]interface{})
	if input == nil || input["id"] != "dep-running" {
		t.Errorf("rollback input id = %v, want dep-running", captured.Variables["input"])
	}
	if !strings.Contains(out.String(), "Rollback started:   dep-new") {
		t.Errorf("rollback output wrong:\n%s", out.String())
	}
}

func TestAppRollbackResolvesRunningDeployment(t *testing.T) {
	appRollbackYes = true
	appRollbackEnv = "production"
	defer func() { appRollbackYes = false; appRollbackEnv = "production" }()

	var captured gqlRequest
	// Both queries hit the same handler; return both keys.
	srv := gqlServer(t, map[string]interface{}{
		"astroliftDeployments": []map[string]interface{}{
			{"id": "dep-3", "status": "deploying", "environmentName": "production", "imageTag": "sha-3", "createdAt": "2026-06-20T12:00:00+00:00"},
			{"id": "dep-2", "status": "running", "environmentName": "production", "imageTag": "sha-2", "createdAt": "2026-06-20T11:00:00+00:00"},
			{"id": "dep-1", "status": "superseded", "environmentName": "production", "imageTag": "sha-1", "createdAt": "2026-06-20T10:00:00+00:00"},
		},
		"rollbackDeployment": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"id": "dep-new", "status": "pending", "environmentName": "production",
				"imageTag": "sha-1", "registeredAppSlug": "web", "triggerKind": "rollback",
				"createdAt": "2026-06-20T12:30:00+00:00",
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("app", "web")
	if err := runAppRollback(cmd, context.Background(), client, ""); err != nil {
		t.Fatalf("runAppRollback: %v", err)
	}
	// The mutation (last request) must target the running deployment dep-2.
	input, _ := captured.Variables["input"].(map[string]interface{})
	if input == nil || input["id"] != "dep-2" {
		t.Errorf("rollback targeted %v, want the running deployment dep-2", captured.Variables["input"])
	}
	if !strings.Contains(out.String(), "Resolved running deployment for web/production: dep-2") {
		t.Errorf("missing resolution line:\n%s", out.String())
	}
}

func TestAppRollbackNoRunningDeployment(t *testing.T) {
	appRollbackYes = true
	defer func() { appRollbackYes = false }()

	srv := gqlServer(t, map[string]interface{}{
		"astroliftDeployments": []map[string]interface{}{
			{"id": "dep-1", "status": "superseded", "environmentName": "production", "imageTag": "sha-1", "createdAt": "2026-06-20T10:00:00+00:00"},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := appTestCmd()
	_ = cmd.Flags().Set("app", "web")
	err := runAppRollback(cmd, context.Background(), client, "")
	if err == nil || !strings.Contains(err.Error(), "no running deployment") {
		t.Fatalf("expected no-running-deployment error, got %v", err)
	}
}

func TestAppRollbackSurfacesMutationError(t *testing.T) {
	appRollbackYes = true
	defer func() { appRollbackYes = false }()

	srv := gqlServer(t, map[string]interface{}{
		"rollbackDeployment": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"code": "precondition", "message": "no prior superseded deployment to roll back to", "field": nil},
			},
			"data": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := appTestCmd()
	err := runAppRollback(cmd, context.Background(), client, "dep-running")
	if err == nil || !strings.Contains(err.Error(), "no prior superseded") {
		t.Fatalf("expected rollback precondition error, got %v", err)
	}
}

// ---- app promote -----------------------------------------------------------

func TestAppPromote(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"promoteDeployment": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"id": "dep-prom", "status": "pending", "environmentName": "production",
				"imageTag": "sha-staging", "registeredAppSlug": "web", "triggerKind": "promotion",
				"createdAt": "2026-06-20T13:00:00+00:00",
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppPromote(cmd, context.Background(), client, "web", "staging", "production"); err != nil {
		t.Fatalf("runAppPromote: %v", err)
	}
	input, _ := captured.Variables["input"].(map[string]interface{})
	if input == nil || input["appSlug"] != "web" ||
		input["sourceEnvironmentName"] != "staging" || input["targetEnvironmentName"] != "production" {
		t.Errorf("promote input = %v, want web staging→production", captured.Variables["input"])
	}
	if !strings.Contains(out.String(), "Promotion started:  dep-prom") ||
		!strings.Contains(out.String(), "staging → production") {
		t.Errorf("promote output wrong:\n%s", out.String())
	}
}

func TestAppPromoteRequiresFromTo(t *testing.T) {
	client := api.NewClient("http://unused", "tok", false)
	cmd, _ := appTestCmd()
	if err := runAppPromote(cmd, context.Background(), client, "web", "", "production"); err == nil ||
		!strings.Contains(err.Error(), "--from and --to are required") {
		t.Fatalf("expected --from/--to required error, got %v", err)
	}
}

// ---- app logs --------------------------------------------------------------

func resetLogsFlags() {
	appLogsEnv = ""
	appLogsSince = "1h"
	appLogsTail = 200
	appLogsFollow = false
	appLogsLevel = ""
	appLogsSearch = ""
}

func logPage(items []map[string]interface{}, nextCursor string) map[string]interface{} {
	return map[string]interface{}{
		"astroliftAppLogs": map[string]interface{}{
			"items": items, "nextCursor": nextCursor,
			"reachedRetention": false, "historicalAvailable": true, "totalCount": -1,
		},
	}
}

func logItem(ts, msg string) map[string]interface{} {
	return map[string]interface{}{
		"podName": "web-1", "container": "web", "timestamp": ts, "message": msg, "level": "info", "stream": "stdout",
	}
}

func TestAppLogsTail(t *testing.T) {
	resetLogsFlags()
	defer resetLogsFlags()

	var captured gqlRequest
	srv := gqlServer(t, logPage([]map[string]interface{}{
		logItem("2026-06-20T10:00:01Z", "first"),
		logItem("2026-06-20T10:00:02Z", "second"),
	}, ""), &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppLogs(cmd, context.Background(), client, "web", "worker"); err != nil {
		t.Fatalf("runAppLogs: %v", err)
	}
	if captured.Variables["appSlug"] != "web" || captured.Variables["workloadSlug"] != "worker" {
		t.Errorf("log vars wrong: appSlug=%v workloadSlug=%v", captured.Variables["appSlug"], captured.Variables["workloadSlug"])
	}
	if out.String() != "2026-06-20T10:00:01Z first\n2026-06-20T10:00:02Z second\n" {
		t.Errorf("log output wrong:\n%q", out.String())
	}
}

func TestAppLogsJSON(t *testing.T) {
	resetLogsFlags()
	defer resetLogsFlags()

	srv := gqlServer(t, logPage([]map[string]interface{}{
		logItem("2026-06-20T10:00:01Z", "first"),
	}, ""), nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAppLogs(cmd, context.Background(), client, "web", ""); err != nil {
		t.Fatalf("runAppLogs --json: %v", err)
	}
	var lines []appLogLine
	if err := json.Unmarshal(out.Bytes(), &lines); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out.String())
	}
	if len(lines) != 1 || lines[0].Message != "first" {
		t.Errorf("decoded log lines wrong: %+v", lines)
	}
}

func TestAppLogsPaginatesAndKeepsMostRecentTail(t *testing.T) {
	resetLogsFlags()
	appLogsTail = 3
	defer resetLogsFlags()

	var mu sync.Mutex
	pages := []map[string]interface{}{
		logPage([]map[string]interface{}{logItem("t1", "l1"), logItem("t2", "l2")}, "cursor-2"),
		logPage([]map[string]interface{}{logItem("t3", "l3"), logItem("t4", "l4")}, ""),
	}
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		page := pages[i]
		if i < len(pages)-1 {
			i++
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": page})
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppLogs(cmd, context.Background(), client, "web", ""); err != nil {
		t.Fatalf("runAppLogs: %v", err)
	}
	// tail=3 over 4 paginated lines keeps the most recent 3 (l2,l3,l4).
	if out.String() != "t2 l2\nt3 l3\nt4 l4\n" {
		t.Errorf("paginated tail wrong:\n%q", out.String())
	}
}

func TestAppLogsCappedWarns(t *testing.T) {
	resetLogsFlags()
	defer resetLogsFlags()

	// Always return a non-empty cursor → paginateAppLogs hits the page cap.
	errBuf := &bytes.Buffer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": logPage([]map[string]interface{}{logItem("t", "x")}, "more"),
		})
	}))
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := appTestCmd()
	cmd.SetErr(errBuf)
	if err := runAppLogs(cmd, context.Background(), client, "web", ""); err != nil {
		t.Fatalf("runAppLogs: %v", err)
	}
	if !strings.Contains(errBuf.String(), "narrow --since") {
		t.Errorf("expected capped warning on stderr, got:\n%s", errBuf.String())
	}
}

func TestAppLogsFollowPrintsOnlyNewLines(t *testing.T) {
	resetLogsFlags()
	appLogsFollow = true
	defer resetLogsFlags()

	prev := appLogsPollInterval
	appLogsPollInterval = 10 * time.Millisecond
	defer func() { appLogsPollInterval = prev }()

	// Snapshot grows each poll; --follow must print each line exactly once.
	var mu sync.Mutex
	snaps := [][]map[string]interface{}{
		{logItem("t1", "a"), logItem("t2", "b")},
		{logItem("t1", "a"), logItem("t2", "b"), logItem("t3", "c")},
		{logItem("t1", "a"), logItem("t2", "b"), logItem("t3", "c"), logItem("t4", "d")},
	}
	i := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		snap := snaps[i]
		if i < len(snaps)-1 {
			i++
		}
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": logPage(snap, "")})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppLogs(cmd, ctx, client, "web", ""); err != nil {
		t.Fatalf("runAppLogs --follow: %v", err)
	}
	if out.String() != "t1 a\nt2 b\nt3 c\nt4 d\n" {
		t.Errorf("follow de-dup wrong, got:\n%q", out.String())
	}
}

// ---- app deregister ----------------------------------------------------------

func deregisterPreviewData() map[string]interface{} {
	return map[string]interface{}{
		"appSlug": "web", "appName": "Web App", "totalResourceCount": 3,
		"k8sObjects": []map[string]interface{}{
			{"clusterSlug": "prd", "namespace": "app-web", "kind": "Deployment", "name": "web"},
		},
		"managedServices": []map[string]interface{}{
			{"name": "web-db", "kind": "postgres", "variant": "rds", "environmentName": "production", "status": "ready"},
		},
		"secretRefs":    []map[string]interface{}{},
		"deployTokens":  []map[string]interface{}{{"name": "ci", "last4": "ab12", "environmentName": nil}},
		"identityRoles": []map[string]interface{}{},
		"sourceWebhook": nil, "registryRepoUri": "123.dkr.ecr/web",
	}
}

func TestAppDeregisterRefusesWithoutYes(t *testing.T) {
	appDeregisterYes = false

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"previewAstroliftDeregister": deregisterPreviewData(),
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	err := runAppDeregister(cmd, context.Background(), client, "web")
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("expected refusal mentioning --yes, got: %v", err)
	}
	// Only the preview query fired — never the mutation.
	if strings.Contains(captured.Query, "deregisterAstroliftApp") {
		t.Errorf("mutation was sent without --yes:\n%s", captured.Query)
	}
	// The preview (what would be destroyed) was printed.
	if !strings.Contains(out.String(), "Resources to destroy: 3") ||
		!strings.Contains(out.String(), "web-db") {
		t.Errorf("preview inventory not printed:\n%s", out.String())
	}
}

func TestAppDeregisterSendsMutationWithYes(t *testing.T) {
	appDeregisterYes = true
	defer func() { appDeregisterYes = false }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"previewAstroliftDeregister": deregisterPreviewData(),
		"deregisterAstroliftApp": map[string]interface{}{
			"ok": true, "errors": []interface{}{},
			"data": map[string]interface{}{
				"workflowId":         "DeregisterAppWorkflow-guid-1",
				"stillLiveResources": []string{},
			},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := appTestCmd()
	if err := runAppDeregister(cmd, context.Background(), client, "web"); err != nil {
		t.Fatalf("runAppDeregister: %v", err)
	}

	// The mutation carried the slug and the preview's appName as the
	// typed-confirmation value.
	input, _ := captured.Variables["input"].(map[string]interface{})
	if input == nil || input["appSlug"] != "web" || input["confirmName"] != "Web App" {
		t.Errorf("deregister input = %v, want appSlug=web confirmName=Web App", captured.Variables["input"])
	}
	if !strings.Contains(out.String(), "DeregisterAppWorkflow-guid-1") ||
		!strings.Contains(out.String(), "grace window") {
		t.Errorf("workflow id / grace-window note missing:\n%s", out.String())
	}
}

func TestAppDeregisterSurfacesEnvelopeError(t *testing.T) {
	appDeregisterYes = true
	defer func() { appDeregisterYes = false }()

	srv := gqlServer(t, map[string]interface{}{
		"previewAstroliftDeregister": deregisterPreviewData(),
		"deregisterAstroliftApp": map[string]interface{}{
			"ok": false,
			"errors": []map[string]interface{}{
				{"code": "VALIDATION", "message": "confirm_name must match the app's name exactly", "field": "confirmName"},
			},
			"data": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := appTestCmd()
	err := runAppDeregister(cmd, context.Background(), client, "web")
	if err == nil || !strings.Contains(err.Error(), "confirm_name must match") {
		t.Fatalf("expected envelope error surfaced, got: %v", err)
	}
}

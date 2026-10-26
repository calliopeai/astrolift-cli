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

	"github.com/spf13/cobra"
)

func TestCIStatusUsesDeploymentIdentity(t *testing.T) {
	const id = "019b3e80-481a-7000-8000-000000000001"
	for _, code := range []int{200, 403, 404, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/api/cli/v1/deployments/"+id+"/status/" {
					t.Errorf("wrong request: %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer ci-token" {
					t.Error("missing deploy credential")
				}
				w.WriteHeader(code)
				if code == 200 {
					fmt.Fprintf(w, `{"deployment_id":%q,"state":"succeeded","status":"running"}`, id)
				} else {
					fmt.Fprint(w, "<html>private error body</html>")
				}
			}))
			defer server.Close()
			t.Setenv("ASTROLIFT_API_URL", server.URL)
			t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "ci-token")
			cmd := &cobra.Command{}
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetContext(context.Background())
			err := ciStatusCmd.RunE(cmd, []string{id})
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			if code == 200 {
				if err != nil || strings.TrimSpace(out.String()) != "succeeded" {
					t.Fatalf("out=%q err=%v", out.String(), err)
				}
			} else if err == nil || strings.Contains(err.Error(), "private error body") || strings.Contains(err.Error(), "<html>") {
				t.Fatalf("unsafe/missing refusal: %v", err)
			}
		})
	}
}

func TestCIStatusRejectsWorkflowIDBeforeRequest(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	t.Setenv("ASTROLIFT_API_URL", srv.URL)
	t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "token")
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	for _, id := range []string{"DeployAppWorkflow-app-env", "../foreign", "not-a-guid"} {
		if err := ciStatusCmd.RunE(cmd, []string{id}); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	if calls != 0 {
		t.Fatalf("invalid identifiers made %d requests", calls)
	}
}

func TestCIDeployPrintsStatusIdentity(t *testing.T) {
	const id = "019b3e80-481a-7000-8000-000000000001"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cli/v1/apps/web/deploy/" {
			t.Errorf("path %s", r.URL.Path)
		}
		fmt.Fprintf(w, `{"deployment_id":%q,"workflow_id":"workflow-one","polling_url":%q}`, id, "/api/cli/v1/deployments/"+id+"/status/")
	}))
	defer srv.Close()
	for key, value := range map[string]string{"ASTROLIFT_API_URL": srv.URL, "ASTROLIFT_DEPLOY_TOKEN": "token", "ASTROLIFT_APP_SLUG": "web", "ASTROLIFT_COMMIT_SHA": "abc", "ASTROLIFT_IMAGE_TAGS": `{"web":"sha-abc"}`} {
		t.Setenv(key, value)
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("no-wait", true, "")
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := ciDeployCmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Deployment ID:   "+id) {
		t.Fatalf("no reusable status ID: %s", out.String())
	}
}

func TestSelfUpdatePreviewsReleaseWithoutPlatformLogin(t *testing.T) {
	clearGitHubTokenEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/repos/"+releaseRepo+"/releases/latest" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v999.0.0", Assets: []githubAsset{{Name: resolveAssetName(), BrowserDownloadURL: "https://example.invalid/archive"}}})
	}))
	defer srv.Close()
	t.Setenv("ASTRO_GITHUB_API_URL", srv.URL)
	for _, asJSON := range []bool{false, true} {
		cmd := &cobra.Command{}
		cmd.SetContext(context.Background())
		cmd.Flags().Bool("json", asJSON, "")
		var out, stderr bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&stderr)
		if err := selfUpdateCmd.RunE(cmd, nil); err != nil {
			t.Fatal(err)
		}
		if asJSON {
			var result map[string]any
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("invalid JSON: %s", out.String())
			}
			if result["latestVersion"] != "v999.0.0" || result["dryRun"] != true {
				t.Fatalf("result=%v", result)
			}
		} else if !strings.Contains(out.String(), "[dry-run]") {
			t.Fatal(out.String())
		}
	}
	if calls != 2 {
		t.Fatalf("preview downloaded or used platform: %d calls", calls)
	}
}

func TestCIDeployJSONIncludesReusableDeploymentID(t *testing.T) {
	const id = "019b3e80-481a-7000-8000-000000000001"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"deployment_id":%q,"workflow_id":"workflow-one","polling_url":%q}`, id, "/api/cli/v1/deployments/"+id+"/status/")
	}))
	defer srv.Close()
	for key, value := range map[string]string{"ASTROLIFT_API_URL": srv.URL, "ASTROLIFT_DEPLOY_TOKEN": "token", "ASTROLIFT_APP_SLUG": "web", "ASTROLIFT_COMMIT_SHA": "abc", "ASTROLIFT_IMAGE_TAGS": `{"web":"sha-abc"}`} {
		t.Setenv(key, value)
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("no-wait", true, "")
	cmd.Flags().Bool("json", true, "")
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	if err := ciDeployCmd.RunE(cmd, nil); err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("not one JSON object: %s", out.String())
	}
	if result["deployment_id"] != id {
		t.Fatalf("wrong identity: %v", result)
	}
}

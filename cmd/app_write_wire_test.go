package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func appWireCredentials(t *testing.T, url string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	viper.Set("api_url", url)
	viper.Set("token", "test-token")
	t.Cleanup(func() {
		viper.Set("api_url", "")
		viper.Set("token", "")
	})
}

func TestSetBuildModeSendsResolvedIDAndPreservesOtherBuildInputs(t *testing.T) {
	for _, mode := range []string{"ci_pushed", "platform_build", "none"} {
		t.Run(mode, func(t *testing.T) {
			appSetBuildModeJSON = true
			appSetBuildModeStrategy = ""
			t.Cleanup(func() { appSetBuildModeJSON = false })
			var requests []gqlRequest
			srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
				requests = append(requests, req)
				if strings.Contains(req.Query, "astroliftApp(") {
					return map[string]interface{}{"astroliftApp": map[string]interface{}{
						"id": "resolved-guid", "slug": "web", "buildMode": "platform_build",
					}}
				}
				return map[string]interface{}{"updateApp": map[string]interface{}{
					"ok": true, "data": map[string]interface{}{
						"id": "resolved-guid", "slug": "web", "buildMode": mode,
					},
				}}
			})
			defer srv.Close()
			appWireCredentials(t, srv.URL)
			cmd, out := appTestCmd()
			cmd.SetContext(context.Background())
			if err := appSetBuildModeCmd.RunE(cmd, []string{"web", mode}); err != nil {
				t.Fatal(err)
			}
			if len(requests) != 2 || requests[0].Variables["slug"] != "web" {
				t.Fatalf("unexpected request sequence: %#v", requests)
			}
			input := requests[1].Variables["input"].(map[string]interface{})
			strategy := "off"
			if mode == "platform_build" {
				strategy = "dockerfile"
			}
			if input["id"] != "resolved-guid" || input["buildMode"] != mode || input["buildStrategy"] != strategy || len(input) != 3 {
				t.Fatalf("unsafe update input: %#v", input)
			}
			var result map[string]interface{}
			if err := json.Unmarshal(out.Bytes(), &result); err != nil || result["buildMode"] != mode {
				t.Fatalf("invalid JSON result: %s (%v)", out.String(), err)
			}
		})
	}
}

func TestSetBuildModeRejectsInvalidStrategyBeforeConnecting(t *testing.T) {
	appSetBuildModeStrategy = "unknown"
	t.Cleanup(func() { appSetBuildModeStrategy = "" })
	appWireCredentials(t, "http://127.0.0.1:1")
	cmd, _ := appTestCmd()
	cmd.SetContext(context.Background())
	err := appSetBuildModeCmd.RunE(cmd, []string{"web", "platform_build"})
	if err == nil || !strings.Contains(err.Error(), "--build-strategy") {
		t.Fatalf("expected local validation, got %v", err)
	}
}

func TestSetBuildModeRefusesMissingAppAndMalformedSuccess(t *testing.T) {
	for _, scenario := range []string{"missing", "denied", "empty-success"} {
		t.Run(scenario, func(t *testing.T) {
			appSetBuildModeStrategy = ""
			requestCount := 0
			srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
				requestCount++
				if strings.Contains(req.Query, "astroliftApp(") {
					if scenario == "missing" {
						return map[string]interface{}{"astroliftApp": nil}
					}
					return map[string]interface{}{"astroliftApp": map[string]interface{}{
						"id": "guid", "slug": "web", "buildMode": "platform_build",
					}}
				}
				if scenario == "denied" {
					return map[string]interface{}{"updateApp": map[string]interface{}{
						"ok": false, "errors": []map[string]string{{"message": "not permitted"}},
					}}
				}
				return map[string]interface{}{"updateApp": map[string]interface{}{"ok": true, "data": nil}}
			})
			defer srv.Close()
			appWireCredentials(t, srv.URL)
			cmd, _ := appTestCmd()
			cmd.SetContext(context.Background())
			err := appSetBuildModeCmd.RunE(cmd, []string{"web", "ci_pushed"})
			if err == nil {
				t.Fatal("expected refusal")
			}
			if scenario == "missing" && requestCount != 1 {
				t.Fatal("a missing app must not trigger an update")
			}
			if scenario == "denied" && !strings.Contains(err.Error(), "not permitted") {
				t.Fatalf("server error lost: %v", err)
			}
		})
	}
}

func resetRegisterWireFlags() {
	appRegisterFile = "astrolift.toml"
	appRegisterProjectID = ""
	appRegisterSourceRepo = ""
	appRegisterSourceKind = "github"
	appRegisterDescription = ""
	appRegisterManifestPath = ""
	appRegisterManifestRaw = false
	appRegisterDefaultBranch = ""
	appRegisterBuildMode = ""
	appRegisterBuildStrategy = ""
	appRegisterDockerfile = ""
	appRegisterBuildContext = ""
	appRegisterJSON = false
}

func TestRegisterSendsExactManifestOnlyWhenRequested(t *testing.T) {
	const manifest = "[app]\nslug = \"web\"\ndisplay_name = \"Web\"\n\n[[workloads]]\nname = \"api\"\nkind = \"deployment\"\n"
	for _, inline := range []bool{false, true} {
		t.Run(map[bool]string{false: "repository", true: "inline"}[inline], func(t *testing.T) {
			resetRegisterWireFlags()
			t.Cleanup(resetRegisterWireFlags)
			path := filepath.Join(t.TempDir(), "astrolift.toml")
			if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			appRegisterFile = path
			appRegisterProjectID = "project-guid"
			appRegisterSourceRepo = "acme/web"
			appRegisterManifestRaw = inline
			appRegisterJSON = true
			var captured gqlRequest
			srv := gqlServer(t, map[string]interface{}{"registerApp": map[string]interface{}{
				"ok": true, "data": map[string]interface{}{"id": "guid", "slug": "web", "name": "Web"},
			}}, &captured)
			defer srv.Close()
			appWireCredentials(t, srv.URL)
			cmd, out := appTestCmd()
			cmd.SetContext(context.Background())
			if err := appRegisterCmd.RunE(cmd, nil); err != nil {
				t.Fatal(err)
			}
			input := captured.Variables["input"].(map[string]interface{})
			raw, sent := input["manifestRaw"]
			if sent != inline || (inline && raw != manifest) {
				t.Fatalf("inline manifest mismatch: %#v", input)
			}
			if input["projectId"] != "project-guid" || input["sourceRepo"] != "acme/web" || input["slug"] != "web" {
				t.Fatalf("registration ownership/source changed: %#v", input)
			}
			if !json.Valid(out.Bytes()) {
				t.Fatalf("invalid JSON response: %s", out.String())
			}
		})
	}
}

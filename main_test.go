package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// Exercise the executable: RunE tests alone cannot detect main appending plain
// text after an inspection command already wrote its JSON error to stderr.
func TestPermissionInspectionExecutableEmitsOneJSONErrorAndExitsNonzero(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "astro")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	for _, fixture := range []struct {
		name    string
		status  int
		body    string
		command []string
	}{
		{"graphql denial", 200, `{"errors":[{"message":"outside credential team","extensions":{"code":"PERMISSION_DENIED","reason":"app is outside the credential's team"}}]}`, []string{"whoami", "--permissions"}},
		{"http denial", 403, "<html>synthetic-token raw-secret</html>", []string{"perms", "diagnose"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Authorization") != "Bearer synthetic-token" {
					t.Error("wrong credential namespace")
				}
				var request struct {
					Query string `json:"query"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if strings.Contains(request.Query, "astroliftOrganizations") {
					_, _ = fmt.Fprint(w, `{"data":{"astroliftOrganizations":[{"id":"org-id","slug":"org"}]}}`)
					return
				}
				if r.Header.Get("X-Astrolift-Organization") != "org-id" {
					t.Error("tenant not verified before identity")
				}
				w.WriteHeader(fixture.status)
				_, _ = fmt.Fprint(w, fixture.body)
			}))
			defer server.Close()
			args := append([]string{"--api-url", server.URL, "--token", "synthetic-token", "--json", "--no-prompt"}, fixture.command...)
			process := exec.Command(binary, args...)
			process.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir(), "ASTROLIFT_NO_UPDATE_CHECK=1", "ASTROLIFT_DEPLOY_TOKEN=", "ASTROLIFT_SERVER=", "ASTROLIFT_DEFAULT_ORG=")
			var stdout, stderr strings.Builder
			process.Stdout, process.Stderr = &stdout, &stderr
			err := process.Run()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 || requests.Load() != 2 || stdout.Len() != 0 {
				t.Fatalf("wrong process result: %v count=%d out=%s err=%s", err, requests.Load(), &stdout, &stderr)
			}
			var envelope struct {
				Error struct {
					Status int `json:"status"`
					Errors []struct {
						Code   string `json:"code"`
						Reason string `json:"reason"`
					} `json:"errors"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(stderr.String()), &envelope); err != nil {
				t.Fatalf("stderr is not exactly one JSON error: %v %s", err, &stderr)
			}
			if envelope.Error.Status != fixture.status {
				t.Fatalf("lost HTTP status: %s", &stderr)
			}
			if fixture.status == 200 && (len(envelope.Error.Errors) != 1 || envelope.Error.Errors[0].Code != "PERMISSION_DENIED" || envelope.Error.Errors[0].Reason != "app is outside the credential's team") {
				t.Fatalf("lost structured denial: %s", &stderr)
			}
			if strings.Contains(stderr.String(), "synthetic-token") || strings.Contains(stderr.String(), "raw-secret") {
				t.Fatal("stderr leaked credentials")
			}
		})
	}
}

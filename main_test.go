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

// version-check prints one JSON result on stdout for a current install and for
// one without public install discovery, and only the latter exits nonzero
// (#178). Only the executable shows main adding nothing after the result.
func TestVersionCheckExecutableReportsOneResultAndExitCode(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "astro")
	build := exec.Command("go", "build", "-ldflags", "-X github.com/calliopeai/astrolift-cli/cmd.Version=0.14.1", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	for _, install := range []struct {
		name      string
		discovery bool
		exit      int
	}{
		{"current install", true, 0},
		{"install without public discovery", false, 1},
	} {
		t.Run(install.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("version-check sent a credential")
				}
				if install.discovery && r.Method == http.MethodPost && r.URL.Path == "/app/gql/config/public/" {
					_, _ = fmt.Fprint(w, `{"data":{"astroliftServerInfo":{"version":"0.1.0","apiVersion":"0.1.0+5d41402abc4b2a76","capabilities":["server_info.handshake"]}}}`)
					return
				}
				w.WriteHeader(http.StatusNotFound)
				_, _ = fmt.Fprint(w, "<!doctype html><title>Not Found</title><h1>Not Found</h1>")
			}))
			defer server.Close()
			process := exec.Command(binary, "--api-url", server.URL, "version-check", "--json")
			process.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir(), "ASTROLIFT_NO_UPDATE_CHECK=1", "ASTROLIFT_DEPLOY_TOKEN=", "ASTROLIFT_SERVER=", "ASTROLIFT_TOKEN=")
			var stdout, stderr strings.Builder
			process.Stdout, process.Stderr = &stdout, &stderr
			exit := 0
			if err := process.Run(); err != nil {
				failed, ok := err.(*exec.ExitError)
				if !ok {
					t.Fatal(err)
				}
				exit = failed.ExitCode()
			}
			var result struct {
				Compatible         bool   `json:"compatible"`
				CLIVersion         string `json:"cliVersion"`
				RequiredCapability string `json:"requiredCapability"`
			}
			if exit != install.exit || stderr.Len() != 0 || json.Unmarshal([]byte(stdout.String()), &result) != nil {
				t.Fatalf("exit=%d stdout=%s stderr=%s", exit, &stdout, &stderr)
			}
			if result.Compatible != install.discovery || result.CLIVersion != "0.14.1" || result.RequiredCapability != "server_info.handshake" {
				t.Fatalf("wrong result: %s", &stdout)
			}
		})
	}
}

// A Zentinelle read refused for the credential's scopes (#179) exits 1 with
// nothing on stdout and exactly one JSON object on stderr: the server's
// refusal and the sign-in that carries the permission.
func TestZentinelleReadExecutableReportsAScopeRefusalAsOneJSONError(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "astro")
	if output, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	for _, fixture := range []struct {
		command    []string
		field      string
		permission string
	}{
		{[]string{"zentinelle", "status"}, "astroliftZentinelleConnection", "zentinelle.status_view"},
		{[]string{"policy", "ls"}, "astroliftZentinellePolicies", "zentinelle.policy_view"},
	} {
		t.Run(strings.Join(fixture.command, " "), func(t *testing.T) {
			var reads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Query string `json:"query"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				switch {
				case strings.Contains(request.Query, "astroliftOrganizations"):
					_, _ = fmt.Fprint(w, `{"data":{"astroliftOrganizations":[{"id":"org-id","slug":"org"}]}}`)
				case strings.Contains(request.Query, "astroliftZentinelleCapability"):
					_, _ = fmt.Fprint(w, `{"data":{"astroliftZentinelleCapability":{"connected":true}}}`)
				case strings.Contains(request.Query, fixture.field):
					reads.Add(1)
					_, _ = fmt.Fprintf(w, `{"data":null,"errors":[{"message":"api token scope does not allow this permission: %[1]s","extensions":{"code":"PERMISSION_DENIED","reason":"api token scope does not allow this permission","permission":"%[1]s"}}]}`, fixture.permission)
				default:
					t.Errorf("unexpected document: %s", request.Query)
				}
			}))
			defer server.Close()
			args := append([]string{"--api-url", server.URL, "--token", "synthetic-token", "--json", "--no-prompt"}, fixture.command...)
			process := exec.Command(binary, args...)
			process.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir(), "ASTROLIFT_NO_UPDATE_CHECK=1", "ASTROLIFT_DEPLOY_TOKEN=", "ASTROLIFT_SERVER=", "ASTROLIFT_DEFAULT_ORG=")
			var stdout, stderr strings.Builder
			process.Stdout, process.Stderr = &stdout, &stderr
			err := process.Run()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 || reads.Load() != 1 || stdout.Len() != 0 {
				t.Fatalf("wrong process result: %v reads=%d out=%s err=%s", err, reads.Load(), &stdout, &stderr)
			}
			var report struct {
				Error struct {
					Errors []struct {
						Code       string `json:"code"`
						Permission string `json:"permission"`
					} `json:"errors"`
				} `json:"error"`
				Credential struct {
					Permission string `json:"permission"`
					LoginScope string `json:"loginScope"`
					Hint       string `json:"hint"`
				} `json:"credential"`
			}
			if err := json.Unmarshal([]byte(stderr.String()), &report); err != nil {
				t.Fatalf("stderr is not exactly one JSON error: %v %s", err, &stderr)
			}
			refusal := report.Error.Errors
			if len(refusal) != 1 || refusal[0].Code != "PERMISSION_DENIED" || refusal[0].Permission != fixture.permission {
				t.Fatalf("lost the server's refusal: %s", &stderr)
			}
			if report.Credential.Permission != fixture.permission || report.Credential.LoginScope != "zentinelle" || !strings.Contains(report.Credential.Hint, "astro auth login --scope zentinelle") {
				t.Fatalf("no sign-in advice: %s", &stderr)
			}
			if strings.Contains(stderr.String(), "synthetic-token") {
				t.Fatal("stderr leaked credentials")
			}
		})
	}
}

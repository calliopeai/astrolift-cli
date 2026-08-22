package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// The relay path exists so a caller with no browser can hand the flow to a
// human. What matters is that --no-wait returns promptly with a usable
// session, and that `wait` finishes the same flow -- so these run a real
// device-flow server rather than asserting on help text.

// fakeAuthServer serves /auth/start, and /auth/complete gated on approve.
func fakeAuthServer(t *testing.T, approve <-chan struct{}) *httptest.Server {
	t.Helper()
	approved := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/auth/start"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"session_id": "sess-abc123",
				"login_url": "https://astrolift.example.com/cli/login?code=WXYZ",
				"poll_interval_seconds": 1,
				"expires_in_seconds": 60
			}`))
		case strings.HasSuffix(r.URL.Path, "/auth/complete"):
			if !approved {
				select {
				case <-approve:
					approved = true
				default:
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"status":"pending"}`))
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"access_token": "alft_at_abc",
				"refresh_token": "alft_rt_def",
				"expires_at": "2099-01-01T00:00:00Z",
				"token_type": "Bearer"
			}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// withConfigHome points the CLI's config at a temp dir and registers one
// server, so a test never reads or writes the developer's real credentials.
func withConfigHome(t *testing.T, apiURL string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("HOME", home)

	dir := filepath.Join(home, "astrolift")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "current_server: test\nservers:\n  test:\n    api_url: " + apiURL + "\n    display_name: test\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		loginNoWait = false
		loginNoBrowser = false
		waitSessionID = ""
	})
	err := rootCmd.Execute()
	return out.String(), err
}

func TestLoginNoWaitReportsASessionAndDoesNotBlock(t *testing.T) {
	// Never approved: if --no-wait polled at all, this test would hang until
	// the session expired rather than returning.
	srv := fakeAuthServer(t, make(chan struct{}))
	withConfigHome(t, srv.URL)

	viper.Set("output_json", true)
	t.Cleanup(func() { viper.Set("output_json", false) })

	out, err := runCmd(t, "auth", "login", "--no-wait", "--no-browser")
	if err != nil {
		t.Fatalf("login --no-wait: %v (%s)", err, out)
	}

	var report loginSessionReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out)
	}
	if report.SessionID != "sess-abc123" {
		t.Errorf("session_id = %q", report.SessionID)
	}
	if !strings.Contains(report.LoginURL, "code=WXYZ") {
		t.Errorf("login_url = %q, want the relayable URL", report.LoginURL)
	}
	if report.Waiting {
		t.Error("waiting = true on --no-wait; a relaying caller would never run `auth wait`")
	}
	if report.PollInterval == 0 || report.ExpiresIn == 0 {
		t.Errorf("poll interval/expiry must be reported so `wait` can be driven: %+v", report)
	}
}

func TestWaitFinishesTheFlowAndStoresCredentials(t *testing.T) {
	approve := make(chan struct{})
	close(approve) // the human has already finished by the time wait runs
	srv := fakeAuthServer(t, approve)
	home := withConfigHome(t, srv.URL)

	out, err := runCmd(t, "auth", "wait", "--session-id", "sess-abc123")
	if err != nil {
		t.Fatalf("auth wait: %v (%s)", err, out)
	}
	if !strings.Contains(out, "Logged in") {
		t.Errorf("unexpected output: %s", out)
	}

	// The point of the whole flow: credentials on disk for the CLI to use.
	creds := filepath.Join(home, "astrolift", "credentials", "test.yaml")
	if _, err := os.Stat(creds); err != nil {
		t.Fatalf("expected credentials at %s: %v", creds, err)
	}
}

func TestWaitRefusesWithoutASessionID(t *testing.T) {
	srv := fakeAuthServer(t, make(chan struct{}))
	withConfigHome(t, srv.URL)

	if _, err := runCmd(t, "auth", "wait"); err == nil {
		t.Fatal("expected a refusal: without a session id there is nothing to poll")
	}
}

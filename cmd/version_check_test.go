package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/auth"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"1.3.0", "1.2.99", 1},
		{"2.0.0", "1.99.99", 1},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.3-rc1", "1.2.3", 0},
		{"1.2.3+build", "1.2.3", 0},
	}
	for _, c := range cases {
		got, err := CompareSemver(c.a, c.b)
		if err != nil {
			t.Errorf("CompareSemver(%q, %q): unexpected error %v", c.a, c.b, err)
			continue
		}
		if got != c.want {
			t.Errorf("CompareSemver(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareSemverInvalid(t *testing.T) {
	if _, err := CompareSemver("not.a.version", "1.2.3"); err == nil {
		t.Error("expected error on non-numeric components")
	}
	if _, err := CompareSemver("1.2", "1.2.3"); err == nil {
		t.Error("expected error on missing patch component")
	}
}

// djangoNotFound is the page Django serves for a path it has no route for, as
// on the install in #178.
const djangoNotFound = `
<!doctype html>
<html lang="en">
<head>
  <title>Not Found</title>
</head>
<body>
  <h1>Not Found</h1><p>The requested resource was not found on this server.</p>
</body>
</html>
`

// versionCheckInstall stands in for an install. With a handshake it answers
// POST /app/gql/config/public/ as astrolift-app does; every other request, and
// every request to an install without one, gets Django's HTML 404.
func versionCheckInstall(t *testing.T, handshake http.HandlerFunc) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Astrolift-Organization") != "" {
			t.Error("version-check sent a credential or an organization")
		}
		if handshake != nil && r.Method == http.MethodPost && r.URL.Path == "/app/gql/config/public/" {
			var body struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !strings.Contains(body.Query, "astroliftServerInfo") {
				t.Errorf("unexpected discovery request: %v %q", err, body.Query)
			}
			handshake(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, djangoNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func handshakeWith(capabilities ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"astroliftServerInfo": map[string]any{
			"version": "0.1.0", "apiVersion": "0.1.0+5d41402abc4b2a76", "capabilities": capabilities,
		}}})
	}
}

func respondWith(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = fmt.Fprint(w, body)
	}
}

// versionCheckFixture makes url the current server, in the given sign-in
// state, for a 0.14.1 build.
func versionCheckFixture(t *testing.T, url, login string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "")
	for _, key := range []string{"server", "api_url", "token"} {
		viper.Set(key, "")
		t.Cleanup(func() { viper.Set(key, "") })
	}
	if url != "" {
		cfg := &config.Config{CurrentServer: "conflict", Servers: map[string]config.ServerEntry{"conflict": {APIURL: url}}}
		if err := cfg.Save(); err != nil {
			t.Fatal(err)
		}
	}
	expires := map[string]time.Duration{"signed in": time.Hour, "expired sign-in": -time.Hour}
	if lifetime, ok := expires[login]; ok {
		creds := &auth.Credentials{AccessToken: "stored-token", RefreshToken: "stored-refresh", ExpiresAt: time.Now().Add(lifetime)}
		if err := config.SaveCredentials("conflict", creds); err != nil {
			t.Fatal(err)
		}
	}
	version := Version
	Version = "0.14.1"
	t.Cleanup(func() { Version = version })
}

// runVersionCheckCommand runs version-check with the global flags it reads
// and returns stdout, stderr and the error main would see.
func runVersionCheckCommand(t *testing.T, asJSON bool) (string, string, error) {
	t.Helper()
	c := &cobra.Command{}
	c.Flags().Bool("json", asJSON, "")
	c.Flags().Bool("debug", false, "")
	c.SetContext(context.Background())
	var stdout, stderr bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&stderr)
	err := versionCheckCmd.RunE(c, nil)
	return stdout.String(), stderr.String(), err
}

func alreadyReported(err error) bool {
	var reported interface{ AlreadyReported() bool }
	return errors.As(err, &reported) && reported.AlreadyReported()
}

func TestVersionCheckReadsPublicDiscoveryWithoutCredentials(t *testing.T) {
	for _, login := range []string{"signed out", "signed in", "expired sign-in"} {
		t.Run(login, func(t *testing.T) {
			srv, requests := versionCheckInstall(t, handshakeWith("audit.read", versionCheckCapability))
			versionCheckFixture(t, srv.URL, login)

			stdout, stderr, err := runVersionCheckCommand(t, true)
			if err != nil || stderr != "" || requests.Load() != 1 {
				t.Fatalf("check failed: %v, requests=%d, stderr=%q", err, requests.Load(), stderr)
			}
			var result versionCheckResult
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatalf("stdout is not one JSON result: %v %q", err, stdout)
			}
			want := versionCheckResult{
				Compatible: true, CLIVersion: "0.14.1", Server: "conflict", APIURL: srv.URL,
				ServerVersion: "0.1.0", APIVersion: "0.1.0+5d41402abc4b2a76", RequiredCapability: versionCheckCapability,
			}
			if result != want {
				t.Fatalf("result = %+v, want %+v", result, want)
			}
		})
	}
}

func TestVersionCheckPrintsTheServerVersion(t *testing.T) {
	srv, _ := versionCheckInstall(t, handshakeWith(versionCheckCapability))
	versionCheckFixture(t, srv.URL, "signed in")

	stdout, stderr, err := runVersionCheckCommand(t, false)
	want := fmt.Sprintf("CLI 0.14.1 is compatible with %s.\nServer version: 0.1.0 (api 0.1.0+5d41402abc4b2a76)\n", srv.URL)
	if err != nil || stderr != "" || stdout != want {
		t.Fatalf("got %v, stdout=%q, stderr=%q", err, stdout, stderr)
	}
}

func TestVersionCheckReportsAnInstallWithoutPublicDiscoveryAsUnsupported(t *testing.T) {
	for _, install := range []struct {
		name      string
		handshake http.HandlerFunc
		cause     string
		version   string
	}{
		{"route missing", nil, "returned HTTP 404", ""},
		{"no handshake in discovery", respondWith(http.StatusOK, `{"errors":[{"message":"Cannot query field 'astroliftServerInfo' on type 'Query'."}]}`), "does not serve astroliftServerInfo", ""},
		{"handshake without capability", handshakeWith("audit.read"), "does not advertise server_info.handshake", "0.1.0"},
	} {
		t.Run(install.name, func(t *testing.T) {
			srv, requests := versionCheckInstall(t, install.handshake)
			versionCheckFixture(t, srv.URL, "signed in")

			stdout, stderr, err := runVersionCheckCommand(t, true)
			if err == nil || !alreadyReported(err) || stderr != "" || requests.Load() != 1 {
				t.Fatalf("want one reported nonzero result: %v, requests=%d, stderr=%q", err, requests.Load(), stderr)
			}
			var result versionCheckResult
			if err := json.Unmarshal([]byte(stdout), &result); err != nil {
				t.Fatalf("stdout is not one JSON result: %v %q", err, stdout)
			}
			if result.Compatible || result.RequiredCapability != versionCheckCapability || result.ServerVersion != install.version ||
				result.Server != "conflict" || result.CLIVersion != "0.14.1" ||
				!strings.Contains(result.Reason, install.cause) || !strings.Contains(result.Reason, "/app/gql/config/public/") {
				t.Fatalf("unsupported result lost its requirement: %+v", result)
			}

			stdout, stderr, err = runVersionCheckCommand(t, false)
			if err == nil || alreadyReported(err) || stdout != "" || stderr != "" {
				t.Fatalf("human mode must leave the error to main: %v, stdout=%q, stderr=%q", err, stdout, stderr)
			}
			message := err.Error()
			if !strings.Contains(message, "CLI 0.14.1 does not support the server at "+srv.URL) || !strings.Contains(message, install.cause) {
				t.Fatalf("unhelpful error: %s", message)
			}
			for _, output := range []string{message, stdout} {
				if strings.Contains(output, "<") || strings.Contains(output, "auth login") {
					t.Fatalf("output echoed the page or blamed sign-in: %s", output)
				}
			}
		})
	}
}

func TestVersionCheckFailureIsOneJSONErrorWithoutSignInHint(t *testing.T) {
	for _, failure := range []struct {
		name      string
		handshake http.HandlerFunc
		status    int
		closed    bool
	}{
		{"anonymous request refused", respondWith(http.StatusUnauthorized, "<html>PRIVATE_BODY</html>"), http.StatusUnauthorized, false},
		{"server unavailable", respondWith(http.StatusServiceUnavailable, "<html>PRIVATE_BODY</html>"), http.StatusServiceUnavailable, false},
		{"graphql error", respondWith(http.StatusOK, `{"errors":[{"message":"handshake unavailable"}]}`), 0, false},
		{"unreachable", nil, 0, true},
	} {
		t.Run(failure.name, func(t *testing.T) {
			srv, _ := versionCheckInstall(t, failure.handshake)
			versionCheckFixture(t, srv.URL, "signed in")
			if failure.closed {
				srv.Close()
			}

			stdout, stderr, err := runVersionCheckCommand(t, true)
			if err == nil || !alreadyReported(err) || stdout != "" {
				t.Fatalf("want one reported JSON error: %v, stdout=%q", err, stdout)
			}
			var envelope struct {
				Error versionCheckError `json:"error"`
			}
			if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
				t.Fatalf("stderr is not one JSON error: %v %q", err, stderr)
			}
			if envelope.Error.Status != failure.status || !strings.Contains(envelope.Error.Message, "public install discovery at "+srv.URL) {
				t.Fatalf("error lost the server or status: %+v", envelope.Error)
			}

			_, _, err = runVersionCheckCommand(t, false)
			if err == nil || alreadyReported(err) {
				t.Fatalf("human mode must leave the error to main: %v", err)
			}
			for _, output := range []string{stderr, err.Error()} {
				if strings.Contains(output, "auth login") || strings.Contains(output, "PRIVATE_BODY") {
					t.Fatalf("error blamed sign-in or echoed the body: %s", output)
				}
			}
		})
	}
}

func TestVersionCheckNeedsAServerNotASignIn(t *testing.T) {
	versionCheckFixture(t, "", "signed out")
	_, _, err := runVersionCheckCommand(t, false)
	if err == nil || !strings.Contains(err.Error(), "astro server add") || strings.Contains(err.Error(), "auth login") {
		t.Fatalf("no server: %v", err)
	}

	srv, requests := versionCheckInstall(t, handshakeWith(versionCheckCapability))
	viper.Set("api_url", srv.URL)
	stdout, _, err := runVersionCheckCommand(t, true)
	var result versionCheckResult
	if err != nil || json.Unmarshal([]byte(stdout), &result) != nil || !result.Compatible || result.Server != "" || requests.Load() != 1 {
		t.Fatalf("--api-url alone: %v, %q", err, stdout)
	}
}

package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/auth"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

// `astro auth logout` used to only delete the local credentials file --
// the access token and refresh chain stayed live at the server until they
// aged out on their own. These tests prove the new /auth/signout call
// happens first, with the right token, and that local logout still
// succeeds even when that call fails (astrolift-app#2070).

// fakeSignoutServer serves /auth/signout, recording every refresh_token it
// was called with. A non-200 statusCode exercises the "server call failed"
// path without needing a truly unreachable server.
func fakeSignoutServer(t *testing.T, statusCode int) (*httptest.Server, *[]string) {
	t.Helper()
	seenTokens := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/auth/signout") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		*seenTokens = append(*seenTokens, body.RefreshToken)
		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"signed_out"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, seenTokens
}

func seedCredentials(t *testing.T, serverSlug, refreshToken string) {
	t.Helper()
	err := config.SaveCredentials(serverSlug, &auth.Credentials{
		AccessToken:  "alft_at_abc",
		RefreshToken: refreshToken,
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seeding credentials: %v", err)
	}
}

func credentialsPath(home, serverSlug string) string {
	return filepath.Join(home, "astrolift", "credentials", serverSlug+".yaml")
}

func TestLogoutCallsSignoutWithTheStoredRefreshTokenThenDeletesLocalCreds(t *testing.T) {
	srv, seenTokens := fakeSignoutServer(t, http.StatusOK)
	home := withConfigHome(t, srv.URL)
	seedCredentials(t, "test", "alft_rt_def")

	out, err := runCmd(t, "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout: %v (%s)", err, out)
	}
	if !strings.Contains(out, "Logged out of test") {
		t.Errorf("unexpected output: %s", out)
	}

	if got := *seenTokens; len(got) != 1 || got[0] != "alft_rt_def" {
		t.Errorf("signout saw tokens %v, want exactly [alft_rt_def]", got)
	}

	if _, statErr := os.Stat(credentialsPath(home, "test")); !os.IsNotExist(statErr) {
		t.Errorf("expected local credentials removed, stat err = %v", statErr)
	}
}

func TestLogoutSucceedsLocallyEvenWhenSignoutFails(t *testing.T) {
	srv, seenTokens := fakeSignoutServer(t, http.StatusInternalServerError)
	home := withConfigHome(t, srv.URL)
	seedCredentials(t, "test", "alft_rt_def")

	out, err := runCmd(t, "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout must succeed locally even when the server call fails: %v (%s)", err, out)
	}
	if !strings.Contains(out, "warning") {
		t.Errorf("expected a warning about the failed server-side signout, got: %s", out)
	}
	if len(*seenTokens) != 1 {
		t.Errorf("expected the signout call to still be attempted, saw %v", *seenTokens)
	}

	if _, statErr := os.Stat(credentialsPath(home, "test")); !os.IsNotExist(statErr) {
		t.Errorf("expected local credentials removed even though signout failed, stat err = %v", statErr)
	}
}

func TestLogoutWithoutStoredCredentialsSkipsSignout(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	withConfigHome(t, srv.URL)

	// No seedCredentials call: nothing was ever logged in for this
	// server, so there is no refresh token to prove possession with.
	out, err := runCmd(t, "auth", "logout")
	if err != nil {
		t.Fatalf("auth logout: %v (%s)", err, out)
	}
	if called {
		t.Error("signout must not be called when there are no local credentials to prove possession with")
	}
}

package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// isolateVersionCheckCache points config.Dir() at a temp directory so the
// staleness cache never touches the developer's real ~/.config/astrolift.
func isolateVersionCheckCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

// stubVersion sets the build stamp for the duration of a test.
func stubVersion(t *testing.T, version string) {
	t.Helper()
	original := Version
	Version = version
	t.Cleanup(func() { Version = original })
}

func TestStalenessNotice(t *testing.T) {
	cases := []struct {
		name            string
		current, latest string
		wantNotice      bool
	}{
		{"behind by a patch", "0.2.2", "0.3.0", true},
		{"behind with v prefixes", "v0.1.1", "v0.3.0", true},
		{"up to date", "0.3.0", "v0.3.0", false},
		{"ahead of the latest release", "0.4.0", "v0.3.0", false},
		{"unstamped source build", "dev", "v0.3.0", false},
		{"unparseable current", "not-a-version", "v0.3.0", false},
		{"missing latest", "0.1.0", "", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			notice := stalenessNotice(testCase.current, testCase.latest)
			if testCase.wantNotice != (notice != "") {
				t.Fatalf("stalenessNotice(%q, %q) = %q, wantNotice=%v",
					testCase.current, testCase.latest, notice, testCase.wantNotice)
			}
			if !testCase.wantNotice {
				return
			}
			if strings.Count(notice, "\n") != 0 {
				t.Errorf("notice must be a single line, got %q", notice)
			}
			for _, want := range []string{"0.3.0", "astro update", noUpdateCheckEnv} {
				if !strings.Contains(notice, want) {
					t.Errorf("notice %q missing %q", notice, want)
				}
			}
		})
	}
}

func TestStalenessCheckEnabled(t *testing.T) {
	newCommand := func() *cobra.Command {
		command := &cobra.Command{}
		command.Flags().Bool("json", false, "")
		return command
	}

	t.Run("enabled for a stamped build with a token", func(t *testing.T) {
		clearGitHubTokenEnv(t)
		t.Setenv("CI", "")
		t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "")
		t.Setenv(noUpdateCheckEnv, "")
		t.Setenv("GITHUB_TOKEN", "token")
		stubVersion(t, "0.2.0")
		if !stalenessCheckEnabled(newCommand()) {
			t.Error("expected the check to be enabled")
		}
	})

	// Each of these must independently suppress the hint.
	suppressors := []struct {
		name  string
		apply func(t *testing.T, command *cobra.Command)
	}{
		{"unstamped dev build", func(t *testing.T, _ *cobra.Command) { stubVersion(t, "dev") }},
		{"--json output", func(t *testing.T, command *cobra.Command) {
			if err := command.Flags().Set("json", "true"); err != nil {
				t.Fatal(err)
			}
		}},
		{"explicit opt-out", func(t *testing.T, _ *cobra.Command) { t.Setenv(noUpdateCheckEnv, "1") }},
		{"CI environment", func(t *testing.T, _ *cobra.Command) { t.Setenv("CI", "true") }},
		{"deploy token present", func(t *testing.T, _ *cobra.Command) { t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "deploy") }},
		{"no GitHub token", func(t *testing.T, _ *cobra.Command) { clearGitHubTokenEnv(t) }},
	}
	for _, suppressor := range suppressors {
		t.Run("suppressed by "+suppressor.name, func(t *testing.T) {
			clearGitHubTokenEnv(t)
			t.Setenv("CI", "")
			t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "")
			t.Setenv(noUpdateCheckEnv, "")
			t.Setenv("GITHUB_TOKEN", "token")
			stubVersion(t, "0.2.0")
			command := newCommand()
			suppressor.apply(t, command)
			if stalenessCheckEnabled(command) {
				t.Errorf("%s should suppress the staleness check", suppressor.name)
			}
		})
	}
}

// TestCachedLatestTagUsesCacheWithinInterval proves the daily cache actually
// prevents a request — the check must not add a round trip to every command.
func TestCachedLatestTagUsesCacheWithinInterval(t *testing.T) {
	isolateVersionCheckCache(t)
	clearGitHubTokenEnv(t)
	t.Setenv("GITHUB_TOKEN", "token")

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v9.9.9"})
	}))
	defer server.Close()
	t.Setenv("ASTRO_GITHUB_API_URL", server.URL)

	now := time.Now()
	writeVersionCheckState(versionCheckState{CheckedAt: now.Add(-time.Hour), LatestTag: "v0.3.0"})

	tag, err := cachedLatestTag(now)
	if err != nil {
		t.Fatalf("cachedLatestTag: %v", err)
	}
	if tag != "v0.3.0" {
		t.Errorf("tag = %q, want the cached v0.3.0", tag)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Errorf("cache should have prevented the request, saw %d", got)
	}

	// Past the interval, it refreshes.
	tag, err = cachedLatestTag(now.Add(staleCheckInterval + time.Minute))
	if err != nil {
		t.Fatalf("cachedLatestTag after expiry: %v", err)
	}
	if tag != "v9.9.9" {
		t.Errorf("tag = %q, want the refreshed v9.9.9", tag)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected exactly one refresh request, saw %d", got)
	}
	state, err := readVersionCheckState()
	if err != nil {
		t.Fatalf("reading cache: %v", err)
	}
	if state.LatestTag != "v9.9.9" {
		t.Errorf("refreshed tag not persisted, cache holds %q", state.LatestTag)
	}
}

// TestCachedLatestTagBacksOffAfterFailure is why the hint can never turn into
// a per-command stall: a failed lookup still stamps the cache.
func TestCachedLatestTagBacksOffAfterFailure(t *testing.T) {
	isolateVersionCheckCache(t)
	clearGitHubTokenEnv(t)
	t.Setenv("GITHUB_TOKEN", "token")

	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	t.Setenv("ASTRO_GITHUB_API_URL", server.URL)

	now := time.Now()
	if _, err := cachedLatestTag(now); err == nil {
		t.Fatal("expected an error from a 404 release lookup")
	}
	state, err := readVersionCheckState()
	if err != nil {
		t.Fatalf("a failed attempt must still be recorded: %v", err)
	}
	if state.CheckedAt.IsZero() {
		t.Fatal("failed attempt did not stamp checked_at, so every command would retry")
	}

	if _, err := cachedLatestTag(now.Add(time.Minute)); err != nil {
		t.Fatalf("cached empty result should not error: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected the failure to back off, saw %d requests", got)
	}
}

// TestMaybeNotifyStaleWritesOneLineToStderr checks the delivery contract:
// stderr only, never stdout, and nothing at all when suppressed.
func TestMaybeNotifyStaleWritesOneLineToStderr(t *testing.T) {
	isolateVersionCheckCache(t)
	clearGitHubTokenEnv(t)
	t.Setenv("CI", "")
	t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "")
	t.Setenv(noUpdateCheckEnv, "")
	t.Setenv("GITHUB_TOKEN", "token")
	t.Setenv("ASTRO_GITHUB_API_URL", "http://127.0.0.1:0")
	stubVersion(t, "0.2.2")
	writeVersionCheckState(versionCheckState{CheckedAt: time.Now(), LatestTag: "v0.3.0"})

	var stdout, stderr bytes.Buffer
	command := &cobra.Command{}
	command.Flags().Bool("json", false, "")
	command.SetOut(&stdout)
	command.SetErr(&stderr)

	maybeNotifyStale(command)
	if stdout.Len() != 0 {
		t.Errorf("the hint must never touch stdout, got %q", stdout.String())
	}
	notice := stderr.String()
	if !strings.Contains(notice, "0.2.2") || !strings.Contains(notice, "0.3.0") {
		t.Errorf("expected a staleness line naming both versions, got %q", notice)
	}
	if strings.Count(strings.TrimSpace(notice), "\n") != 0 {
		t.Errorf("expected exactly one line, got %q", notice)
	}

	// With --json the same state must produce nothing.
	stderr.Reset()
	if err := command.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	maybeNotifyStale(command)
	if stderr.Len() != 0 {
		t.Errorf("--json output must stay clean, got %q", stderr.String())
	}
}

// TestMaybeNotifyStaleIsSilentWhenLookupFails is the "never blocking, silent
// on failure" requirement: an unreachable API produces no output and no error.
func TestMaybeNotifyStaleIsSilentWhenLookupFails(t *testing.T) {
	isolateVersionCheckCache(t)
	clearGitHubTokenEnv(t)
	t.Setenv("CI", "")
	t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "")
	t.Setenv(noUpdateCheckEnv, "")
	t.Setenv("GITHUB_TOKEN", "token")
	stubVersion(t, "0.2.2")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	t.Setenv("ASTRO_GITHUB_API_URL", server.URL)

	var stdout, stderr bytes.Buffer
	command := &cobra.Command{}
	command.Flags().Bool("json", false, "")
	command.SetOut(&stdout)
	command.SetErr(&stderr)

	maybeNotifyStale(command)
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Errorf("a failed lookup must be silent, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestVersionCheckCacheLivesInConfigDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	path := versionCheckCachePath()
	if !strings.HasPrefix(path, root) {
		t.Errorf("cache path %q should live under the config dir %q", path, root)
	}
	if !strings.HasSuffix(path, "version-check.json") {
		t.Errorf("unexpected cache filename: %q", path)
	}
	writeVersionCheckState(versionCheckState{CheckedAt: time.Now(), LatestTag: "v1.0.0"})
	if _, err := os.Stat(path); err != nil {
		t.Errorf("cache file was not written: %v", err)
	}
}

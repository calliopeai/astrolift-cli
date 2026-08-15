package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// clearGitHubTokenEnv removes every credential source so a test starts from a
// known state regardless of the developer's shell (GH_TOKEN is commonly set).
func clearGitHubTokenEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"ASTRO_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		t.Setenv(key, "")
	}
}

func TestGitHubTokenPrecedence(t *testing.T) {
	clearGitHubTokenEnv(t)
	if got := githubToken(); got != "" {
		t.Fatalf("expected no token, got %q", got)
	}

	t.Setenv("GH_TOKEN", "gh-token")
	if got := githubToken(); got != "gh-token" {
		t.Errorf("GH_TOKEN not picked up, got %q", got)
	}
	t.Setenv("GITHUB_TOKEN", "github-token")
	if got := githubToken(); got != "github-token" {
		t.Errorf("GITHUB_TOKEN should outrank GH_TOKEN, got %q", got)
	}
	t.Setenv("ASTRO_GITHUB_TOKEN", "astro-token")
	if got := githubToken(); got != "astro-token" {
		t.Errorf("ASTRO_GITHUB_TOKEN should outrank the rest, got %q", got)
	}
	t.Setenv("ASTRO_GITHUB_TOKEN", "   ")
	if got := githubToken(); got != "github-token" {
		t.Errorf("whitespace-only token should be ignored, got %q", got)
	}
}

func TestGitHubAPIBaseURLDefaultAndOverride(t *testing.T) {
	t.Setenv("ASTRO_GITHUB_API_URL", "")
	if got := githubAPIBaseURL(); got != defaultGitHubAPIBaseURL {
		t.Errorf("default base URL = %q", got)
	}
	t.Setenv("ASTRO_GITHUB_API_URL", "https://ghe.example.com/api/v3/")
	if got := githubAPIBaseURL(); got != "https://ghe.example.com/api/v3" {
		t.Errorf("override should drop the trailing slash, got %q", got)
	}
	if got := latestReleaseURL(); got != "https://ghe.example.com/api/v3/repos/"+releaseRepo+"/releases/latest" {
		t.Errorf("latestReleaseURL() = %q", got)
	}
}

// TestReleaseAccessErrorIsActionable pins the requirement from #53: a private
// release must not fail with a bare status code.
func TestReleaseAccessErrorIsActionable(t *testing.T) {
	withoutToken := (&releaseAccessError{Status: 404, HadToken: false, Operation: "cannot read the latest astro release"}).Error()
	for _, want := range []string{"private repository", "GITHUB_TOKEN", "gh auth token", "docker", "cannot read the latest astro release"} {
		if !strings.Contains(withoutToken, want) {
			t.Errorf("token-less error message missing %q:\n%s", want, withoutToken)
		}
	}
	withToken := (&releaseAccessError{Status: 404, HadToken: true, Operation: "cannot read the latest astro release"}).Error()
	if !strings.Contains(withToken, "sees 404, not 403") {
		t.Errorf("error should explain GitHub's 404-for-private behavior:\n%s", withToken)
	}
	rateLimited := (&releaseAccessError{Status: 403, HadToken: true, Operation: "op"}).Error()
	if !strings.Contains(rateLimited, "rate-limited") {
		t.Errorf("403 with a token should mention rate limiting:\n%s", rateLimited)
	}
}

func TestFetchJSONSendsAuthAndMapsAccessFailures(t *testing.T) {
	var gotAuth, gotAccept, gotVersion string
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotVersion = r.Header.Get("X-GitHub-Api-Version")
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v1.4.0"})
	}))
	defer server.Close()

	var release githubRelease
	if err := fetchJSON(context.Background(), server.URL, "secret-token", &release); err != nil {
		t.Fatalf("fetchJSON: %v", err)
	}
	if release.TagName != "v1.4.0" {
		t.Errorf("tag = %q", release.TagName)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
	if gotAccept != "application/vnd.github+json" {
		t.Errorf("Accept header = %q", gotAccept)
	}
	if gotVersion == "" {
		t.Error("X-GitHub-Api-Version header not sent")
	}

	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		status = code
		err := fetchJSON(context.Background(), server.URL, "", &release)
		var accessErr *releaseAccessError
		if !errors.As(err, &accessErr) {
			t.Errorf("status %d should produce a releaseAccessError, got %v", code, err)
			continue
		}
		if accessErr.Status != code {
			t.Errorf("access error recorded status %d, want %d", accessErr.Status, code)
		}
	}
}

func TestDownloadFileRequestsOctetStreamWithAuth(t *testing.T) {
	var gotAuth, gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		_, _ = w.Write([]byte("archive-bytes"))
	}))
	defer server.Close()

	file, err := os.CreateTemp(t.TempDir(), "asset")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := downloadFile(context.Background(), server.URL, "secret-token", file); err != nil {
		t.Fatalf("downloadFile: %v", err)
	}
	// The API asset endpoint returns JSON metadata unless octet-stream is
	// requested explicitly, so this header is load-bearing, not cosmetic.
	if gotAccept != "application/octet-stream" {
		t.Errorf("Accept header = %q, want application/octet-stream", gotAccept)
	}
	if gotAuth != "Bearer secret-token" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
	body, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "archive-bytes" {
		t.Errorf("downloaded body = %q", body)
	}
}

// TestRunUpdateResolvesRealAssetNameAndAuthenticatedURL exercises the whole
// #53 chain: the name astro asks for, and the URL it downloads from when a
// token is present. Before the fix it asked for astro_<version>_Darwin_x86_64
// and downloaded from browser_download_url, so both halves 404'd.
func TestRunUpdateResolvesRealAssetNameAndAuthenticatedURL(t *testing.T) {
	clearGitHubTokenEnv(t)
	t.Setenv("GITHUB_TOKEN", "secret-token")

	assetName := resolveAssetName()
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		release := githubRelease{
			TagName: "v9.9.9",
			Assets: []githubAsset{
				{
					Name:               assetName,
					URL:                "https://api.example.com/repos/" + releaseRepo + "/releases/assets/42",
					BrowserDownloadURL: "https://github.com/" + releaseRepo + "/releases/download/v9.9.9/" + assetName,
				},
				{Name: "astro-checksums.txt", URL: "https://api.example.com/assets/43"},
			},
		}
		_ = json.NewEncoder(w).Encode(release)
	}))
	defer server.Close()
	t.Setenv("ASTRO_GITHUB_API_URL", server.URL)

	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)
	command.SetErr(&out)
	if err := runUpdate(command, true); err != nil {
		t.Fatalf("runUpdate dry-run: %v", err)
	}

	if want := "/repos/" + releaseRepo + "/releases/latest"; requestedPath != want {
		t.Errorf("requested %q, want %q", requestedPath, want)
	}
	got := out.String()
	if !strings.Contains(got, "releases/assets/42") {
		t.Errorf("update should download via the authenticated asset API, got:\n%s", got)
	}
	if strings.Contains(got, "releases/download/") {
		t.Errorf("browser_download_url 404s on a private release, got:\n%s", got)
	}
}

// TestRunUpdateWithoutTokenExplainsHowToAuthenticate covers the user-facing
// half of #53 item 3.
func TestRunUpdateWithoutTokenExplainsHowToAuthenticate(t *testing.T) {
	clearGitHubTokenEnv(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GitHub hides private repositories behind 404 for anonymous callers.
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	t.Setenv("ASTRO_GITHUB_API_URL", server.URL)

	command := &cobra.Command{}
	command.SetOut(new(bytes.Buffer))
	command.SetErr(new(bytes.Buffer))
	err := runUpdate(command, true)
	if err == nil {
		t.Fatal("expected an error when the release cannot be read")
	}
	var accessErr *releaseAccessError
	if !errors.As(err, &accessErr) {
		t.Fatalf("expected a releaseAccessError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "GITHUB_TOKEN") {
		t.Errorf("error should tell the user how to authenticate:\n%v", err)
	}
}

// TestRunUpdateReportsMissingAssetForPlatform guards the "release exists but
// has no archive for me" case, which should name what it looked for.
func TestRunUpdateReportsMissingAssetForPlatform(t *testing.T) {
	clearGitHubTokenEnv(t)
	t.Setenv("GITHUB_TOKEN", "secret-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubRelease{
			TagName: "v9.9.9",
			Assets:  []githubAsset{{Name: "astro-plan9-mips.tar.gz"}},
		})
	}))
	defer server.Close()
	t.Setenv("ASTRO_GITHUB_API_URL", server.URL)

	command := &cobra.Command{}
	command.SetOut(new(bytes.Buffer))
	command.SetErr(new(bytes.Buffer))
	err := runUpdate(command, true)
	if err == nil {
		t.Fatal("expected an error when no asset matches this platform")
	}
	if !strings.Contains(err.Error(), resolveAssetName()) {
		t.Errorf("error should name the asset it looked for (%s):\n%v", resolveAssetName(), err)
	}
}

// TestRunUpdateStopsWhenAlreadyCurrent keeps the no-op path honest.
func TestRunUpdateStopsWhenAlreadyCurrent(t *testing.T) {
	clearGitHubTokenEnv(t)
	t.Setenv("GITHUB_TOKEN", "secret-token")
	original := Version
	Version = "9.9.9"
	defer func() { Version = original }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(githubRelease{TagName: "v9.9.9"})
	}))
	defer server.Close()
	t.Setenv("ASTRO_GITHUB_API_URL", server.URL)

	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)
	command.SetErr(&out)
	if err := runUpdate(command, false); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf("Already at latest version %s", Version)) {
		t.Errorf("expected a no-op message, got:\n%s", out.String())
	}
}

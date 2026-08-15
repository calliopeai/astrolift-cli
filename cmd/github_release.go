package cmd

import (
	"fmt"
	"os"
	"strings"
)

// releaseRepo is the GitHub repository that publishes astro release archives.
const releaseRepo = "calliopeai/astrolift-cli"

// defaultGitHubAPIBaseURL is the public GitHub API root.
const defaultGitHubAPIBaseURL = "https://api.github.com"

// githubAPIBaseURL returns the API root used for release lookups. The override
// exists for tests and for GitHub Enterprise mirrors of the release repo; it is
// never required in normal use.
func githubAPIBaseURL() string {
	if override := strings.TrimSpace(os.Getenv("ASTRO_GITHUB_API_URL")); override != "" {
		return strings.TrimSuffix(override, "/")
	}
	return defaultGitHubAPIBaseURL
}

// githubToken resolves the credential used to read release metadata and
// archives. calliopeai/astrolift-cli is private, so every release read needs
// one: anonymous requests get 404, not 401 (GitHub hides private repos rather
// than admitting they exist).
//
// ASTRO_GITHUB_TOKEN wins so a user can point astro at a different identity
// than the ambient GITHUB_TOKEN their CI job already exports.
func githubToken() string {
	for _, key := range []string{"ASTRO_GITHUB_TOKEN", "GITHUB_TOKEN", "GH_TOKEN"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

// releaseAccessError is returned for the HTTP statuses that mean "you cannot
// read this release", so callers can print a fix instead of a bare status code.
type releaseAccessError struct {
	Status    int
	URL       string
	HadToken  bool
	Operation string
}

func (e *releaseAccessError) Error() string {
	var reason string
	switch {
	case !e.HadToken:
		reason = fmt.Sprintf(""+
			"%s is a private repository and no GitHub token was found, so the\n"+
			"release API returned %d.", releaseRepo, e.Status)
	case e.Status == 403:
		reason = "the GitHub token was rejected (403). It is either rate-limited or\n" +
			"lacks read access to " + releaseRepo + "."
	default:
		reason = fmt.Sprintf(""+
			"the GitHub token cannot read %s (%d). A token without access to a\n"+
			"private repository sees 404, not 403.", releaseRepo, e.Status)
	}

	return fmt.Sprintf(`%s: %s

Set a token that can read the release repository:

  export GITHUB_TOKEN="$(gh auth token)"   # reuse an existing gh login
  export GITHUB_TOKEN=<PAT>                # or a token with the 'repo' scope

No token, no problem — the container image is public:

  docker run --rm calliopeai/astrolift-cli:latest version`,
		e.Operation, reason)
}

// isReleaseAccessStatus reports whether a status code means the caller could
// not read the release because of auth or repository visibility.
func isReleaseAccessStatus(status int) bool {
	return status == 401 || status == 403 || status == 404
}

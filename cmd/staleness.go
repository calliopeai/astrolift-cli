package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// The staleness hint exists because an installed binary silently drifting
// behind the platform is invisible until a command fails with a confusing
// error (#46). It is advisory only: one line on stderr, at most once a day,
// never on the data path, never fatal.
const (
	// staleCheckInterval bounds how often astro asks GitHub for the latest tag.
	staleCheckInterval = 24 * time.Hour
	// staleCheckTimeout keeps the hint from delaying a command noticeably.
	// A failed attempt still records its timestamp, so a machine that is
	// offline or token-less pays this once a day, not once a command.
	staleCheckTimeout = 2 * time.Second
	// noUpdateCheckEnv opts out entirely.
	noUpdateCheckEnv = "ASTROLIFT_NO_UPDATE_CHECK"
)

// versionCheckState is the on-disk cache for the daily check.
type versionCheckState struct {
	CheckedAt time.Time `json:"checked_at"`
	LatestTag string    `json:"latest_tag"`
}

func versionCheckCachePath() string {
	return filepath.Join(config.Dir(), "version-check.json")
}

// stalenessNotice returns the one-line hint, or "" when there is nothing
// useful to say. Pure: no clock, no network, no disk.
func stalenessNotice(current, latest string) string {
	if current == "" || latest == "" || current == "dev" {
		return ""
	}
	comparison, err := CompareSemver(strings.TrimPrefix(current, "v"), strings.TrimPrefix(latest, "v"))
	if err != nil || comparison >= 0 {
		return ""
	}
	return fmt.Sprintf(
		"astro %s is behind the latest release %s — run 'astro update' (set %s=1 to silence)",
		strings.TrimPrefix(current, "v"), strings.TrimPrefix(latest, "v"), noUpdateCheckEnv,
	)
}

// stalenessCheckEnabled reports whether this invocation should even consider
// the hint. Every branch here is a case where the line would be noise or a
// correctness hazard rather than a help.
func stalenessCheckEnabled(cmd *cobra.Command) bool {
	// An unstamped source build has no meaningful version to compare.
	if Version == "dev" {
		return false
	}
	// Machine-readable output must never gain an extra line, on either stream.
	if jsonOut, err := cmd.Flags().GetBool("json"); err == nil && jsonOut {
		return false
	}
	if truthyEnv(noUpdateCheckEnv) {
		return false
	}
	// CI runs pin a version deliberately; nagging a pipeline is pure noise,
	// and the extra request would run on every job.
	if os.Getenv("CI") != "" || os.Getenv("ASTROLIFT_DEPLOY_TOKEN") != "" {
		return false
	}
	// The release repository is private (#53). Without a token the lookup can
	// only ever 404, so skip it rather than burn a request and a timeout.
	return githubToken() != ""
}

func truthyEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

// maybeNotifyStale prints the hint when warranted. It swallows every error by
// design — a version check must never change the outcome of a command.
func maybeNotifyStale(cmd *cobra.Command) {
	if !stalenessCheckEnabled(cmd) {
		return
	}
	latest, err := cachedLatestTag(time.Now())
	if err != nil || latest == "" {
		return
	}
	if notice := stalenessNotice(Version, latest); notice != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), notice)
	}
}

// cachedLatestTag returns the latest release tag, consulting the daily cache
// before the network.
func cachedLatestTag(now time.Time) (string, error) {
	state, readErr := readVersionCheckState()
	if readErr == nil && !state.CheckedAt.IsZero() && now.Sub(state.CheckedAt) < staleCheckInterval {
		return state.LatestTag, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), staleCheckTimeout)
	defer cancel()

	var release githubRelease
	if err := fetchJSON(ctx, latestReleaseURL(), githubToken(), &release); err != nil {
		// Record the attempt so the next command does not retry immediately.
		writeVersionCheckState(versionCheckState{CheckedAt: now, LatestTag: state.LatestTag})
		return "", err
	}
	writeVersionCheckState(versionCheckState{CheckedAt: now, LatestTag: release.TagName})
	return release.TagName, nil
}

func readVersionCheckState() (versionCheckState, error) {
	var state versionCheckState
	body, err := os.ReadFile(versionCheckCachePath())
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return versionCheckState{}, err
	}
	return state, nil
}

// writeVersionCheckState persists the cache on a best-effort basis.
func writeVersionCheckState(state versionCheckState) {
	body, err := json.Marshal(state)
	if err != nil {
		return
	}
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(versionCheckCachePath(), body, 0o644)
}

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// CompatStatus is what the platform returns from
// GET /api/cli/v1/compat/<cli-version>/.
type CompatStatus struct {
	Compatible    bool   `json:"compatible"`
	MinSupported  string `json:"min_supported_cli"`
	LatestStable  string `json:"latest_stable_cli"`
	UpgradeURL    string `json:"upgrade_url"`
	Reason        string `json:"reason,omitempty"`
}

var versionCheckCmd = &cobra.Command{
	Use:   "version-check",
	Short: "Check whether this CLI version is compatible with the platform",
	Long: `Calls /api/cli/v1/compat/<cli-version>/ on the current server.
Compares this binary's version (set via -ldflags at build time)
against the server's min_supported_cli + latest_stable.

Exits non-zero when the server reports incompatible.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		client, _, _, err := loadActiveClient(ctx, false)
		if err != nil {
			return err
		}
		var status CompatStatus
		if err := client.Get(ctx, fmt.Sprintf("/api/cli/v1/compat/%s/", Version), &status); err != nil {
			return err
		}
		if !status.Compatible {
			return fmt.Errorf(
				"CLI %s is not compatible with the platform.\nReason: %s\nMin supported: %s\nLatest stable: %s\nUpgrade: %s",
				Version, status.Reason,
				status.MinSupported, status.LatestStable,
				status.UpgradeURL,
			)
		}
		fmt.Fprintf(cmd.OutOrStdout(),
			"CLI %s is compatible with this platform.\nLatest stable: %s\n",
			Version, status.LatestStable,
		)
		return nil
	},
}

var selfUpdateCmd = &cobra.Command{
	Use:   "self-update",
	Short: "Print the upgrade URL for this platform's recommended CLI version",
	Long: `Doesn't actually self-replace the binary (that would need
admin perms on most installs). Prints the platform's recommended
upgrade path so the operator can re-install via brew/scoop/curl.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
		defer cancel()
		client, _, _, err := loadActiveClient(ctx, false)
		if err != nil {
			return err
		}
		var status CompatStatus
		if err := client.Get(ctx, fmt.Sprintf("/api/cli/v1/compat/%s/", Version), &status); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Latest stable CLI: %s\nUpgrade: %s\n",
			status.LatestStable, status.UpgradeURL,
		)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCheckCmd, selfUpdateCmd)
}

// CompareSemver returns -1, 0, or 1 for a vs b. Used by tests +
// any caller that needs to compare CLI versions without calling
// the server.
func CompareSemver(a, b string) (int, error) {
	aMaj, aMin, aPatch, err := parseSemver(a)
	if err != nil {
		return 0, fmt.Errorf("parsing %q: %w", a, err)
	}
	bMaj, bMin, bPatch, err := parseSemver(b)
	if err != nil {
		return 0, fmt.Errorf("parsing %q: %w", b, err)
	}
	for i, pair := range [][2]int{{aMaj, bMaj}, {aMin, bMin}, {aPatch, bPatch}} {
		if pair[0] < pair[1] {
			return -1, nil
		}
		if pair[0] > pair[1] {
			return 1, nil
		}
		_ = i
	}
	return 0, nil
}

func parseSemver(v string) (int, int, int, error) {
	v = strings.TrimPrefix(v, "v")
	// Strip pre-release/build metadata
	for _, sep := range []string{"-", "+"} {
		if idx := strings.Index(v, sep); idx >= 0 {
			v = v[:idx]
		}
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, errors.New("expected MAJOR.MINOR.PATCH")
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	patch, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, errors.New("non-integer version component")
	}
	return maj, min, patch, nil
}


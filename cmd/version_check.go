package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// CompatStatus is what the platform returns from
// GET /api/cli/v1/compat/<cli-version>/.
type CompatStatus struct {
	Compatible   bool   `json:"compatible"`
	MinSupported string `json:"min_supported_cli"`
	LatestStable string `json:"latest_stable_cli"`
	UpgradeURL   string `json:"upgrade_url"`
	Reason       string `json:"reason,omitempty"`
}

// versionCheckCapability is what a server advertises when it serves the public
// install handshake this CLI reads before login. The server publishes no
// minimum CLI version and a fixed server version, so this capability is the
// requirement an older server can be told about.
const versionCheckCapability = "server_info.handshake"

// versionCheckQuery is validated against the app's public discovery schema by
// integration/test_version_check.py.
const versionCheckQuery = `query VersionCheck { astroliftServerInfo { version apiVersion capabilities } }`

// versionCheckRequirement ends every unsupported-server reason.
const versionCheckRequirement = "This CLI reads astroliftServerInfo at /app/gql/config/public/ and needs " +
	versionCheckCapability + " there: upgrade the Astrolift server, or ask its operator to expose that route."

// versionCheckResult is `astro version-check --json`.
type versionCheckResult struct {
	Compatible         bool   `json:"compatible"`
	CLIVersion         string `json:"cliVersion"`
	Server             string `json:"server,omitempty"`
	APIURL             string `json:"apiUrl"`
	ServerVersion      string `json:"serverVersion,omitempty"`
	APIVersion         string `json:"apiVersion,omitempty"`
	RequiredCapability string `json:"requiredCapability"`
	Reason             string `json:"reason,omitempty"`
}

// versionCheckError is a check that could not finish. With --json it is the
// one {"error": ...} object on stderr.
type versionCheckError struct {
	Message string `json:"message"`
	Status  int    `json:"status,omitempty"`
}

func (e *versionCheckError) Error() string { return e.Message }

// versionCheckReported was already written as JSON; main exits 1 without
// printing it again.
type versionCheckReported struct{ error }

func (e *versionCheckReported) Unwrap() error         { return e.error }
func (e *versionCheckReported) AlreadyReported() bool { return true }

var versionCheckCmd = &cobra.Command{
	Use:   "version-check",
	Short: "Check whether the selected server supports this CLI",
	Long: `Reads the selected server's public install discovery
(astroliftServerInfo at /app/gql/config/public/), the handshake this CLI
uses to learn a server's version and capabilities. It sends no
credentials, so it works before ` + "`astro auth login`" + `.

A server that answers it and advertises server_info.handshake is
compatible: the command prints the server version and exits 0.
Individual commands may still need a newer server. A server without
public install discovery is not supported: the command names the
missing capability and exits 1.

With --json, stdout carries one result object (compatible, cliVersion,
server, apiUrl, serverVersion, apiVersion, requiredCapability, reason),
for an unsupported server too. A check that cannot finish exits 1 with
one {"error": {...}} object on stderr.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON := boolFlag(cmd, "json")
		result, err := runVersionCheck(cmd)
		if err != nil {
			if asJSON {
				return reportVersionCheckError(cmd, err)
			}
			return err
		}
		if asJSON {
			if err := renderJSON(cmd, result); err != nil {
				return err
			}
			if !result.Compatible {
				return &versionCheckReported{errors.New(result.Reason)}
			}
			return nil
		}
		if !result.Compatible {
			return fmt.Errorf("CLI %s does not support the server at %s. %s", result.CLIVersion, result.APIURL, result.Reason)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "CLI %s is compatible with %s.\nServer version: %s (api %s)\n",
			result.CLIVersion, result.APIURL, result.ServerVersion, result.APIVersion)
		return nil
	},
}

// runVersionCheck resolves the selected server the way loadActiveClient does,
// without loading or refreshing credentials: public discovery needs none.
func runVersionCheck(cmd *cobra.Command) (versionCheckResult, error) {
	cfg, err := config.Load()
	if err != nil {
		return versionCheckResult{}, err
	}
	slug, apiURL := "", strings.TrimSpace(viper.GetString("api_url"))
	if cfg.CurrentServer != "" || strings.TrimSpace(viper.GetString("server")) != "" {
		var entry config.ServerEntry
		if slug, entry, err = selectedServer(cfg, nil); err != nil {
			return versionCheckResult{}, err
		}
		if apiURL == "" {
			apiURL = entry.APIURL
		}
	}
	if apiURL == "" {
		return versionCheckResult{}, errors.New("no current server. run `astro server add <slug> <api-url>` or pass --api-url")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
	defer cancel()
	result, err := checkServerCompatibility(ctx, api.NewClient(apiURL, "", boolFlag(cmd, "debug")))
	result.Server = slug
	return result, err
}

// checkServerCompatibility reads public install discovery. An absent route or
// handshake is an unsupported-server result; any other failure is an error.
// Neither ever carries the response body.
func checkServerCompatibility(ctx context.Context, client *api.Client) (versionCheckResult, error) {
	result := versionCheckResult{
		CLIVersion:         Version,
		APIURL:             publicServerURL(client.BaseURL()),
		RequiredCapability: versionCheckCapability,
	}
	var response struct {
		Info *struct {
			Version      string   `json:"version"`
			APIVersion   string   `json:"apiVersion"`
			Capabilities []string `json:"capabilities"`
		} `json:"astroliftServerInfo"`
	}
	err := client.PublicGraphQL(ctx, versionCheckQuery, nil, &response)
	var httpErr *api.HTTPError
	var refusal *api.GraphQLResponseError
	switch {
	case errors.As(err, &httpErr) && httpErr.Status == http.StatusNotFound:
		return result.unsupported("The server has no public install discovery (POST /app/gql/config/public/ returned HTTP 404).")
	case errors.Is(err, api.ErrSchemaMismatch):
		return result.unsupported("The server's public install discovery does not serve astroliftServerInfo.")
	case errors.As(err, &httpErr):
		// HTTPError's own text sends a 401 to `astro auth login`, but this
		// request carried no credential, so signing in cannot change it.
		return result, &versionCheckError{
			Message: fmt.Sprintf("public install discovery at %s returned HTTP %d %s", result.APIURL, httpErr.Status, http.StatusText(httpErr.Status)),
			Status:  httpErr.Status,
		}
	case errors.As(err, &refusal) && refusal.Status >= http.StatusBadRequest:
		// The server's rate limit on this route answers 429 with a
		// GraphQL-shaped body; --json keeps that status.
		return result, &versionCheckError{
			Message: fmt.Sprintf("reading public install discovery at %s: %v", result.APIURL, err),
			Status:  refusal.Status,
		}
	case err != nil:
		return result, &versionCheckError{Message: fmt.Sprintf("reading public install discovery at %s: %v", result.APIURL, err)}
	case response.Info == nil:
		return result.unsupported("The server's public install discovery does not serve astroliftServerInfo.")
	}
	result.ServerVersion = response.Info.Version
	result.APIVersion = response.Info.APIVersion
	if !slices.Contains(response.Info.Capabilities, versionCheckCapability) {
		return result.unsupported("The server's public install discovery does not advertise " + versionCheckCapability + ".")
	}
	result.Compatible = true
	return result, nil
}

// unsupported is the result for a server without the handshake this CLI needs.
func (r versionCheckResult) unsupported(cause string) (versionCheckResult, error) {
	r.Reason = cause + " " + versionCheckRequirement
	return r, nil
}

// reportVersionCheckError writes the one machine-readable error for --json.
func reportVersionCheckError(cmd *cobra.Command, err error) error {
	var failure *versionCheckError
	if !errors.As(err, &failure) {
		failure = &versionCheckError{Message: err.Error()}
	}
	encoder := json.NewEncoder(cmd.ErrOrStderr())
	encoder.SetIndent("", "  ")
	if encodeErr := encoder.Encode(map[string]any{"error": failure}); encodeErr != nil {
		return fmt.Errorf("writing version-check error: %w (original error: %v)", encodeErr, err)
	}
	return &versionCheckReported{err}
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

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/auth"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// loadActiveClient returns an authenticated API client for the
// current server, refreshing the access token if it's near-expiry.
//
// Returns auth-needed errors when no current server is configured
// or when no credentials exist.
func loadActiveClient(ctx context.Context, debug bool) (*api.Client, *config.Config, *config.ServerEntry, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	if cfg.CurrentServer == "" {
		return nil, nil, nil, errors.New(
			"no current server. run `astro server add <slug> <api-url>` and `astro auth login`",
		)
	}
	entry, ok := cfg.Servers[cfg.CurrentServer]
	if !ok {
		return nil, nil, nil, fmt.Errorf("current server %q missing from config", cfg.CurrentServer)
	}

	// CI mode: read token from env. Skip stored credentials.
	if envToken := os.Getenv("ASTROLIFT_DEPLOY_TOKEN"); envToken != "" {
		client := api.NewClient(entry.APIURL, envToken, debug)
		return client, cfg, &entry, nil
	}

	creds, err := config.LoadCredentials(cfg.CurrentServer)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("loading credentials (run `astro auth login`): %w", err)
	}

	// Auto-refresh if expiring within 60s
	if creds.IsExpired(time.Minute) {
		fresh, err := auth.RefreshCredentials(ctx, entry.APIURL, creds.RefreshToken)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("token expired and refresh failed (run `astro auth login`): %w", err)
		}
		creds = fresh
		if err := config.SaveCredentials(cfg.CurrentServer, fresh); err != nil {
			return nil, nil, nil, fmt.Errorf("saving refreshed credentials: %w", err)
		}
	}

	client := api.NewClient(entry.APIURL, creds.AccessToken, debug)
	return client, cfg, &entry, nil
}

// scaffoldManifest writes a minimal astrolift.toml at path.
// Refuses to overwrite an existing file.
func scaffoldManifest(path string, cmd *cobra.Command) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}
	content := "# astrolift.toml — Astrolift app manifest\n" +
		"# Spec ref: spec 05 (manifest)\n" +
		"#\n" +
		"# Run `astro app register` after editing to register this app\n" +
		"# on the platform.\n" +
		"\n" +
		"[app]\n" +
		"slug = \"my-app\"\n" +
		"display_name = \"My App\"\n" +
		"\n" +
		"[[environments]]\n" +
		"name = \"production\"\n" +
		"\n" +
		"# Uncomment + edit the workload(s) your app actually runs.\n" +
		"# [[workloads]]\n" +
		"# slug = \"web\"\n" +
		"# image = \"ghcr.io/myorg/my-app:latest\"\n" +
		"# port = 8080\n" +
		"#\n" +
		"# [workloads.resources]\n" +
		"# cpu_request = \"100m\"\n" +
		"# cpu_limit = \"500m\"\n" +
		"# memory_request = \"128Mi\"\n" +
		"# memory_limit = \"256Mi\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", path)
	fmt.Fprintln(cmd.OutOrStdout(), "Edit the file, then run `astro app register`.")
	return nil
}

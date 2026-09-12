package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/auth"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	overrideAPIURL := strings.TrimSpace(viper.GetString("api_url"))
	explicitToken := strings.TrimSpace(viper.GetString("token"))
	deployToken := strings.TrimSpace(os.Getenv("ASTROLIFT_DEPLOY_TOKEN"))
	if cfg.CurrentServer == "" && strings.TrimSpace(viper.GetString("server")) == "" {
		if overrideAPIURL != "" && (explicitToken != "" || deployToken != "") {
			entry := config.ServerEntry{APIURL: overrideAPIURL, DisplayName: "ephemeral"}
			token := explicitToken
			if token == "" {
				token = deployToken
			}
			client := api.NewClient(entry.APIURL, token, debug)
			return client, cfg, &entry, nil
		}
		return nil, nil, nil, errors.New(
			"no current server. run `astro server add <slug> <api-url>` and `astro auth login`",
		)
	}
	serverSlug, entry, err := selectedServer(cfg, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	if overrideAPIURL != "" {
		entry.APIURL = overrideAPIURL
	}

	// The global --token flag (and ASTROLIFT_TOKEN through Viper) is an
	// explicit operator override. Honor it before CI/stored credentials as the
	// root help and CLI precedence contract promise.
	if explicitToken != "" {
		client := api.NewClient(entry.APIURL, explicitToken, debug)
		return client, cfg, &entry, nil
	}

	// CI mode: read token from env. Skip stored credentials.
	if deployToken != "" {
		client := api.NewClient(entry.APIURL, deployToken, debug)
		return client, cfg, &entry, nil
	}

	creds, err := config.LoadCredentials(serverSlug)
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
		if err := config.SaveCredentials(serverSlug, fresh); err != nil {
			return nil, nil, nil, fmt.Errorf("saving refreshed credentials: %w", err)
		}
	}

	client := api.NewClient(entry.APIURL, creds.AccessToken, debug)
	return client, cfg, &entry, nil
}

// detectedFramework summarises what detect() found in a directory.
type detectedFramework struct {
	// Name is a human-readable label ("Django", "Next.js", …)
	Name string
	// Port is the default listen port for this framework (0 = unknown)
	Port int
	// CPULimit is a sensible default cpu_limit for the primary workload
	CPULimit string
	// MemoryLimit is a sensible default memory_limit
	MemoryLimit string
}

// fileExists reports whether a path exists relative to dir.
func fileExists(dir, rel string) bool {
	_, err := os.Stat(filepath.Join(dir, rel))
	return err == nil
}

// detectFramework inspects a directory and returns the most likely framework.
// Returns nil if nothing recognisable is found — a generic manifest is used.
func detectFramework(dir string) *detectedFramework {
	type probe struct {
		files       []string
		content     string // substring to look for in the first match
		contentFile string
		fw          detectedFramework
	}

	probes := []probe{
		// Next.js (check before generic Node.js)
		{files: []string{"next.config.js", "next.config.ts", "next.config.mjs"}, fw: detectedFramework{"Next.js", 3000, "500m", "512Mi"}},
		// Django
		{files: []string{"manage.py"}, contentFile: "requirements.txt", content: "django", fw: detectedFramework{"Django", 8000, "500m", "256Mi"}},
		// FastAPI / generic Python ASGI
		{files: []string{"requirements.txt"}, content: "fastapi", fw: detectedFramework{"FastAPI", 8000, "500m", "256Mi"}},
		// Flask
		{files: []string{"requirements.txt"}, content: "flask", fw: detectedFramework{"Flask", 5000, "200m", "128Mi"}},
		// Generic Python (Pipfile or pyproject without a specific marker)
		{files: []string{"Pipfile", "pyproject.toml"}, fw: detectedFramework{"Python", 8000, "500m", "256Mi"}},
		// Go module
		{files: []string{"go.mod"}, fw: detectedFramework{"Go", 8080, "200m", "64Mi"}},
		// Ruby on Rails
		{files: []string{"Gemfile"}, content: "rails", fw: detectedFramework{"Rails", 3000, "500m", "256Mi"}},
		// Generic Ruby
		{files: []string{"Gemfile"}, fw: detectedFramework{"Ruby", 3000, "500m", "256Mi"}},
		// Spring (Maven)
		{files: []string{"pom.xml"}, fw: detectedFramework{"Spring (Maven)", 8080, "500m", "512Mi"}},
		// Spring (Gradle)
		{files: []string{"build.gradle", "build.gradle.kts"}, fw: detectedFramework{"Spring (Gradle)", 8080, "500m", "512Mi"}},
		// Generic Node.js
		{files: []string{"package.json"}, fw: detectedFramework{"Node.js", 3000, "500m", "256Mi"}},
	}

	for _, p := range probes {
		found := false
		for _, f := range p.files {
			if fileExists(dir, f) {
				found = true
				// If content check is required, scan the file
				if p.content != "" {
					checkFile := f
					if p.contentFile != "" {
						checkFile = p.contentFile
					}
					data, err := os.ReadFile(filepath.Join(dir, checkFile))
					if err != nil || !strings.Contains(strings.ToLower(string(data)), p.content) {
						found = false
					}
				}
				break
			}
		}
		if found {
			fw := p.fw
			return &fw
		}
	}
	// Dockerfile present → generic container workload
	if fileExists(dir, "Dockerfile") {
		return &detectedFramework{"Docker", 8080, "500m", "256Mi"}
	}
	return nil
}

// scaffoldManifest writes a minimal astrolift.toml at path.
// Refuses to overwrite an existing file. Detects the project framework
// and generates a tailored starting template.
func scaffoldManifest(path string, cmd *cobra.Command) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("%s already exists", path)
	}

	dir := filepath.Dir(path)
	fw := detectFramework(dir)

	appSlug := filepath.Base(dir)
	if appSlug == "." || appSlug == "" {
		appSlug = "my-app"
	}

	var port int = 8080
	var cpuLimit string = "500m"
	var memLimit string = "256Mi"
	var fwNote string = "# No framework detected — using generic defaults."

	if fw != nil {
		port = fw.Port
		if fw.Port == 0 {
			port = 8080
		}
		cpuLimit = fw.CPULimit
		memLimit = fw.MemoryLimit
		fwNote = fmt.Sprintf("# Detected framework: %s", fw.Name)
	}

	content := fmt.Sprintf(`# astrolift.toml — Astrolift app manifest
# Reference: https://astrolift.dev/reference/astrolift-toml/
%s
#
# Run `+"`astro app register`"+` after editing to register this app
# on the platform.

astrolift_version = 1
name = %q

[app]
slug = %q
display_name = %q

[environments.production]

[[workloads]]
name = "web"
kind = "deployment"
replicas = 1
# Renders a Service + Ingress on a managed hostname. Without this the
# app deploys private with no route — set false for internal services.
is_public = true
cpu_request = "100m"
cpu_limit = %q
memory_request = "128Mi"
memory_limit = %q

  [[workloads.containers]]
  name = "web"
  is_primary = true
  port = %d

    [workloads.containers.healthcheck]
    kind = "http"
    value = "/health"
    port = %d
`, fwNote, appSlug, appSlug, appSlug, cpuLimit, memLimit, port, port)

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	if fw != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Detected: %s\n", fw.Name)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Created %s\n", path)
	fmt.Fprintln(cmd.OutOrStdout(), "Edit the file, then run `astro app register`.")
	return nil
}

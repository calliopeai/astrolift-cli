package cmd

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func jsonDecode(r io.Reader, v interface{}) error {
	return json.NewDecoder(r).Decode(v)
}

// githubRelease is a subset of the GitHub Releases API response.
type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	// URL is the api.github.com asset endpoint. It is the only one that
	// accepts a bearer token, so it is the download URL for private releases.
	URL string `json:"url"`
	// BrowserDownloadURL only works for public releases.
	BrowserDownloadURL string `json:"browser_download_url"`
}

// updateCmd replaces the running binary with the latest release from GitHub.
var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Self-update the astro CLI to the latest release",
	Long: `Downloads and installs the latest astro CLI binary from the
GitHub release page (github.com/calliopeai/astrolift-cli).

The current binary is replaced in-place. On Windows the replacement
happens via a rename; on Unix it's an atomic swap.

Use --dry-run to preview the update without applying it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		return runUpdate(cmd, dryRun)
	},
}

func init() {
	updateCmd.Flags().Bool("dry-run", false, "Show what would be updated without downloading")
	rootCmd.AddCommand(updateCmd)
}

// latestReleaseURL is the release-metadata endpoint for the current API root.
func latestReleaseURL() string {
	return fmt.Sprintf("%s/repos/%s/releases/latest", githubAPIBaseURL(), releaseRepo)
}

func runUpdate(cmd *cobra.Command, dryRun bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Fprintln(cmd.OutOrStdout(), "Checking for latest release…")

	token := githubToken()
	var release githubRelease
	if err := fetchJSON(ctx, latestReleaseURL(), token, &release); err != nil {
		var accessErr *releaseAccessError
		if errors.As(err, &accessErr) {
			return err
		}
		return fmt.Errorf("checking latest release: %w", err)
	}

	latest := strings.TrimPrefix(release.TagName, "v")
	current := strings.TrimPrefix(Version, "v")

	if latest == "" {
		return fmt.Errorf("could not determine latest version from GitHub API")
	}

	cmp, err := CompareSemver(current, latest)
	if err != nil {
		// Non-semver current version (e.g. "dev") — proceed with update
		cmp = -1
	}
	if cmp >= 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Already at latest version %s — nothing to do.\n", Version)
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Current: %s → Latest: %s\n", Version, release.TagName)

	// Determine the correct asset name for this OS/arch
	assetName := resolveAssetName()
	var downloadURL string
	for _, a := range release.Assets {
		if a.Name == assetName {
			// The bearer token only authenticates against the API asset
			// endpoint; browser_download_url 404s on a private release.
			downloadURL = a.URL
			if downloadURL == "" || token == "" {
				downloadURL = a.BrowserDownloadURL
			}
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf(
			"no release asset found for %s/%s (looked for %q). "+
				"Download manually from https://github.com/%s/releases/tag/%s",
			runtime.GOOS, runtime.GOARCH, assetName, releaseRepo, release.TagName,
		)
	}

	if dryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "[dry-run] Would download: %s\n", downloadURL)
		fmt.Fprintln(cmd.OutOrStdout(), "[dry-run] No changes made.")
		return nil
	}

	// Determine current binary path
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding current binary: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("resolving symlink: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Downloading %s…\n", assetName)

	tmpFile, err := os.CreateTemp("", "astro-update-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if err := downloadFile(ctx, downloadURL, token, tmpFile); err != nil {
		var accessErr *releaseAccessError
		if errors.As(err, &accessErr) {
			return err
		}
		return fmt.Errorf("downloading update: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Extract the binary from the archive
	binaryPath, err := extractBinary(tmpFile.Name(), assetName)
	if err != nil {
		return fmt.Errorf("extracting binary: %w", err)
	}
	defer os.Remove(binaryPath)

	// Make executable
	if err := os.Chmod(binaryPath, 0o755); err != nil {
		return fmt.Errorf("setting permissions: %w", err)
	}

	// Atomic replace: rename old binary, move new one in
	backup := self + ".old"
	_ = os.Remove(backup) // ignore error — old backup may not exist
	if err := os.Rename(self, backup); err != nil {
		return fmt.Errorf("backing up old binary: %w", err)
	}
	if err := os.Rename(binaryPath, self); err != nil {
		// Restore backup on failure
		_ = os.Rename(backup, self)
		return fmt.Errorf("installing new binary: %w", err)
	}
	_ = os.Remove(backup)

	fmt.Fprintf(cmd.OutOrStdout(), "Updated to %s at %s\n", release.TagName, self)
	fmt.Fprintln(cmd.OutOrStdout(), "Run `astro version` to confirm.")
	return nil
}

// resolveAssetName maps the current GOOS/GOARCH to a GitHub release asset
// filename.
//
// This MUST stay in lockstep with `archives.name_template` in
// .goreleaser.yaml, which publishes `astro-{os}-{arch}` using raw GOOS/GOARCH
// values: astro-linux-amd64.tar.gz, astro-darwin-arm64.tar.gz,
// astro-windows-amd64.zip. There is no version segment in the name, the
// separator is a hyphen, and nothing is capitalized or translated to x86_64
// (#53). TestResolveAssetNameMatchesGoreleaserTemplate pins that contract.
func resolveAssetName() string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("astro-%s-%s.zip", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("astro-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

// authorize applies the release-repo credential, if any.
func authorize(req *http.Request, token string) {
	req.Header.Set("User-Agent", "astrolift-cli/"+Version)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
}

func fetchJSON(ctx context.Context, url, token string, v interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	authorize(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if isReleaseAccessStatus(resp.StatusCode) {
		return &releaseAccessError{
			Status:    resp.StatusCode,
			URL:       url,
			HadToken:  token != "",
			Operation: "cannot read the latest astro release",
		}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}
	return jsonDecode(resp.Body, v)
}

func downloadFile(ctx context.Context, url, token string, dst *os.File) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	// The API asset endpoint serves release metadata as JSON unless the
	// caller explicitly asks for the bytes.
	req.Header.Set("Accept", "application/octet-stream")
	authorize(req, token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if isReleaseAccessStatus(resp.StatusCode) {
		return &releaseAccessError{
			Status:    resp.StatusCode,
			URL:       url,
			HadToken:  token != "",
			Operation: "cannot download the astro release archive",
		}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}
	_, err = io.Copy(dst, resp.Body)
	return err
}

// extractBinary extracts the `astro` binary from a tar.gz or zip archive.
// Returns the path to the extracted file.
func extractBinary(archivePath, assetName string) (string, error) {
	if strings.HasSuffix(assetName, ".zip") {
		return extractFromZip(archivePath)
	}
	return extractFromTarGz(archivePath)
}

func extractFromTarGz(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		binaryName := "astro"
		if hdr.Name == binaryName || filepath.Base(hdr.Name) == binaryName {
			out, err := os.CreateTemp("", "astro-new-*")
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				os.Remove(out.Name())
				return "", err
			}
			out.Close()
			return out.Name(), nil
		}
	}
	return "", fmt.Errorf("binary 'astro' not found in archive")
}

func extractFromZip(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer r.Close()

	for _, f := range r.File {
		binaryName := "astro.exe"
		if f.Name == binaryName || filepath.Base(f.Name) == binaryName {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()
			out, err := os.CreateTemp("", "astro-new-*.exe")
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, rc); err != nil {
				out.Close()
				os.Remove(out.Name())
				return "", err
			}
			out.Close()
			return out.Name(), nil
		}
	}
	return "", fmt.Errorf("binary 'astro.exe' not found in zip archive")
}

package cmd

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallScriptUsesReleaseAssetShapeAndVerifiesChecksum(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("installer supports macOS and Linux")
	}
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" {
		t.Skip("installer supports amd64 and arm64")
	}
	releaseDir := t.TempDir()
	archiveName := fmt.Sprintf("astro-%s-%s.tar.gz", runtime.GOOS, arch)
	archivePath := filepath.Join(releaseDir, archiveName)
	writeTestAstroArchive(t, archivePath)
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(body), archiveName)
	if err := os.WriteFile(filepath.Join(releaseDir, "astro-checksums.txt"), []byte(checksum), 0o644); err != nil {
		t.Fatal(err)
	}

	installDir := filepath.Join(t.TempDir(), "bin")
	command := exec.Command("sh", filepath.Join("..", "scripts", "install.sh"))
	command.Env = append(os.Environ(),
		"ASTRO_INSTALL_BASE_URL=file://"+releaseDir,
		"ASTRO_INSTALL_DIR="+installDir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installer failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "astro test-version") {
		t.Fatalf("installer did not run the installed binary:\n%s", output)
	}
	installed := filepath.Join(installDir, "astro")
	if info, err := os.Stat(installed); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("installed binary is missing or not executable: info=%v err=%v", info, err)
	}
	installedMan := filepath.Join(filepath.Dir(installDir), "share", "man", "man1", "astro.1")
	if info, err := os.Stat(installedMan); err != nil || info.Size() == 0 {
		t.Fatalf("installed man page is missing or empty: info=%v err=%v", info, err)
	}
}

func TestInstallScriptDownloadsPrivateReleaseWithGitHubCLI(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("installer supports macOS and Linux")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("installer supports amd64 and arm64")
	}

	releaseDir := t.TempDir()
	archiveName := fmt.Sprintf("astro-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archivePath := filepath.Join(releaseDir, archiveName)
	writeTestAstroArchive(t, archivePath)
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(body), archiveName)
	if err := os.WriteFile(filepath.Join(releaseDir, "astro-checksums.txt"), []byte(checksum), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeBin := t.TempDir()
	fakeGH := `#!/bin/sh
set -eu
case "$1 $2" in
  "auth status") exit 0 ;;
  "release view") printf '%s\n' 'v-test' ;;
  "release download")
    shift 2
    destination=.
    patterns=""
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --dir) destination="$2"; shift 2 ;;
        --pattern) patterns="${patterns} $2"; shift 2 ;;
        --repo) shift 2 ;;
        *) shift ;;
      esac
    done
    for pattern in $patterns; do
      cp "${FAKE_GH_RELEASE_DIR}/${pattern}" "${destination}/${pattern}"
    done
    ;;
  *) printf '%s\n' "unexpected gh command: $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(fakeBin, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}

	installDir := filepath.Join(t.TempDir(), "bin")
	command := exec.Command("sh", filepath.Join("..", "scripts", "install.sh"))
	command.Env = append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_GH_RELEASE_DIR="+releaseDir,
		"ASTRO_INSTALL_DIR="+installDir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installer failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "from calliopeai/astrolift-cli v-test") {
		t.Fatalf("installer did not resolve and download the private release:\n%s", output)
	}
	if !strings.Contains(string(output), "astro test-version") {
		t.Fatalf("installer did not run the installed binary:\n%s", output)
	}
}

// stubReleaseAPI serves a realistic private-release view of the GitHub REST
// API: metadata at /releases/latest and bytes at /releases/assets/<id>, both
// requiring a bearer token. Field order matches GitHub's real response so the
// installer's dependency-free JSON extraction is genuinely exercised.
func stubReleaseAPI(t *testing.T, token, archiveName, archivePath, checksumsPath string) *httptest.Server {
	t.Helper()
	const archiveID, checksumsID = 4242, 4243
	mux := http.NewServeMux()

	mux.HandleFunc("/repos/calliopeai/astrolift-cli/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			// GitHub hides private repositories behind 404.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
  "url": "https://api.github.com/repos/calliopeai/astrolift-cli/releases/1",
  "id": 1,
  "tag_name": "v9.9.9",
  "name": "v9.9.9",
  "author": { "login": "octocat", "id": 583231 },
  "assets": [
    {
      "url": "https://api.github.com/repos/calliopeai/astrolift-cli/releases/assets/%d",
      "id": %d,
      "node_id": "RA_kwDO",
      "name": "%s",
      "label": null,
      "uploader": { "login": "github-actions[bot]", "id": 41898282 },
      "content_type": "application/gzip",
      "state": "uploaded"
    },
    {
      "url": "https://api.github.com/repos/calliopeai/astrolift-cli/releases/assets/%d",
      "id": %d,
      "node_id": "RA_kwDP",
      "name": "astro-checksums.txt",
      "label": null,
      "uploader": { "login": "github-actions[bot]", "id": 41898282 },
      "content_type": "text/plain",
      "state": "uploaded"
    }
  ]
}`, archiveID, archiveID, archiveName, checksumsID, checksumsID)
	})

	serveAsset := func(path string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			// The real endpoint returns JSON metadata unless the caller asks
			// for the bytes, so hold the installer to that contract.
			if r.Header.Get("Accept") != "application/octet-stream" {
				t.Errorf("asset request Accept = %q, want application/octet-stream", r.Header.Get("Accept"))
			}
			http.ServeFile(w, r, path)
		}
	}
	mux.HandleFunc(fmt.Sprintf("/repos/calliopeai/astrolift-cli/releases/assets/%d", archiveID), serveAsset(archivePath))
	mux.HandleFunc(fmt.Sprintf("/repos/calliopeai/astrolift-cli/releases/assets/%d", checksumsID), serveAsset(checksumsPath))

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// unauthenticatedGH puts a `gh` on PATH that fails `auth status`, which is the
// realistic shape of a container that has the CLI but no login. The installer
// must fall through to the token path instead of dead-ending.
func unauthenticatedGH(t *testing.T) string {
	t.Helper()
	fakeBin := t.TempDir()
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(filepath.Join(fakeBin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return fakeBin
}

// installerEnv builds an environment with every GitHub credential cleared, so
// a developer's ambient GH_TOKEN cannot change what the test exercises.
// Duplicate keys are resolved last-wins by os/exec.
func installerEnv(t *testing.T, fakeBin string, extra ...string) []string {
	t.Helper()
	env := append(os.Environ(),
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"ASTRO_GITHUB_TOKEN=",
		"GITHUB_TOKEN=",
		"GH_TOKEN=",
		"ASTRO_INSTALL_BASE_URL=",
		"ASTRO_INSTALL_TAG=",
	)
	return append(env, extra...)
}

func skipUnlessInstallerPlatform(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("installer supports macOS and Linux")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("installer supports amd64 and arm64")
	}
}

// writeReleaseFixture produces the archive + checksums pair a real release
// publishes, named exactly as GoReleaser names them.
func writeReleaseFixture(t *testing.T) (dir, archiveName, archivePath, checksumsPath string) {
	t.Helper()
	dir = t.TempDir()
	archiveName = fmt.Sprintf("astro-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	archivePath = filepath.Join(dir, archiveName)
	writeTestAstroArchive(t, archivePath)
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	checksumsPath = filepath.Join(dir, "astro-checksums.txt")
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(body), archiveName)
	if err := os.WriteFile(checksumsPath, []byte(checksum), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, archiveName, archivePath, checksumsPath
}

// TestInstallScriptDownloadsPrivateReleaseWithToken covers the #53 gap that
// remained after the asset-name fix: a machine with no usable GitHub CLI. It
// must authenticate with GITHUB_TOKEN over the REST API and still verify the
// checksum.
func TestInstallScriptDownloadsPrivateReleaseWithToken(t *testing.T) {
	skipUnlessInstallerPlatform(t)
	_, archiveName, archivePath, checksumsPath := writeReleaseFixture(t)
	server := stubReleaseAPI(t, "test-token", archiveName, archivePath, checksumsPath)

	installDir := filepath.Join(t.TempDir(), "bin")
	command := exec.Command("sh", filepath.Join("..", "scripts", "install.sh"))
	command.Env = installerEnv(t, unauthenticatedGH(t),
		"GITHUB_TOKEN=test-token",
		"ASTRO_GITHUB_API_URL="+server.URL,
		"ASTRO_INSTALL_DIR="+installDir,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installer failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "releases/assets/4242") {
		t.Errorf("installer did not download through the authenticated asset API:\n%s", output)
	}
	if !strings.Contains(string(output), "astro test-version") {
		t.Errorf("installer did not run the installed binary:\n%s", output)
	}
	if info, err := os.Stat(filepath.Join(installDir, "astro")); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("installed binary missing or not executable: info=%v err=%v", info, err)
	}
}

// TestInstallScriptRejectsCorruptedArchive proves the checksum step is load
// bearing rather than decorative — the whole point of adding it in #53.
func TestInstallScriptRejectsCorruptedArchive(t *testing.T) {
	skipUnlessInstallerPlatform(t)
	releaseDir, archiveName, archivePath, checksumsPath := writeReleaseFixture(t)
	// Corrupt the archive after the checksum file was computed.
	if err := os.WriteFile(archivePath, []byte("not the archive you signed for"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = checksumsPath

	installDir := filepath.Join(t.TempDir(), "bin")
	command := exec.Command("sh", filepath.Join("..", "scripts", "install.sh"))
	command.Env = installerEnv(t, unauthenticatedGH(t),
		"ASTRO_INSTALL_BASE_URL=file://"+releaseDir,
		"ASTRO_INSTALL_DIR="+installDir,
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("installer accepted a corrupted archive:\n%s", output)
	}
	if !strings.Contains(string(output), "checksum mismatch for "+archiveName) {
		t.Errorf("expected a checksum mismatch error, got:\n%s", output)
	}
	if _, err := os.Stat(filepath.Join(installDir, "astro")); !os.IsNotExist(err) {
		t.Error("installer left a binary behind after a checksum failure")
	}
}

// TestInstallScriptWithoutCredentialsExplainsOptions is the actionable-error
// requirement: no credentials must not look like a missing asset.
func TestInstallScriptWithoutCredentialsExplainsOptions(t *testing.T) {
	skipUnlessInstallerPlatform(t)
	command := exec.Command("sh", filepath.Join("..", "scripts", "install.sh"))
	command.Env = installerEnv(t, unauthenticatedGH(t),
		"ASTRO_INSTALL_DIR="+filepath.Join(t.TempDir(), "bin"),
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("installer should fail without credentials:\n%s", output)
	}
	for _, want := range []string{"private repository", "gh auth login", "GITHUB_TOKEN", "docker run"} {
		if !strings.Contains(string(output), want) {
			t.Errorf("credential error should mention %q:\n%s", want, output)
		}
	}
}

// TestInstallScriptSurfacesRejectedToken covers the 404-means-auth case that
// made the original bug so confusing to diagnose.
func TestInstallScriptSurfacesRejectedToken(t *testing.T) {
	skipUnlessInstallerPlatform(t)
	_, archiveName, archivePath, checksumsPath := writeReleaseFixture(t)
	server := stubReleaseAPI(t, "correct-token", archiveName, archivePath, checksumsPath)

	command := exec.Command("sh", filepath.Join("..", "scripts", "install.sh"))
	command.Env = installerEnv(t, unauthenticatedGH(t),
		"GITHUB_TOKEN=wrong-token",
		"ASTRO_GITHUB_API_URL="+server.URL,
		"ASTRO_INSTALL_DIR="+filepath.Join(t.TempDir(), "bin"),
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("installer should fail with a rejected token:\n%s", output)
	}
	if !strings.Contains(string(output), "404") {
		t.Errorf("expected the status code in the error:\n%s", output)
	}
	if !strings.Contains(string(output), "wrong or unscoped") {
		t.Errorf("expected an explanation that 404 means auth here:\n%s", output)
	}
}

func writeTestAstroArchive(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	files := []struct {
		name string
		mode int64
		body []byte
	}{
		{name: "astro", mode: 0o755, body: []byte("#!/bin/sh\necho 'astro test-version'\n")},
		{name: "share/man/man1/astro.1", mode: 0o644, body: []byte(".TH ASTRO 1\n")},
		{name: "share/doc/astrolift/llms.txt", mode: 0o644, body: []byte("# Astrolift\n")},
	}
	for _, fixture := range files {
		if err := tw.WriteHeader(&tar.Header{Name: fixture.name, Mode: fixture.mode, Size: int64(len(fixture.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(fixture.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

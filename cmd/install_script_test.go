package cmd

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
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

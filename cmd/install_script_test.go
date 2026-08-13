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
}

func writeTestAstroArchive(t *testing.T, path string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho 'astro test-version'\n")
	if err := tw.WriteHeader(&tar.Header{Name: "astro", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
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

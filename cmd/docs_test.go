package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportPortableDocs(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "portable")
	count, err := exportPortableDocs(destination, false)
	if err != nil {
		t.Fatalf("exportPortableDocs: %v", err)
	}
	if count < 10 {
		t.Fatalf("exported only %d files", count)
	}
	for _, relative := range []string{
		"README.md",
		"llms.txt",
		"guides/manifest.md",
		"guides/mcp.md",
		"commands/astro.md",
		"man/man1/astro.1",
	} {
		info, err := os.Stat(filepath.Join(destination, relative))
		if err != nil {
			t.Errorf("missing %s: %v", relative, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", relative)
		}
	}

	if _, err := exportPortableDocs(destination, false); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("second export error = %v, want overwrite refusal", err)
	}
	if _, err := exportPortableDocs(destination, true); err != nil {
		t.Fatalf("forced export: %v", err)
	}
}

func TestExportManPages(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "man1")
	count, err := exportManPages(destination, false)
	if err != nil {
		t.Fatalf("exportManPages: %v", err)
	}
	if count < 2 {
		t.Fatalf("generated only %d man pages", count)
	}
	body, err := os.ReadFile(filepath.Join(destination, "astro.1"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Astrolift CLI Manual") {
		t.Fatalf("astro.1 has an unexpected header:\n%s", body)
	}
}

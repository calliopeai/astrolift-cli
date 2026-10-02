package privatefile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateFileExclusiveCreationAndBoundedRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.json")
	f, err := CreateExclusive(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("private metadata"); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := SyncParent(path); err != nil {
		t.Fatal(err)
	}
	if again, err := CreateExclusive(path); err == nil {
		_ = again.Close()
		t.Fatal("replaced an existing request")
	}
	data, err := ReadFile(path, 100)
	if err != nil || string(data) != "private metadata" {
		t.Fatalf("private reload failed: %q %v", data, err)
	}
	if _, err := ReadFile(path, 4); err == nil {
		t.Fatal("accepted oversized metadata")
	}
}

func TestPrivateFileReplacementLeavesOnlyProtectedFinalContents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.yaml")
	for _, value := range []string{"old private token", "new private token"} {
		if err := Write(path, []byte(value)); err != nil {
			t.Fatal(err)
		}
		actual, err := ReadFile(path, 1024)
		if err != nil || !bytes.Equal(actual, []byte(value)) {
			t.Fatalf("protected replacement failed: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "credentials.yaml" {
		t.Fatal("private replacement leaked a temporary file")
	}
}

func TestPrivateFileNonRegularTargetsAreRefused(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir, 1024); err == nil {
		t.Fatal("accepted directory as private content")
	}
	if err := Write(dir, []byte("must not write")); err == nil {
		t.Fatal("replaced directory")
	}
}

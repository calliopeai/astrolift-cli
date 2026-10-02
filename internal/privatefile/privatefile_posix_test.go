//go:build !windows

package privatefile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateFilePOSIXModeAndSymlinkRefusal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private")
	if err := Write(path, []byte("keep")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("POSIX private file is not mode 0600")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(path, 1024); err == nil {
		t.Fatal("accepted broad POSIX permissions")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(link, 1024); err == nil {
		t.Fatal("followed private file symlink")
	}
	if err := Write(link, []byte("replace")); err == nil {
		t.Fatal("replaced private file symlink")
	}
	data, err := ReadFile(path, 1024)
	if err != nil || string(data) != "keep" {
		t.Fatal("symlink refusal modified its referent")
	}
}

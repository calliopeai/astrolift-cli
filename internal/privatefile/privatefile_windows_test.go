//go:build windows

package privatefile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/sys/windows"
)

func changeDACL(t *testing.T, path, sddl string, protected bool) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if !protected {
		flags = windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, flags, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(sd)
}

func TestPrivateFileWindowsACLIsPrivateDespiteReportedMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private")
	if err := Write(path, []byte("test-private-content")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0666 {
		t.Fatalf("expected Go's writable Windows mode 0666, got %o", info.Mode().Perm())
	}
	f, err := Open(path, 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sd, err := securityInfo(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateACL(sd); err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil || acl.AceCount < 1 || acl.AceCount > 2 {
		t.Fatal("new file has unexpected ACL entries")
	}
}

func TestPrivateFileWindowsTamperedACLsRefuseBeforeContentRead(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	owner := "(A;;FA;;;" + user.String() + ")"
	for _, sample := range []struct {
		name, sddl string
		protected  bool
	}{
		{"broad", "D:P" + owner + "(A;;FR;;;WD)", true},
		{"null", "D:NO_ACCESS_CONTROL", true},
		{"empty", "D:P", true},
		{"unprotected", "D:" + owner, false},
		{"inherited-entry", "D:P(A;ID;FA;;;" + user.String() + ")", true},
		{"unsupported-deny-entry", "D:P(D;;FW;;;WD)" + owner, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private")
			if err := Write(path, []byte("must-not-read")); err != nil {
				t.Fatal(err)
			}
			changeDACL(t, path, sample.sddl, sample.protected)
			if data, err := ReadFile(path, 1024); err == nil || len(data) != 0 {
				t.Fatal("read content protected by an invalid or permissive ACL")
			}
			// Restore owner access for ordinary test-directory cleanup.
			changeDACL(t, path, "D:P"+owner+"(A;;FA;;;SY)", true)
		})
	}
}

func TestPrivateFileWindowsForeignOwnerDescriptorIsRefused(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	foreign := "S-1-5-19"
	if user.String() == foreign {
		foreign = "S-1-5-20"
	}
	sd, err := windows.SecurityDescriptorFromString("O:" + foreign + "D:P(A;;FA;;;" + user.String() + ")")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateACL(sd); err == nil {
		t.Fatal("accepted another account as private file owner")
	}
}

func TestPrivateFileWindowsActualSymlinkRefusesReadAndReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private")
	if err := Write(path, []byte("original")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	// Native CI must provide symlink privileges; do not silently skip this gate.
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadFile(link, 1024); err == nil || len(data) != 0 {
		t.Fatal("followed a Windows reparse target")
	}
	if err := Write(link, []byte("replacement")); err == nil {
		t.Fatal("replaced a Windows reparse target")
	}
	actual, err := ReadFile(path, 1024)
	if err != nil || string(actual) != "original" {
		t.Fatal("reparse refusal modified its referent")
	}
}

//go:build windows

package config

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/auth"
	"golang.org/x/sys/windows"
)

func TestCredentialsWindowsExplicitLoginReplacesOwnedLegacyBroadACL(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := SaveCredentials("local", &auth.Credentials{AccessToken: "old-test-token"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(Dir(), "credentials", "local.yaml")
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(sd)
	if _, err := LoadCredentials("local"); err == nil {
		t.Fatal("silently read a legacy broad Windows ACL")
	}
	if err := SaveCredentials("local", &auth.Credentials{AccessToken: "new-test-token"}); err != nil {
		t.Fatal(err)
	}
	actual, err := LoadCredentials("local")
	if err != nil || actual.AccessToken != "new-test-token" {
		t.Fatalf("explicit login did not install a protected replacement: %v", err)
	}
}

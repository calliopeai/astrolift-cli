package config

import (
	"path/filepath"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/auth"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
)

func TestCredentialsPrivateRoundTripAndRefresh(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, token := range []string{"initial-test-token", "refreshed-test-token"} {
		original := &auth.Credentials{AccessToken: token, RefreshToken: "test-refresh"}
		if err := SaveCredentials("local", original); err != nil {
			t.Fatal(err)
		}
		actual, err := LoadCredentials("local")
		if err != nil || actual.AccessToken != token || actual.RefreshToken != original.RefreshToken {
			t.Fatalf("saved credentials cannot reload: %v", err)
		}
		path := filepath.Join(Dir(), "credentials", "local.yaml")
		if f, err := privatefile.Open(path, 1<<20); err != nil {
			t.Fatalf("credentials lost their private boundary: %v", err)
		} else {
			_ = f.Close()
		}
	}
}

package cmd

import (
	"context"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/viper"
)

func TestLoadActiveClientHonorsExplicitTokenWithoutStoredCredentials(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &config.Config{
		CurrentServer: "test",
		Servers: map[string]config.ServerEntry{
			"test": {APIURL: "https://astrolift.example.test"},
		},
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	viper.Set("token", "explicit-token")
	t.Cleanup(func() { viper.Set("token", "") })

	client, _, _, err := loadActiveClient(context.Background(), false)
	if err != nil {
		t.Fatalf("loadActiveClient: %v", err)
	}
	if client.Token() != "explicit-token" {
		t.Fatalf("explicit token was ignored: %q", client.Token())
	}
}

func TestLoadActiveClientSupportsEphemeralAPIURLAndToken(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	viper.Set("api_url", "https://ephemeral.astrolift.example.test")
	viper.Set("token", "ephemeral-token")
	t.Cleanup(func() {
		viper.Set("api_url", "")
		viper.Set("token", "")
	})

	client, _, entry, err := loadActiveClient(context.Background(), false)
	if err != nil {
		t.Fatalf("loadActiveClient: %v", err)
	}
	if client.BaseURL() != "https://ephemeral.astrolift.example.test" || client.Token() != "ephemeral-token" {
		t.Fatalf("ephemeral overrides ignored: url=%q token=%q", client.BaseURL(), client.Token())
	}
	if entry == nil || entry.DisplayName != "ephemeral" {
		t.Fatalf("ephemeral server metadata missing: %#v", entry)
	}
}

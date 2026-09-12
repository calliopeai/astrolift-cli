package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/auth"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func serverSelectionFixture(t *testing.T, selectedURL string) *config.Config {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("ASTROLIFT_DEPLOY_TOKEN", "")
	for _, key := range []string{"server", "api_url", "token"} {
		viper.Set(key, "")
		t.Cleanup(func() { viper.Set(key, "") })
	}
	cfg := &config.Config{CurrentServer: "default", DefaultOrg: "org", Servers: map[string]config.ServerEntry{
		"default": {APIURL: "http://default.invalid"}, "selected": {APIURL: selectedURL},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"default", "selected"} {
		if err := config.SaveCredentials(slug, &auth.Credentials{AccessToken: slug + "-token", RefreshToken: slug + "-refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	viper.Set("server", "selected")
	return cfg
}

func TestSelectedServerPairsEndpointAndCredentialsWithoutChangingDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer selected-token" {
			t.Error("request used another server's credentials")
		}
		_, _ = fmt.Fprint(w, `{"data":{"value":"selected"}}`)
	}))
	defer server.Close()
	serverSelectionFixture(t, server.URL)
	client, cfg, _, err := loadActiveClient(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ Value string }
	if err := client.GraphQL(context.Background(), "query { value }", nil, &result); err != nil || result.Value != "selected" {
		t.Fatalf("selected endpoint was not used: %v, %+v", err, result)
	}
	// A command such as org use can save the returned config without changing
	// the server selected by other clients.
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	saved, err := config.Load()
	if err != nil || saved.CurrentServer != "default" {
		t.Fatalf("saved selection changed: %v, %+v", err, saved)
	}
	url, err := onboardAPIURL()
	if err != nil || url != server.URL || describeAuthState().Server != "selected" {
		t.Fatalf("onboarding used another server: %v, %s", err, url)
	}
	var out bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&out)
	if err := authStatusCmd.RunE(command, nil); err != nil || !strings.Contains(out.String(), "Server:     selected") {
		t.Fatalf("auth status used another server: %v, %s", err, out.String())
	}
}

func TestSelectedServerRefreshWritesOnlyItsOwnCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["refresh_token"] != "selected-refresh" {
			t.Error("wrong refresh credential")
		}
		_ = json.NewEncoder(w).Encode(auth.Credentials{AccessToken: "fresh-selected", RefreshToken: "fresh-refresh", ExpiresAt: time.Now().Add(time.Hour)})
	}))
	defer server.Close()
	serverSelectionFixture(t, server.URL)
	if err := config.SaveCredentials("selected", &auth.Credentials{RefreshToken: "selected-refresh", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	client, _, _, err := loadActiveClient(context.Background(), false)
	if err != nil || client.Token() != "fresh-selected" {
		t.Fatalf("selected token was not refreshed: %v", err)
	}
	for slug, expected := range map[string]string{"selected": "fresh-selected", "default": "default-token"} {
		creds, err := config.LoadCredentials(slug)
		if err != nil || creds.AccessToken != expected {
			t.Fatalf("wrong credentials stored for %s: %v", slug, err)
		}
	}
}

func TestSelectedServerRejectsUnknownAndConflictingTargets(t *testing.T) {
	cfg := serverSelectionFixture(t, "https://selected.invalid")
	for _, test := range []struct{ server, url string }{
		{"missing", ""}, {"selected", "https://another.invalid"},
	} {
		viper.Set("server", test.server)
		viper.Set("api_url", test.url)
		if _, _, _, err := loadActiveClient(context.Background(), false); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	viper.Set("server", "selected")
	viper.Set("api_url", "https://selected.invalid/")
	if _, _, err := selectedServer(cfg, []string{"default"}); err == nil {
		t.Fatal("conflicting positional server accepted")
	}
	if _, _, err := selectedServer(cfg, nil); err != nil {
		t.Fatal(err)
	}
}

func TestBoxCommandsUseSelectedServerAndOrganization(t *testing.T) {
	for _, operation := range []string{"attach", "rm"} {
		t.Run(operation, func(t *testing.T) {
			boxRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer selected-token" {
					t.Error("box request used another server's credentials")
				}
				var body struct{ Query string }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if strings.Contains(body.Query, "astroliftOrganizations") {
					_, _ = fmt.Fprint(w, `{"data":{"astroliftOrganizations":[{"id":"default-org","slug":"org"},{"id":"selected-org","slug":"selected"}]}}`)
					return
				}
				boxRequests++
				if r.Header.Get("X-Astrolift-Organization") != "selected-org" {
					t.Error("box request used another organization")
				}
				if operation == "rm" {
					_, _ = fmt.Fprint(w, `{"data":{"destroyAgentBox":{"ok":true}}}`)
				} else {
					// Stop before dialing a terminal; the lookup itself must be scoped.
					_, _ = fmt.Fprint(w, `{"data":{"agentBox":null}}`)
				}
			}))
			defer server.Close()
			serverSelectionFixture(t, server.URL)
			command := testCmd()
			command.SetContext(context.Background())
			command.SetOut(&bytes.Buffer{})
			if err := command.Flags().Set("org", "selected"); err != nil {
				t.Fatal(err)
			}
			previous := boxRmYes
			boxRmYes = true
			t.Cleanup(func() { boxRmYes = previous })
			var err error
			if operation == "rm" {
				err = boxRmCmd.RunE(command, []string{"same-slug"})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				err = boxAttachCmd.RunE(command, []string{"same-slug"})
				if err == nil || !strings.Contains(err.Error(), "not found") {
					t.Fatalf("unexpected attach result: %v", err)
				}
			}
			if boxRequests != 1 {
				t.Fatalf("expected one scoped box request, got %d", boxRequests)
			}
		})
	}
}

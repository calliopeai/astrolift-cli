package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestIdentityProviderEditUsesReviewedVersionAndExplicitFields(t *testing.T) {
	var seen []gqlRequest
	projectCommandServer(t, "updateIdentityProvider", map[string]any{
		"ok":   true,
		"data": map[string]any{"id": webProjectGUID, "name": "Workforce", "version": 8},
	}, &seen)
	cfg := filepath.Join(t.TempDir(), "provider.json")
	if err := os.WriteFile(cfg, []byte(`{"region":"us-east-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newIdentityProvidersCommand()
	cmd.PersistentFlags().Bool("json", false, "")
	out := new(bytes.Buffer)
	cmd.SetOut(out)
	cmd.SetArgs([]string{"edit", webProjectGUID, "--if-version", "7", "--name", "Workforce", "--config-file", cfg, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	req, found := sentMutation(seen, "updateIdentityProvider")
	if !found {
		t.Fatal("no update mutation was sent")
	}
	want := map[string]any{
		"id": webProjectGUID, "ifMatchVersion": float64(7), "displayName": "Workforce",
		"config": map[string]any{"region": "us-east-1"},
	}
	if got := req.Variables["input"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("update input = %v, want %v", got, want)
	}
	var provider identityProviderRow
	if err := json.Unmarshal(out.Bytes(), &provider); err != nil || provider.Version != 8 {
		t.Fatalf("update output = %q, err = %v", out, err)
	}
}

func TestIdentityProviderEditReturnsVersionConflict(t *testing.T) {
	var seen []gqlRequest
	projectCommandServer(t, "updateIdentityProvider", refusedResult("STALE_VERSION", "", "Provider changed"), &seen)
	cmd := newIdentityProvidersCommand()
	cmd.SetArgs([]string{"edit", webProjectGUID, "--if-version", "7", "--name", "Workforce"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "Provider changed") {
		t.Fatalf("expected server version conflict, got %v", err)
	}
}

func TestIdentityProviderCommandsRefuseIncompleteInputBeforeNetwork(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "provider.json")
	if err := os.WriteFile(cfg, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"edit requires version", []string{"edit", webProjectGUID, "--name", "Workforce"}, "if-version"},
		{"activate requires confirmation", []string{"activate", webProjectGUID}, "--yes"},
		{"delete requires confirmation", []string{"delete", webProjectGUID}, "--yes"},
		{"config must be object", []string{"create", "oidc", "--config-file", cfg}, "JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newIdentityProvidersCommand()
			cmd.SetArgs(tc.args)
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestIdentityProviderMFAEditPreservesExistingConfiguration(t *testing.T) {
	var seen []gqlRequest
	server := gqlServerFunc(t, func(req gqlRequest) map[string]any {
		seen = append(seen, req)
		switch {
		case strings.Contains(req.Query, "astroliftOrganizations"):
			return map[string]any{"astroliftOrganizations": []map[string]any{
				{"id": projectTestOrgGUID, "slug": "acme", "name": "Acme"},
			}}
		case strings.Contains(req.Query, "astroliftIdentityProviders"):
			return map[string]any{"astroliftIdentityProviders": []map[string]any{
				{"id": webProjectGUID, "config": map[string]any{"region": "us-east-1"}},
			}}
		default:
			return map[string]any{"updateIdentityProvider": map[string]any{
				"ok": true, "data": map[string]any{"id": webProjectGUID},
			}}
		}
	})
	defer server.Close()
	withConfigHome(t, server.URL)
	viper.Set("token", "cli-credential")
	t.Cleanup(func() { viper.Set("token", "") })

	cmd := newIdentityProvidersCommand()
	cmd.SetArgs([]string{"edit", webProjectGUID, "--if-version", "7", "--require-mfa"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	req, found := sentMutation(seen, "updateIdentityProvider")
	if !found {
		t.Fatal("no MFA policy update was sent")
	}
	input := req.Variables["input"].(map[string]any)
	want := map[string]any{"require_mfa": true, "region": "us-east-1"}
	if got := input["config"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("config = %v, want %v", got, want)
	}
}

func TestIdentityProviderCreateUsesTheApprovedConnection(t *testing.T) {
	var mutation gqlRequest
	const url = "https://login.example.com/.well-known/openid-configuration"
	server := gqlServerFunc(t, func(req gqlRequest) map[string]any {
		switch {
		case strings.Contains(req.Query, "astroliftOrganizations"):
			return map[string]any{"astroliftOrganizations": []map[string]any{
				{"id": projectTestOrgGUID, "slug": "acme", "name": "Acme"},
			}}
		case strings.Contains(req.Query, "astroliftBrowserSsoConnections"):
			return map[string]any{"astroliftBrowserSsoConnections": []map[string]any{
				{"reference": "workforce", "discoveryUrl": url, "clientId": "console", "protocol": "oidc"},
				{"reference": "team-github", "discoveryUrl": "", "clientId": "github-console", "protocol": "github"},
				{"reference": "workforce-saml", "metadataUrl": "https://idp.example.com/metadata", "protocol": "saml"},
			}}
		default:
			mutation = req
			return map[string]any{"createIdentityProvider": map[string]any{
				"ok": true, "data": map[string]any{"id": webProjectGUID},
			}}
		}
	})
	defer server.Close()
	withConfigHome(t, server.URL)
	viper.Set("token", "cli-credential")
	t.Cleanup(func() { viper.Set("token", "") })

	cmd := newIdentityProvidersCommand()
	cmd.SetArgs([]string{"create", "oidc", "--connection", "workforce"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"kind": "oidc", "clientId": "console", "oidcDiscoveryUrl": url, "clientSecretRef": "workforce",
	}
	if got := mutation.Variables["input"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("create input = %v, want %v", got, want)
	}

	cmd = newIdentityProvidersCommand()
	cmd.SetArgs([]string{"create", "github", "--connection", "team-github"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want = map[string]any{
		"kind": "github", "clientId": "github-console", "oidcDiscoveryUrl": "", "metadataUrl": "", "clientSecretRef": "team-github",
	}
	if got := mutation.Variables["input"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("create input = %v, want %v", got, want)
	}

	cmd = newIdentityProvidersCommand()
	cmd.SetArgs([]string{"create", "saml", "--connection", "workforce-saml"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want = map[string]any{
		"kind": "saml", "clientId": "", "oidcDiscoveryUrl": "", "metadataUrl": "https://idp.example.com/metadata", "clientSecretRef": "workforce-saml",
	}
	if got := mutation.Variables["input"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("create input = %v, want %v", got, want)
	}
}

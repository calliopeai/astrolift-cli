package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestSSOApprovalCreatesAndRotatesWithoutPrintingTheCredential(t *testing.T) {
	var saved []map[string]any
	row := map[string]any{
		"reference": "workforce", "organizationSlug": "acme", "protocol": "oidc",
		"config":  map[string]any{"discovery_url": "https://id.example.test/oidc", "client_id": "console"},
		"version": 1, "installationWide": true,
	}
	server := gqlServerFunc(t, func(req gqlRequest) map[string]any {
		if strings.Contains(req.Query, "astroliftBrowserSsoApprovals") {
			return map[string]any{"astroliftBrowserSsoApprovals": []map[string]any{row}}
		}
		input := req.Variables["input"].(map[string]any)
		saved = append(saved, input)
		return map[string]any{"saveBrowserSsoApproval": map[string]any{"ok": true, "data": row}}
	})
	defer server.Close()
	withConfigHome(t, server.URL)
	viper.Set("token", "operator-credential")
	t.Cleanup(func() { viper.Set("token", "") })
	config := filepath.Join(t.TempDir(), "public.json")
	if err := os.WriteFile(config, []byte(`{"discovery_url":"https://id.example.test/oidc","client_id":"console"}`), 0600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	run := func(credential string, args ...string) {
		t.Helper()
		cmd := newIdentityProvidersCommand()
		cmd.SetIn(strings.NewReader(credential + "\n"))
		cmd.SetOut(&output)
		cmd.SetArgs(append([]string{"connections", "approve", "workforce", "--credential-file", "-"}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	run("first-private-secret", "--organization", "acme", "--protocol", "oidc", "--config-file", config, "--installation-wide")
	run("second-private-secret", "--if-version", "1")
	if len(saved) != 2 || saved[0]["credential"] != "first-private-secret" ||
		saved[1]["credential"] != "second-private-secret" || saved[1]["version"] != float64(1) ||
		saved[1]["installationWide"] != true {
		t.Fatal("the approval requests did not carry the entered credentials, reviewed version and scope")
	}
	if strings.Contains(output.String(), "private-secret") {
		t.Fatal("the command printed a credential")
	}
}

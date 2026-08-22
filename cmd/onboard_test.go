package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// The command's whole job is assembling files correctly, so these assert on
// what lands on disk and what the report says about it -- not on log lines.

func TestMCPDocumentPointsAtTheGatewayRoute(t *testing.T) {
	body, err := mcpDocument("https://astrolift.example.com")
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}

	entry, ok := doc.MCPServers["astrolift"]
	if !ok {
		t.Fatalf("no astrolift server entry: %s", body)
	}
	if want := "https://astrolift.example.com/api/mcp/v1/"; entry.URL != want {
		t.Errorf("url = %q, want %q", entry.URL, want)
	}
	if entry.Type != "http" {
		t.Errorf("type = %q, want http", entry.Type)
	}
	if got := entry.Headers["Authorization"]; got != "Bearer ${ASTROLIFT_TOKEN}" {
		t.Errorf("Authorization = %q, want the unexpanded env reference", got)
	}
}

func TestMCPDocumentNeverWritesARealToken(t *testing.T) {
	// A resolved bearer in a file that lands in a repo is how tokens leak.
	t.Setenv("ASTROLIFT_TOKEN", "alft_at_supersecretvalue")
	viper.Reset()
	viper.SetEnvPrefix("ASTROLIFT")
	viper.AutomaticEnv()
	t.Cleanup(viper.Reset)

	body, err := mcpDocument("https://astrolift.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "supersecretvalue") {
		t.Fatalf("the token was baked into the config: %s", body)
	}
}

func TestEndpointDoesNotDoubleTheSlash(t *testing.T) {
	if got := mcpEndpoint("https://x.example.com/"); got != "https://x.example.com/api/mcp/v1/" {
		t.Errorf("got %q", got)
	}
}

func TestWriteMCPConfigTargets(t *testing.T) {
	cases := []struct {
		target string
		want   []string
	}{
		{"claude", []string{".mcp.json"}},
		{"codex", []string{filepath.Join(".codex", "mcp.json")}},
		{"both", []string{".mcp.json", filepath.Join(".codex", "mcp.json")}},
	}
	for _, c := range cases {
		t.Run(c.target, func(t *testing.T) {
			dir := t.TempDir()
			if _, err := writeMCPConfig(dir, c.target, "https://x.example.com", false, false); err != nil {
				t.Fatal(err)
			}
			for _, rel := range c.want {
				if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
					t.Errorf("expected %s: %v", rel, err)
				}
			}
		})
	}
}

func TestDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	written, err := writeMCPConfig(dir, "both", "https://x.example.com", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 {
		t.Fatalf("expected 2 planned writes, got %d", len(written))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("dry run touched the filesystem: %v", entries)
	}
}

func TestAnExistingFileIsNotClobbered(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(target, []byte(`{"mine":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := writeMCPConfig(dir, "claude", "https://x.example.com", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || !written[0].Skipped {
		t.Fatalf("expected a skip, got %+v", written)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"mine":true}` {
		t.Fatalf("the operator's file was overwritten: %s", body)
	}
}

func TestForceReplacesIt(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(target, []byte(`{"mine":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeMCPConfig(dir, "claude", "https://x.example.com", false, true); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"mine"`) {
		t.Fatal("--force did not replace the file")
	}
}

func TestInstallSkillsLandsTheCatalogue(t *testing.T) {
	dir := t.TempDir()
	written, err := installSkills(dir, "claude", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) == 0 {
		t.Fatal("no skill files written")
	}
	// The astro-cli skill is the one an onboarding agent needs first, so
	// pin it by name rather than trusting a count.
	target := filepath.Join(dir, ".claude", "skills", "astro-cli", "SKILL.md")
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected the astro-cli skill installed: %v", err)
	}
}

func TestInstallAgentDocsIncludesTheAuthTopicAndAnIndex(t *testing.T) {
	dir := t.TempDir()
	if _, err := installAgentDocs(dir, false, false); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, ".astrolift", "docs")

	// `start` is the topic that says how to log in. Its absence from the
	// embedded set is the gap this whole change exists to close, so assert
	// it by name.
	if _, err := os.Stat(filepath.Join(base, "start.md")); err != nil {
		t.Errorf("expected the getting-started topic: %v", err)
	}

	index, err := os.ReadFile(filepath.Join(base, "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"start.md", "mcp.md", "manifest.md"} {
		if !strings.Contains(string(index), want) {
			t.Errorf("index does not mention %s", want)
		}
	}
}

func TestResolveComponents(t *testing.T) {
	all, err := resolveOnboardComponents(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("default should be everything, got %v", all)
	}

	// Order follows the canonical list, not the typing order, so reports diff.
	got, err := resolveOnboardComponents([]string{"docs,mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "mcp" || got[1] != "docs" {
		t.Fatalf("got %v, want [mcp docs]", got)
	}

	if _, err := resolveOnboardComponents([]string{"nonsense"}); err == nil {
		t.Fatal("an unknown component should be refused, not ignored")
	}
}

func TestResolveTarget(t *testing.T) {
	if got, err := resolveOnboardTarget(""); err != nil || got != "both" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := resolveOnboardTarget("emacs"); err == nil {
		t.Fatal("an unknown target should be refused")
	}
}

func TestAuthStateReportsTheHeadlessToken(t *testing.T) {
	t.Setenv("ASTROLIFT_TOKEN", "alft_at_abc")
	viper.Reset()
	viper.SetEnvPrefix("ASTROLIFT")
	viper.AutomaticEnv()
	t.Cleanup(viper.Reset)

	state := describeAuthState()
	if !state.Authenticated {
		t.Fatalf("ASTROLIFT_TOKEN should read as authenticated: %+v", state)
	}
}

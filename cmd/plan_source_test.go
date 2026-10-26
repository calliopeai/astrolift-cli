package cmd

import (
	"context"
	"fmt"
	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func planFixture(t *testing.T) (*cobra.Command, *api.Client, string) {
	t.Helper()
	resetDispatchFlags()
	t.Cleanup(resetDispatchFlags)
	dir := t.TempDir()
	files := map[string]string{
		"specs/manifest.json": `{"items":[{"id":"build/parser","title":"Parser","type":"story","status":"planned","estimate":"S","path":"specs/parser.md","labels":["repo.engine","phase.build"]}]}`,
		"specs/parser.md":     "---\ntitle: Parser\n---\n## Description\nBuild the parser.\n## Acceptance\n- [ ] `go test ./parser` passes\n## References\n[Design](knowledge/design.md)\n",
		"knowledge/design.md": "Parser design",
		"app/brain.json":      `{"nodes":[{"id":"spec:build/parser","kind":"spec-node","title":"Parser"},{"id":"decision:syntax","kind":"decision"}],"edges":[{"source":"spec:build/parser","target":"decision:syntax","rel":"references"}]}`,
		"brain-schema.json":   `{"contextRecipes":{"$comment":"Only named recipes are executable configuration","spec-node":{"hops":[{"rel":"references","direction":"out","as":"decisions"}]}}}`,
	}
	for file, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(file))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("remote", "add", "origin", "https://github.com/example/brain.git")
	git("add", ".")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	revision := git("rev-parse", "HEAD")
	srv := gqlServer(t, map[string]interface{}{"astroliftBrain": map[string]interface{}{"slug": "test-brain"}, "astroliftApp": map[string]interface{}{"sourceRepo": "example/brain", "sourceUrl": ""}}, nil)
	t.Cleanup(srv.Close)
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	sourceFlags(cmd)
	_ = cmd.Flags().Set("source", dir)
	cmd.Flags().String("spec", "build/parser", "")
	cmd.Flags().String("branch", "", "")
	cmd.Flags().Int("attempt", 1, "")
	cmd.Flags().Int("issue", 17, "")
	cmd.Flags().Bool("dry-run", true, "")
	agentDispatchBrains = []string{"test-brain"}
	return cmd, api.NewClient(srv.URL, "test-token", false), revision
}

func TestPlanSourceRefusesForeignRepositoryAndUnsafePaths(t *testing.T) {
	cmd, client, _ := planFixture(t)
	dir := stringFlag(cmd, "source")
	if err := exec.Command("git", "-C", dir, "remote", "set-url", "origin", "https://github.com/other/brain.git").Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := openPlanSource(cmd, context.Background(), client, "test-brain"); err == nil {
		t.Fatal("foreign source accepted")
	}
	for _, file := range []string{"../secret", "/etc/passwd", "a/../secret", "a\\b", "x\n"} {
		if safePlanPath(file) == nil {
			t.Fatalf("accepted %q", file)
		}
	}
}

func TestRemotePlanReadsOnlyPinnedAuthorizedSource(t *testing.T) {
	const revision = "0123456789012345678901234567890123456789"
	for _, wrong := range []bool{false, true} {
		t.Run(fmt.Sprint(wrong), func(t *testing.T) {
			calls := 0
			server := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
				if strings.Contains(req.Query, "PlanSource") {
					return map[string]interface{}{"astroliftBrain": map[string]interface{}{"slug": "brain"}, "astroliftApp": map[string]interface{}{"sourceRepo": "example/brain"}}
				}
				calls++
				if req.Variables["connection"] != "connection-guid" || req.Variables["ref"] != revision || req.Variables["repo"] != "example/brain" {
					t.Errorf("wrong identity: %v", req.Variables)
				}
				ref := revision
				if wrong {
					ref = "different"
				}
				return map[string]interface{}{"astroliftSourceFile": map[string]interface{}{"repoFullName": "example/brain", "path": "specs/manifest.json", "ref": ref, "content": `{"items":[]}`, "errorCode": nil}}
			})
			defer server.Close()
			cmd := &cobra.Command{}
			sourceFlags(cmd)
			_ = cmd.Flags().Set("connection", "connection-guid")
			_ = cmd.Flags().Set("revision", revision)
			source, err := openPlanSource(cmd, context.Background(), api.NewClient(server.URL, "token", false), "brain")
			if err != nil {
				t.Fatal(err)
			}
			_, err = readPlan(source)
			if wrong && err == nil {
				t.Fatal("accepted a replaced revision")
			}
			if !wrong && err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatalf("reads=%d", calls)
			}
		})
	}
}

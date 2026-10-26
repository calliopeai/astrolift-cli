package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecDispatchCompilesCommittedEnvelopeWithoutMutation(t *testing.T) {
	cmd, client, revision := planFixture(t)
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runAgentDispatch(cmd, context.Background(), client, "builder"); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	brain := envelope["brain"].(map[string]interface{})
	if brain["commit"] != revision || brain["mcp"] != "test-brain" {
		t.Fatalf("brain=%v", brain)
	}
	repo := envelope["repo"].(map[string]interface{})
	if repo["name"] != "example/engine" || repo["issue"] != float64(17) {
		t.Fatalf("repo=%v", repo)
	}
	pack := envelope["task_pack"].(map[string]interface{})["inline"].(map[string]interface{})
	if len(pack["decisions"].([]interface{})) != 1 {
		t.Fatalf("missing context recipe: %v", pack)
	}
	if !strings.Contains(out.String(), "go test ./parser") || !strings.Contains(out.String(), "knowledge/design.md") {
		t.Fatal(out.String())
	}
}

func TestSpecUsesPinnedGitObjectsNotUncommittedInstructions(t *testing.T) {
	cmd, client, _ := planFixture(t)
	dir := stringFlag(cmd, "source")
	if err := os.WriteFile(filepath.Join(dir, "specs/parser.md"), []byte("uncommitted replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := specEnvelope(cmd, context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result["story"].(map[string]interface{})["body"].(string), "uncommitted") {
		t.Fatal("read uncommitted source")
	}
}

func TestSpecRefusesAmbiguousBrainAndMixedPayload(t *testing.T) {
	cmd, client, _ := planFixture(t)
	agentDispatchBrains = append(agentDispatchBrains, "other")
	if _, err := specEnvelope(cmd, context.Background(), client); err == nil {
		t.Fatal("ambiguous brain accepted")
	}
	agentDispatchBrains = []string{"test-brain"}
	agentDispatchInput = "{}"
	if _, err := specEnvelope(cmd, context.Background(), client); err == nil {
		t.Fatal("mixed payload accepted")
	}
}

func TestSpecDispatchRejectsMissingTestsAndMissingContext(t *testing.T) {
	for _, missing := range []string{"test", "context"} {
		t.Run(missing, func(t *testing.T) {
			cmd, client, _ := planFixture(t)
			dir := stringFlag(cmd, "source")
			if missing == "test" {
				if err := os.WriteFile(filepath.Join(dir, "specs/parser.md"), []byte("## Description\nDo work\n## Acceptance\n- [ ] Looks good\n"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(dir, "app/brain.json"), []byte(`{"nodes":[],"edges":[]}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{{"add", "."}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.test", "-c", "commit.gpgsign=false", "commit", "-qm", "invalid source"}} {
				if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
					t.Fatalf("%s %v", out, err)
				}
			}
			if _, err := specEnvelope(cmd, context.Background(), client); err == nil {
				t.Fatalf("accepted missing %s", missing)
			}
		})
	}
}

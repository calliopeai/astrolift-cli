package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

const applyFixture = `
[[env_spec]]
slug = "claude-dev"
agent_type = "claude"
runtime = "claude-code"
run_as_non_root = true
env = { LOG_LEVEL = "info" }
secrets = { GITHUB_TOKEN = "agents/claude-dev/github-token", A_KEY = "agents/claude-dev/a" }

[[env_spec]]
slug = "codex-dev"
image_tag = "ecr/agent:2"
`

func writeApplyFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agents.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadApplyFileTracksDeclaredKeysPerTable(t *testing.T) {
	specs, defined, err := loadApplyFile(writeApplyFile(t, applyFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs", len(specs))
	}
	for _, key := range []string{"agent_type", "runtime", "run_as_non_root", "env", "secrets"} {
		if !defined[0][key] {
			t.Errorf("claude-dev: %s should be declared", key)
		}
	}
	if defined[0]["image_tag"] || defined[0]["allow_install"] {
		t.Errorf("claude-dev: undeclared keys marked declared: %v", defined[0])
	}
	if !defined[1]["image_tag"] || defined[1]["runtime"] || defined[1]["run_as_non_root"] {
		t.Errorf("codex-dev: wrong declared keys: %v", defined[1])
	}
}

func TestLoadApplyFileRefusesUnknownKeysAndDuplicates(t *testing.T) {
	if _, _, err := loadApplyFile(writeApplyFile(t, "[[env_spec]]\nslug = \"a\"\nnon_root = true\n")); err == nil ||
		!strings.Contains(err.Error(), "unknown keys") {
		t.Errorf("unknown key: got %v", err)
	}
	if _, _, err := loadApplyFile(writeApplyFile(t, "[[env_spec]]\nslug = \"a\"\n[[env_spec]]\nslug = \"a\"\n")); err == nil ||
		!strings.Contains(err.Error(), "declared twice") {
		t.Errorf("duplicate: got %v", err)
	}
}

func TestPlanSpecOnlyTouchesDeclaredKeysThatDiffer(t *testing.T) {
	specs, defined, _ := loadApplyFile(writeApplyFile(t, applyFixture))
	current := currentSpec{
		"agentType":    "claude",
		"runtime":      "claude-code",
		"runAsNonRoot": false,
		"imageTag":     "keep-me",
		"envVars":      map[string]interface{}{"LOG_LEVEL": "info"},
		// Server order differs from file order; must not read as a change.
		"secretRefs": []interface{}{
			map[string]interface{}{"env_var": "GITHUB_TOKEN", "uri": "agents/claude-dev/github-token"},
			map[string]interface{}{"env_var": "A_KEY", "uri": "agents/claude-dev/a"},
		},
	}
	plan := planSpec(specs[0], defined[0], current)

	if plan.Create || len(plan.Changes) != 1 || plan.Changes[0].Field != "run_as_non_root" {
		t.Fatalf("want one run_as_non_root change, got %+v", plan)
	}
	if len(plan.Input) != 1 || plan.Input["runAsNonRoot"] != true {
		t.Errorf("update input should carry only the change: %v", plan.Input)
	}
}

func TestPlanSpecIsANoopWhenEverythingMatches(t *testing.T) {
	specs, defined, _ := loadApplyFile(writeApplyFile(t, applyFixture))
	plan := planSpec(specs[1], defined[1], currentSpec{"imageTag": "ecr/agent:2"})
	if !plan.noop() {
		t.Errorf("expected no-op, got %+v", plan)
	}
}

func TestRunApplyCreatesUpdatesAndSkips(t *testing.T) {
	path := writeApplyFile(t, applyFixture+"\n[[env_spec]]\nslug = \"same\"\nruntime = \"aider\"\n")
	var mutations []gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		switch {
		case strings.Contains(req.Query, "agentEnvironmentSpec(slug"):
			switch req.Variables["slug"] {
			case "claude-dev":
				return map[string]interface{}{"agentEnvironmentSpec": nil}
			case "codex-dev":
				return map[string]interface{}{"agentEnvironmentSpec": map[string]interface{}{"imageTag": "ecr/agent:1"}}
			default:
				return map[string]interface{}{"agentEnvironmentSpec": map[string]interface{}{"runtime": "aider"}}
			}
		case strings.Contains(req.Query, "createAgentEnvironmentSpec"):
			mutations = append(mutations, req)
			return map[string]interface{}{"createAgentEnvironmentSpec": map[string]interface{}{"ok": true, "errors": []interface{}{}}}
		case strings.Contains(req.Query, "updateAgentEnvironmentSpec"):
			mutations = append(mutations, req)
			return map[string]interface{}{"updateAgentEnvironmentSpec": map[string]interface{}{"ok": true, "errors": []interface{}{}}}
		}
		t.Errorf("unexpected request: %s", req.Query)
		return nil
	})
	defer srv.Close()

	cmd, out := agentTestCmd()
	client := api.NewClient(srv.URL, "tok", false)
	if err := runApply(cmd, client, func() (string, error) { return "org-1", nil }, path, false); err != nil {
		t.Fatal(err)
	}

	if len(mutations) != 2 {
		t.Fatalf("want create + update, got %d mutations", len(mutations))
	}
	create := mutations[0].Variables["input"].(map[string]interface{})
	if create["slug"] != "claude-dev" || create["runAsNonRoot"] != true || mutations[0].Variables["orgId"] != "org-1" {
		t.Errorf("create input wrong: %v", mutations[0].Variables)
	}
	if update := mutations[1].Variables["input"].(map[string]interface{}); len(update) != 1 || update["imageTag"] != "ecr/agent:2" {
		t.Errorf("update input wrong: %v", update)
	}
	for _, want := range []string{"create  env-spec claude-dev", "update  env-spec codex-dev", "image_tag: ecr/agent:1 -> ecr/agent:2", "unchanged env-spec same"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunApplyDryRunSendsNoMutations(t *testing.T) {
	path := writeApplyFile(t, applyFixture)
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "mutation") {
			t.Errorf("dry run sent a mutation: %s", req.Query)
		}
		if req.Variables["slug"] == "codex-dev" {
			return map[string]interface{}{"agentEnvironmentSpec": map[string]interface{}{"imageTag": "ecr/agent:1"}}
		}
		return map[string]interface{}{"agentEnvironmentSpec": nil}
	})
	defer srv.Close()

	cmd, out := agentTestCmd()
	err := runApply(cmd, api.NewClient(srv.URL, "tok", false), func() (string, error) { return "org-1", nil }, path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry run: nothing was changed") {
		t.Errorf("missing dry-run notice:\n%s", out.String())
	}
}

func TestRunApplyNeedsAgentTypeToCreate(t *testing.T) {
	path := writeApplyFile(t, "[[env_spec]]\nslug = \"new\"\nruntime = \"aider\"\n")
	srv := gqlServerFunc(t, func(gqlRequest) map[string]interface{} {
		return map[string]interface{}{"agentEnvironmentSpec": nil}
	})
	defer srv.Close()

	cmd, _ := agentTestCmd()
	err := runApply(cmd, api.NewClient(srv.URL, "tok", false), func() (string, error) { return "o", nil }, path, false)
	if err == nil || !strings.Contains(err.Error(), "needs agent_type") {
		t.Errorf("got %v", err)
	}
}

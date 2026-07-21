package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func TestAgentSecretSetSendsMutationAndConfirms(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"setAgentSecretValue": map[string]interface{}{
			"ok":     true,
			"errors": []interface{}{},
			"data":   map[string]interface{}{"envVar": "GITHUB_TOKEN", "uri": "sm:gh", "exists": true},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	if err := runAgentSecretSet(cmd, context.Background(), client, "claude-dev", "GITHUB_TOKEN", "ghp_secret"); err != nil {
		t.Fatalf("runAgentSecretSet: %v", err)
	}

	if !strings.Contains(captured.Query, "setAgentSecretValue(envSpecSlug: $slug, envVar: $envVar, value: $value)") {
		t.Errorf("mutation did not call setAgentSecretValue with expected args:\n%s", captured.Query)
	}
	if captured.Variables["slug"] != "claude-dev" || captured.Variables["envVar"] != "GITHUB_TOKEN" {
		t.Errorf("slug/envVar vars wrong: %v", captured.Variables)
	}
	if captured.Variables["value"] != "ghp_secret" {
		t.Errorf("value var = %v, want ghp_secret", captured.Variables["value"])
	}
	if !strings.Contains(out.String(), "Set GITHUB_TOKEN on env-spec claude-dev") {
		t.Errorf("missing confirmation line:\n%s", out.String())
	}
}

func TestAgentSecretSetSurfacesMutationError(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"setAgentSecretValue": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"message": "no secret ref bound to env var 'NOPE'"},
			},
			"data": nil,
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, _ := agentTestCmd()
	err := runAgentSecretSet(cmd, context.Background(), client, "claude-dev", "NOPE", "x")
	if err == nil || !strings.Contains(err.Error(), "no secret ref bound") {
		t.Fatalf("expected surfaced mutation error, got %v", err)
	}
}

func TestAgentSecretLsRendersTable(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"agentEnvironmentSpecSecretStatus": []map[string]interface{}{
			{"envVar": "TOKEN_A", "uri": "sm:a", "exists": true, "error": ""},
			{"envVar": "TOKEN_B", "uri": "sm:b", "exists": false, "error": ""},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	if err := runAgentSecretLs(cmd, context.Background(), client, "claude-dev"); err != nil {
		t.Fatalf("runAgentSecretLs: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ENV_VAR", "TOKEN_A", "sm:a", "set", "TOKEN_B", "missing"} {
		if !strings.Contains(got, want) {
			t.Errorf("ls output missing %q:\n%s", want, got)
		}
	}
}

func TestAgentSecretLsShowsErrorStatus(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"agentEnvironmentSpecSecretStatus": []map[string]interface{}{
			{"envVar": "TOKEN_X", "uri": "sm:x", "exists": false, "error": "store unreachable"},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	if err := runAgentSecretLs(cmd, context.Background(), client, "claude-dev"); err != nil {
		t.Fatalf("runAgentSecretLs: %v", err)
	}
	if !strings.Contains(out.String(), "error: store unreachable") {
		t.Errorf("expected error status rendered:\n%s", out.String())
	}
}

func TestAgentSecretLsJSON(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"agentEnvironmentSpecSecretStatus": []map[string]interface{}{
			{"envVar": "TOKEN_A", "uri": "sm:a", "exists": true, "error": ""},
		},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAgentSecretLs(cmd, context.Background(), client, "claude-dev"); err != nil {
		t.Fatalf("runAgentSecretLs --json: %v", err)
	}
	var rows []agentSecretStatus
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, out.String())
	}
	if len(rows) != 1 || rows[0].EnvVar != "TOKEN_A" || !rows[0].Exists {
		t.Errorf("decoded rows wrong: %+v", rows)
	}
}

func TestAgentSecretRmSendsMutation(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"deleteAgentSecretValue": map[string]interface{}{
			"ok":     true,
			"errors": []interface{}{},
			"data":   map[string]interface{}{"envVar": "GITHUB_TOKEN", "uri": "sm:gh", "exists": false},
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	if err := runAgentSecretRm(cmd, context.Background(), client, "claude-dev", "GITHUB_TOKEN"); err != nil {
		t.Fatalf("runAgentSecretRm: %v", err)
	}
	if captured.Variables["slug"] != "claude-dev" || captured.Variables["envVar"] != "GITHUB_TOKEN" {
		t.Errorf("rm vars wrong: %v", captured.Variables)
	}
	if !strings.Contains(out.String(), "Deleted GITHUB_TOKEN on env-spec claude-dev") {
		t.Errorf("missing confirmation:\n%s", out.String())
	}
}

func TestReadAgentSecretValueFromStdin(t *testing.T) {
	agentSecretValue = ""
	agentSecretStdin = true
	defer func() { agentSecretStdin = false }()

	cmd, _ := agentTestCmd()
	cmd.SetIn(strings.NewReader("ghp_from_stdin\n"))
	v, err := readAgentSecretValue(cmd)
	if err != nil {
		t.Fatalf("readAgentSecretValue: %v", err)
	}
	if v != "ghp_from_stdin" {
		t.Errorf("value = %q, want ghp_from_stdin (trailing newline trimmed)", v)
	}
}

func TestReadAgentSecretValueFromFlag(t *testing.T) {
	agentSecretValue = "ghp_flag"
	agentSecretStdin = false
	defer func() { agentSecretValue = "" }()

	cmd, _ := agentTestCmd()
	v, err := readAgentSecretValue(cmd)
	if err != nil {
		t.Fatalf("readAgentSecretValue: %v", err)
	}
	if v != "ghp_flag" {
		t.Errorf("value = %q, want ghp_flag", v)
	}
}

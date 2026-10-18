package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func resetZentinelleReads() {
	zentinelleReadAgent, zentinelleReadSince, zentinelleReadUntil = "", "", ""
	zentinelleTailFollow, zentinelleTailEvery = false, time.Millisecond
	zentinelleExportFmt, zentinelleExportOut = "ndjson", ""
	zentinelleCapabilityMemo = map[string]zentinelleState{}
	for _, c := range zentinelleReadCommands() {
		c.Hidden = true
	}
}

// zentinelleServer answers the org list and the capability itself, and hands
// every other request to respond. A nil capability answers as a server
// without the field.
func zentinelleServer(t *testing.T, capability interface{}, respond func(gqlRequest) map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		req.Organization = r.Header.Get("X-Astrolift-Organization")
		var data map[string]interface{}
		switch {
		case strings.Contains(req.Query, "astroliftOrganizations"):
			data = zentinelleOrg
		case strings.Contains(req.Query, "astroliftZentinelleCapability"):
			if req.Organization != "org-guid" {
				t.Errorf("capability asked without the working org: %q", req.Organization)
			}
			if capability == nil {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"errors": []map[string]string{
					{"message": "Cannot query field 'astroliftZentinelleCapability' on type 'Query'."},
				}})
				return
			}
			data = map[string]interface{}{"astroliftZentinelleCapability": map[string]interface{}{"connected": capability}}
		default:
			if req.Organization != "org-guid" {
				t.Errorf("read without the working org: %q", req.Organization)
			}
			data = respond(req)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}))
}

func zentinelleReadCmd() (*cobra.Command, *bytes.Buffer) {
	cmd, out := appTestCmd()
	cmd.Flags().String("team", "", "")
	return cmd, out
}

func TestZentinelleReadsSayWhyTheyCannotRun(t *testing.T) {
	for _, tc := range []struct {
		capability interface{}
		want       string
	}{
		{nil, "https://astro.example does not offer Zentinelle reads"},
		{false, "Zentinelle is not connected on https://astro.example"},
		{true, ""},
	} {
		resetZentinelleReads()
		srv := zentinelleServer(t, tc.capability, nil)
		cmd, _ := zentinelleReadCmd()
		err := requireZentinelle(cmd, context.Background(), api.NewClient(srv.URL, "token", false), &config.Config{}, "https://astro.example")
		srv.Close()
		if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)) {
			t.Fatalf("capability %v: got %v, want %q", tc.capability, err, tc.want)
		}
	}
}

func TestZentinelleCapabilityIsAskedOncePerRun(t *testing.T) {
	resetZentinelleReads()
	asked := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{
			"astroliftZentinelleCapability": map[string]interface{}{"connected": true},
		}})
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	for i := 0; i < 3; i++ {
		if state, err := zentinelleCapability(context.Background(), client, "org-guid"); err != nil || state != zentinelleConnected {
			t.Fatalf("state %v err %v", state, err)
		}
	}
	if asked != 1 {
		t.Fatalf("asked %d times", asked)
	}
}

func TestZentinelleCommandsAreHiddenUntilConnected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Cleanup(func() { viper.Set("api_url", ""); viper.Set("token", ""); resetZentinelleReads() })
	for _, connected := range []interface{}{nil, false, true} {
		resetZentinelleReads()
		srv := zentinelleServer(t, connected, nil)
		viper.Set("api_url", srv.URL)
		viper.Set("token", "token")
		out := &bytes.Buffer{}
		rootCmd.SetOut(out)
		rootCmd.SetArgs([]string{"--help"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
		rootCmd.SetOut(nil)
		rootCmd.SetArgs(nil)
		srv.Close()
		listed := strings.Contains(out.String(), "\n  policy ") && strings.Contains(out.String(), "\n  audit ")
		if listed != (connected == true) {
			t.Fatalf("connected=%v: policy/audit listed=%v\n%s", connected, listed, out.String())
		}
		if !strings.Contains(out.String(), "\n  zentinelle ") {
			t.Fatalf("zentinelle status/connect must stay listed:\n%s", out.String())
		}
	}
}

func TestAgentUsageResolvesTheProjectAndTotals(t *testing.T) {
	resetZentinelleReads()
	t.Cleanup(resetZentinelleReads)
	var sent gqlRequest
	srv := zentinelleServer(t, true, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "astroliftProjects") {
			return map[string]interface{}{"astroliftProjects": []map[string]interface{}{
				{"id": "proj-guid", "slug": "intake", "name": "Intake"},
			}}
		}
		sent = req
		return map[string]interface{}{"astroliftZentinelleUsage": map[string]interface{}{
			"since": "2026-10-01T00:00:00+00:00",
			"rows": []map[string]interface{}{
				{"agentSlug": "triage", "provider": "anthropic", "model": "claude", "requests": 2, "inputTokens": 100, "outputTokens": 50},
				{"agentSlug": "review", "provider": "openai", "model": "gpt", "requests": 1, "inputTokens": 3000000000, "outputTokens": 10},
			},
		}}
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	cmd, out := zentinelleReadCmd()
	_ = cmd.Flags().Set("project", "intake")
	zentinelleReadSince = "7d"
	if err := requireZentinelle(cmd, context.Background(), client, &config.Config{}, srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := runAgentUsage(cmd, context.Background(), client, &config.Config{}, nil); err != nil {
		t.Fatal(err)
	}
	if sent.Variables["projectId"] != "proj-guid" || sent.Variables["agentSlug"] != nil {
		t.Fatalf("variables: %#v", sent.Variables)
	}
	since, _ := time.Parse(time.RFC3339, sent.Variables["since"].(string))
	if d := time.Since(since); d < 167*time.Hour || d > 169*time.Hour {
		t.Fatalf("--since 7d sent %v", sent.Variables["since"])
	}
	if !strings.Contains(out.String(), "TOTAL") || !strings.Contains(out.String(), "3000000100") {
		t.Fatalf("output: %s", out.String())
	}
	if strings.Contains(sent.Query, "costUsd") || strings.Contains(out.String(), "COST") {
		t.Fatalf("token usage requested or displayed monetary data: %s\n%s", sent.Query, out.String())
	}

	cmd, out = zentinelleReadCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAgentUsage(cmd, context.Background(), client, &config.Config{}, []string{"triage"}); err != nil {
		t.Fatal(err)
	}
	if sent.Variables["agentSlug"] != "triage" {
		t.Fatalf("variables: %#v", sent.Variables)
	}
	var decoded zentinelleUsage
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil || len(decoded.Rows) != 2 {
		t.Fatalf("json: %v %s", err, out.String())
	}
	if strings.Contains(out.String(), "costUsd") {
		t.Fatalf("token JSON exposes monetary data: %s", out.String())
	}
}

func TestAgentUsageTakesOneSelector(t *testing.T) {
	resetZentinelleReads()
	cmd, _ := zentinelleReadCmd()
	_ = cmd.Flags().Set("team", "medops")
	if err := runAgentUsage(cmd, context.Background(), nil, &config.Config{}, []string{"triage"}); err == nil {
		t.Fatal("an agent and --team were both accepted")
	}
}

func TestPolicyShowFindsThePolicyAndRefusesATeam(t *testing.T) {
	resetZentinelleReads()
	t.Cleanup(resetZentinelleReads)
	var sent gqlRequest
	srv := zentinelleServer(t, true, func(req gqlRequest) map[string]interface{} {
		sent = req
		return map[string]interface{}{"astroliftZentinellePolicies": []map[string]interface{}{
			{"id": "pol-1", "name": "oversight", "policyType": "human_oversight", "enforcement": "enforce", "scope": "endpoint", "priority": 10, "description": "Named reviewer"},
		}}
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg("org-guid")
	zentinelleReadAgent = "triage"
	cmd, out := zentinelleReadCmd()
	if err := runPolicyShow(cmd, context.Background(), client, &config.Config{}, []string{"pol-1"}); err != nil {
		t.Fatal(err)
	}
	if sent.Variables["agentSlug"] != "triage" || !strings.Contains(out.String(), "Description: Named reviewer") {
		t.Fatalf("variables %#v output %s", sent.Variables, out.String())
	}
	if err := runPolicyShow(cmd, context.Background(), client, &config.Config{}, []string{"pol-2"}); err == nil || !strings.Contains(err.Error(), "not in effect") {
		t.Fatalf("unknown policy: %v", err)
	}
	cmd, _ = zentinelleReadCmd()
	_ = cmd.Flags().Set("team", "medops")
	zentinelleReadAgent = ""
	if err := runPolicyLs(cmd, context.Background(), client, &config.Config{}, nil); err == nil || !strings.Contains(err.Error(), "not a team") {
		t.Fatalf("--team on policies: %v", err)
	}
}

func TestAuditTailFollowsTheCursorAndPrintsEachEventOnce(t *testing.T) {
	resetZentinelleReads()
	t.Cleanup(resetZentinelleReads)
	var cursors []interface{}
	srv := zentinelleServer(t, true, func(req gqlRequest) map[string]interface{} {
		cursors = append(cursors, req.Variables["cursor"])
		at := "2026-10-01T00:00:00+00:00"
		page := map[string]interface{}{
			"events":     []map[string]interface{}{{"id": "evt-1", "occurredAt": at, "action": "evaluate", "agentSlug": "triage"}},
			"nextCursor": "c2",
		}
		if req.Variables["cursor"] == "c2" {
			page = map[string]interface{}{
				"events":     []map[string]interface{}{{"id": "evt-1", "occurredAt": at}, {"id": "evt-2", "occurredAt": at, "action": "block"}},
				"nextCursor": nil,
			}
		}
		return map[string]interface{}{"astroliftZentinelleAuditPage": page}
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg("org-guid")
	cmd, out := zentinelleReadCmd()
	_ = cmd.Flags().Set("json", "true")
	if err := runAuditTail(cmd, context.Background(), client, &config.Config{}, nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"evt-1"`) || !strings.Contains(lines[1], `"evt-2"`) {
		t.Fatalf("output: %s", out.String())
	}
	if len(cursors) != 2 || cursors[0] != nil || cursors[1] != "c2" {
		t.Fatalf("cursors: %#v", cursors)
	}
}

func TestAuditExportWritesTheFilePrivately(t *testing.T) {
	resetZentinelleReads()
	t.Cleanup(resetZentinelleReads)
	var sent gqlRequest
	refuse := false
	srv := zentinelleServer(t, true, func(req gqlRequest) map[string]interface{} {
		sent = req
		if refuse {
			return map[string]interface{}{"exportZentinelleAudit": map[string]interface{}{
				"ok": false, "errors": []map[string]string{{"code": "PERMISSION_DENIED", "message": "missing zentinelle.audit_export"}},
			}}
		}
		return map[string]interface{}{"exportZentinelleAudit": map[string]interface{}{
			"ok": true, "data": map[string]interface{}{"filename": "z.csv", "content": "id\nevt-1\n", "contentType": "text/csv", "rowCount": 1},
		}}
	})
	defer srv.Close()
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg("org-guid")
	cmd, _ := zentinelleReadCmd()
	if err := runAuditExport(cmd, context.Background(), client, &config.Config{}, nil); err == nil || !strings.Contains(err.Error(), "--since") {
		t.Fatalf("export without --since: %v", err)
	}
	zentinelleReadSince, zentinelleReadUntil, zentinelleExportFmt = "2026-10-01", "2026-10-02", "csv"
	zentinelleExportOut = filepath.Join(t.TempDir(), "audit.csv")
	if err := os.WriteFile(zentinelleExportOut, []byte("older export"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runAuditExport(cmd, context.Background(), client, &config.Config{}, nil); err != nil {
		t.Fatal(err)
	}
	input := sent.Variables["input"].(map[string]interface{})
	if input["since"] != "2026-10-01T00:00:00Z" || input["until"] != "2026-10-02T00:00:00Z" || input["format"] != "csv" {
		t.Fatalf("input: %#v", input)
	}
	info, err := os.Stat(zentinelleExportOut)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("export file: %v %v", info, err)
	}
	if data, _ := os.ReadFile(zentinelleExportOut); string(data) != "id\nevt-1\n" {
		t.Fatalf("content: %q", data)
	}
	refuse = true
	if err := runAuditExport(cmd, context.Background(), client, &config.Config{}, nil); err == nil || !strings.Contains(err.Error(), "zentinelle.audit_export") {
		t.Fatalf("refusal: %v", err)
	}
}

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{
		"":                     "",
		"24h":                  "2026-10-14T12:00:00Z",
		"7d":                   "2026-10-08T12:00:00Z",
		"2026-10-01":           "2026-10-01T00:00:00Z",
		"2026-10-01T05:00:00Z": "2026-10-01T05:00:00Z",
	} {
		if got, err := parseSince(in, now); err != nil || got != want {
			t.Errorf("parseSince(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := parseSince("yesterday", now); err == nil {
		t.Error("yesterday was accepted")
	}
}

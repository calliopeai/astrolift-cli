package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

// governanceFixture is one fake control plane for the governance lines.
type governanceFixture struct {
	capability interface{} // nil: the server has no capability field
	forbidden  bool        // the policy read is refused
	pages      map[string][]map[string]interface{}
	auditVars  []map[string]interface{}
}

func (f *governanceFixture) server(t *testing.T) *api.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		if r.Header.Get("X-Astrolift-Organization") != "org-guid" {
			t.Errorf("request without the working org: %s", req.Query)
		}
		reply := func(v map[string]interface{}) { _ = json.NewEncoder(w).Encode(v) }
		switch {
		case strings.Contains(req.Query, "astroliftZentinelleCapability"):
			if f.capability == nil {
				reply(map[string]interface{}{"errors": []map[string]string{{"message": "Cannot query field 'astroliftZentinelleCapability' on type 'Query'."}}})
				return
			}
			reply(map[string]interface{}{"data": map[string]interface{}{"astroliftZentinelleCapability": map[string]interface{}{"connected": f.capability}}})
		case strings.Contains(req.Query, "astroliftZentinellePolicies"):
			if req.Variables["agentSlug"] != "triage" {
				t.Errorf("policies for %v", req.Variables)
			}
			if f.forbidden {
				reply(map[string]interface{}{"errors": []map[string]interface{}{{"message": "permission denied: zentinelle.policy_view", "extensions": map[string]string{"code": "FORBIDDEN"}}}})
				return
			}
			reply(map[string]interface{}{"data": map[string]interface{}{"astroliftZentinellePolicies": []map[string]interface{}{
				{"id": "p1", "name": "pii-guard", "enforcement": "enforce"},
				{"id": "p2", "name": "cost-cap", "enforcement": "audit"},
			}}})
		case strings.Contains(req.Query, "astroliftZentinelleAuditPage"):
			f.auditVars = append(f.auditVars, req.Variables)
			cursor, _ := req.Variables["cursor"].(string)
			var next interface{}
			if cursor == "" && f.pages["page-2"] != nil {
				next = "page-2"
			}
			reply(map[string]interface{}{"data": map[string]interface{}{"astroliftZentinelleAuditPage": map[string]interface{}{
				"events": f.pages[cursor], "nextCursor": next,
			}}})
		case strings.Contains(req.Query, "agentTaskLogs"):
			reply(map[string]interface{}{"data": map[string]interface{}{"agentTaskLogs": []string{"hello"}}})
		case strings.Contains(req.Query, "agentTask("):
			reply(map[string]interface{}{"data": map[string]interface{}{"agentTask": map[string]interface{}{
				"id": "task-1", "status": "completed", "agentSlug": "triage",
				"createdAt": "2026-10-16T09:59:00Z", "startedAt": "2026-10-16T10:00:00Z", "finishedAt": "2026-10-16T10:05:00Z",
			}}})
		default:
			t.Errorf("unexpected query %s", req.Query)
		}
	}))
	t.Cleanup(srv.Close)
	client := api.NewClient(srv.URL, "token", false)
	client.SetOrg("org-guid")
	return client
}

func governanceEvent(id, at, action, resourceType string) map[string]interface{} {
	return map[string]interface{}{"id": id, "occurredAt": at, "action": action, "resourceType": resourceType,
		"resourceId": "r-" + id, "agentSlug": "triage", "actor": "ops"}
}

var governancePages = map[string][]map[string]interface{}{
	"": {
		governanceEvent("1", "2026-10-16T10:01:00Z", "create", "execution_approval"),
		governanceEvent("2", "2026-10-16T10:02:00Z", "update", "agent_enforcement"),
		governanceEvent("3", "2026-10-16T10:02:30Z", "access", "llm_provider_key"),
	},
	"page-2": {
		governanceEvent("4", "2026-10-16T10:03:00Z", "policy_violation", "policy"),
		governanceEvent("5", "2026-10-16T10:09:00Z", "update", "approval_request"),
	},
}

func inspectGovernance(t *testing.T, f *governanceFixture, asJSON bool) string {
	t.Helper()
	resetZentinelleReads()
	t.Cleanup(resetZentinelleReads)
	prior := agentInspectJSON
	agentInspectJSON = asJSON
	t.Cleanup(func() { agentInspectJSON = prior })
	cmd, out := agentTestCmd()
	if err := runAgentInspect(cmd, context.Background(), f.server(t), &config.Config{}, "task-1"); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

const inspectRecord = `Task ID:       task-1
Status:        completed
VNC enabled:   no
Created at:    2026-10-16T09:59:00Z
Started at:    2026-10-16T10:00:00Z
Finished at:   2026-10-16T10:05:00Z
`

func TestAgentInspectGovernanceGolden(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fixture governanceFixture
		want    string
	}{
		{"connected and paged", governanceFixture{capability: true, pages: governancePages},
			inspectRecord + "Governance:    triage, last 24h: 1 block, 1 violation, 2 approvals\nPolicies:      pii-guard (enforce), cost-cap (audit)\n"},
		{"connected and empty", governanceFixture{capability: true},
			inspectRecord + "Governance:    triage, last 24h: 0 blocks, 0 violations, 0 approvals\nPolicies:      pii-guard (enforce), cost-cap (audit)\n"},
		{"not connected", governanceFixture{capability: false},
			inspectRecord + "Governance:    Zentinelle is not connected on {server}\n"},
		{"forbidden", governanceFixture{capability: true, forbidden: true},
			inspectRecord + "Governance:    reading Zentinelle policies: graphql errors: FORBIDDEN: permission denied: zentinelle.policy_view\n"},
		{"not offered", governanceFixture{}, inspectRecord},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetZentinelleReads()
			client := tc.fixture.server(t)
			cmd, out := agentTestCmd()
			agentInspectJSON = false
			if err := runAgentInspect(cmd, context.Background(), client, &config.Config{}, "task-1"); err != nil {
				t.Fatal(err)
			}
			if want := strings.ReplaceAll(tc.want, "{server}", client.BaseURL()); out.String() != want {
				t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
			}
			if len(tc.fixture.auditVars) > 0 {
				since, _ := time.Parse(time.RFC3339, tc.fixture.auditVars[0]["since"].(string))
				if d := time.Since(since); d < 23*time.Hour || d > 25*time.Hour {
					t.Fatalf("inspect asked since %v", since)
				}
			}
		})
	}
}

func TestAgentInspectGovernanceJSON(t *testing.T) {
	f := &governanceFixture{capability: true, pages: governancePages}
	var record struct {
		ID         string `json:"id"`
		Governance struct {
			AgentSlug string                 `json:"agentSlug"`
			Policies  []zentinellePolicy     `json:"policies"`
			Blocks    int                    `json:"blocks"`
			Approvals int                    `json:"approvals"`
			Events    []zentinelleAuditEvent `json:"events"`
		} `json:"governance"`
	}
	if err := json.Unmarshal([]byte(inspectGovernance(t, f, true)), &record); err != nil {
		t.Fatal(err)
	}
	g := record.Governance
	if record.ID != "task-1" || g.AgentSlug != "triage" || len(g.Policies) != 2 || g.Blocks != 1 || g.Approvals != 2 || len(g.Events) != 4 {
		t.Fatalf("json: %+v", record)
	}

	out := inspectGovernance(t, &governanceFixture{capability: false}, true)
	if !strings.Contains(out, `"unavailable": "Zentinelle is not connected on`) {
		t.Fatalf("json reason: %s", out)
	}
	if out := inspectGovernance(t, &governanceFixture{}, true); strings.Contains(out, "governance") {
		t.Fatalf("not offered must add nothing: %s", out)
	}
}

func TestAgentLogsGovernanceGolden(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fixture       governanceFixture
		want, wantErr string
	}{
		{"connected and paged", governanceFixture{capability: true, pages: governancePages}, `hello
[governance] triage from 2026-10-16T10:00:00Z to 2026-10-16T10:05:00Z: 1 block, 1 violation, 1 approval
[governance] 2026-10-16T10:01:00Z approval   create execution_approval/r-1 ops
[governance] 2026-10-16T10:02:00Z block      update agent_enforcement/r-2 ops
[governance] 2026-10-16T10:03:00Z violation  policy_violation policy/r-4 ops
`, ""},
		{"connected and empty", governanceFixture{capability: true}, "hello\n[governance] triage from 2026-10-16T10:00:00Z to 2026-10-16T10:05:00Z: 0 blocks, 0 violations, 0 approvals\n", ""},
		{"not connected", governanceFixture{capability: false}, "hello\n", "governance: Zentinelle is not connected on {server}\n"},
		{"forbidden", governanceFixture{capability: true, forbidden: true}, "hello\n", "governance: reading Zentinelle policies: graphql errors: FORBIDDEN: permission denied: zentinelle.policy_view\n"},
		{"not offered", governanceFixture{}, "hello\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetZentinelleReads()
			t.Cleanup(resetZentinelleReads)
			agentLogsFollow, agentLogsTail = false, 100
			client := tc.fixture.server(t)
			cmd, out := agentTestCmd()
			errOut := &bytes.Buffer{}
			cmd.SetErr(errOut)
			if err := runAgentLogs(cmd, context.Background(), client, "task-1"); err != nil {
				t.Fatal(err)
			}
			printLogGovernance(cmd.OutOrStdout(), cmd.ErrOrStderr(), readAgentGovernance(context.Background(), client, "task-1", true, time.Now()))
			if out.String() != tc.want || errOut.String() != strings.ReplaceAll(tc.wantErr, "{server}", client.BaseURL()) {
				t.Fatalf("stdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
			}
			if len(tc.fixture.auditVars) > 0 && tc.fixture.auditVars[0]["since"] != "2026-10-16T10:00:00Z" {
				t.Fatalf("logs asked since %v", tc.fixture.auditVars[0]["since"])
			}
		})
	}
}

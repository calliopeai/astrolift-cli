package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

// resetAgentWorkloadFlags restores the package-level flag vars between tests
// (cobra flag vars are global, so a leaked value silently changes the next
// test's behaviour).
func resetAgentWorkloadFlags() {
	agentWorkloadsProject = ""
	agentWorkloadsLimit = 50
	agentWorkloadsJSON = false
}

// workloadRow builds an agentWorkloads response row with sane defaults so a
// test only has to state the fields it cares about.
func workloadRow(slug, guid string, over map[string]interface{}) map[string]interface{} {
	row := map[string]interface{}{
		"id": guid, "name": slug, "slug": slug,
		"appSlug": "agents", "projectSlug": "demo",
		"sourceRepo": "calliopeai/" + slug, "sourceUrl": "https://github.com/calliopeai/" + slug,
		"runFamily": "once", "runMode": "Once", "runPaused": false,
		"runCronExpression": "", "lastRunStatus": nil, "lastRunAt": nil,
		"runningCount": 0, "runMaxParallel": nil, "replicas": 1,
	}
	for k, v := range over {
		row[k] = v
	}
	return row
}

// The table must carry BOTH identifiers: the slug `agent dispatch` takes and
// the GUID `workflow create --bind` takes. That pairing is the whole point of
// the command — a picker that shows only one of them can't do the other job.
func TestAgentWorkloadsListShowsSlugAndGUID(t *testing.T) {
	resetAgentWorkloadFlags()
	defer resetAgentWorkloadFlags()

	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": []map[string]interface{}{
			{"id": "org-1", "name": "Acme", "slug": "acme"},
		},
		"agentWorkloads": []map[string]interface{}{
			workloadRow("triage-bot", "3f0c9a1e-0000-4000-8000-000000000001", nil),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	_ = cmd.Flags().Set("org", "acme")
	if err := runAgentWorkloadsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runAgentWorkloadsList: %v", err)
	}

	got := out.String()
	for _, want := range []string{"triage-bot", "3f0c9a1e-0000-4000-8000-000000000001", "demo", "calliopeai/triage-bot"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q\n%s", want, got)
		}
	}
}

// --project must reach the resolver as projectSlug; filtering client-side
// would silently return the whole fleet on an org with >limit agents.
func TestAgentWorkloadsListSendsProjectFilter(t *testing.T) {
	resetAgentWorkloadFlags()
	agentWorkloadsProject = "alpha"
	defer resetAgentWorkloadFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": []map[string]interface{}{
			{"id": "org-1", "name": "Acme", "slug": "acme"},
		},
		"agentWorkloads": []map[string]interface{}{},
	}, &captured)
	defer srv.Close()

	cmd, out := agentTestCmd()
	_ = cmd.Flags().Set("org", "acme")
	if err := runAgentWorkloadsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runAgentWorkloadsList: %v", err)
	}

	if got := captured.Variables["projectSlug"]; got != "alpha" {
		t.Errorf("projectSlug variable = %v, want alpha", got)
	}
	// An empty project should say which project was empty, not just "none".
	if !strings.Contains(out.String(), `project "alpha"`) {
		t.Errorf("empty-project message should name the project, got: %s", out.String())
	}
}

// Omitting --project must omit the variable entirely rather than sending "",
// which the resolver would treat as a real (never-matching) project slug.
func TestAgentWorkloadsListOmitsEmptyProjectVariable(t *testing.T) {
	resetAgentWorkloadFlags()
	defer resetAgentWorkloadFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": []map[string]interface{}{
			{"id": "org-1", "name": "Acme", "slug": "acme"},
		},
		"agentWorkloads": []map[string]interface{}{},
	}, &captured)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	_ = cmd.Flags().Set("org", "acme")
	if err := runAgentWorkloadsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runAgentWorkloadsList: %v", err)
	}

	if _, present := captured.Variables["projectSlug"]; present {
		t.Errorf("projectSlug should be absent when --project is unset, got %#v", captured.Variables)
	}
}

func TestAgentWorkloadsListAppliesLimit(t *testing.T) {
	resetAgentWorkloadFlags()
	agentWorkloadsLimit = 1
	defer resetAgentWorkloadFlags()

	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": []map[string]interface{}{
			{"id": "org-1", "name": "Acme", "slug": "acme"},
		},
		"agentWorkloads": []map[string]interface{}{
			workloadRow("first-agent", "guid-1", nil),
			workloadRow("second-agent", "guid-2", nil),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	_ = cmd.Flags().Set("org", "acme")
	if err := runAgentWorkloadsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runAgentWorkloadsList: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "first-agent") {
		t.Errorf("expected the first row, got: %s", got)
	}
	if strings.Contains(got, "second-agent") {
		t.Errorf("--limit 1 should have truncated the second row, got: %s", got)
	}
	if !strings.Contains(got, "1 agent workload(s) shown.") {
		t.Errorf("count line should reflect the truncated set, got: %s", got)
	}
}

// --json is what the IDE consumes; it must emit the raw identifiers rather
// than the table's decorated labels.
func TestAgentWorkloadsListJSONCarriesIdentifiers(t *testing.T) {
	resetAgentWorkloadFlags()
	agentWorkloadsJSON = true
	defer resetAgentWorkloadFlags()

	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": []map[string]interface{}{
			{"id": "org-1", "name": "Acme", "slug": "acme"},
		},
		"agentWorkloads": []map[string]interface{}{
			workloadRow("triage-bot", "guid-abc", map[string]interface{}{
				"runMode": "Loop", "runPaused": true, "runningCount": 2,
			}),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	_ = cmd.Flags().Set("org", "acme")
	if err := runAgentWorkloadsList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runAgentWorkloadsList: %v", err)
	}

	var rows []agentWorkload
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decoding JSON output: %v\n%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].Slug != "triage-bot" || rows[0].ID != "guid-abc" {
		t.Errorf("identifiers not carried through JSON: %+v", rows[0])
	}
	// The "(paused)" decoration belongs to the table, not the machine surface.
	if rows[0].RunMode != "Loop" || !rows[0].RunPaused {
		t.Errorf("run mode/paused should be separate fields in JSON: %+v", rows[0])
	}
	if rows[0].RunningCount != 2 {
		t.Errorf("runningCount = %d, want 2", rows[0].RunningCount)
	}
}

// A paused scheduled agent looks idle in a picker unless the table says so.
func TestRunModeLabelMarksPaused(t *testing.T) {
	if got := runModeLabel(agentWorkload{RunMode: "Schedule", RunPaused: true}); got != "Schedule (paused)" {
		t.Errorf("runModeLabel(paused) = %q", got)
	}
	if got := runModeLabel(agentWorkload{RunMode: "Once"}); got != "Once" {
		t.Errorf("runModeLabel(active) = %q", got)
	}
}

func TestLastRunLabelHandlesNeverRun(t *testing.T) {
	if got := lastRunLabel(agentWorkload{}); got != "-" {
		t.Errorf("never-run agent should render %q, got %q", "-", got)
	}
	status := "completed"
	if got := lastRunLabel(agentWorkload{LastRunStatus: &status}); got != "completed" {
		t.Errorf("status without timestamp = %q", got)
	}
	at := "2026-06-17T10:00:00.123456+00:00"
	if got := lastRunLabel(agentWorkload{LastRunStatus: &status, LastRunAt: &at}); !strings.HasPrefix(got, "completed ") {
		t.Errorf("status with timestamp = %q", got)
	}
}

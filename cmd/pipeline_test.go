package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// pipelineTestCmd returns a bare command with stdout captured. The pipeline
// run-funcs read their inputs from package-level flag vars (set per-test) and
// write to cmd.OutOrStdout().
func pipelineTestCmd() (*cobra.Command, *bytes.Buffer) {
	c := &cobra.Command{}
	out := &bytes.Buffer{}
	c.SetOut(out)
	return c, out
}

func TestRunPipelineListSendsNonNullLimit(t *testing.T) {
	pipelineListLimit = 25
	pipelineListJSON = false
	defer func() { pipelineListLimit = 0; pipelineListJSON = false }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftPipelinesPage": map[string]interface{}{"items": []map[string]interface{}{
			{"id": reviewedPipelineTestID, "name": "deploy", "repoUrl": "https://git/x", "defaultBranch": "main", "tomlPath": ".astro/pipeline.toml", "createdAt": "2026-01-01T00:00:00Z", "organizationId": reviewedPipelineTestOrg, "version": 7},
		}, "nextCursor": nil},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	client.SetOrg(reviewedPipelineTestOrg)
	cmd, out := pipelineTestCmd()
	if err := runPipelineList(cmd, context.Background(), client); err != nil {
		t.Fatalf("runPipelineList: %v", err)
	}

	if !strings.Contains(captured.Query, "astroliftPipelinesPage(limit: $limit, after: $after, search: $search)") {
		t.Errorf("list query lost limit arg:\n%s", captured.Query)
	}
	// Schema: astroliftPipelines(limit: Int! = 100) — must be declared Int!.
	if !strings.Contains(captured.Query, "$limit: Int!") {
		t.Errorf("list limit must be declared Int! to match schema:\n%s", captured.Query)
	}
	if got := captured.Variables["limit"]; got != float64(25) {
		t.Errorf("limit variable = %v, want 25", got)
	}
	if !strings.Contains(out.String(), "deploy") {
		t.Errorf("expected pipeline name in output:\n%s", out.String())
	}
}

func TestRunPipelineRunsSendsRequiredStringPipelineID(t *testing.T) {
	pipelineRunsPipeline = "deploy"
	pipelineRunsLimit = 10
	defer func() { pipelineRunsPipeline = ""; pipelineRunsLimit = 0 }()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		// resolvePipelineID lookup (name -> guid)
		"astroliftPipelinesPage": map[string]interface{}{"items": []map[string]interface{}{
			{"id": "00000000-0000-4000-8000-000000000099", "name": "deploy"},
		}, "nextCursor": nil},
		"astroliftPipelineRunsPage": map[string]interface{}{"items": []map[string]interface{}{func() map[string]interface{} {
			r := reviewedPipelineTestRun("request-1")
			r["pipelineId"] = "00000000-0000-4000-8000-000000000099"
			return r
		}()}, "nextCursor": nil},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	client.SetOrg(reviewedPipelineTestOrg)
	cmd, out := pipelineTestCmd()
	if err := runPipelineRuns(cmd, context.Background(), client); err != nil {
		t.Fatalf("runPipelineRuns: %v", err)
	}

	// Schema: astroliftPipelineRuns(pipelineId: String!, limit: Int! = 50).
	if !strings.Contains(captured.Query, "astroliftPipelineRunsPage(pipelineId: $pipelineId, limit: $limit, after: $after, search: $search)") {
		t.Errorf("runs query arg shape wrong:\n%s", captured.Query)
	}
	if !strings.Contains(captured.Query, "$pipelineId: String!") {
		t.Errorf("runs pipelineId must be required String! (was optional ID):\n%s", captured.Query)
	}
	if !strings.Contains(captured.Query, "$limit: Int!") {
		t.Errorf("runs limit must be Int!:\n%s", captured.Query)
	}
	if got := captured.Variables["pipelineId"]; got != "00000000-0000-4000-8000-000000000099" {
		t.Errorf("pipelineId variable = %v, want resolved guid-99", got)
	}
	if !strings.Contains(out.String(), "#3") {
		t.Errorf("expected run row in output:\n%s", out.String())
	}
}

func TestResolvePipelineIDNotFound(t *testing.T) {
	srv := gqlServer(t, map[string]interface{}{
		"astroliftPipelinesPage": map[string]interface{}{"items": []map[string]interface{}{{"id": "00000000-0000-4000-8000-000000000001", "name": "other"}}, "nextCursor": nil},
	}, nil)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	client.SetOrg(reviewedPipelineTestOrg)
	if _, err := resolvePipelineID(context.Background(), client, "missing"); err == nil {
		t.Error("expected not-found error for unknown pipeline name/id")
	}
}

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

const nvkeWorkloadTestID = "c098fc8d-3f48-45fd-b9b0-7d670261e9ef"
const nvkeWorkloadContextID = "3636227e-50df-4c08-b7cf-245b72d0b41d"

func nvkeWorkloadTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", "workload", verb})
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	command.SetContext(context.Background())
	command.SetOut(out)
	command.SetErr(out)
	for flag, value := range map[string]string{"site": nvkeTestSite, "site-version": "7", "authority-version": "3", "version": "4", "plan": nvkePlanTestID, "plan-version": "2", "plan-digest": strings.Repeat("b", 64), "chart-artifact": nvkeChartTestID, "chart-artifact-version": "2", "chart-artifact-digest": strings.Repeat("c", 64), "tree": nvkeTreeTestID, "tree-version": "2", "tree-digest": strings.Repeat("d", 64), "artifact": nvkeWorkloadTestID} {
		if err = command.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	return command, out
}

func nvkeWorkloadTestBody() string {
	return `{"format":1,"artifactId":"` + nvkeWorkloadTestID + `","planId":"` + nvkePlanTestID + `","planDigest":"` + strings.Repeat("b", 64) + `","treeId":"` + nvkeTreeTestID + `","treeDigest":"` + strings.Repeat("d", 64) + `","contextBindings":{"nktp":{"contextId":"` + nvkeWorkloadContextID + `","contextDigest":"` + strings.Repeat("a", 64) + `"}},"maximumBytes":"67108864","maximumResources":10000,"timeoutSeconds":300}`
}

func nvkeWorkloadTestFixture() *api.NVKEInstallPlanWorkload {
	image := nvkeImageTestFixture()
	items, resources, uses, refs, issues := nvkeInstallPlanFixture().ItemCount, 2, 3, 1, 0
	return &api.NVKEInstallPlanWorkload{ID: nvkeWorkloadTestID, Version: image.Version, Status: image.Status, PlanID: image.PlanID, PlanVersion: image.PlanVersion, ChartArtifactID: image.ChartArtifactID, ChartArtifactVersion: image.ChartArtifactVersion, ChartArtifactDigest: image.ChartArtifactDigest, TreeID: image.TreeID, TreeVersion: image.TreeVersion, TreeDigest: image.TreeDigest, OperationID: image.OperationID, OperationVersion: image.OperationVersion, SiteID: image.SiteID, SiteVersion: image.SiteVersion, AuthorityID: image.AuthorityID, AuthorityVersion: image.AuthorityVersion, PlanDigest: image.PlanDigest, ArtifactDigest: image.ArtifactDigest, SourceDigest: image.SourceDigest, CaptureDigest: image.CaptureDigest, WorkloadDigest: strings.Repeat("4", 64), ItemCount: &items, ResourceCount: &resources, ImageUseCount: &uses, ImageReferenceCount: &refs, IssueCount: &issues, ObservedAt: image.ObservedAt, ExecutionState: "requires-reviewed-operator-images-platform-credentials-rewrite-import-use-and-linked-admission", ContinuationAuthorized: image.ContinuationAuthorized}
}

func nvkeWorkloadTestReply(t *testing.T, req gqlRequest, w http.ResponseWriter, value any) {
	t.Helper()
	data := nvkeInstallPlanTestData()
	data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
	data["reviewNvkeOperatorInstallPlanCharts"] = map[string]any{"ok": true, "data": nvkeChartTestFixture()}
	data["reviewNvkeOperatorInstallPlanTree"] = map[string]any{"ok": true, "data": nvkeTreeTestFixture()}
	for _, field := range []string{"prepareNvkeOperatorInstallPlanWorkload", "reviewNvkeOperatorInstallPlanWorkload", "recoverNvkeOperatorInstallPlanWorkloadPreparation"} {
		if strings.Contains(req.Query, field+"(") {
			data[field] = map[string]any{"ok": true, "data": value}
		}
	}
	if strings.Contains(req.Query, "requestNvkeOperatorJob") {
		t.Error("inventory admitted native work")
	}
	writePipelineTestResponse(t, w, data)
}

func TestNVKEWorkloadReviewAndRetryBindExactParents(t *testing.T) {
	for _, verb := range []string{"review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command, out := nvkeWorkloadTestCommand(t, verb)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "InstallPlanWorkload(") || strings.Contains(req.Query, "InstallPlanWorkloadPreparation(") {
					calls++
					input := req.Variables["input"].(map[string]any)
					if input["artifactId"] != nvkeWorkloadTestID || input["treeId"] != nvkeTreeTestID || input["expectedTreeVersion"] != float64(2) {
						t.Error("workload source changed")
					}
					if _, ok := input["requestText"]; ok {
						t.Error("historical read forwarded private intent")
					}
				}
				nvkeWorkloadTestReply(t, req, w, nvkeWorkloadTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanWorkload(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !strings.Contains(out.String(), strings.Repeat("4", 64)) {
				t.Fatal("missing or repeated inventory RPC")
			}
		})
	}
}

func TestNVKEWorkloadJournalPrecedesMutationAndLostReplyNeverReplays(t *testing.T) {
	command, _ := nvkeWorkloadTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	body := nvkeWorkloadTestBody()
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanWorkload(") {
			calls++
			data, err := os.ReadFile(journal)
			var saved nvkeWorkloadJournal
			if err != nil || decodeNVKEWorkloadStrict(data, &saved) != nil || saved.Kind != "nvke-install-plan-workload" || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) || bytes.Contains(data, []byte(nvkeWorkloadContextID)) {
				t.Error("private journal missing or leaked bindings")
			}
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = connection.Close()
			return
		}
		nvkeWorkloadTestReply(t, req, w, nvkeWorkloadTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanWorkload(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("lost reply replayed or succeeded")
	}
	if _, err := os.Stat(journal); err != nil {
		t.Fatal("journal lost")
	}
	var changed map[string]any
	_ = json.Unmarshal([]byte(body), &changed)
	changed["timeoutSeconds"] = 299
	data, _ := json.Marshal(changed)
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runNVKEAgentInstallPlanWorkload(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("changed private intent reused identity")
	}
}

func TestNVKEWorkloadMalformedInputRefusesBeforeJournalOrMutation(t *testing.T) {
	for name, pair := range map[string][2]string{
		"root-duplicate": {`"format":1`, `"format":1,"format":1`}, "root-alias": {`"artifactId"`, `"ArtifactId"`},
		"binding-duplicate": {`"contextId":"` + nvkeWorkloadContextID + `"`, `"contextId":"` + nvkeWorkloadContextID + `","contextId":"` + nvkeWorkloadContextID + `"`},
		"binding-alias":     {`"contextId"`, `"ContextId"`}, "binding-unknown": {`"contextId"`, `"privatePath"`},
		"alias-identity": {nvkeWorkloadContextID, nvkeWorkloadTestID}, "trailing-digest": {strings.Repeat("a", 64), strings.Repeat("a", 64) + `\n`},
		"too-large": {`"maximumBytes":"67108864"`, `"maximumBytes":"67108865"`}, "zero-resource": {`"maximumResources":10000`, `"maximumResources":0`},
		"null-bindings": {`"contextBindings":{"nktp":{"contextId":"` + nvkeWorkloadContextID + `","contextDigest":"` + strings.Repeat("a", 64) + `"}}`, `"contextBindings":null`},
	} {
		t.Run(name, func(t *testing.T) {
			command, _ := nvkeWorkloadTestCommand(t, "prepare")
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
			data := strings.Replace(nvkeWorkloadTestBody(), pair[0], pair[1], 1)
			if err := privatefile.Write(input, []byte(data)); err != nil {
				t.Fatal(err)
			}
			_ = command.Flags().Set("file", input)
			_ = command.Flags().Set("request-file", journal)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanWorkload(") {
					calls++
				}
				nvkeWorkloadTestReply(t, req, w, nvkeWorkloadTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanWorkload(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 0 {
				t.Fatal("malformed intent reached mutation")
			}
			if _, err := os.Stat(journal); !os.IsNotExist(err) {
				t.Fatal("malformed intent persisted journal")
			}
		})
	}
}

func TestNVKEWorkloadRejectsTruncatedStaleAndAuthorizingEvidence(t *testing.T) {
	for _, name := range []string{"missing-count", "source", "digest", "counts", "continuation", "state"} {
		t.Run(name, func(t *testing.T) {
			command, _ := nvkeWorkloadTestCommand(t, "review")
			row := nvkeWorkloadTestFixture()
			switch name {
			case "missing-count":
				row.ImageReferenceCount = nil
			case "source":
				row.TreeDigest = strings.Repeat("f", 64)
			case "digest":
				row.WorkloadDigest = "invalid"
			case "counts":
				*row.ImageReferenceCount = *row.ImageUseCount + 1
			case "continuation":
				*row.ContinuationAuthorized = true
			case "state":
				row.ExecutionState = "ready"
			}
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) { nvkeWorkloadTestReply(t, req, w, row) })
			defer server.Close()
			if err := runNVKEAgentInstallPlanWorkload(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review"); err == nil {
				t.Fatal("invalid observation trusted")
			}
		})
	}
}

func TestNVKEWorkloadPreparePublishesOnlyBoundedSummary(t *testing.T) {
	command, out := nvkeWorkloadTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	if err := privatefile.Write(input, []byte(nvkeWorkloadTestBody())); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanWorkload(") {
			calls++
			if _, err := os.Stat(journal); err != nil {
				t.Error("journal missing before mutation")
			}
		}
		nvkeWorkloadTestReply(t, req, w, nvkeWorkloadTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanWorkload(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || strings.Contains(out.String(), nvkeWorkloadContextID) || !strings.Contains(out.String(), strings.Repeat("4", 64)) {
		t.Fatal("private or unbound result")
	}
}

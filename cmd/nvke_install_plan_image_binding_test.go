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

const nvkeImageBindingTestID = "61ba2d0d-ae12-4c09-9c0d-36bd92dd2dfd"

func nvkeImageBindingTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", "image-binding", verb})
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	command.SetContext(context.Background())
	command.SetOut(out)
	command.SetErr(out)
	for flag, value := range map[string]string{"site": nvkeTestSite, "site-version": "7", "authority-version": "3", "version": "4", "plan": nvkePlanTestID, "plan-version": "2", "plan-digest": strings.Repeat("b", 64), "chart-artifact": nvkeChartTestID, "chart-artifact-version": "2", "chart-artifact-digest": strings.Repeat("c", 64), "tree": nvkeTreeTestID, "tree-version": "2", "tree-digest": strings.Repeat("d", 64), "artifact": nvkeImageBindingTestID, "workload": nvkeWorkloadTestID, "workload-version": "2", "workload-digest": strings.Repeat("4", 64)} {
		if err = command.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	return command, out
}

func nvkeImageBindingTestBody() string {
	return `{"format":1,"artifactId":"` + nvkeImageBindingTestID + `","planId":"` + nvkePlanTestID + `","planDigest":"` + strings.Repeat("b", 64) + `","treeId":"` + nvkeTreeTestID + `","treeDigest":"` + strings.Repeat("d", 64) + `","workloadId":"` + nvkeWorkloadTestID + `","workloadDigest":"` + strings.Repeat("4", 64) + `","imageBindings":{"private.example.test/owned:v1":{"sourceKind":"registry","artifactId":"` + nvkeImageTestID + `","artifactDigest":"` + strings.Repeat("1", 64) + `"}},"maximumBytes":"67108864","maximumImageBytes":"134217728","timeoutSeconds":300}`
}

func nvkeImageBindingTestFixture() *api.NVKEInstallPlanImageBinding {
	image, workload := nvkeImageTestFixture(), nvkeWorkloadTestFixture()
	images, imports, rewrites := 1, 0, 1
	return &api.NVKEInstallPlanImageBinding{ID: nvkeImageBindingTestID, Version: image.Version, Status: image.Status, PlanID: image.PlanID, PlanVersion: image.PlanVersion, ChartArtifactID: image.ChartArtifactID, ChartArtifactVersion: image.ChartArtifactVersion, ChartArtifactDigest: image.ChartArtifactDigest, TreeID: image.TreeID, TreeVersion: image.TreeVersion, TreeDigest: image.TreeDigest, OperationID: image.OperationID, OperationVersion: image.OperationVersion, SiteID: image.SiteID, SiteVersion: image.SiteVersion, AuthorityID: image.AuthorityID, AuthorityVersion: image.AuthorityVersion, PlanDigest: image.PlanDigest, ArtifactDigest: image.ArtifactDigest, SourceDigest: image.SourceDigest, CaptureDigest: image.CaptureDigest, WorkloadID: workload.ID, WorkloadVersion: workload.Version, WorkloadDigest: workload.WorkloadDigest, BindingDigest: strings.Repeat("5", 64), ImageCount: &images, ImageUseCount: workload.ImageUseCount, ImportCount: &imports, RewriteCount: &rewrites, VerifiedImageBytes: image.Bytes, WorkloadIssueCount: workload.IssueCount, ObservedAt: image.ObservedAt, ExecutionState: "requires-reviewed-operator-images-node-platform-credentials-rewrite-import-use-and-linked-admission", ContinuationAuthorized: image.ContinuationAuthorized}
}

func nvkeImageBindingTestReply(t *testing.T, req gqlRequest, w http.ResponseWriter, value *api.NVKEInstallPlanImageBinding, workload *api.NVKEInstallPlanWorkload) {
	t.Helper()
	data := nvkeInstallPlanTestData()
	data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
	data["reviewNvkeOperatorInstallPlanCharts"] = map[string]any{"ok": true, "data": nvkeChartTestFixture()}
	data["reviewNvkeOperatorInstallPlanTree"] = map[string]any{"ok": true, "data": nvkeTreeTestFixture()}
	data["reviewNvkeOperatorInstallPlanWorkload"] = map[string]any{"ok": true, "data": workload}
	for _, field := range []string{"prepareNvkeOperatorInstallPlanImageBinding", "reviewNvkeOperatorInstallPlanImageBinding", "recoverNvkeOperatorInstallPlanImageBindingPreparation"} {
		if strings.Contains(req.Query, field+"(") {
			data[field] = map[string]any{"ok": true, "data": value}
		}
	}
	if strings.Contains(req.Query, "requestNvkeOperatorJob") {
		t.Error("binding admitted native work")
	}
	writePipelineTestResponse(t, w, data)
}

func TestNVKEImageBindingReviewAndRetryRetainWorkloadWithoutPrivateInput(t *testing.T) {
	for _, verb := range []string{"review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command, out := nvkeImageBindingTestCommand(t, verb)
			calls, reads := 0, 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanWorkload(") {
					reads++
				}
				if strings.Contains(req.Query, "InstallPlanImageBinding(") || strings.Contains(req.Query, "InstallPlanImageBindingPreparation(") {
					calls++
					input := req.Variables["input"].(map[string]any)
					if input["artifactId"] != nvkeImageBindingTestID || input["workloadId"] != nvkeWorkloadTestID || input["expectedWorkloadVersion"] != float64(2) || input["workloadDigest"] != strings.Repeat("4", 64) {
						t.Error("workload source changed")
					}
					if _, ok := input["requestText"]; ok {
						t.Error("historical read forwarded private intent")
					}
				}
				nvkeImageBindingTestReply(t, req, w, nvkeImageBindingTestFixture(), nvkeWorkloadTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanImageBinding(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || reads != 1 || !strings.Contains(out.String(), strings.Repeat("5", 64)) {
				t.Fatal("missing or repeated binding RPC")
			}
		})
	}
}

func TestNVKEImageBindingJournalPrecedesLostReplyAndChangedIntentCannotReplay(t *testing.T) {
	command, _ := nvkeImageBindingTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	body := nvkeImageBindingTestBody()
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanImageBinding(") {
			calls++
			data, err := os.ReadFile(journal)
			var saved nvkeImageBindingJournal
			if err != nil || decodeNVKEImageBindingStrict(data, &saved) != nil || saved.Kind != "nvke-install-plan-image-binding" || saved.WorkloadID != nvkeWorkloadTestID || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) || bytes.Contains(data, []byte("private.example.test")) || bytes.Contains(data, []byte(nvkeImageTestID)) {
				t.Error("journal missing or exposed private selection")
			}
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = connection.Close()
			return
		}
		nvkeImageBindingTestReply(t, req, w, nvkeImageBindingTestFixture(), nvkeWorkloadTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanImageBinding(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("lost reply replayed or accepted")
	}
	var changed map[string]any
	_ = json.Unmarshal([]byte(body), &changed)
	changed["timeoutSeconds"] = 299
	data, _ := json.Marshal(changed)
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runNVKEAgentInstallPlanImageBinding(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("changed private intent reused identity")
	}
}

func TestNVKEImageBindingMalformedSelectionsRefuseBeforeJournalOrMutation(t *testing.T) {
	for name, pair := range map[string][2]string{
		"duplicate-root": {`"format":1`, `"format":1,"format":1`}, "alias-root": {`"artifactId"`, `"ArtifactId"`},
		"duplicate-selection": {`"sourceKind":"registry"`, `"sourceKind":"registry","sourceKind":"local"`},
		"alias-selection":     {`"sourceKind"`, `"SourceKind"`}, "unknown-selection": {`"sourceKind"`, `"privatePath"`},
		"aliased-identity": {nvkeImageTestID, nvkeWorkloadTestID}, "wrong-workload": {strings.Repeat("4", 64), strings.Repeat("f", 64)},
		"output-budget": {`"maximumBytes":"67108864"`, `"maximumBytes":"67108865"`}, "image-budget": {`"maximumImageBytes":"134217728"`, `"maximumImageBytes":"0"`},
		"null-selection": {`"sourceKind":"registry"`, `"sourceKind":null`},
	} {
		t.Run(name, func(t *testing.T) {
			command, _ := nvkeImageBindingTestCommand(t, "prepare")
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
			if err := privatefile.Write(input, []byte(strings.Replace(nvkeImageBindingTestBody(), pair[0], pair[1], 1))); err != nil {
				t.Fatal(err)
			}
			_ = command.Flags().Set("file", input)
			_ = command.Flags().Set("request-file", journal)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanImageBinding(") {
					calls++
				}
				nvkeImageBindingTestReply(t, req, w, nvkeImageBindingTestFixture(), nvkeWorkloadTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanImageBinding(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 0 {
				t.Fatal("malformed selection reached mutation")
			}
			if _, err := os.Stat(journal); !os.IsNotExist(err) {
				t.Fatal("malformed selection persisted journal")
			}
		})
	}
}

func TestNVKEImageBindingRejectsStaleWorkloadAndMalformedObservations(t *testing.T) {
	for _, name := range []string{"workload-version", "missing-count", "count", "issue-count", "digest", "bytes", "continuation", "state"} {
		t.Run(name, func(t *testing.T) {
			command, _ := nvkeImageBindingTestCommand(t, "review")
			row, workload := nvkeImageBindingTestFixture(), nvkeWorkloadTestFixture()
			switch name {
			case "workload-version":
				workload.Version++
			case "missing-count":
				row.ImageUseCount = nil
			case "count":
				*row.RewriteCount = 2
			case "issue-count":
				*row.WorkloadIssueCount = 1
			case "digest":
				row.BindingDigest = "invalid"
			case "bytes":
				row.VerifiedImageBytes = "0"
			case "continuation":
				*row.ContinuationAuthorized = true
			case "state":
				row.ExecutionState = "ready"
			}
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) { nvkeImageBindingTestReply(t, req, w, row, workload) })
			defer server.Close()
			if err := runNVKEAgentInstallPlanImageBinding(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review"); err == nil {
				t.Fatal("unbound observation trusted")
			}
		})
	}
}

func TestNVKEImageBindingPreparePublishesOnlyBoundedMetadata(t *testing.T) {
	command, out := nvkeImageBindingTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	if err := privatefile.Write(input, []byte(nvkeImageBindingTestBody())); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		nvkeImageBindingTestReply(t, req, w, nvkeImageBindingTestFixture(), nvkeWorkloadTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanImageBinding(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "private.example.test") || strings.Contains(out.String(), nvkeImageTestID) || !strings.Contains(out.String(), strings.Repeat("5", 64)) {
		t.Fatal("private or unbound result")
	}
}

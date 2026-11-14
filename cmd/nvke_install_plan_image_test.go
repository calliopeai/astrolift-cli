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

const nvkeImageTestID = "ae5f5fe0-8a48-435d-a60c-e51e6d81ac89"

func nvkeImageTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", "image", verb})
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	command.SetContext(context.Background())
	command.SetOut(out)
	command.SetErr(out)
	for flag, value := range map[string]string{
		"site": nvkeTestSite, "site-version": "7", "authority-version": "3", "version": "4",
		"plan": nvkePlanTestID, "plan-version": "2", "plan-digest": strings.Repeat("b", 64),
		"chart-artifact": nvkeChartTestID, "chart-artifact-version": "2", "chart-artifact-digest": strings.Repeat("c", 64),
		"tree": nvkeTreeTestID, "tree-version": "2", "tree-digest": strings.Repeat("d", 64),
		"artifact": nvkeImageTestID,
	} {
		if err = command.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	return command, out
}

func nvkeImageTestFixture() *api.NVKEInstallPlanImage {
	p := nvkeInstallPlanFixture()
	return &api.NVKEInstallPlanImage{ID: nvkeImageTestID, Version: 2, Status: "prepared", PlanID: p.ID, PlanVersion: p.Version, PlanDigest: p.Plan.PlanDigest,
		ChartArtifactID: nvkeChartTestID, ChartArtifactVersion: 2, ChartArtifactDigest: strings.Repeat("c", 64), TreeID: nvkeTreeTestID, TreeVersion: 2, TreeDigest: strings.Repeat("d", 64),
		OperationID: p.OperationID, OperationVersion: p.OperationVersion, SiteID: p.SiteID, SiteVersion: p.SiteVersion, AuthorityID: p.AuthorityID, AuthorityVersion: p.AuthorityVersion,
		ArtifactDigest: strings.Repeat("1", 64), SourceDigest: strings.Repeat("2", 64), CaptureDigest: strings.Repeat("3", 64), RootDigest: "sha256:" + strings.Repeat("4", 64), BlobCount: nvkeInstallPlanInt(6), Bytes: "67108864", ObservedAt: "2026-11-09T16:00:00+00:00", ExecutionState: "requires-workload-platform-credential-phase-use-and-linked-admission", ContinuationAuthorized: nvkeInstallPlanBool(false)}
}

func nvkeImageTestBody() string {
	return `{"format":1,"artifactId":"` + nvkeImageTestID + `","planId":"` + nvkePlanTestID + `","planDigest":"` + strings.Repeat("b", 64) + `","treeId":"` + nvkeTreeTestID + `","treeDigest":"` + strings.Repeat("d", 64) + `","reference":"private.example.test/owned:v1","policy":"reviewed","maximumBytes":"268435456","maximumCalls":512,"timeoutSeconds":300}`
}

func nvkeImageTestReply(t *testing.T, req gqlRequest, w http.ResponseWriter, value any) {
	t.Helper()
	data := nvkeInstallPlanTestData()
	data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
	data["reviewNvkeOperatorInstallPlanCharts"] = map[string]any{"ok": true, "data": nvkeChartTestFixture()}
	data["reviewNvkeOperatorInstallPlanTree"] = map[string]any{"ok": true, "data": nvkeTreeTestFixture()}
	data["astroliftNvkeOperatorInstallPlanItemPage"] = map[string]any{"ok": true, "data": nvkeInstallPlanTestItems()}
	for _, field := range []string{"prepareNvkeOperatorInstallPlanImage", "reviewNvkeOperatorInstallPlanImage", "recoverNvkeOperatorInstallPlanImagePreparation"} {
		if strings.Contains(req.Query, field+"(") {
			data[field] = map[string]any{"ok": true, "data": value}
		}
	}
	if strings.Contains(req.Query, "requestNvkeOperatorJob") {
		t.Error("image capture admitted native work")
	}
	writePipelineTestResponse(t, w, data)
}

func TestNVKEInstallPlanImageReviewAndRetryBindExactTree(t *testing.T) {
	for _, verb := range []string{"review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command, out := nvkeImageTestCommand(t, verb)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "InstallPlanImage(") || strings.Contains(req.Query, "InstallPlanImagePreparation(") {
					calls++
					input := req.Variables["input"].(map[string]any)
					if input["artifactId"] != nvkeImageTestID || input["treeId"] != nvkeTreeTestID || input["expectedTreeVersion"] != float64(2) || input["treeDigest"] != strings.Repeat("d", 64) || input["expectedActorId"] != float64(42) {
						t.Error("image source changed")
					}
					if _, private := input["requestText"]; private {
						t.Error("read/retry forwarded private text")
					}
				}
				nvkeImageTestReply(t, req, w, nvkeImageTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !strings.Contains(out.String(), strings.Repeat("3", 64)) {
				t.Fatal("missing or repeated image RPC")
			}
		})
	}
}

func TestNVKEInstallPlanImageJournalPrecedesPrivateMutationAndProtectsExactInput(t *testing.T) {
	command, _ := nvkeImageTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	body := nvkeImageTestBody()
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanImage(") {
			calls++
			data, err := privatefile.ReadFile(journal, 16384)
			var saved nvkeImageJournal
			if err != nil || bytes.Contains(data, []byte("private.example.test/owned:v1")) || decodeNVKEImageStrict(data, &saved) != nil || saved.Kind != "nvke-install-plan-image" || saved.TreeID != nvkeTreeTestID || saved.TreeVersion != 2 || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) {
				t.Error("missing, private or unbound recovery journal")
			}
			if req.Variables["input"].(map[string]any)["requestText"] != body {
				t.Error("exact private body lost precision")
			}
		}
		nvkeImageTestReply(t, req, w, nvkeImageTestFixture())
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKEAgentInstallPlanImage(command, context.Background(), client, nvkeTestOperation, "prepare"); err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Write(input, []byte(body+"\n")); err != nil {
		t.Fatal(err)
	}
	if err := runNVKEAgentInstallPlanImage(command, context.Background(), client, nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("changed exact input crossed journal")
	}
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	before, err := privatefile.ReadFile(journal, 16384)
	if err != nil {
		t.Fatal(err)
	}
	for field, value := range map[string]any{"treeVersion": 99, "treeDigest": strings.Repeat("f", 64), "item": "controller", "ordinal": 0, "actorId": 99, "chartArtifactVersion": 99, "server": "https://other.example"} {
		t.Run(field, func(t *testing.T) {
			var saved map[string]any
			if e := json.Unmarshal(before, &saved); e != nil {
				t.Fatal(e)
			}
			saved[field] = value
			data, e := json.Marshal(saved)
			if e != nil {
				t.Fatal(e)
			}
			if e = privatefile.Write(journal, data); e != nil {
				t.Fatal(e)
			}
			if e = runNVKEAgentInstallPlanImage(command, context.Background(), client, nvkeTestOperation, "prepare"); e == nil || calls != 1 {
				t.Fatal("changed journal crossed private capture")
			}
		})
	}
}

func TestNVKEInstallPlanImageMalformedBodyNeverPublishesJournalOrCapture(t *testing.T) {
	changes := map[string][2]string{
		"duplicate": {`"format":1`, `"format":1,"format":1`}, "alias": {`"artifactId"`, `"ArtifactId"`},
		"credentials": {`"format":1`, `"format":1,"credentials":"PRIVATE_INPUT"`}, "output-path": {`"format":1`, `"format":1,"destination":"PRIVATE_INPUT"`},
		"foreign-tree": {nvkeTreeTestID, nvkeTestOrg}, "foreign-artifact": {nvkeImageTestID, nvkeTestOrg},
		"http-reference": {`private.example.test/owned:v1`, `https://private.example.test/owned:v1`}, "policy-path": {`"policy":"reviewed"`, `"policy":"../private"`},
		"missing-reference": {`,"reference":"private.example.test/owned:v1"`, ``},
		"numeric-budget":    {`"268435456"`, `268435456`}, "zero-budget": {`"268435456"`, `"0"`}, "overflow-budget": {`"268435456"`, `"9223372036854775808"`}, "leading-zero": {`"268435456"`, `"01"`},
		"null-calls": {`"maximumCalls":512`, `"maximumCalls":null`}, "large-calls": {`"maximumCalls":512`, `"maximumCalls":4097`}, "bool-calls": {`"maximumCalls":512`, `"maximumCalls":true`}, "large-timeout": {`"timeoutSeconds":300`, `"timeoutSeconds":301`},
	}

	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			command, out := nvkeImageTestCommand(t, "prepare")
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
			if err := privatefile.Write(input, []byte(strings.Replace(nvkeImageTestBody(), change[0], change[1], 1))); err != nil {
				t.Fatal(err)
			}
			_ = command.Flags().Set("file", input)
			_ = command.Flags().Set("request-file", journal)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanImage(") {
					calls++
				}
				nvkeImageTestReply(t, req, w, nvkeImageTestFixture())
			})
			defer server.Close()
			err := runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare")
			if err == nil || calls != 0 || out.Len() != 0 || strings.Contains(err.Error(), "PRIVATE_INPUT") {
				t.Fatal("malformed image crossed capture")
			}
			if _, e := os.Stat(journal); !os.IsNotExist(e) {
				t.Fatal("malformed input published a recovery journal")
			}
		})
	}
}

func TestNVKEInstallPlanImageRejectsUnboundTruncatedOrAuthorizingReplies(t *testing.T) {
	changes := map[string]any{"id": nvkeTestOrg, "planVersion": 99, "chartArtifactVersion": 99, "treeId": nvkeTestOrg, "treeVersion": 99, "treeDigest": strings.Repeat("f", 64), "artifactDigest": "PRIVATE_INPUT", "sourceDigest": nil, "captureDigest": "PRIVATE_INPUT", "rootDigest": "sha256:short", "blobCount": -1, "bytes": "0", "observedAt": "2026-11-09T00:00:00", "continuationAuthorized": true, "executionState": "ready", "status": "queued"}

	for field, value := range changes {
		t.Run(field, func(t *testing.T) {
			command, out := nvkeImageTestCommand(t, "review")
			row := nvkeInstallPlanTestJSON(t, nvkeImageTestFixture())
			row[field] = value
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) { nvkeImageTestReply(t, req, w, row) })
			defer server.Close()
			err := runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review")
			if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "PRIVATE_INPUT") {
				t.Fatal("unbound image reply was exposed")
			}
		})
	}
}

func TestNVKEInstallPlanImageLostReplyPreservesJournalAndNeverAutomaticallyReplays(t *testing.T) {
	command, _ := nvkeImageTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	if err := privatefile.Write(input, []byte(nvkeImageTestBody())); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanImage(") {
			calls++
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("PRIVATE_PEER_ERROR"))
			return
		}
		nvkeImageTestReply(t, req, w, nvkeImageTestFixture())
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKEAgentInstallPlanImage(command, context.Background(), client, nvkeTestOperation, "prepare"); err == nil || strings.Contains(err.Error(), "PRIVATE_PEER_ERROR") {
		t.Fatal("lost capture reply was accepted or leaked")
	}
	review, _ := nvkeImageTestCommand(t, "review")
	if err := runNVKEAgentInstallPlanImage(review, context.Background(), client, nvkeTestOperation, "review"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("lost capture reply caused replay")
	}
	if _, err := privatefile.ReadFile(journal, 16384); err != nil {
		t.Fatal("capture journal was discarded")
	}
}

func TestNVKEInstallPlanImageStaleTreeRefusesBeforeImageRPC(t *testing.T) {
	for field, value := range map[string]string{"tree-version": "99", "tree-digest": strings.Repeat("f", 64)} {
		t.Run(field, func(t *testing.T) {
			command, _ := nvkeImageTestCommand(t, "review")
			_ = command.Flags().Set(field, value)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanImage(") {
					calls++
				}
				nvkeImageTestReply(t, req, w, nvkeImageTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review"); err == nil || calls != 0 {
				t.Fatal("stale tree crossed image RPC")
			}
		})
	}
}

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

const nvkeContextTestID = "590ad665-658d-43da-9614-09e01fbc4a01"

func nvkeContextTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", "context", verb})
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
		"item": "nktp", "ordinal": "1", "artifact": nvkeContextTestID,
	} {
		if err = command.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	return command, out
}

func nvkeContextTestFixture() *api.NVKEInstallPlanContext {
	p := nvkeInstallPlanFixture()
	return &api.NVKEInstallPlanContext{
		ID: nvkeContextTestID, Version: 2, Status: "prepared", PlanID: p.ID, PlanVersion: p.Version, PlanDigest: p.Plan.PlanDigest,
		ChartArtifactID: nvkeChartTestID, ChartArtifactVersion: 2, ChartArtifactDigest: strings.Repeat("c", 64),
		TreeID: nvkeTreeTestID, TreeVersion: 2, TreeDigest: strings.Repeat("d", 64),
		OperationID: p.OperationID, OperationVersion: p.OperationVersion, SiteID: p.SiteID, SiteVersion: p.SiteVersion,
		AuthorityID: p.AuthorityID, AuthorityVersion: p.AuthorityVersion, Item: "nktp", Ordinal: nvkeInstallPlanInt(1),
		CaptureDigest: strings.Repeat("1", 64), FrozenChartSHA256: strings.Repeat("2", 64), ContextDigest: strings.Repeat("3", 64),
		DocumentCount: nvkeInstallPlanInt(1), LookupCount: nvkeInstallPlanInt(0), ObservedAt: "2026-11-09T16:00:00+00:00",
		ExecutionState: "requires-authenticated-phase-verification-images-and-linked-admission", ContinuationAuthorized: nvkeInstallPlanBool(false),
	}
}

func nvkeContextTestBody() string {
	return `{"format":1,"artifactId":"` + nvkeContextTestID + `","planId":"` + nvkePlanTestID + `","planDigest":"` + strings.Repeat("b", 64) + `","treeId":"` + nvkeTreeTestID + `","treeDigest":"` + strings.Repeat("d", 64) + `","item":"nktp","revision":"9007199254740993","upgrade":true,"maximumBytes":"16777216","maximumArtifactBytes":"1048576","maximumCalls":512,"maximumIterations":64,"timeoutSeconds":300}`
}

func nvkeContextTestReply(t *testing.T, req gqlRequest, w http.ResponseWriter, value any) {
	t.Helper()
	data := nvkeInstallPlanTestData()
	data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
	data["reviewNvkeOperatorInstallPlanCharts"] = map[string]any{"ok": true, "data": nvkeChartTestFixture()}
	data["reviewNvkeOperatorInstallPlanTree"] = map[string]any{"ok": true, "data": nvkeTreeTestFixture()}
	data["astroliftNvkeOperatorInstallPlanItemPage"] = map[string]any{"ok": true, "data": nvkeInstallPlanTestItems()}
	for _, field := range []string{"prepareNvkeOperatorInstallPlanContext", "reviewNvkeOperatorInstallPlanContext", "recoverNvkeOperatorInstallPlanContextPreparation"} {
		if strings.Contains(req.Query, field+"(") {
			data[field] = map[string]any{"ok": true, "data": value}
		}
	}
	if strings.Contains(req.Query, "requestNvkeOperatorJob") {
		t.Error("context capture admitted native work")
	}
	writePipelineTestResponse(t, w, data)
}

func TestNVKEInstallPlanContextReviewAndRetryBindExactTreeAndItem(t *testing.T) {
	for _, verb := range []string{"review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command, out := nvkeContextTestCommand(t, verb)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "InstallPlanContext(") || strings.Contains(req.Query, "InstallPlanContextPreparation(") {
					calls++
					input := req.Variables["input"].(map[string]any)
					if input["artifactId"] != nvkeContextTestID || input["treeId"] != nvkeTreeTestID || input["expectedTreeVersion"] != float64(2) || input["treeDigest"] != strings.Repeat("d", 64) || input["item"] != "nktp" || input["ordinal"] != float64(1) || input["expectedActorId"] != float64(42) {
						t.Error("context source changed")
					}
					if _, private := input["requestText"]; private {
						t.Error("read/retry forwarded private text")
					}
				}
				nvkeContextTestReply(t, req, w, nvkeContextTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanContext(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !strings.Contains(out.String(), strings.Repeat("3", 64)) {
				t.Fatal("missing or repeated context RPC")
			}
		})
	}
}

func TestNVKEInstallPlanContextJournalPrecedesPrivateMutationAndProtectsExactInput(t *testing.T) {
	command, _ := nvkeContextTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	body := nvkeContextTestBody()
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanContext(") {
			calls++
			data, err := privatefile.ReadFile(journal, 16384)
			var saved nvkeContextJournal
			if err != nil || bytes.Contains(data, []byte("9007199254740993")) || decodeNVKEContextStrict(data, &saved) != nil || saved.Kind != "nvke-install-plan-context" || saved.TreeID != nvkeTreeTestID || saved.TreeVersion != 2 || saved.Item != "nktp" || saved.Ordinal != 1 || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) {
				t.Error("missing, private or unbound recovery journal")
			}
			if req.Variables["input"].(map[string]any)["requestText"] != body {
				t.Error("exact private body lost precision")
			}
		}
		nvkeContextTestReply(t, req, w, nvkeContextTestFixture())
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKEAgentInstallPlanContext(command, context.Background(), client, nvkeTestOperation, "prepare"); err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Write(input, []byte(body+"\n")); err != nil {
		t.Fatal(err)
	}
	if err := runNVKEAgentInstallPlanContext(command, context.Background(), client, nvkeTestOperation, "prepare"); err == nil || calls != 1 {
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
			if e = runNVKEAgentInstallPlanContext(command, context.Background(), client, nvkeTestOperation, "prepare"); e == nil || calls != 1 {
				t.Fatal("changed journal crossed private capture")
			}
		})
	}
}

func TestNVKEInstallPlanContextMalformedBodyNeverPublishesJournalOrCapture(t *testing.T) {
	changes := map[string][2]string{
		"duplicate": {`"format":1`, `"format":1,"format":1`}, "alias": {`"artifactId"`, `"ArtifactId"`},
		"profile":          {`"format":1`, `"format":1,"helperProgram":"PRIVATE_INPUT"`},
		"numeric-revision": {`"9007199254740993"`, `9007199254740993`}, "overflow-revision": {`"9007199254740993"`, `"9223372036854775808"`},
		"zero-revision": {`"9007199254740993"`, `"0"`}, "leading-zero": {`"9007199254740993"`, `"01"`},
		"install-revision": {`"upgrade":true`, `"upgrade":false`}, "null-mode": {`"upgrade":true`, `"upgrade":null`},
		"missing-mode": {`,"upgrade":true`, ``}, "null-calls": {`"maximumCalls":512`, `"maximumCalls":null`},
		"large-calls": {`"maximumCalls":512`, `"maximumCalls":2049`}, "bool-calls": {`"maximumCalls":512`, `"maximumCalls":true`},
		"large-iterations": {`"maximumIterations":64`, `"maximumIterations":65`}, "large-timeout": {`"timeoutSeconds":300`, `"timeoutSeconds":301`},
		"numeric-budget": {`"16777216"`, `16777216`}, "zero-budget": {`"16777216"`, `"0"`}, "large-budget": {`"16777216"`, `"536870913"`},
		"small-artifact": {`"1048576"`, `"65535"`}, "large-artifact": {`"1048576"`, `"134217729"`},
		"foreign-tree": {nvkeTreeTestID, nvkeTestOrg}, "foreign-item": {`"item":"nktp"`, `"item":"controller"`},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			command, out := nvkeContextTestCommand(t, "prepare")
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
			if err := privatefile.Write(input, []byte(strings.Replace(nvkeContextTestBody(), change[0], change[1], 1))); err != nil {
				t.Fatal(err)
			}
			_ = command.Flags().Set("file", input)
			_ = command.Flags().Set("request-file", journal)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanContext(") {
					calls++
				}
				nvkeContextTestReply(t, req, w, nvkeContextTestFixture())
			})
			defer server.Close()
			err := runNVKEAgentInstallPlanContext(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare")
			if err == nil || calls != 0 || out.Len() != 0 || strings.Contains(err.Error(), "PRIVATE_INPUT") {
				t.Fatal("malformed context crossed capture")
			}
			if _, e := os.Stat(journal); !os.IsNotExist(e) {
				t.Fatal("malformed input published a recovery journal")
			}
		})
	}
}

func TestNVKEInstallPlanContextRejectsUnboundTruncatedOrAuthorizingReplies(t *testing.T) {
	changes := map[string]any{"id": nvkeTestOrg, "planVersion": 99, "chartArtifactVersion": 99, "treeId": nvkeTestOrg, "treeVersion": 99, "treeDigest": strings.Repeat("f", 64), "item": "controller", "ordinal": 0, "captureDigest": "PRIVATE_INPUT", "frozenChartSha256": "PRIVATE_INPUT", "contextDigest": nil, "documentCount": -1, "lookupCount": 2049, "observedAt": "2026-11-09T00:00:00", "continuationAuthorized": true, "executionState": "ready", "status": "queued"}
	for field, value := range changes {
		t.Run(field, func(t *testing.T) {
			command, out := nvkeContextTestCommand(t, "review")
			row := nvkeInstallPlanTestJSON(t, nvkeContextTestFixture())
			row[field] = value
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) { nvkeContextTestReply(t, req, w, row) })
			defer server.Close()
			err := runNVKEAgentInstallPlanContext(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review")
			if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "PRIVATE_INPUT") {
				t.Fatal("unbound context reply was exposed")
			}
		})
	}
}

func TestNVKEInstallPlanContextLostReplyPreservesJournalAndNeverAutomaticallyReplays(t *testing.T) {
	command, _ := nvkeContextTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	if err := privatefile.Write(input, []byte(nvkeContextTestBody())); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanContext(") {
			calls++
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("PRIVATE_PEER_ERROR"))
			return
		}
		nvkeContextTestReply(t, req, w, nvkeContextTestFixture())
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKEAgentInstallPlanContext(command, context.Background(), client, nvkeTestOperation, "prepare"); err == nil || strings.Contains(err.Error(), "PRIVATE_PEER_ERROR") {
		t.Fatal("lost capture reply was accepted or leaked")
	}
	review, _ := nvkeContextTestCommand(t, "review")
	if err := runNVKEAgentInstallPlanContext(review, context.Background(), client, nvkeTestOperation, "review"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("lost capture reply caused replay")
	}
	if _, err := privatefile.ReadFile(journal, 16384); err != nil {
		t.Fatal("capture journal was discarded")
	}
}

func TestNVKEInstallPlanContextStaleTreeOrItemRefusesBeforeContextRPC(t *testing.T) {
	for field, value := range map[string]string{"tree-version": "99", "tree-digest": strings.Repeat("f", 64), "item": "missing", "ordinal": "0"} {
		t.Run(field, func(t *testing.T) {
			command, _ := nvkeContextTestCommand(t, "review")
			_ = command.Flags().Set(field, value)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanContext(") {
					calls++
				}
				nvkeContextTestReply(t, req, w, nvkeContextTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanContext(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review"); err == nil || calls != 0 {
				t.Fatal("stale tree or item crossed context RPC")
			}
		})
	}
}

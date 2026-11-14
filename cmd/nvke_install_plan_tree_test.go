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

const nvkeTreeTestID = "87a76c80-3468-43aa-b2be-e511d587d312"

func nvkeTreeTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", "tree", verb})
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	command.SetContext(context.Background())
	command.SetOut(out)
	command.SetErr(out)
	for flag, value := range map[string]string{"site": nvkeTestSite, "site-version": "7", "authority-version": "3", "version": "4", "plan": nvkePlanTestID, "plan-version": "2", "plan-digest": strings.Repeat("b", 64), "artifact": nvkeTreeTestID, "chart-artifact": nvkeChartTestID, "chart-artifact-version": "2", "chart-artifact-digest": strings.Repeat("c", 64)} {
		if e := command.Flags().Set(flag, value); e != nil {
			t.Fatal(e)
		}
	}
	return command, out
}

func nvkeTreeTestFixture() *api.NVKEInstallPlanTree {
	p := nvkeInstallPlanFixture()
	return &api.NVKEInstallPlanTree{ID: nvkeTreeTestID, Version: 2, Status: "prepared", PlanID: p.ID, PlanVersion: p.Version, PlanDigest: p.Plan.PlanDigest, ChartArtifactID: nvkeChartTestID, ChartArtifactVersion: 2, ChartArtifactDigest: strings.Repeat("c", 64), OperationID: p.OperationID, OperationVersion: p.OperationVersion, SiteID: p.SiteID, SiteVersion: p.SiteVersion, AuthorityID: p.AuthorityID, AuthorityVersion: p.AuthorityVersion, TreeDigest: strings.Repeat("d", 64), FileCount: nvkeInstallPlanInt(3), Bytes: "9007199254740994", ExecutionState: "requires-native-rendering-images-and-live-verification", ContinuationAuthorized: nvkeInstallPlanBool(false)}
}

func nvkeTreeTestBody() string {
	return `{"format":1,"artifactId":"` + nvkeTreeTestID + `","planId":"` + nvkePlanTestID + `","planDigest":"` + strings.Repeat("b", 64) + `","chartArtifactId":"` + nvkeChartTestID + `","chartArtifactDigest":"` + strings.Repeat("c", 64) + `","maximumBytes":"9007199254740994"}`
}

func nvkeTreeTestReply(t *testing.T, req gqlRequest, w http.ResponseWriter, value any) {
	t.Helper()
	data := nvkeInstallPlanTestData()
	data["reviewNvkeOperatorInstallPlanCharts"] = map[string]any{"ok": true, "data": nvkeChartTestFixture()}
	data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
	for _, field := range []string{"prepareNvkeOperatorInstallPlanTree", "reviewNvkeOperatorInstallPlanTree", "recoverNvkeOperatorInstallPlanTreePreparation"} {
		if strings.Contains(req.Query, field+"(") {
			data[field] = map[string]any{"ok": true, "data": value}
		}
	}
	if strings.Contains(req.Query, "requestNvkeOperatorJob") {
		t.Error("tree stage admitted native work")
	}
	writePipelineTestResponse(t, w, data)
}

func TestNVKEInstallPlanTreeReviewAndRetryBindExactSourceWithoutPrivateInput(t *testing.T) {
	for _, verb := range []string{"review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command, out := nvkeTreeTestCommand(t, verb)
			chartCalls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "InstallPlanTree(") || strings.Contains(req.Query, "InstallPlanTreePreparation(") {
					chartCalls++
					input := req.Variables["input"].(map[string]any)
					if input["artifactId"] != nvkeTreeTestID || input["planId"] != nvkePlanTestID || input["expectedPlanVersion"] != float64(2) || input["planDigest"] != strings.Repeat("b", 64) || input["expectedActorId"] != float64(42) || input["chartArtifactId"] != nvkeChartTestID || input["expectedChartArtifactVersion"] != float64(2) || input["chartArtifactDigest"] != strings.Repeat("c", 64) {
						t.Error("chart source changed")
					}
					if _, private := input["requestText"]; private {
						t.Error("read/retry forwarded private text")
					}
				}
				nvkeTreeTestReply(t, req, w, nvkeTreeTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanTree(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if chartCalls != 1 || !strings.Contains(out.String(), "9007199254740994") {
				t.Fatal("chart result lost precision or repeated its RPC")
			}
		})
	}
}

func TestNVKEInstallPlanTreeJournalPrecedesPrivateMutationAndProtectsExactText(t *testing.T) {
	command, _ := nvkeTreeTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "recovery.json")
	body := nvkeTreeTestBody()
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanTree(") {
			calls++
			data, err := privatefile.ReadFile(journal, 16384)
			if err != nil || bytes.Contains(data, []byte("maximumBytes")) {
				t.Error("metadata journal missing or contains raw private input")
			}
			var saved nvkeTreeJournal
			if decodeNVKETreeStrict(data, &saved) != nil || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) || saved.ArtifactID != nvkeTreeTestID || saved.PlanVersion != 2 {
				t.Error("journal source/input is not durable")
			}
			if req.Variables["input"].(map[string]any)["requestText"] != body {
				t.Error("exact private text changed")
			}
		}
		nvkeTreeTestReply(t, req, w, nvkeTreeTestFixture())
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKEAgentInstallPlanTree(command, context.Background(), client, nvkeTestOperation, "prepare"); err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Write(input, []byte(body+"\n")); err != nil {
		t.Fatal(err)
	}
	if err := runNVKEAgentInstallPlanTree(command, context.Background(), client, nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("changed exact text crossed the artifact journal")
	}
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	original, err := privatefile.ReadFile(journal, 16384)
	if err != nil {
		t.Fatal(err)
	}
	for field, changed := range map[string]any{"server": "https://different.example", "organizationId": nvkeTestSite, "actorId": 99, "planVersion": 99, "chartArtifactVersion": 99, "chartArtifactId": nvkeTestOrg, "chartArtifactDigest": strings.Repeat("f", 64), "planDigest": strings.Repeat("f", 64), "artifactId": nvkeTestOrg} {
		t.Run(field, func(t *testing.T) {
			var metadata map[string]any
			if e := json.Unmarshal(original, &metadata); e != nil {
				t.Fatal(e)
			}
			metadata[field] = changed
			data, e := json.Marshal(metadata)
			if e != nil {
				t.Fatal(e)
			}
			if e = privatefile.Write(journal, data); e != nil {
				t.Fatal(e)
			}
			if e = runNVKEAgentInstallPlanTree(command, context.Background(), client, nvkeTestOperation, "prepare"); e == nil || calls != 1 {
				t.Fatal("changed chart journal crossed private mutation")
			}
		})
	}
}

func TestNVKEInstallPlanTreeMalformedInputCannotPublishJournalOrPrepare(t *testing.T) {
	for _, change := range []string{"duplicate", "case-alias", "unknown", "null-http", "null-timeout", "bool-timeout", "large-timeout", "overflow", "numeric-budget", "zero-budget", "wrong-artifact", "wrong-digest"} {
		t.Run(change, func(t *testing.T) {
			command, out := nvkeTreeTestCommand(t, "prepare")
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "recovery.json")
			body := nvkeTreeTestBody()
			switch change {
			case "duplicate":
				body = strings.Replace(body, `"format":1`, `"format":1,"format":1`, 1)
			case "case-alias":
				body = strings.Replace(body, `"artifactId"`, `"ArtifactId"`, 1)
			case "unknown":
				body = strings.Replace(body, `"format":1`, `"format":1,"private":"PRIVATE_INPUT"`, 1)
			case "null-http":
				body = strings.Replace(body, `"format":1`, `"format":1,"allowPlainHttp":null`, 1)
			case "null-timeout":
				body = strings.Replace(body, `"format":1`, `"format":1,"timeoutSeconds":null`, 1)
			case "bool-timeout":
				body = strings.Replace(body, `"format":1`, `"format":1,"timeoutSeconds":true`, 1)
			case "large-timeout":
				body = strings.Replace(body, `"format":1`, `"format":1,"timeoutSeconds":3601`, 1)
			case "overflow":
				body = strings.Replace(body, "9007199254740994", "9223372036854775808", 1)
			case "numeric-budget":
				body = strings.Replace(body, `"9007199254740994"`, `9007199254740994`, 1)
			case "zero-budget":
				body = strings.Replace(body, `"9007199254740994"`, `"0"`, 1)
			case "wrong-artifact":
				body = strings.Replace(body, nvkeTreeTestID, nvkeTestOrg, 1)
			case "wrong-digest":
				body = strings.Replace(body, strings.Repeat("b", 64), strings.Repeat("f", 64), 1)
			}
			if err := privatefile.Write(input, []byte(body)); err != nil {
				t.Fatal(err)
			}
			_ = command.Flags().Set("file", input)
			_ = command.Flags().Set("request-file", journal)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanTree(") {
					calls++
				}
				nvkeTreeTestReply(t, req, w, nvkeTreeTestFixture())
			})
			defer server.Close()
			err := runNVKEAgentInstallPlanTree(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare")
			if err == nil || calls != 0 || strings.Contains(err.Error(), "PRIVATE_INPUT") || out.Len() != 0 {
				t.Fatalf("unsafe chart input %s", change)
			}
			if _, err = os.Stat(journal); !os.IsNotExist(err) {
				t.Fatal("invalid input published a journal")
			}
		})
	}
}

func TestNVKEInstallPlanTreeRefusesForeignTruncatedOrAuthorizingResults(t *testing.T) {
	for _, change := range []string{"id", "plan", "plan-version", "plan-digest", "operation", "operation-version", "site", "site-version", "authority", "authority-version", "status", "digest", "chart", "chart-version", "chart-digest", "count", "missing-count", "missing-continuation", "continuation", "state", "numeric-bytes", "overflow", "zero-size", "refused"} {
		t.Run(change, func(t *testing.T) {
			command, out := nvkeTreeTestCommand(t, "review")
			value := nvkeInstallPlanTestJSON(t, nvkeTreeTestFixture())
			switch change {
			case "id":
				value["id"] = nvkeTestOrg
			case "plan":
				value["planId"] = nvkeTestOrg
			case "plan-version":
				value["planVersion"] = 99
			case "plan-digest":
				value["planDigest"] = strings.Repeat("f", 64)
			case "operation":
				value["operationId"] = nvkeTestOrg
			case "operation-version":
				value["operationVersion"] = 99
			case "site":
				value["siteId"] = nvkeTestOrg
			case "site-version":
				value["siteVersion"] = 99
			case "authority":
				value["authorityId"] = nvkeTestOrg
			case "authority-version":
				value["authorityVersion"] = 99
			case "status":
				value["status"] = "ready"
			case "digest":
				value["treeDigest"] = "PRIVATE_INPUT"
			case "chart":
				value["chartArtifactId"] = nvkeTestOrg
			case "chart-version":
				value["chartArtifactVersion"] = 99
			case "chart-digest":
				value["chartArtifactDigest"] = strings.Repeat("f", 64)
			case "count":
				value["fileCount"] = 100001
			case "missing-count":
				delete(value, "fileCount")
			case "missing-continuation":
				delete(value, "continuationAuthorized")
			case "continuation":
				value["continuationAuthorized"] = true
			case "state":
				value["executionState"] = "ready"
			case "numeric-bytes":
				value["bytes"] = 9007199254740993
			case "overflow":
				value["bytes"] = "9223372036854775808"
			case "zero-size":
				value["bytes"] = "0"
			}
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if change == "refused" && strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanTree(") {
					writePipelineTestResponse(t, w, map[string]any{"reviewNvkeOperatorInstallPlanTree": map[string]any{"ok": false, "errors": []map[string]any{{"code": "OPERATOR_CHARTS_INCOMPLETE", "message": "PRIVATE_INPUT"}}}})
					return
				}
				nvkeTreeTestReply(t, req, w, value)
			})
			defer server.Close()
			err := runNVKEAgentInstallPlanTree(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review")
			if err == nil || strings.Contains(err.Error(), "PRIVATE_INPUT") || out.Len() != 0 {
				t.Fatalf("unsafe chart reply %s", change)
			}
		})
	}
}

func TestNVKEInstallPlanTreeLostReplyDoesNotRetryAndReviewUsesRetainedArtifact(t *testing.T) {
	prepare, _ := nvkeTreeTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "recovery.json")
	if err := privatefile.Write(input, []byte(nvkeTreeTestBody())); err != nil {
		t.Fatal(err)
	}
	_ = prepare.Flags().Set("file", input)
	_ = prepare.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanTree(") {
			calls++
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("PRIVATE_PEER_ERROR"))
			return
		}
		nvkeTreeTestReply(t, req, w, nvkeTreeTestFixture())
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKEAgentInstallPlanTree(prepare, context.Background(), client, nvkeTestOperation, "prepare"); err == nil || strings.Contains(err.Error(), "PRIVATE_PEER_ERROR") {
		t.Fatal("lost chart reply was accepted or exposed")
	}
	review, _ := nvkeTreeTestCommand(t, "review")
	if err := runNVKEAgentInstallPlanTree(review, context.Background(), client, nvkeTestOperation, "review"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("lost chart reply replayed preparation")
	}
	if _, err := privatefile.ReadFile(journal, 16384); err != nil {
		t.Fatal("lost chart reply discarded recovery identity")
	}
}

func TestNVKEInstallPlanTreeStalePlanRefusedBeforeChartRPC(t *testing.T) {
	command, _ := nvkeTreeTestCommand(t, "review")
	_ = command.Flags().Set("plan-version", "99")
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanTree(") {
			calls++
		}
		nvkeTreeTestReply(t, req, w, nvkeTreeTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanTree(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review"); err == nil || calls != 0 {
		t.Fatal("stale parent crossed chart RPC")
	}
}

func TestNVKEInstallPlanTreePublicationBounds(t *testing.T) {
	for _, change := range []string{"budget", "under-chart-bytes", "under-chart-count", "empty"} {
		t.Run(change, func(t *testing.T) {
			row := nvkeTreeTestFixture()
			pub := nvkeChartTestFixture()
			var budget int64
			switch change {
			case "budget":
				budget = 1
			case "under-chart-bytes":
				row.Bytes = "1"
			case "under-chart-count":
				pub.ChartCount = nvkeInstallPlanInt(4)
			case "empty":
				row.FileCount = nvkeInstallPlanInt(0)
			}
			if validateNVKEInstallPlanTree(row, nvkeInstallPlanFixture(), pub, nvkeTreeTestID, budget) == nil {
				t.Fatal("accepted unbounded or truncated execution tree")
			}
		})
	}
}

func TestNVKEInstallPlanTreeStaleChartRefusedBeforeTreeRPC(t *testing.T) {
	for _, field := range []string{"chart-artifact-version", "chart-artifact-digest"} {
		t.Run(field, func(t *testing.T) {
			command, _ := nvkeTreeTestCommand(t, "review")
			value := "99"
			if field == "chart-artifact-digest" {
				value = strings.Repeat("f", 64)
			}
			_ = command.Flags().Set(field, value)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanTree(") {
					calls++
				}
				nvkeTreeTestReply(t, req, w, nvkeTreeTestFixture())
			})
			defer server.Close()
			if runNVKEAgentInstallPlanTree(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review") == nil || calls != 0 {
				t.Fatal("stale chart source crossed tree RPC")
			}
		})
	}
}

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

const nvkeChartTestID = "e7ee5a93-bc3c-4238-9db5-930a082c4d4d"

func nvkeChartTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", "charts", verb})
	if err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	command.SetContext(context.Background())
	command.SetOut(out)
	command.SetErr(out)
	for flag, value := range map[string]string{"site": nvkeTestSite, "site-version": "7", "authority-version": "3", "version": "4", "plan": nvkePlanTestID, "plan-version": "2", "plan-digest": strings.Repeat("b", 64), "artifact": nvkeChartTestID} {
		if e := command.Flags().Set(flag, value); e != nil {
			t.Fatal(e)
		}
	}
	return command, out
}

func nvkeChartTestFixture() *api.NVKEInstallPlanCharts {
	p := nvkeInstallPlanFixture()
	return &api.NVKEInstallPlanCharts{ID: nvkeChartTestID, Version: 2, Status: "prepared", PlanID: p.ID, PlanVersion: p.Version, PlanDigest: p.Plan.PlanDigest, OperationID: p.OperationID, OperationVersion: p.OperationVersion, SiteID: p.SiteID, SiteVersion: p.SiteVersion, AuthorityID: p.AuthorityID, AuthorityVersion: p.AuthorityVersion, ArtifactDigest: strings.Repeat("c", 64), ChartCount: nvkeInstallPlanInt(1), Bytes: "9007199254740993", ExpandedBytes: "9223372036854775807", ExecutionState: "requires-rendering-images-and-live-verification", ContinuationAuthorized: nvkeInstallPlanBool(false)}
}

func nvkeChartTestBody() string {
	return `{"format":1,"artifactId":"` + nvkeChartTestID + `","planId":"` + nvkePlanTestID + `","planDigest":"` + strings.Repeat("b", 64) + `","maximumBytes":"9007199254740993","maximumExpandedBytes":"9223372036854775807"}`
}

func nvkeChartTestReply(t *testing.T, req gqlRequest, w http.ResponseWriter, value any) {
	t.Helper()
	data := nvkeInstallPlanTestData()
	data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
	for _, field := range []string{"prepareNvkeOperatorInstallPlanCharts", "reviewNvkeOperatorInstallPlanCharts", "recoverNvkeOperatorInstallPlanChartsPreparation"} {
		if strings.Contains(req.Query, field+"(") {
			data[field] = map[string]any{"ok": true, "data": value}
		}
	}
	if strings.Contains(req.Query, "requestNvkeOperatorJob") {
		t.Error("chart stage admitted native work")
	}
	writePipelineTestResponse(t, w, data)
}

func TestNVKEInstallPlanChartsReviewAndRetryBindExactSourceWithoutPrivateInput(t *testing.T) {
	for _, verb := range []string{"review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command, out := nvkeChartTestCommand(t, verb)
			chartCalls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "InstallPlanCharts(") || strings.Contains(req.Query, "InstallPlanChartsPreparation(") {
					chartCalls++
					input := req.Variables["input"].(map[string]any)
					if input["artifactId"] != nvkeChartTestID || input["planId"] != nvkePlanTestID || input["expectedPlanVersion"] != float64(2) || input["planDigest"] != strings.Repeat("b", 64) || input["expectedActorId"] != float64(42) {
						t.Error("chart source changed")
					}
					if _, private := input["requestText"]; private {
						t.Error("read/retry forwarded private text")
					}
				}
				nvkeChartTestReply(t, req, w, nvkeChartTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanCharts(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if chartCalls != 1 || !strings.Contains(out.String(), "9007199254740993") || !strings.Contains(out.String(), "9223372036854775807") {
				t.Fatal("chart result lost precision or repeated its RPC")
			}
		})
	}
}

func TestNVKEInstallPlanChartsJournalPrecedesPrivateMutationAndProtectsExactText(t *testing.T) {
	command, _ := nvkeChartTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "recovery.json")
	body := nvkeChartTestBody()
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanCharts(") {
			calls++
			data, err := privatefile.ReadFile(journal, 16384)
			if err != nil || bytes.Contains(data, []byte("maximumBytes")) {
				t.Error("metadata journal missing or contains raw private input")
			}
			var saved nvkeChartJournal
			if decodeNVKEChartStrict(data, &saved) != nil || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) || saved.ArtifactID != nvkeChartTestID || saved.PlanVersion != 2 {
				t.Error("journal source/input is not durable")
			}
			if req.Variables["input"].(map[string]any)["requestText"] != body {
				t.Error("exact private text changed")
			}
		}
		nvkeChartTestReply(t, req, w, nvkeChartTestFixture())
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKEAgentInstallPlanCharts(command, context.Background(), client, nvkeTestOperation, "prepare"); err != nil {
		t.Fatal(err)
	}
	if err := privatefile.Write(input, []byte(body+"\n")); err != nil {
		t.Fatal(err)
	}
	if err := runNVKEAgentInstallPlanCharts(command, context.Background(), client, nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("changed exact text crossed the artifact journal")
	}
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	original, err := privatefile.ReadFile(journal, 16384)
	if err != nil {
		t.Fatal(err)
	}
	for field, changed := range map[string]any{"server": "https://different.example", "organizationId": nvkeTestSite, "actorId": 99, "planVersion": 99, "planDigest": strings.Repeat("f", 64), "artifactId": nvkeTestOrg} {
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
			if e = runNVKEAgentInstallPlanCharts(command, context.Background(), client, nvkeTestOperation, "prepare"); e == nil || calls != 1 {
				t.Fatal("changed chart journal crossed private mutation")
			}
		})
	}
}

func TestNVKEInstallPlanChartsMalformedInputCannotPublishJournalOrPrepare(t *testing.T) {
	for _, change := range []string{"duplicate", "case-alias", "unknown", "null-http", "null-timeout", "bool-timeout", "large-timeout", "overflow", "numeric-budget", "zero-budget", "wrong-artifact", "wrong-digest"} {
		t.Run(change, func(t *testing.T) {
			command, out := nvkeChartTestCommand(t, "prepare")
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "recovery.json")
			body := nvkeChartTestBody()
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
				body = strings.Replace(body, "9223372036854775807", "9223372036854775808", 1)
			case "numeric-budget":
				body = strings.Replace(body, `"9007199254740993"`, `9007199254740993`, 1)
			case "zero-budget":
				body = strings.Replace(body, `"9007199254740993"`, `"0"`, 1)
			case "wrong-artifact":
				body = strings.Replace(body, nvkeChartTestID, nvkeTestOrg, 1)
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
				if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanCharts(") {
					calls++
				}
				nvkeChartTestReply(t, req, w, nvkeChartTestFixture())
			})
			defer server.Close()
			err := runNVKEAgentInstallPlanCharts(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare")
			if err == nil || calls != 0 || strings.Contains(err.Error(), "PRIVATE_INPUT") || out.Len() != 0 {
				t.Fatalf("unsafe chart input %s", change)
			}
			if _, err = os.Stat(journal); !os.IsNotExist(err) {
				t.Fatal("invalid input published a journal")
			}
		})
	}
}

func TestNVKEInstallPlanChartsRefusesForeignTruncatedOrAuthorizingResults(t *testing.T) {
	for _, change := range []string{"id", "plan", "plan-version", "plan-digest", "operation", "operation-version", "site", "site-version", "authority", "authority-version", "status", "digest", "count", "missing-count", "missing-continuation", "continuation", "state", "numeric-bytes", "overflow", "zero-size", "refused"} {
		t.Run(change, func(t *testing.T) {
			command, out := nvkeChartTestCommand(t, "review")
			value := nvkeInstallPlanTestJSON(t, nvkeChartTestFixture())
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
				value["artifactDigest"] = "PRIVATE_INPUT"
			case "count":
				value["chartCount"] = 3
			case "missing-count":
				delete(value, "chartCount")
			case "missing-continuation":
				delete(value, "continuationAuthorized")
			case "continuation":
				value["continuationAuthorized"] = true
			case "state":
				value["executionState"] = "ready"
			case "numeric-bytes":
				value["bytes"] = 9007199254740993
			case "overflow":
				value["expandedBytes"] = "9223372036854775808"
			case "zero-size":
				value["bytes"] = "0"
			}
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if change == "refused" && strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanCharts(") {
					writePipelineTestResponse(t, w, map[string]any{"reviewNvkeOperatorInstallPlanCharts": map[string]any{"ok": false, "errors": []map[string]any{{"code": "OPERATOR_CHARTS_INCOMPLETE", "message": "PRIVATE_INPUT"}}}})
					return
				}
				nvkeChartTestReply(t, req, w, value)
			})
			defer server.Close()
			err := runNVKEAgentInstallPlanCharts(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review")
			if err == nil || strings.Contains(err.Error(), "PRIVATE_INPUT") || out.Len() != 0 {
				t.Fatalf("unsafe chart reply %s", change)
			}
		})
	}
}

func TestNVKEInstallPlanChartsLostReplyDoesNotRetryAndReviewUsesRetainedArtifact(t *testing.T) {
	prepare, _ := nvkeChartTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "recovery.json")
	if err := privatefile.Write(input, []byte(nvkeChartTestBody())); err != nil {
		t.Fatal(err)
	}
	_ = prepare.Flags().Set("file", input)
	_ = prepare.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanCharts(") {
			calls++
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("PRIVATE_PEER_ERROR"))
			return
		}
		nvkeChartTestReply(t, req, w, nvkeChartTestFixture())
	})
	defer server.Close()
	client := nvkeTestClient(server)
	if err := runNVKEAgentInstallPlanCharts(prepare, context.Background(), client, nvkeTestOperation, "prepare"); err == nil || strings.Contains(err.Error(), "PRIVATE_PEER_ERROR") {
		t.Fatal("lost chart reply was accepted or exposed")
	}
	review, _ := nvkeChartTestCommand(t, "review")
	if err := runNVKEAgentInstallPlanCharts(review, context.Background(), client, nvkeTestOperation, "review"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("lost chart reply replayed preparation")
	}
	if _, err := privatefile.ReadFile(journal, 16384); err != nil {
		t.Fatal("lost chart reply discarded recovery identity")
	}
}

func TestNVKEInstallPlanChartsStalePlanRefusedBeforeChartRPC(t *testing.T) {
	command, _ := nvkeChartTestCommand(t, "review")
	_ = command.Flags().Set("plan-version", "99")
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanCharts(") {
			calls++
		}
		nvkeChartTestReply(t, req, w, nvkeChartTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanCharts(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review"); err == nil || calls != 0 {
		t.Fatal("stale parent crossed chart RPC")
	}
}

func TestNVKEInstallPlanChartsPreparationRejectsReplyBeyondReviewedBudget(t *testing.T) {
	err := validateNVKEInstallPlanCharts(nvkeChartTestFixture(), nvkeInstallPlanFixture(), nvkeChartTestID, nvkeChartLimits{bytes: 1, expanded: 1})
	if err == nil {
		t.Fatal("accepted chart bytes beyond reviewed budget")
	}
}

func TestNVKEInstallPlanChartsEmptyStageRemainsNonAuthorizing(t *testing.T) {
	row := nvkeChartTestFixture()
	row.ChartCount, row.Bytes, row.ExpandedBytes = nvkeInstallPlanInt(0), "0", "0"
	if err := validateNVKEInstallPlanCharts(row, nvkeInstallPlanFixture(), nvkeChartTestID, nvkeChartLimits{}); err != nil {
		t.Fatal(err)
	}
	if row.ContinuationAuthorized == nil || *row.ContinuationAuthorized {
		t.Fatal("empty stage authorized execution")
	}
}

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
	"github.com/spf13/pflag"
)

func nvkeNodePlatformsTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", "node-platforms", verb})
	if err != nil || command.Name() != verb {
		t.Fatalf("node/platform command unavailable: %v", err)
	}
	source, _ := nvkeImmutableTestCommand(t, verb, false)
	source.Flags().Visit(func(flag *pflag.Flag) {
		if err := command.Flags().Set(flag.Name, flag.Value.String()); err != nil {
			t.Fatal(err)
		}
	})
	out := &bytes.Buffer{}
	command.SetOut(out)
	command.SetErr(out)
	command.SetContext(context.Background())
	return command, out
}

func nvkeNodePlatformsTestBody() string {
	var body map[string]any
	_ = json.Unmarshal([]byte(nvkeImmutableTestBody(false)), &body)
	delete(body, "item")
	delete(body, "maximumResources")
	body["nodePlatforms"] = map[string]any{"private-node.example": map[string]any{"uid": "private-uid", "runtimeProfile": "containerd/v2.3.0", "platform": map[string]any{"os": "linux", "architecture": "arm64", "variant": "v8", "os.features": []string{"owned"}}}}
	body["maximumImageBytes"] = "9007199254740993"
	body["maximumMetadataBytes"] = "16777216"
	body["maximumObservationBytes"] = "16777216"
	body["maximumHelperBytes"] = "9223372036854775807"
	body["maximumCalls"] = 512
	body["maximumPairs"] = 10000
	raw, _ := json.Marshal(body)
	return string(raw)
}

func nvkeNodePlatformsTestFixture() *api.NVKEInstallPlanNodePlatforms {
	raw, _ := json.Marshal(nvkeImmutableTestFixture(false))
	row := &api.NVKEInstallPlanNodePlatforms{}
	_ = json.Unmarshal(raw, row)
	nodes, contents, pairs, issues := 1, 1, 1, 4
	row.NodeCount = &nodes
	row.ContentCount = &contents
	row.PairCount = &pairs
	row.IssueCount = &issues
	row.ImageReferenceCount = nvkeImageBindingTestFixture().ImageCount
	row.PlatformDigest = strings.Repeat("7", 64)
	row.ExecutionState = nvkeNodePlatformsState
	return row
}

func nvkeNodePlatformsTestReply(t *testing.T, req gqlRequest, w http.ResponseWriter, row *api.NVKEInstallPlanNodePlatforms) {
	t.Helper()
	data := nvkeInstallPlanTestData()
	data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
	data["reviewNvkeOperatorInstallPlanCharts"] = map[string]any{"ok": true, "data": nvkeChartTestFixture()}
	data["reviewNvkeOperatorInstallPlanTree"] = map[string]any{"ok": true, "data": nvkeTreeTestFixture()}
	data["reviewNvkeOperatorInstallPlanWorkload"] = map[string]any{"ok": true, "data": nvkeWorkloadTestFixture()}
	data["reviewNvkeOperatorInstallPlanImageBinding"] = map[string]any{"ok": true, "data": nvkeImageBindingTestFixture()}
	for _, field := range []string{"prepareNvkeOperatorInstallPlanNodePlatforms", "reviewNvkeOperatorInstallPlanNodePlatforms", "recoverNvkeOperatorInstallPlanNodePlatformsPreparation"} {
		if strings.Contains(req.Query, field+"(") {
			data[field] = map[string]any{"ok": true, "data": row}
		}
	}
	if strings.Contains(req.Query, "requestNvkeOperatorJob") {
		t.Error("node/platform observation admitted native work")
	}
	writePipelineTestResponse(t, w, data)
}

func TestNVKENodePlatformsStrictScopeAndBudgets(t *testing.T) {
	body := nvkeNodePlatformsTestBody()
	parsed, err := parseNVKENodePlatformsBody([]byte(body))
	if err != nil || parsed.MaximumImageBytes != "9007199254740993" || parsed.MaximumHelperBytes != "9223372036854775807" {
		t.Fatalf("exact large budgets changed: %v", err)
	}
	for _, profile := range []string{"containerd/v2.3.0", "containerd/v2.3.7", "containerd/v2.2.3", "containerd/v2.1.7", "containerd/v1.7.31"} {
		if _, err := parseNVKENodePlatformsBody([]byte(strings.Replace(body, "containerd/v2.3.0", profile, 1))); err != nil {
			t.Fatal(profile, err)
		}
	}
	cases := map[string]string{
		"alias":        strings.Replace(body, `"uid":`, `"UID":`, 1),
		"profile":      strings.Replace(body, "containerd/v2.3.0", "containerd/v99", 1),
		"duplicate":    strings.Replace(body, `"uid":"private-uid"`, `"uid":"private-uid","uid":"private-uid"`, 1),
		"null-feature": strings.Replace(body, `"os.features":["owned"]`, `"os.features":[null]`, 1),
		"null-variant": strings.Replace(body, `"variant":"v8"`, `"variant":null`, 1),
		"calls":        strings.Replace(body, `"maximumCalls":512`, `"maximumCalls":7`, 1),
		"calls-type":   strings.Replace(body, `"maximumCalls":512`, `"maximumCalls":true`, 1),
		"decimal":      strings.Replace(body, `"maximumImageBytes":"9007199254740993"`, `"maximumImageBytes":"01"`, 1),
		"bytes-type":   strings.Replace(body, `"maximumImageBytes":"9007199254740993"`, `"maximumImageBytes":9007199254740993`, 1),
		"path":         strings.Replace(body, `"variant":"v8"`, `"helperProgram":"/PRIVATE"`, 1),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNVKENodePlatformsBody([]byte(value)); err == nil {
				t.Fatal("invalid or aliased scope accepted")
			}
		})
	}
	var value map[string]any
	_ = json.Unmarshal([]byte(body), &value)
	rows := value["nodePlatforms"].(map[string]any)
	rows["other-node"] = rows["private-node.example"]
	raw, _ := json.Marshal(value)
	if _, err := parseNVKENodePlatformsBody(raw); err == nil {
		t.Fatal("duplicate UID accepted")
	}
}

func TestNVKENodePlatformsReviewAndRetryKeepSourcePrivate(t *testing.T) {
	for _, verb := range []string{"review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command, out := nvkeNodePlatformsTestCommand(t, verb)
			if command.Flags().Lookup("file") != nil || command.Flags().Lookup("request-file") != nil {
				t.Fatal("recovery accepts new private input")
			}
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "NvkeOperatorInstallPlanNodePlatformsReviewInput!") {
					calls++
					input := req.Variables["input"].(map[string]any)
					if input["artifactId"] != nvkeImmutableTestID || input["bindingId"] != nvkeImageBindingTestID || input["expectedBindingVersion"] != float64(2) || input["workloadId"] != nvkeWorkloadTestID {
						t.Error("reviewed source changed")
					}
				}
				nvkeNodePlatformsTestReply(t, req, w, nvkeNodePlatformsTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanNodePlatforms(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !strings.Contains(out.String(), `"continuationAuthorized": false`) || strings.Contains(out.String(), "private-node") {
				t.Fatal("unexpected calls or private output")
			}
		})
	}
}

func TestNVKENodePlatformsJournalPrecedesLostReplyAndRefusesChangedInput(t *testing.T) {
	command, _ := nvkeNodePlatformsTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	body := nvkeNodePlatformsTestBody()
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "PrepareNvkeOperatorInstallPlanNodePlatformsInput!") {
			calls++
			data, err := privatefile.ReadFile(journal, 16384)
			var saved nvkeImmutableJournal
			if err != nil || decodeNVKEImmutableStrict(data, &saved) != nil || saved.Kind != "nvke-install-plan-node-platforms" || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) || bytes.Contains(data, []byte("private-node")) || bytes.Contains(data, []byte("nodePlatforms")) {
				t.Error("journal absent, changed or private scope retained")
			}
			value := req.Variables["input"].(map[string]any)
			if value["requestText"] != body {
				t.Error("exact private input changed")
			}
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = connection.Close()
			return
		}
		nvkeNodePlatformsTestReply(t, req, w, nvkeNodePlatformsTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanNodePlatforms(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("lost reply accepted or automatically retried")
	}
	changed := strings.Replace(body, `"timeoutSeconds":300`, `"timeoutSeconds":299`, 1)
	if err := os.WriteFile(input, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runNVKEAgentInstallPlanNodePlatforms(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("changed private input reached preparation")
	}
}

func TestNVKENodePlatformsRejectMismatchedSummary(t *testing.T) {
	changes := map[string]func(*api.NVKEInstallPlanNodePlatforms){
		"source":        func(r *api.NVKEInstallPlanNodePlatforms) { r.BindingVersion++ },
		"pairs":         func(r *api.NVKEInstallPlanNodePlatforms) { *r.PairCount++ },
		"missing":       func(r *api.NVKEInstallPlanNodePlatforms) { r.NodeCount = nil },
		"continuation":  func(r *api.NVKEInstallPlanNodePlatforms) { value := true; r.ContinuationAuthorized = &value },
		"private-state": func(r *api.NVKEInstallPlanNodePlatforms) { r.ExecutionState = "ready" },
		"time":          func(r *api.NVKEInstallPlanNodePlatforms) { r.ObservedAt = "2020-01-01T00:00:00Z" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			row := nvkeNodePlatformsTestFixture()
			change(row)
			if err := validateNVKEInstallPlanNodePlatforms(row, nvkeInstallPlanFixture(), nvkeChartTestFixture(), nvkeTreeTestFixture(), nvkeWorkloadTestFixture(), nvkeImageBindingTestFixture(), nvkeImmutableTestID); err == nil {
				t.Fatal("mismatched observation accepted")
			}
		})
	}
}

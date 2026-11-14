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

func nvkePullAuthorityTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", "pull-authority", verb})
	if err != nil || command.Name() != verb {
		t.Fatalf("pull-authority command unavailable: %v", err)
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

func nvkePullAuthorityTestBody() string {
	var body map[string]any
	_ = json.Unmarshal([]byte(nvkeImmutableTestBody(false)), &body)
	delete(body, "item")
	delete(body, "maximumResources")
	body["selectedPods"] = map[string]string{"private-namespace/private-pod": "private-uid"}
	body["maximumBytes"] = "1048576"
	body["maximumCalls"] = 512
	raw, _ := json.Marshal(body)
	return string(raw)
}

func nvkePullAuthorityTestFixture() *api.NVKEInstallPlanPullAuthority {
	raw, _ := json.Marshal(nvkeImmutableTestFixture(false))
	row := &api.NVKEInstallPlanPullAuthority{}
	_ = json.Unmarshal(raw, row)
	pods, accounts, secrets, missing := 1, 1, 1, 0
	row.PodCount = &pods
	row.ServiceAccountCount = &accounts
	row.SecretCount = &secrets
	row.MissingReferenceCount = &missing
	row.AuthorityDigest = strings.Repeat("7", 64)
	row.ExecutionState = nvkePullAuthorityState
	return row
}

func nvkePullAuthorityTestReply(t *testing.T, req gqlRequest, w http.ResponseWriter, row *api.NVKEInstallPlanPullAuthority) {
	t.Helper()
	data := nvkeInstallPlanTestData()
	data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
	data["reviewNvkeOperatorInstallPlanCharts"] = map[string]any{"ok": true, "data": nvkeChartTestFixture()}
	data["reviewNvkeOperatorInstallPlanTree"] = map[string]any{"ok": true, "data": nvkeTreeTestFixture()}
	data["reviewNvkeOperatorInstallPlanWorkload"] = map[string]any{"ok": true, "data": nvkeWorkloadTestFixture()}
	data["reviewNvkeOperatorInstallPlanImageBinding"] = map[string]any{"ok": true, "data": nvkeImageBindingTestFixture()}
	for _, field := range []string{"prepareNvkeOperatorInstallPlanPullAuthority", "reviewNvkeOperatorInstallPlanPullAuthority", "recoverNvkeOperatorInstallPlanPullAuthorityPreparation"} {
		if strings.Contains(req.Query, field+"(") {
			data[field] = map[string]any{"ok": true, "data": row}
		}
	}
	if strings.Contains(req.Query, "requestNvkeOperatorJob") {
		t.Error("pull-authority observation admitted native work")
	}
	writePipelineTestResponse(t, w, data)
}

func TestNVKEPullAuthorityStrictScopeAndBudgets(t *testing.T) {
	body := nvkePullAuthorityTestBody()
	parsed, err := parseNVKEPullAuthorityBody([]byte(body))
	if err != nil || len(parsed.SelectedPods) != 1 || parsed.MaximumBytes != "1048576" {
		t.Fatalf("explicit Pod scope changed: %v", err)
	}
	cases := map[string]string{
		"alias":         strings.Replace(body, `"selectedPods":`, `"selectedpods":`, 1),
		"duplicate":     strings.Replace(body, `"private-namespace/private-pod":"private-uid"`, `"private-namespace/private-pod":"private-uid","private-namespace/private-pod":"other"`, 1),
		"duplicate-uid": strings.Replace(body, `"private-namespace/private-pod":"private-uid"`, `"private-namespace/private-pod":"private-uid","owned/other":"private-uid"`, 1),
		"uid-type":      strings.Replace(body, `"private-uid"`, `7`, 1),
		"uid-null":      strings.Replace(body, `"private-uid"`, `null`, 1),
		"calls":         strings.Replace(body, `"maximumCalls":512`, `"maximumCalls":3`, 1),
		"calls-type":    strings.Replace(body, `"maximumCalls":512`, `"maximumCalls":true`, 1),
		"decimal":       strings.Replace(body, `"maximumBytes":"1048576"`, `"maximumBytes":"01"`, 1),
		"bytes-type":    strings.Replace(body, `"maximumBytes":"1048576"`, `"maximumBytes":1048576`, 1),
		"scope-path":    strings.Replace(body, `private-namespace/private-pod`, `../private-pod`, 1),
		"source-path":   strings.Replace(body, `"maximumCalls":512`, `"maximumCalls":512,"credentialPath":"/PRIVATE"`, 1),
		"empty":         strings.Replace(body, `"selectedPods":{"private-namespace/private-pod":"private-uid"}`, `"selectedPods":{}`, 1),
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNVKEPullAuthorityBody([]byte(value)); err == nil {
				t.Fatal("invalid or aliased Pod scope accepted")
			}
		})
	}
	long := strings.Replace(body, "private-namespace/private-pod", "owned/"+strings.Repeat("p", 100), 1)
	if _, err := parseNVKEPullAuthorityBody([]byte(long)); err != nil {
		t.Fatal("valid long native Pod name refused")
	}
}

func TestNVKEPullAuthorityReviewAndRetryKeepSourcePrivate(t *testing.T) {
	for _, verb := range []string{"review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command, out := nvkePullAuthorityTestCommand(t, verb)
			if command.Flags().Lookup("file") != nil || command.Flags().Lookup("request-file") != nil {
				t.Fatal("recovery accepts new private input")
			}
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "NvkeOperatorInstallPlanPullAuthorityReviewInput!") {
					calls++
					input := req.Variables["input"].(map[string]any)
					if input["artifactId"] != nvkeImmutableTestID || input["bindingId"] != nvkeImageBindingTestID || input["expectedBindingVersion"] != float64(2) || input["workloadId"] != nvkeWorkloadTestID {
						t.Error("reviewed source changed")
					}
				}
				nvkePullAuthorityTestReply(t, req, w, nvkePullAuthorityTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanPullAuthority(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !strings.Contains(out.String(), `"continuationAuthorized": false`) || strings.Contains(out.String(), "private-namespace/private-pod") {
				t.Fatal("unexpected calls or private output")
			}
		})
	}
}

func TestNVKEPullAuthorityJournalPrecedesLostReplyAndRefusesChangedInput(t *testing.T) {
	command, _ := nvkePullAuthorityTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	body := nvkePullAuthorityTestBody()
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "PrepareNvkeOperatorInstallPlanPullAuthorityInput!") {
			calls++
			data, err := privatefile.ReadFile(journal, 16384)
			var saved nvkeImmutableJournal
			if err != nil || decodeNVKEImmutableStrict(data, &saved) != nil || saved.Kind != "nvke-install-plan-pull-authority" || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) || bytes.Contains(data, []byte("private-namespace/private-pod")) || bytes.Contains(data, []byte("selectedPods")) {
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
		nvkePullAuthorityTestReply(t, req, w, nvkePullAuthorityTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanPullAuthority(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("lost reply accepted or automatically retried")
	}
	changed := strings.Replace(body, `"timeoutSeconds":300`, `"timeoutSeconds":299`, 1)
	if err := os.WriteFile(input, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runNVKEAgentInstallPlanPullAuthority(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("changed private input reached preparation")
	}
}

func TestNVKEPullAuthorityRejectMismatchedSummary(t *testing.T) {
	changes := map[string]func(*api.NVKEInstallPlanPullAuthority){
		"source":        func(r *api.NVKEInstallPlanPullAuthority) { r.BindingVersion++ },
		"accounts":      func(r *api.NVKEInstallPlanPullAuthority) { *r.ServiceAccountCount = 2 },
		"missing-count": func(r *api.NVKEInstallPlanPullAuthority) { *r.MissingReferenceCount = 3 },
		"missing":       func(r *api.NVKEInstallPlanPullAuthority) { r.PodCount = nil },
		"overflow":      func(r *api.NVKEInstallPlanPullAuthority) { *r.SecretCount = int(^uint(0) >> 1) },
		"continuation":  func(r *api.NVKEInstallPlanPullAuthority) { value := true; r.ContinuationAuthorized = &value },
		"private-state": func(r *api.NVKEInstallPlanPullAuthority) { r.ExecutionState = "ready" },
		"time":          func(r *api.NVKEInstallPlanPullAuthority) { r.ObservedAt = "2020-01-01T00:00:00Z" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			row := nvkePullAuthorityTestFixture()
			change(row)
			if err := validateNVKEInstallPlanPullAuthority(row, nvkeInstallPlanFixture(), nvkeChartTestFixture(), nvkeTreeTestFixture(), nvkeWorkloadTestFixture(), nvkeImageBindingTestFixture(), nvkeImmutableTestID); err == nil {
				t.Fatal("mismatched pull observation accepted")
			}
		})
	}
}

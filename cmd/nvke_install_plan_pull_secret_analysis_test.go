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

func nvkePullSecretAnalysisTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", "pull-secret-analysis", verb})
	if err != nil || command.Name() != verb {
		t.Fatalf("pull-secret-analysis command unavailable: %v", err)
	}
	source, _ := nvkeImmutableTestCommand(t, verb, false)
	source.Flags().Visit(func(flag *pflag.Flag) {
		if err := command.Flags().Set(flag.Name, flag.Value.String()); err != nil {
			t.Fatal(err)
		}
	})
	for key, value := range map[string]string{"pull-authority": nvkePullSecretParentID, "pull-authority-version": "2", "pull-authority-digest": strings.Repeat("7", 64)} {
		if err := command.Flags().Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	out := &bytes.Buffer{}
	command.SetOut(out)
	command.SetErr(out)
	command.SetContext(context.Background())
	return command, out
}

const nvkePullSecretParentID = "8b540bf9-cb54-4cb7-9fbd-c2377f713f24"

func nvkePullSecretParentFixture() *api.NVKEInstallPlanPullAuthority {
	row := nvkePullAuthorityTestFixture()
	row.ID = nvkePullSecretParentID
	row.Version = 2
	return row
}

func nvkePullSecretAnalysisTestBody() string {
	var body map[string]any
	_ = json.Unmarshal([]byte(nvkePullAuthorityTestBody()), &body)
	delete(body, "selectedPods")
	delete(body, "maximumCalls")
	body["pullAuthorityId"] = nvkePullSecretParentID
	body["pullAuthorityDigest"] = strings.Repeat("7", 64)
	body["maximumHelperBytes"] = "65536"
	body["imageSelections"] = []any{map[string]any{"podKey": "private-namespace/private-pod", "imagePointer": "/spec/containers/0/image", "ensureSecretPulledImages": true}}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func nvkePullSecretAnalysisTestFixture() *api.NVKEInstallPlanPullSecretAnalysis {
	parent := nvkePullSecretParentFixture()
	raw, _ := json.Marshal(nvkePullAuthorityTestFixture())
	row := &api.NVKEInstallPlanPullSecretAnalysis{}
	_ = json.Unmarshal(raw, row)
	one, zero := 1, 0
	row.ImageUseCount, row.CandidateCount, row.MissingReferenceCount = &one, &one, &zero
	row.PullAuthorityID, row.PullAuthorityVersion, row.PullAuthorityDigest = parent.ID, parent.Version, parent.AuthorityDigest
	row.AnalyzedAt, row.SourceObservedAt = parent.ObservedAt, parent.ObservedAt
	row.ExecutionState = nvkePullSecretAnalysisState
	return row
}

func nvkePullSecretAnalysisTestReply(t *testing.T, req gqlRequest, w http.ResponseWriter, row *api.NVKEInstallPlanPullSecretAnalysis) {
	t.Helper()
	data := nvkeInstallPlanTestData()
	data["reviewNvkeOperatorInstallPlanPullAuthority"] = map[string]any{"ok": true, "data": nvkePullSecretParentFixture()}
	data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
	data["reviewNvkeOperatorInstallPlanCharts"] = map[string]any{"ok": true, "data": nvkeChartTestFixture()}
	data["reviewNvkeOperatorInstallPlanTree"] = map[string]any{"ok": true, "data": nvkeTreeTestFixture()}
	data["reviewNvkeOperatorInstallPlanWorkload"] = map[string]any{"ok": true, "data": nvkeWorkloadTestFixture()}
	data["reviewNvkeOperatorInstallPlanImageBinding"] = map[string]any{"ok": true, "data": nvkeImageBindingTestFixture()}
	for _, field := range []string{"prepareNvkeOperatorInstallPlanPullSecretAnalysis", "reviewNvkeOperatorInstallPlanPullSecretAnalysis", "recoverNvkeOperatorInstallPlanPullSecretAnalysisPreparation"} {
		if strings.Contains(req.Query, field+"(") {
			data[field] = map[string]any{"ok": true, "data": row}
		}
	}
	if strings.Contains(req.Query, "requestNvkeOperatorJob") {
		t.Error("pull-secret-analysis observation admitted native work")
	}
	writePipelineTestResponse(t, w, data)
}

func TestNVKEPullSecretAnalysisStrictScopeAndBudgets(t *testing.T) {
	body := nvkePullSecretAnalysisTestBody()
	parsed, err := parseNVKEPullSecretAnalysisBody([]byte(body))
	if err != nil || len(parsed.ImageSelections) != 1 || parsed.ImageSelections[0].EnsureSecretPulledImages == nil || !*parsed.ImageSelections[0].EnsureSecretPulledImages {
		t.Fatalf("explicit image scope changed: %v", err)
	}
	for name, value := range map[string]string{
		"alias":             strings.Replace(body, `"imageSelections":`, `"imageselections":`, 1),
		"nested-alias":      strings.Replace(body, `"podKey":`, `"PodKey":`, 1),
		"duplicate":         strings.Replace(body, `"podKey":`, `"podKey":"owned/other","podKey":`, 1),
		"escaped-duplicate": strings.Replace(body, `"podKey":`, `"pod\u004bey":"owned/other","podKey":`, 1),
		"feature-type":      strings.Replace(body, `"ensureSecretPulledImages":true`, `"ensureSecretPulledImages":1`, 1),
		"feature-null":      strings.Replace(body, `"ensureSecretPulledImages":true`, `"ensureSecretPulledImages":null`, 1),
		"feature-missing":   strings.Replace(body, `"ensureSecretPulledImages":true,`, ``, 1),
		"feature-duplicate": strings.Replace(body, `"ensureSecretPulledImages":true`, `"ensureSecretPulledImages":true,"ensureSecretPulledImages":false`, 1),
		"pointer":           strings.Replace(body, `/spec/containers/0/image`, `/status/image`, 1),
		"leading-index":     strings.Replace(body, `/spec/containers/0/image`, `/spec/containers/00/image`, 1),
		"newline-pointer":   strings.Replace(body, `/spec/containers/0/image`, `/spec/containers/0/image\n`, 1),
		"scope":             strings.Replace(body, `private-namespace/private-pod`, `../private-pod`, 1),
		"null-scope":        strings.Replace(body, `"imageSelections":[`, `"imageSelections":null,"discard":[`, 1),
		"helper-size":       strings.Replace(body, `"maximumHelperBytes":"65536"`, `"maximumHelperBytes":"1048577"`, 1),
		"helper-type":       strings.Replace(body, `"maximumHelperBytes":"65536"`, `"maximumHelperBytes":65536`, 1),
		"decimal":           strings.Replace(body, `"maximumBytes":"1048576"`, `"maximumBytes":"01"`, 1),
		"private":           strings.Replace(body, `"timeoutSeconds":300`, `"timeoutSeconds":300,"credentialPath":"/PRIVATE"`, 1),
		"trailing":          body + ` {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNVKEPullSecretAnalysisBody([]byte(value)); err == nil {
				t.Fatal("invalid or aliased private interpretation accepted")
			}
		})
	}
	falseBody := strings.Replace(body, `"ensureSecretPulledImages":true`, `"ensureSecretPulledImages":false`, 1)
	accepted, err := parseNVKEPullSecretAnalysisBody([]byte(falseBody))
	if err != nil || accepted.ImageSelections[0].EnsureSecretPulledImages == nil || *accepted.ImageSelections[0].EnsureSecretPulledImages {
		t.Fatal("explicit false setting defaulted or refused")
	}
	var decoded map[string]any
	_ = json.Unmarshal([]byte(body), &decoded)
	rows := decoded["imageSelections"].([]any)
	decoded["imageSelections"] = append(rows, rows[0])
	duplicate, _ := json.Marshal(decoded)
	if _, err := parseNVKEPullSecretAnalysisBody(duplicate); err == nil {
		t.Fatal("duplicate image use accepted")
	}
}

func TestNVKEPullSecretAnalysisReviewAndRetryKeepSourcePrivate(t *testing.T) {
	for _, verb := range []string{"review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command, out := nvkePullSecretAnalysisTestCommand(t, verb)
			if command.Flags().Lookup("file") != nil || command.Flags().Lookup("request-file") != nil {
				t.Fatal("recovery accepts new private input")
			}
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "NvkeOperatorInstallPlanPullSecretAnalysisReviewInput!") {
					calls++
					input := req.Variables["input"].(map[string]any)
					if input["artifactId"] != nvkeImmutableTestID || input["bindingId"] != nvkeImageBindingTestID || input["expectedBindingVersion"] != float64(2) || input["workloadId"] != nvkeWorkloadTestID || input["pullAuthorityId"] != nvkePullSecretParentID || input["expectedPullAuthorityVersion"] != float64(2) || input["pullAuthorityDigest"] != strings.Repeat("7", 64) {
						t.Error("reviewed source changed")
					}
				}
				nvkePullSecretAnalysisTestReply(t, req, w, nvkePullSecretAnalysisTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanPullSecretAnalysis(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !strings.Contains(out.String(), `"continuationAuthorized": false`) || strings.Contains(out.String(), "private-namespace/private-pod") {
				t.Fatal("unexpected calls or private output")
			}
		})
	}
}

func TestNVKEPullSecretAnalysisJournalPrecedesLostReplyAndRefusesChangedInput(t *testing.T) {
	command, _ := nvkePullSecretAnalysisTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	body := nvkePullSecretAnalysisTestBody()
	if err := privatefile.Write(input, []byte(body)); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "PrepareNvkeOperatorInstallPlanPullSecretAnalysisInput!") {
			calls++
			data, err := privatefile.ReadFile(journal, 16384)
			var saved nvkePullSecretAnalysisJournal
			if err != nil || decodeNVKEPullSecretExact(data, &saved) != nil || saved.Kind != "nvke-install-plan-pull-secret-analysis" || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) || saved.PullAuthorityID != nvkePullSecretParentID || saved.PullAuthorityVersion != 2 || saved.PullAuthorityDigest != strings.Repeat("7", 64) || bytes.Contains(data, []byte("private-namespace/private-pod")) || bytes.Contains(data, []byte("imageSelections")) {
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
		nvkePullSecretAnalysisTestReply(t, req, w, nvkePullSecretAnalysisTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanPullSecretAnalysis(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("lost reply accepted or automatically retried")
	}
	changed := strings.Replace(body, `"timeoutSeconds":300`, `"timeoutSeconds":299`, 1)
	if err := os.WriteFile(input, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runNVKEAgentInstallPlanPullSecretAnalysis(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 1 {
		t.Fatal("changed private input reached preparation")
	}
}

func TestNVKEPullSecretAnalysisRejectMismatchedSummary(t *testing.T) {
	changes := map[string]func(*api.NVKEInstallPlanPullSecretAnalysis){
		"source":        func(r *api.NVKEInstallPlanPullSecretAnalysis) { r.BindingVersion++ },
		"parent":        func(r *api.NVKEInstallPlanPullSecretAnalysis) { r.PullAuthorityVersion++ },
		"parent-digest": func(r *api.NVKEInstallPlanPullSecretAnalysis) { r.PullAuthorityDigest = strings.Repeat("8", 64) },
		"missing-count": func(r *api.NVKEInstallPlanPullSecretAnalysis) { *r.MissingReferenceCount = 10001 },
		"missing":       func(r *api.NVKEInstallPlanPullSecretAnalysis) { r.ImageUseCount = nil },
		"overflow":      func(r *api.NVKEInstallPlanPullSecretAnalysis) { *r.CandidateCount = int(^uint(0) >> 1) },
		"continuation":  func(r *api.NVKEInstallPlanPullSecretAnalysis) { value := true; r.ContinuationAuthorized = &value },
		"state":         func(r *api.NVKEInstallPlanPullSecretAnalysis) { r.ExecutionState = "ready" },
		"time":          func(r *api.NVKEInstallPlanPullSecretAnalysis) { r.AnalyzedAt = "2020-01-01T00:00:00Z" },
		"source-time":   func(r *api.NVKEInstallPlanPullSecretAnalysis) { r.SourceObservedAt = "2099-01-01T00:00:00Z" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			row := nvkePullSecretAnalysisTestFixture()
			change(row)
			if err := validateNVKEInstallPlanPullSecretAnalysis(row, nvkeInstallPlanFixture(), nvkeChartTestFixture(), nvkeTreeTestFixture(), nvkeWorkloadTestFixture(), nvkeImageBindingTestFixture(), nvkePullSecretParentFixture(), nvkeImmutableTestID); err == nil {
				t.Fatal("mismatched private interpretation accepted")
			}
		})
	}
}

func TestNVKEPullSecretAnalysisChangedParentRefusesBeforeJournalAndPreparation(t *testing.T) {
	for _, field := range []string{"version", "digest"} {
		t.Run(field, func(t *testing.T) {
			command, _ := nvkePullSecretAnalysisTestCommand(t, "prepare")
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
			if err := privatefile.Write(input, []byte(nvkePullSecretAnalysisTestBody())); err != nil {
				t.Fatal(err)
			}
			_ = command.Flags().Set("file", input)
			_ = command.Flags().Set("request-file", journal)
			posts := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "PrepareNvkeOperatorInstallPlanPullSecretAnalysisInput!") {
					posts++
				}
				if strings.Contains(req.Query, "NvkeOperatorInstallPlanPullAuthorityReviewInput!") {
					parent := nvkePullSecretParentFixture()
					if field == "version" {
						parent.Version++
					} else {
						parent.AuthorityDigest = strings.Repeat("8", 64)
					}
					writePipelineTestResponse(t, w, map[string]any{"reviewNvkeOperatorInstallPlanPullAuthority": map[string]any{"ok": true, "data": parent}})
					return
				}
				nvkePullSecretAnalysisTestReply(t, req, w, nvkePullSecretAnalysisTestFixture())
			})
			defer server.Close()
			err := runNVKEAgentInstallPlanPullSecretAnalysis(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare")
			if err == nil || !strings.Contains(err.Error(), "pull observation changed") || posts != 0 {
				t.Fatal("changed parent reached interpretation preparation")
			}
			if _, err = os.Stat(journal); !os.IsNotExist(err) {
				t.Fatal("changed parent created a new private intent")
			}
		})
	}
}

func TestNVKEPullSecretAnalysisSuccessfulPreparationAndClosedJournal(t *testing.T) {
	command, out := nvkePullSecretAnalysisTestCommand(t, "prepare")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	if err := privatefile.Write(input, []byte(nvkePullSecretAnalysisTestBody())); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	posts := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "PrepareNvkeOperatorInstallPlanPullSecretAnalysisInput!") {
			posts++
		}
		nvkePullSecretAnalysisTestReply(t, req, w, nvkePullSecretAnalysisTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanPullSecretAnalysis(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err != nil {
		t.Fatal(err)
	}
	if posts != 1 || !strings.Contains(out.String(), `"candidateCount": 1`) || strings.Contains(out.String(), "private-namespace/private-pod") {
		t.Fatal("private interpretation output or preparation count changed")
	}
	data, err := privatefile.ReadFile(journal, 16384)
	if err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(data, []byte(`"pullAuthorityVersion"`), []byte(`"PullAuthorityVersion"`), 1)
	if bytes.Equal(changed, data) {
		t.Fatal("parent version was not journaled")
	}
	if err = privatefile.Write(journal, changed); err != nil {
		t.Fatal(err)
	}
	if err = runNVKEAgentInstallPlanPullSecretAnalysis(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || posts != 1 {
		t.Fatal("aliased parent metadata reached preparation")
	}
}

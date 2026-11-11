package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

const nvkeImmutableTestID = "709813b1-a359-465f-bc15-41b648ece1b1"

func nvkeImmutableTestCommand(t *testing.T, verb string, tree bool) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", nvkeImmutableName(tree), verb})
	if err != nil || command.Name() != verb {
		t.Fatalf("immutable command unavailable: %v", err)
	}
	source, _ := nvkeImageBindingTestCommand(t, verb)
	source.Flags().Visit(func(flag *pflag.Flag) {
		if err := command.Flags().Set(flag.Name, flag.Value.String()); err != nil {
			t.Fatal(err)
		}
	})
	for flag, value := range map[string]string{"artifact": nvkeImmutableTestID, "binding": nvkeImageBindingTestID, "binding-version": "2", "binding-digest": strings.Repeat("5", 64)} {
		if err := command.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	out := &bytes.Buffer{}
	command.SetOut(out)
	command.SetErr(out)
	command.SetContext(context.Background())
	return command, out
}

func nvkeImmutableTestBody(tree bool) string {
	body := map[string]any{"format": 1, "artifactId": nvkeImmutableTestID, "planId": nvkePlanTestID, "planDigest": strings.Repeat("b", 64), "treeId": nvkeTreeTestID, "treeDigest": strings.Repeat("d", 64), "bindingId": nvkeImageBindingTestID, "bindingDigest": strings.Repeat("5", 64), "maximumBytes": "1048576", "timeoutSeconds": 300}
	if tree {
		choices := map[string]any{}
		for i := 0; i < nvkeInstallPlanFixture().ItemCount; i++ {
			choices[fmt.Sprintf("NKTP_Phase_%d", i)] = map[string]string{"artifactId": fmt.Sprintf("f0000000-0000-4000-8000-%012d", i+1), "artifactDigest": strings.Repeat("9", 64)}
		}
		body["payloadBindings"] = choices
	} else {
		body["item"] = "NKTP_Phase_0"
		body["maximumResources"] = 100
	}
	raw, _ := json.Marshal(body)
	return string(raw)
}

func nvkeImmutableTestFixture(tree bool) *api.NVKEInstallPlanImmutableArtifact {
	binding := nvkeImageBindingTestFixture()
	raw, _ := json.Marshal(binding)
	row := &api.NVKEInstallPlanImmutableArtifact{}
	_ = json.Unmarshal(raw, row)
	row.ID = nvkeImmutableTestID
	row.BindingID = binding.ID
	row.BindingVersion = binding.Version
	row.SourceObservedAt = binding.ObservedAt
	files, resources, rewrites, imports := 1, 1, 1, 0
	row.FileCount = &files
	// The wire only returns the fields selected for this artifact kind.
	row.ImageUseCount = nil
	row.RewriteCount = nil
	row.ImportCount = nil
	if tree {
		plan := nvkeInstallPlanFixture()
		phases, skipped := plan.ItemCount-*plan.Plan.FirstRemainingOrdinal, *plan.Plan.FirstRemainingOrdinal
		row.PhaseCount = &phases
		row.SkippedPhaseCount = &skipped
		row.ImportCount = &imports
		row.Bytes = "128"
		row.ExecutionDigest = strings.Repeat("7", 64)
		row.ExecutionState = nvkeImmutableTreeState
	} else {
		row.ResourceCount = &resources
		row.ImageUseCount = binding.ImageUseCount
		row.RewriteCount = &rewrites
		row.PayloadBytes = "64"
		row.PayloadDigest = strings.Repeat("8", 64)
		row.ExecutionState = nvkeImmutablePayloadState
	}
	return row
}

func nvkeImmutableTestReply(t *testing.T, req gqlRequest, w http.ResponseWriter, row *api.NVKEInstallPlanImmutableArtifact, binding *api.NVKEInstallPlanImageBinding, tree bool) {
	t.Helper()
	data := nvkeInstallPlanTestData()
	data["reviewNvkeOperatorInstallPlan"] = map[string]any{"ok": true, "data": nvkeInstallPlanFixture()}
	data["reviewNvkeOperatorInstallPlanCharts"] = map[string]any{"ok": true, "data": nvkeChartTestFixture()}
	data["reviewNvkeOperatorInstallPlanTree"] = map[string]any{"ok": true, "data": nvkeTreeTestFixture()}
	data["reviewNvkeOperatorInstallPlanWorkload"] = map[string]any{"ok": true, "data": nvkeWorkloadTestFixture()}
	data["reviewNvkeOperatorInstallPlanImageBinding"] = map[string]any{"ok": true, "data": binding}
	suffix := "ImagePayload"
	if tree {
		suffix = "ImmutableExecutionTree"
	}
	for _, field := range []string{"prepareNvkeOperatorInstallPlan" + suffix, "reviewNvkeOperatorInstallPlan" + suffix, "recoverNvkeOperatorInstallPlan" + suffix + "Preparation"} {
		if strings.Contains(req.Query, field+"(") {
			data[field] = map[string]any{"ok": true, "data": row}
		}
	}
	if strings.Contains(req.Query, "requestNvkeOperatorJob") {
		t.Error("immutable artifact admitted native work")
	}
	writePipelineTestResponse(t, w, data)
}

func TestNVKEInstallPlanImmutableReviewAndRetryKeepReviewedSource(t *testing.T) {
	for _, tree := range []bool{false, true} {
		for _, verb := range []string{"review", "retry-preparation"} {
			t.Run(nvkeImmutableName(tree)+"/"+verb, func(t *testing.T) {
				command, out := nvkeImmutableTestCommand(t, verb, tree)
				if command.Flags().Lookup("file") != nil || command.Flags().Lookup("request-file") != nil {
					t.Fatal("historical recovery accepts new private input")
				}
				calls, reads := 0, 0
				server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
					if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanImageBinding(") {
						reads++
					}
					if strings.Contains(req.Query, "NvkeOperatorInstallPlanImmutableReviewInput!") {
						calls++
						input := req.Variables["input"].(map[string]any)
						if input["artifactId"] != nvkeImmutableTestID || input["bindingId"] != nvkeImageBindingTestID || input["expectedBindingVersion"] != float64(2) || input["bindingDigest"] != strings.Repeat("5", 64) || input["workloadId"] != nvkeWorkloadTestID {
							t.Error("reviewed source changed")
						}
						if _, ok := input["requestText"]; ok {
							t.Error("historical recovery resent private input")
						}
					}
					nvkeImmutableTestReply(t, req, w, nvkeImmutableTestFixture(tree), nvkeImageBindingTestFixture(), tree)
				})
				defer server.Close()
				if err := runNVKEAgentInstallPlanImmutable(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb, tree); err != nil {
					t.Fatal(err)
				}
				if calls != 1 || reads != 1 || !strings.Contains(out.String(), nvkeImmutableTestID) || strings.Contains(out.String(), "NKTP_Phase") {
					t.Fatal("missing/repeated request or private selection in output")
				}
			})
		}
	}
}

func TestNVKEInstallPlanImmutableJournalPrecedesLostReplyAndPreventsChangedReplay(t *testing.T) {
	for _, tree := range []bool{false, true} {
		t.Run(nvkeImmutableName(tree), func(t *testing.T) {
			command, _ := nvkeImmutableTestCommand(t, "prepare", tree)
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
			body := nvkeImmutableTestBody(tree)
			if err := privatefile.Write(input, []byte(body)); err != nil {
				t.Fatal(err)
			}
			_ = command.Flags().Set("file", input)
			_ = command.Flags().Set("request-file", journal)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "PrepareNvkeOperatorInstallPlanImmutableInput!") {
					calls++
					data, err := privatefile.ReadFile(journal, 16384)
					var saved nvkeImmutableJournal
					if err != nil || decodeNVKEImmutableStrict(data, &saved) != nil || saved.Kind != "nvke-install-plan-"+nvkeImmutableName(tree) || saved.BindingID != nvkeImageBindingTestID || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) || bytes.Contains(data, []byte("NKTP_Phase")) || bytes.Contains(data, []byte("payloadBindings")) {
						t.Error("private recovery journal absent or leaked selections")
					}
					connection, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Fatal(err)
					}
					_ = connection.Close()
					return
				}
				nvkeImmutableTestReply(t, req, w, nvkeImmutableTestFixture(tree), nvkeImageBindingTestFixture(), tree)
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanImmutable(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare", tree); err == nil || calls != 1 {
				t.Fatal("lost result replayed or accepted")
			}
			changed := strings.Replace(body, `"timeoutSeconds":300`, `"timeoutSeconds":299`, 1)
			if err := os.WriteFile(input, []byte(changed), 0600); err != nil {
				t.Fatal(err)
			}
			if err := runNVKEAgentInstallPlanImmutable(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare", tree); err == nil || calls != 1 {
				t.Fatal("changed private input reused original identity")
			}
		})
	}
}

func TestNVKEInstallPlanImmutableMalformedRequestsRefuseBeforeJournalAndMutation(t *testing.T) {
	for _, tree := range []bool{false, true} {
		pairs := map[string][2]string{"duplicate-root": {`"format":1`, `"format":1,"format":1`}, "alias-root": {`"artifactId"`, `"ArtifactId"`}, "wrong-binding": {strings.Repeat("5", 64), strings.Repeat("f", 64)}, "zero-budget": {`"maximumBytes":"1048576"`, `"maximumBytes":"0"`}, "noncanonical-budget": {`"maximumBytes":"1048576"`, `"maximumBytes":"01"`}, "unknown-private-control": {`"format":1`, `"format":1,"privatePath":"PRIVATE_DO_NOT_PRINT"`}, "float-timeout": {`"timeoutSeconds":300`, `"timeoutSeconds":1.5`}}
		if tree {
			pairs["duplicate-choice-key"] = [2]string{`"artifactDigest":"` + strings.Repeat("9", 64) + `"`, `"artifactDigest":"` + strings.Repeat("9", 64) + `","artifactDigest":"` + strings.Repeat("9", 64) + `"`}
			pairs["aliased-choice-key"] = [2]string{`"artifactDigest"`, `"ArtifactDigest"`}
			pairs["parent-choice-collision"] = [2]string{"f0000000-0000-4000-8000-000000000001", nvkeWorkloadTestID}
			pairs["invalid-phase"] = [2]string{"NKTP_Phase_0", "invalid/phase"}
		} else {
			pairs["resource-budget"] = [2]string{`"maximumResources":100`, `"maximumResources":10001`}
			pairs["byte-budget"] = [2]string{`"maximumBytes":"1048576"`, `"maximumBytes":"67108865"`}
			pairs["null-item"] = [2]string{`"item":"NKTP_Phase_0"`, `"item":null`}
		}
		for name, pair := range pairs {
			t.Run(nvkeImmutableName(tree)+"/"+name, func(t *testing.T) {
				command, out := nvkeImmutableTestCommand(t, "prepare", tree)
				input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
				body := strings.Replace(nvkeImmutableTestBody(tree), pair[0], pair[1], 1)
				if err := privatefile.Write(input, []byte(body)); err != nil {
					t.Fatal(err)
				}
				_ = command.Flags().Set("file", input)
				_ = command.Flags().Set("request-file", journal)
				calls := 0
				server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
					if strings.Contains(req.Query, "PrepareNvkeOperatorInstallPlanImmutableInput!") {
						calls++
					}
					nvkeImmutableTestReply(t, req, w, nvkeImmutableTestFixture(tree), nvkeImageBindingTestFixture(), tree)
				})
				defer server.Close()
				err := runNVKEAgentInstallPlanImmutable(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare", tree)
				if err == nil || calls != 0 || strings.Contains(err.Error()+out.String(), "PRIVATE_DO_NOT_PRINT") {
					t.Fatal("invalid request reached mutation or exposed private input")
				}
				if _, err := os.Stat(journal); !os.IsNotExist(err) {
					t.Fatal("invalid request persisted journal")
				}
			})
		}
	}
}

func TestNVKEInstallPlanImmutableRejectsStaleParentsAndMalformedObservations(t *testing.T) {
	for _, tree := range []bool{false, true} {
		for _, name := range []string{"binding-version", "missing-file-count", "digest", "bytes", "continuation", "state", "source-time", "cross-kind"} {
			t.Run(nvkeImmutableName(tree)+"/"+name, func(t *testing.T) {
				command, _ := nvkeImmutableTestCommand(t, "review", tree)
				row, binding := nvkeImmutableTestFixture(tree), nvkeImageBindingTestFixture()
				switch name {
				case "binding-version":
					binding.Version++
				case "missing-file-count":
					row.FileCount = nil
				case "digest":
					row.CaptureDigest = "invalid"
				case "bytes":
					if tree {
						row.Bytes = "01"
					} else {
						row.PayloadBytes = "01"
					}
				case "continuation":
					value := true
					row.ContinuationAuthorized = &value
				case "state":
					row.ExecutionState = "ready"
				case "source-time":
					row.SourceObservedAt = "2000-01-01T00:00:00Z"
				case "cross-kind":
					if tree {
						row.PayloadDigest = strings.Repeat("8", 64)
					} else {
						row.Bytes = "128"
					}
				}
				calls := 0
				server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
					if strings.Contains(req.Query, "NvkeOperatorInstallPlanImmutableReviewInput!") {
						calls++
					}
					nvkeImmutableTestReply(t, req, w, row, binding, tree)
				})
				defer server.Close()
				if err := runNVKEAgentInstallPlanImmutable(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review", tree); err == nil {
					t.Fatal("stale parent or malformed artifact accepted")
				}
				if name == "binding-version" && calls != 0 {
					t.Fatal("stale binding reached immutable RPC")
				}
			})
		}
	}
}

func TestNVKEInstallPlanImmutablePreparationChecksResponseBounds(t *testing.T) {
	for _, tree := range []bool{false, true} {
		t.Run(nvkeImmutableName(tree), func(t *testing.T) {
			command, _ := nvkeImmutableTestCommand(t, "prepare", tree)
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
			if err := privatefile.Write(input, []byte(nvkeImmutableTestBody(tree))); err != nil {
				t.Fatal(err)
			}
			_ = command.Flags().Set("file", input)
			_ = command.Flags().Set("request-file", journal)
			row := nvkeImmutableTestFixture(tree)
			if tree {
				row.Bytes = "1048577"
			} else {
				row.PayloadBytes = "1048577"
			}
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				nvkeImmutableTestReply(t, req, w, row, nvkeImageBindingTestFixture(), tree)
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanImmutable(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare", tree); err == nil {
				t.Fatal("oversized prepared artifact accepted")
			}
		})
	}
}

func TestNVKEInstallPlanImmutablePreparePreservesExactIntentAndLargeBudget(t *testing.T) {
	for _, tree := range []bool{false, true} {
		t.Run(nvkeImmutableName(tree), func(t *testing.T) {
			command, out := nvkeImmutableTestCommand(t, "prepare", tree)
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
			body := nvkeImmutableTestBody(tree)
			if tree {
				body = strings.Replace(body, `"maximumBytes":"1048576"`, `"maximumBytes":"9223372036854775807"`, 1)
			}
			if err := privatefile.Write(input, []byte(body)); err != nil {
				t.Fatal(err)
			}
			_ = command.Flags().Set("file", input)
			_ = command.Flags().Set("request-file", journal)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "PrepareNvkeOperatorInstallPlanImmutableInput!") {
					calls++
					value := req.Variables["input"].(map[string]any)
					if value["requestText"] != body || value["bindingDigest"] != strings.Repeat("5", 64) {
						t.Error("exact private intent or reviewed binding changed")
					}
				}
				nvkeImmutableTestReply(t, req, w, nvkeImmutableTestFixture(tree), nvkeImageBindingTestFixture(), tree)
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanImmutable(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare", tree); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || strings.Contains(out.String(), "NKTP_Phase") || strings.Contains(out.String(), "payloadBindings") {
				t.Fatal("preparation repeated or exposed private selections")
			}
		})
	}
}

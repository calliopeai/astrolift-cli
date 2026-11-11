package cmd

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const nvkePullRuntimeNodeID = "d8185d06-b9ac-4e09-a370-44897f4a72b6"

func nvkePullRuntimeTestCommand(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	command, _, err := newNVKEAgentCommand().Find([]string{"install-plan", "pull-runtime", "review"})
	if err != nil || command.Name() != "review" {
		t.Fatalf("pull-runtime review unavailable: %v", err)
	}
	source, _ := nvkePullSecretAnalysisTestCommand(t, "review")
	source.Flags().Visit(func(flag *pflag.Flag) {
		if err := command.Flags().Set(flag.Name, flag.Value.String()); err != nil {
			t.Fatal(err)
		}
	})
	for key, value := range map[string]string{"analysis-version": "2", "analysis-digest": strings.Repeat("7", 64), "node-platform": nvkePullRuntimeNodeID, "node-platform-version": "2", "node-platform-digest": strings.Repeat("7", 64)} {
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

func nvkePullRuntimeTestFixture() *api.NVKEInstallPlanPullRuntime {
	analysis := nvkePullSecretAnalysisTestFixture()
	nodes := nvkeNodePlatformsTestFixture()
	nodes.ID = nvkePullRuntimeNodeID
	one, no := 1, false
	return &api.NVKEInstallPlanPullRuntime{Analysis: analysis, NodePlatforms: nodes, CorrespondenceDigest: strings.Repeat("a", 64), ImageUseCount: &one, NodeCount: &one, SourceObservedAt: analysis.SourceObservedAt, NodeObservedAt: nodes.ObservedAt, ExecutionState: nvkePullRuntimeState, ContinuationAuthorized: &no}
}

func TestNVKEPullRuntimeReadOnlyExactSource(t *testing.T) {
	command, out := nvkePullRuntimeTestCommand(t)
	for _, flag := range []string{"file", "request-file"} {
		if command.Flags().Lookup(flag) != nil {
			t.Fatal("read accepts private preparation input")
		}
	}
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanPullRuntime(") {
			calls++
			input := req.Variables["input"].(map[string]any)
			if input["analysisId"] != nvkeImmutableTestID || input["expectedAnalysisVersion"] != float64(2) || input["analysisDigest"] != strings.Repeat("7", 64) || input["nodePlatformId"] != nvkePullRuntimeNodeID || input["expectedNodePlatformVersion"] != float64(2) || input["nodePlatformDigest"] != strings.Repeat("7", 64) || input["pullAuthorityId"] != nvkePullSecretParentID || input["bindingId"] != nvkeImageBindingTestID {
				t.Error("reviewed capture ancestry changed")
			}
			writePipelineTestResponse(t, w, map[string]any{"reviewNvkeOperatorInstallPlanPullRuntime": map[string]any{"ok": true, "data": nvkePullRuntimeTestFixture()}})
			return
		}
		nvkePullSecretAnalysisTestReply(t, req, w, nvkePullSecretAnalysisTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanPullRuntime(command, context.Background(), nvkeTestClient(server), nvkeTestOperation); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !strings.Contains(out.String(), `"continuationAuthorized": false`) {
		t.Fatal("read did not preserve unresolved execution state")
	}
}

func TestNVKEPullRuntimeInvalidPinsBeforeNetwork(t *testing.T) {
	for name, value := range map[string]string{"analysis-version": "0", "analysis-digest": "bad", "node-platform": nvkePullSecretParentID, "node-platform-version": "0", "node-platform-digest": "bad"} {
		t.Run(name, func(t *testing.T) {
			command, _ := nvkePullRuntimeTestCommand(t)
			_ = command.Flags().Set(name, value)
			calls := 0
			server := nvkeTestServer(t, func(_ gqlRequest, _ http.ResponseWriter) { calls++ })
			defer server.Close()
			if err := runNVKEAgentInstallPlanPullRuntime(command, context.Background(), nvkeTestClient(server), nvkeTestOperation); err == nil || calls != 0 {
				t.Fatal("invalid capture pins reached network")
			}
		})
	}
}

func TestNVKEPullRuntimeRejectsChangedSummary(t *testing.T) {
	changes := map[string]func(*api.NVKEInstallPlanPullRuntime){
		"analysis-version": func(r *api.NVKEInstallPlanPullRuntime) { r.Analysis.Version++ },
		"analysis-digest":  func(r *api.NVKEInstallPlanPullRuntime) { r.Analysis.AuthorityDigest = strings.Repeat("0", 64) },
		"node-source":      func(r *api.NVKEInstallPlanPullRuntime) { r.NodePlatforms.BindingVersion++ },
		"node-digest":      func(r *api.NVKEInstallPlanPullRuntime) { r.NodePlatforms.PlatformDigest = strings.Repeat("0", 64) },
		"count":            func(r *api.NVKEInstallPlanPullRuntime) { n := 2; r.NodeCount = &n },
		"missing-count":    func(r *api.NVKEInstallPlanPullRuntime) { r.ImageUseCount = nil },
		"continuation":     func(r *api.NVKEInstallPlanPullRuntime) { yes := true; r.ContinuationAuthorized = &yes },
		"old-node":         func(r *api.NVKEInstallPlanPullRuntime) { r.NodeObservedAt = "1900-01-01T00:00:00Z" },
		"source-time":      func(r *api.NVKEInstallPlanPullRuntime) { r.SourceObservedAt = "1900-01-01T00:00:00Z" },
		"digest":           func(r *api.NVKEInstallPlanPullRuntime) { r.CorrespondenceDigest = "bad" },
		"missing-parent":   func(r *api.NVKEInstallPlanPullRuntime) { r.Analysis = nil },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			command, out := nvkePullRuntimeTestCommand(t)
			row := nvkePullRuntimeTestFixture()
			change(row)
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanPullRuntime(") {
					calls++
					writePipelineTestResponse(t, w, map[string]any{"reviewNvkeOperatorInstallPlanPullRuntime": map[string]any{"ok": true, "data": row}})
					return
				}
				nvkePullSecretAnalysisTestReply(t, req, w, nvkePullSecretAnalysisTestFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanPullRuntime(command, context.Background(), nvkeTestClient(server), nvkeTestOperation); err == nil || out.Len() != 0 || calls != 1 {
				t.Fatal("changed summary published or summary validation not reached")
			}
		})
	}
}

func TestNVKEPullRuntimeLostReadNeverRetries(t *testing.T) {
	command, out := nvkePullRuntimeTestCommand(t)
	calls := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanPullRuntime(") {
			calls++
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = connection.Close()
			return
		}
		nvkePullSecretAnalysisTestReply(t, req, w, nvkePullSecretAnalysisTestFixture())
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanPullRuntime(command, context.Background(), nvkeTestClient(server), nvkeTestOperation); err == nil || calls != 1 || out.Len() != 0 {
		t.Fatal("lost read retried or published")
	}
}

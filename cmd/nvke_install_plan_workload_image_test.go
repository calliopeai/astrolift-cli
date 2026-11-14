package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

func nvkeWorkloadImageCommand(t *testing.T, verb string) *cobra.Command {
	t.Helper()
	command, _ := nvkeImageTestCommand(t, verb)
	for key, value := range map[string]string{"workload": nvkeWorkloadTestID, "workload-version": "2", "workload-digest": strings.Repeat("4", 64)} {
		if err := command.Flags().Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	return command
}

func nvkeWorkloadImageFixture() *api.NVKEInstallPlanImage {
	row := nvkeImageTestFixture()
	row.SourceKind = "registry"
	row.WorkloadID, row.WorkloadVersion, row.WorkloadDigest = nvkeWorkloadTestID, nvkeInstallPlanInt(2), strings.Repeat("4", 64)
	return row
}

func nvkeWorkloadImageReply(t *testing.T, req gqlRequest, w http.ResponseWriter, row *api.NVKEInstallPlanImage) {
	t.Helper()
	if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanWorkload(") {
		writePipelineTestResponse(t, w, map[string]any{"reviewNvkeOperatorInstallPlanWorkload": map[string]any{"ok": true, "data": nvkeWorkloadTestFixture()}})
		return
	}
	nvkeImageTestReply(t, req, w, row)
}

func TestNVKEWorkloadImagePrepareReviewAndRetryBindInventory(t *testing.T) {
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command := nvkeWorkloadImageCommand(t, verb)
			journal := filepath.Join(t.TempDir(), "recovery.json")
			if verb == "prepare" {
				var body map[string]any
				if err := json.Unmarshal([]byte(nvkeImageTestBody()), &body); err != nil {
					t.Fatal(err)
				}
				body["workloadId"], body["workloadDigest"] = nvkeWorkloadTestID, strings.Repeat("4", 64)
				data, err := json.Marshal(body)
				if err != nil {
					t.Fatal(err)
				}
				input := filepath.Join(t.TempDir(), "input.json")
				if err = privatefile.Write(input, data); err != nil {
					t.Fatal(err)
				}
				_ = command.Flags().Set("file", input)
				_ = command.Flags().Set("request-file", journal)
			}
			reviews, captures := 0, 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanWorkload(") {
					reviews++
				}
				if strings.Contains(req.Query, "InstallPlanImage(") || strings.Contains(req.Query, "InstallPlanImagePreparation(") {
					captures++
					input := req.Variables["input"].(map[string]any)
					if input["workloadId"] != nvkeWorkloadTestID || input["expectedWorkloadVersion"] != float64(2) || input["workloadDigest"] != strings.Repeat("4", 64) {
						t.Error("image lost reviewed inventory")
					}
					if verb == "prepare" {
						data, err := privatefile.ReadFile(journal, 16384)
						var saved nvkeImageJournal
						if err != nil || decodeNVKEImageStrict(data, &saved) != nil || saved.WorkloadID != nvkeWorkloadTestID || saved.WorkloadVersion != 2 || saved.WorkloadDigest != strings.Repeat("4", 64) || bytes.Contains(data, []byte("private.example.test")) {
							t.Error("journal lost workload metadata or retained private reference")
						}
					} else if _, ok := input["requestText"]; ok {
						t.Error("recovery forwarded private text")
					}
				}
				nvkeWorkloadImageReply(t, req, w, nvkeWorkloadImageFixture())
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if reviews != 1 || captures != 1 {
				t.Fatalf("reviews=%d captures=%d", reviews, captures)
			}
		})
	}
}

func TestNVKEWorkloadImageInvalidSourceRefusesBeforeRPC(t *testing.T) {
	for name, change := range map[string]map[string]string{
		"missing-id": {"workload": ""}, "missing-digest": {"workload-digest": ""}, "missing-version": {"workload-version": "0"},
		"alias": {"workload": nvkeTreeTestID}, "bad-digest": {"workload-digest": "bad"}, "local": {"source-kind": "local"},
	} {
		t.Run(name, func(t *testing.T) {
			command := nvkeWorkloadImageCommand(t, "review")
			for key, value := range change {
				_ = command.Flags().Set(key, value)
			}
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				calls++
				nvkeWorkloadImageReply(t, req, w, nvkeWorkloadImageFixture())
			})
			defer server.Close()
			if runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review") == nil || calls != 0 {
				t.Fatal("invalid source contacted API")
			}
		})
	}
}

func TestNVKEWorkloadImageRefusesChangedAndDowngradedReply(t *testing.T) {
	for name, change := range map[string]func(*api.NVKEInstallPlanImage){
		"missing": func(r *api.NVKEInstallPlanImage) { r.WorkloadID = ""; r.WorkloadDigest = ""; r.WorkloadVersion = nil },
		"id":      func(r *api.NVKEInstallPlanImage) { r.WorkloadID = nvkeTreeTestID },
		"version": func(r *api.NVKEInstallPlanImage) { r.WorkloadVersion = nvkeInstallPlanInt(3) },
		"digest":  func(r *api.NVKEInstallPlanImage) { r.WorkloadDigest = strings.Repeat("9", 64) },
	} {
		t.Run(name, func(t *testing.T) {
			command := nvkeWorkloadImageCommand(t, "review")
			row := nvkeWorkloadImageFixture()
			change(row)
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) { nvkeWorkloadImageReply(t, req, w, row) })
			defer server.Close()
			if runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review") == nil {
				t.Fatal("changed workload source was accepted")
			}
		})
	}
}

func TestNVKEWorkloadImageCannotAddInventoryToGraphOnlyReply(t *testing.T) {
	command, _ := nvkeImageTestCommand(t, "review")
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) { nvkeImageTestReply(t, req, w, nvkeWorkloadImageFixture()) })
	defer server.Close()
	if runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review") == nil {
		t.Fatal("graph-only request accepted workload-bound image")
	}
}

func TestNVKEWorkloadImageRejectsChangedInventoryBeforeImageRPC(t *testing.T) {
	command := nvkeWorkloadImageCommand(t, "review")
	captures := 0
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		if strings.Contains(req.Query, "reviewNvkeOperatorInstallPlanWorkload(") {
			workload := nvkeWorkloadTestFixture()
			workload.Version++
			writePipelineTestResponse(t, w, map[string]any{"reviewNvkeOperatorInstallPlanWorkload": map[string]any{"ok": true, "data": workload}})
			return
		}
		if strings.Contains(req.Query, "InstallPlanImage(") {
			captures++
		}
		nvkeImageTestReply(t, req, w, nvkeWorkloadImageFixture())
	})
	defer server.Close()
	if runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review") == nil || captures != 0 {
		t.Fatal("changed inventory allowed acquisition")
	}
}

func TestNVKEWorkloadImagePrivateBodyMustMatchBeforeJournal(t *testing.T) {
	for _, linked := range []bool{false, true} {
		command := nvkeWorkloadImageCommand(t, "prepare")
		var body map[string]any
		if err := json.Unmarshal([]byte(nvkeImageTestBody()), &body); err != nil {
			t.Fatal(err)
		}
		if linked {
			body["workloadId"], body["workloadDigest"] = nvkeWorkloadTestID, strings.Repeat("9", 64)
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		input, journal := filepath.Join(dir, "input.json"), filepath.Join(dir, "journal.json")
		if err = privatefile.Write(input, data); err != nil {
			t.Fatal(err)
		}
		_ = command.Flags().Set("file", input)
		_ = command.Flags().Set("request-file", journal)
		captures := 0
		server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
			if strings.Contains(req.Query, "prepareNvkeOperatorInstallPlanImage(") {
				captures++
			}
			nvkeWorkloadImageReply(t, req, w, nvkeWorkloadImageFixture())
		})
		err = runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare")
		server.Close()
		if err == nil || captures != 0 {
			t.Fatal("mismatched private workload intent accepted")
		}
		if _, err = privatefile.ReadFile(journal, 16384); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid intent wrote journal")
		}
	}
}

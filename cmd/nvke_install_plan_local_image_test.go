package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/privatefile"
)

func nvkeLocalImageTestBody(t *testing.T) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(nvkeImageTestBody()), &body); err != nil {
		t.Fatal(err)
	}
	delete(body, "policy")
	delete(body, "maximumCalls")
	body["sourceKind"] = "local"
	body["reference"] = "private.example.test/local@sha256:" + strings.Repeat("4", 64)
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestNVKELocalImagePrepareAndHistoricalRecovery(t *testing.T) {
	for _, verb := range []string{"prepare", "review", "retry-preparation"} {
		t.Run(verb, func(t *testing.T) {
			command, out := nvkeImageTestCommand(t, verb)
			_ = command.Flags().Set("source-kind", "local")
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
			body := nvkeLocalImageTestBody(t)
			if verb == "prepare" {
				if err := privatefile.Write(input, []byte(body)); err != nil {
					t.Fatal(err)
				}
				_ = command.Flags().Set("file", input)
				_ = command.Flags().Set("request-file", journal)
			}
			calls := 0
			server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
				if strings.Contains(req.Query, "InstallPlanImage(") || strings.Contains(req.Query, "InstallPlanImagePreparation(") {
					calls++
					if !strings.Contains(req.Query, "sourceKind") {
						t.Error("source kind missing from contract")
					}
					if verb == "prepare" {
						data, err := privatefile.ReadFile(journal, 16384)
						var saved nvkeImageJournal
						if err != nil || decodeNVKEImageStrict(data, &saved) != nil || saved.SourceKind != "local" || saved.InputSHA256 != nvkeParameterDigest([]byte(body)) || strings.Contains(string(data), "private.example.test") {
							t.Error("local journal did not bind private intent")
						}
						if req.Variables["input"].(map[string]any)["requestText"] != body {
							t.Error("local intent changed")
						}
					} else if _, ok := req.Variables["input"].(map[string]any)["requestText"]; ok {
						t.Error("recovery replaced intent")
					}
				}
				row := nvkeImageTestFixture()
				row.SourceKind = "local"
				nvkeImageTestReply(t, req, w, row)
			})
			defer server.Close()
			if err := runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, verb); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || !strings.Contains(out.String(), `"sourceKind": "local"`) {
				t.Fatal("missing local recovery or repeated request")
			}
		})
	}
}

func TestNVKELocalImageMalformedInputNeverPublishesIntent(t *testing.T) {
	changes := map[string]func(map[string]any){
		"policy":          func(b map[string]any) { b["policy"] = "reviewed" },
		"calls":           func(b map[string]any) { b["maximumCalls"] = 0 },
		"unknown-kind":    func(b map[string]any) { b["sourceKind"] = "registry" },
		"missing-kind":    func(b map[string]any) { delete(b, "sourceKind") },
		"tag":             func(b map[string]any) { b["reference"] = "private.example.test/local:v1" },
		"wrong-digest":    func(b map[string]any) { b["reference"] = "private.example.test/local@sha256:short" },
		"missing-timeout": func(b map[string]any) { delete(b, "timeoutSeconds") },
		"null-timeout":    func(b map[string]any) { b["timeoutSeconds"] = nil },
		"missing-bytes":   func(b map[string]any) { delete(b, "maximumBytes") },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			command, out := nvkeImageTestCommand(t, "prepare")
			_ = command.Flags().Set("source-kind", "local")
			var body map[string]any
			if err := json.Unmarshal([]byte(nvkeLocalImageTestBody(t)), &body); err != nil {
				t.Fatal(err)
			}
			change(body)
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
			if err = privatefile.Write(input, data); err != nil {
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
			if err = runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || calls != 0 || out.Len() != 0 {
				t.Fatal("malformed local input crossed mutation")
			}
			if _, err = os.Stat(journal); !os.IsNotExist(err) {
				t.Fatal("malformed local input published journal")
			}
		})
	}
}

func TestNVKEImageSourceKindAndLocalRootCannotChange(t *testing.T) {
	for _, kind := range []string{"registry", "local"} {
		for _, reply := range []string{"registry", "local", "other"} {
			if kind == reply {
				continue
			}
			t.Run(kind+"-"+reply, func(t *testing.T) {
				command, out := nvkeImageTestCommand(t, "review")
				_ = command.Flags().Set("source-kind", kind)
				server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
					row := nvkeImageTestFixture()
					row.SourceKind = reply
					nvkeImageTestReply(t, req, w, row)
				})
				defer server.Close()
				if err := runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "review"); err == nil || out.Len() != 0 {
					t.Fatal("wrong source kind exposed")
				}
			})
		}
	}
	command, out := nvkeImageTestCommand(t, "prepare")
	_ = command.Flags().Set("source-kind", "local")
	input, journal := filepath.Join(t.TempDir(), "input.json"), filepath.Join(t.TempDir(), "journal.json")
	if err := privatefile.Write(input, []byte(nvkeLocalImageTestBody(t))); err != nil {
		t.Fatal(err)
	}
	_ = command.Flags().Set("file", input)
	_ = command.Flags().Set("request-file", journal)
	server := nvkeTestServer(t, func(req gqlRequest, w http.ResponseWriter) {
		row := nvkeImageTestFixture()
		row.SourceKind = "local"
		row.RootDigest = "sha256:" + strings.Repeat("5", 64)
		nvkeImageTestReply(t, req, w, row)
	})
	defer server.Close()
	if err := runNVKEAgentInstallPlanImage(command, context.Background(), nvkeTestClient(server), nvkeTestOperation, "prepare"); err == nil || out.Len() != 0 {
		t.Fatal("wrong local root exposed")
	}
}

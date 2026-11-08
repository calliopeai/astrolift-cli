package cmd

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/nvkecatalog"
	"github.com/spf13/cobra"
)

func agentTestCommand(t *testing.T, verb string) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd, _, err := newNVKEAgentCommand().Find([]string{verb})
	if err != nil {
		t.Fatal(err)
	}
	cmd.SetContext(context.Background())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	values := map[string]string{}
	if verb != "catalog" && verb != "recover" {
		values["site"] = nvkeTestSite
	}
	if verb != "catalog" && verb != "recover" && verb != "get" {
		values["site-version"] = "7"
		values["authority-version"] = "3"
	}
	if verb == "configure" {
		values["endpoint"] = "https://agent.test"
		values["instance"] = nvkeNativeTestSource
		values["bundle-sha256"] = strings.Repeat("a", 64)
	}
	if verb == "request" {
		values["request-file"] = filepath.Join(t.TempDir(), "recovery.json")
	}
	for key, value := range values {
		if err = cmd.Flags().Set(key, value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd, out
}
func agentTestMetadata(verb string) map[string]any {
	row := map[string]any{"id": nvkeTestOperation, "version": 4, "siteId": nvkeTestSite, "siteVersion": 8, "endpoint": "https://agent.test", "instanceId": nvkeNativeTestSource, "bundleManifestSha256": strings.Repeat("a", 64), "isEnabled": verb == "configure" || verb == "enable" || verb == "verify" || verb == "get", "timeoutSeconds": 30, "hasCredentials": true}
	if verb == "get" || verb == "verify" {
		row["version"], row["siteVersion"] = 3, 7
	}
	if verb == "verify" {
		row["verifiedAt"], row["verificationCode"] = "2026-11-08T00:00:00Z", "AGENT_VERIFIED"
	}
	if verb == "remove" {
		row["deletedAt"] = "2026-11-08T00:00:00Z"
	}
	return row
}
func agentTestData() map[string]any {
	data := nvkeBaseData()
	authority := agentTestMetadata("verify")
	data["astroliftNvkeOperatorAuthority"] = authority
	data["astroliftNvkeOperatorContract"] = map[string]any{"revision": nvkecatalog.Pinned().Revision, "commands": []map[string]any{{"key": "generate.sitespec", "positionals": []string{"string"}, "options": []any{}}}}
	return data
}
func TestNVKEAgentAuthorityWritesKeepReviewedScopeAndSecretsPrivate(t *testing.T) {
	for _, verb := range []string{"configure", "verify", "enable", "disable", "remove"} {
		t.Run(verb, func(t *testing.T) {
			cmd, out := agentTestCommand(t, verb)
			if verb == "configure" {
				_ = cmd.Flags().Set("credentials-file", "-")
				cmd.SetIn(strings.NewReader(`{"clientCertificatePem":"PRIVATE_CERT","clientKeyPem":"PRIVATE_KEY"}`))
			}
			writes := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nvkeBaseData()
				if strings.HasPrefix(request.Query, "mutation") {
					writes++
					input := request.Variables["input"].(map[string]any)
					if input["siteId"] != nvkeTestSite || input["expectedActorId"] != float64(42) || input["expectedSiteVersion"] != float64(7) || input["expectedVersion"] != float64(3) {
						t.Error("reviewed agent scope changed")
					}
					if verb == "configure" && input["credentials"].(map[string]any)["clientKeyPem"] != "PRIVATE_KEY" {
						t.Error("mTLS credentials changed")
					}
					field := map[string]string{"configure": "configureNvkeOperatorAuthority", "verify": "verifyNvkeOperatorAuthority", "enable": "setNvkeOperatorAuthorityEnabled", "disable": "setNvkeOperatorAuthorityEnabled", "remove": "removeNvkeOperatorAuthority"}[verb]
					row := agentTestMetadata(verb)
					row["clientKeyPem"] = "PRIVATE_BAD_ECHO"
					data[field] = map[string]any{"ok": true, "data": row}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			var err error
			if verb == "configure" {
				err = runNVKEAgentConfigure(cmd, context.Background(), nvkeTestClient(server), nil)
			} else {
				err = runNVKEAgentReview(cmd, context.Background(), nvkeTestClient(server), verb)
			}
			if err != nil || writes != 1 || strings.Contains(out.String(), "PRIVATE") {
				t.Fatalf("unsafe agent change writes=%d err=%v output=%s", writes, err, out.String())
			}
		})
	}
}
func TestNVKEAgentRejectsUnsafeConfigurationBeforeNetwork(t *testing.T) {
	for _, test := range []struct{ flag, value, credentials string }{
		{"endpoint", "https://PRIVATE_USER:PRIVATE_PASSWORD@agent.test", ""},
		{"endpoint", "http://agent.test", ""}, {"endpoint", "https://agent.test/path", ""},
		{"instance", "PRIVATE_BAD_INSTANCE", ""}, {"bundle-sha256", "PRIVATE_BAD_DIGEST", ""},
		{"credentials-file", "-", `{"token":"PRIVATE_TOKEN"}`},
		{"credentials-file", "-", `{"clientKeyPem":"PRIVATE_KEY"}`},
	} {
		t.Run(test.flag+test.value, func(t *testing.T) {
			cmd, out := agentTestCommand(t, "configure")
			_ = cmd.Flags().Set(test.flag, test.value)
			cmd.SetIn(strings.NewReader(test.credentials))
			requests := 0
			server := nvkeTestServer(t, func(_ gqlRequest, w http.ResponseWriter) { requests++; writePipelineTestResponse(t, w, nvkeBaseData()) })
			defer server.Close()
			err := runNVKEAgentConfigure(cmd, context.Background(), nvkeTestClient(server), nil)
			if err == nil || requests != 0 || strings.Contains(err.Error()+out.String(), "PRIVATE") {
				t.Fatalf("unsafe config requests=%d err=%v", requests, err)
			}
		})
	}
}
func agentTestInput(t *testing.T, cmd *cobra.Command) string {
	t.Helper()
	text := `{"format":1,"revision":"` + nvkecatalog.Pinned().Revision + `","siteId":"` + nvkeTestSite + `","command":"generate.sitespec","positionals":["private-site"],"options":{"nodes":"input:nodes.csv"},"files":{"nodes.csv":"PRIVATE_NODES_9007199254740993"}}`
	file := filepath.Join(t.TempDir(), "native.json")
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Flags().Set("file", file)
	return text
}
func TestNVKEAgentLostAdmissionReplyRecoversWithoutInputOrAnotherMutation(t *testing.T) {
	cmd, out := agentTestCommand(t, "request")
	text := agentTestInput(t, cmd)
	filename := resourceStringFlag(cmd, "request-file")
	writes := 0
	var receipt map[string]any
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := agentTestData()
		if strings.Contains(request.Query, "astroliftNvkeRequest") {
			data["astroliftNvkeRequest"] = receipt
		}
		if strings.Contains(request.Query, "requestNvkeOperatorJob") {
			writes++
			saved, err := readNVKEAgentRecovery(filename)
			if err != nil {
				t.Error("recovery not durable before admission")
			}
			input := request.Variables["input"].(map[string]any)
			if input["requestText"] != text || input["requestId"] != saved.RequestID || input["expectedAuthorityVersion"] != float64(3) {
				t.Error("native envelope or reviewed identity changed")
			}
			receipt = map[string]any{"id": nvkeTestOperation, "siteId": nvkeTestSite, "requestId": saved.RequestID, "operationKey": "tooling.generate.sitespec", "version": 1, "status": "QUEUED"}
			http.Error(w, "PRIVATE_LOST_REPLY", http.StatusServiceUnavailable)
			return
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	if err := runNVKEAgentRequest(cmd, context.Background(), nvkeTestClient(server), false); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("lost response unsafe: %v", err)
	}
	recovery, err := os.ReadFile(filename)
	if err != nil || bytes.Contains(recovery, []byte("PRIVATE_NODES")) {
		t.Fatal("recovery retained private input")
	}
	recovered, out2 := agentTestCommand(t, "recover")
	_ = recovered.Flags().Set("request-file", filename)
	if err = runNVKEAgentRequest(recovered, context.Background(), nvkeTestClient(server), true); err != nil || writes != 1 || strings.Contains(out.String()+out2.String(), "PRIVATE") {
		t.Fatalf("recovery replayed or leaked writes=%d err=%v", writes, err)
	}
}
func TestNVKEAgentPendingRecoveryRefusesChangedInputAndRecoverNeverAdmits(t *testing.T) {
	cmd, _ := agentTestCommand(t, "request")
	originalText := agentTestInput(t, cmd)
	filename := resourceStringFlag(cmd, "request-file")
	writes := 0
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := agentTestData()
		data["astroliftNvkeRequest"] = nil
		if strings.Contains(request.Query, "requestNvkeOperatorJob") {
			writes++
			http.Error(w, "PRIVATE_LOST_REPLY", http.StatusServiceUnavailable)
			return
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	_ = runNVKEAgentRequest(cmd, context.Background(), nvkeTestClient(server), false)
	recovery, _ := agentTestCommand(t, "recover")
	_ = recovery.Flags().Set("request-file", filename)
	if err := runNVKEAgentRequest(recovery, context.Background(), nvkeTestClient(server), true); err == nil || writes != 1 {
		t.Fatal("recover admitted work")
	}
	_ = os.WriteFile(resourceStringFlag(cmd, "file"), []byte(`{"PRIVATE":"changed"}`), 0600)
	if err := runNVKEAgentRequest(cmd, context.Background(), nvkeTestClient(server), false); err == nil || writes != 1 {
		t.Fatal("changed input replaced original intent")
	}
	if err := os.WriteFile(resourceStringFlag(cmd, "file"), []byte(originalText), 0600); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []struct{ flag, value, original string }{
		{"site-version", "8", "7"}, {"authority-version", "4", "3"},
		{"operation-timeout", "60", "7200"}, {"acknowledge-infrastructure-change", "true", "false"},
	} {
		if err := cmd.Flags().Set(changed.flag, changed.value); err != nil {
			t.Fatal(err)
		}
		if err := runNVKEAgentRequest(cmd, context.Background(), nvkeTestClient(server), false); err == nil || writes != 1 {
			t.Fatalf("changed original %s admitted replacement work", changed.flag)
		}
		if err := cmd.Flags().Set(changed.flag, changed.original); err != nil {
			t.Fatal(err)
		}
	}
}
func TestNVKEAgentArtifactChunksAreBoundAndOnlyCompleteHashesSucceed(t *testing.T) {
	content := []byte("PRIVATE_CONTENT")
	sum := sha256.Sum256(content)
	digest := hex.EncodeToString(sum[:])
	for _, mode := range []string{"good", "wrong-id", "wrong-name", "wrong-range", "wrong-hash", "changed-hash", "too-large", "no-progress"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				calls++
				input := request.Variables["input"].(map[string]any)
				offset := input["offset"].(string)
				start := 0
				if offset != "0" {
					start = 2
				}
				end := len(content)
				if start == 0 {
					end = 2
				}
				chunk := map[string]any{"id": nvkeTestOperation, "name": "stdout", "offset": offset, "nextOffset": end, "size": len(content), "sha256": digest, "dataBase64": base64.StdEncoding.EncodeToString(content[start:end])}
				chunk["nextOffset"] = strconv.Itoa(end)
				chunk["size"] = strconv.Itoa(len(content))
				switch mode {
				case "wrong-id":
					chunk["id"] = nvkeTestSite
				case "wrong-name":
					chunk["name"] = "other"
				case "wrong-range":
					chunk["offset"] = "1"
				case "wrong-hash":
					chunk["sha256"] = strings.Repeat("b", 64)
				case "changed-hash":
					if calls > 1 {
						chunk["sha256"] = strings.Repeat("b", 64)
					}
				case "too-large":
					chunk["size"] = "9999999"
				case "no-progress":
					chunk["nextOffset"], chunk["dataBase64"] = "0", ""
				}
				text, _ := json.Marshal(chunk)
				writePipelineTestResponse(t, w, map[string]any{"astroliftNvkeOperatorArtifactText": map[string]any{"ok": true, "data": string(text)}})
			})
			defer server.Close()
			output := filepath.Join(t.TempDir(), "artifact")
			input := api.NVKEAgentArtifactInput{NVKEAgentJobReview: api.NVKEAgentJobReview{OperationID: nvkeTestOperation}, Name: "stdout", Limit: 262144}
			size, got, err := downloadNVKEAgentArtifact(context.Background(), nvkeTestClient(server), input, output, 1024)
			if mode == "good" {
				data, readErr := os.ReadFile(output)
				if err != nil || readErr != nil || !bytes.Equal(data, content) || size != int64(len(content)) || got != digest || calls != 2 {
					t.Fatalf("valid artifact refused: %v", err)
				}
			} else if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("unsafe artifact mode=%s err=%v", mode, err)
			}
			previous := calls
			if _, _, err = downloadNVKEAgentArtifact(context.Background(), nvkeTestClient(server), input, output, 1024); err == nil || calls != previous {
				t.Fatal("artifact overwrote an existing path")
			}
		})
	}
}

func TestNVKEAgentInfrastructureAcknowledgementPrecedesRecoveryCreation(t *testing.T) {
	cmd, _ := agentTestCommand(t, "request")
	agentTestInput(t, cmd)
	writes := 0
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := agentTestData()
		data["astroliftNvkeOperatorContract"].(map[string]any)["commands"].([]map[string]any)[0]["infrastructureChange"] = true
		if strings.HasPrefix(request.Query, "mutation") {
			writes++
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	if err := runNVKEAgentRequest(cmd, context.Background(), nvkeTestClient(server), false); err == nil || writes != 0 {
		t.Fatal("unacknowledged infrastructure admitted")
	}
	if _, err := os.Stat(resourceStringFlag(cmd, "request-file")); !os.IsNotExist(err) {
		t.Fatal("unreviewed infrastructure persisted a recovery identity")
	}
}

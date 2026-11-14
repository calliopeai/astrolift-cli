package cmd

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

const nvkeNativeTestSource = "123e4567-e89b-12d3-a456-426614174004"

func nativeTestCommand(t *testing.T) (*cobra.Command, *bytes.Buffer, string) {
	t.Helper()
	cmd := newNVKENativeRequestCommand("request WORKFLOW", "")
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", true, "")
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	file := filepath.Join(t.TempDir(), "native-recovery.json")
	for flag, value := range map[string]string{"site": nvkeTestSite, "site-version": "7", "authority-version": "3", "source-operation": nvkeNativeTestSource, "source-version": "9", "request-file": file} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	nvkeTestInput(t, cmd, `{"clusterID":"123e4567-e89b-12d3-a456-426614174005","thresholdDays":120,"force":true}`)
	return cmd, out, file
}

func nativeTestData() map[string]any {
	data := nvkeBaseData()
	authority := temporalTestMetadata("get")
	authority["version"] = 3
	authority["siteVersion"] = 7
	authority["isEnabled"] = true
	data["astroliftNvkeTemporalAuthority"] = authority
	data["astroliftNvkeOperation"] = map[string]any{"id": nvkeNativeTestSource, "version": 9, "siteId": nvkeTestSite, "operationKey": "getCluster", "status": "SUCCEEDED"}
	data["astroliftNvkeRequest"] = nil
	data["astroliftNvkeContract"].(map[string]any)["nativeWorkflows"] = []map[string]any{{"name": "RenewK8sCertsV2", "permission": "nvke.admin", "inputSchema": map[string]any{"type": "object"}}}
	return data
}

func nativeTestReceipt(input map[string]interface{}) map[string]any {
	return map[string]any{"id": nvkeTestOperation, "version": 1, "siteId": input["siteId"], "requestId": input["requestId"], "operationKey": "temporal." + input["workflow"].(string), "status": "AWAITING_READY", "stage": "native_workflow_running"}
}

func TestNVKENativeLostReplyRetainsDurableExactTuple(t *testing.T) {
	cmd, out, file := nativeTestCommand(t)
	inputs := []map[string]interface{}{}
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := nativeTestData()
		if strings.Contains(request.Query, "requestNvkeNativeWorkflow(") {
			input := request.Variables["input"].(map[string]interface{})
			inputs = append(inputs, input)
			saved, err := readNVKENativeRecovery(file)
			if err != nil || saved.RequestID != input["requestId"] || saved.AuthorityVersion != 3 || saved.SourceOperationID != nvkeNativeTestSource || saved.SourceVersion != 9 {
				t.Error("native admission preceded durable original review")
			}
			if input["expectedActorId"] != float64(42) || input["expectedSiteVersion"] != float64(7) || input["expectedAuthorityVersion"] != float64(3) || input["expectedSourceVersion"] != float64(9) || input["sourceOperationId"] != nvkeNativeTestSource || !strings.Contains(input["parametersText"].(string), `"thresholdDays":120`) {
				t.Error("native reviewed tuple changed")
			}
			if len(inputs) == 1 {
				w.WriteHeader(503)
				_, _ = w.Write([]byte("PRIVATE_NATIVE_REPLY"))
				return
			}
			data["requestNvkeNativeWorkflow"] = map[string]any{"ok": true, "data": nativeTestReceipt(input)}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	c := nvkeTestClient(server)
	if err := runNVKENativeRequest(cmd, context.Background(), c, "RenewK8sCertsV2"); err == nil || !strings.Contains(err.Error(), "preserve") || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("unsafe lost reply: %v", err)
	}
	before, err := privatefile.ReadFile(file, 16384)
	if err != nil {
		t.Fatal(err)
	}
	if err = runNVKENativeRequest(cmd, context.Background(), c, "RenewK8sCertsV2"); err != nil {
		t.Fatal(err)
	}
	after, _ := privatefile.ReadFile(file, 16384)
	if len(inputs) != 2 || !reflect.DeepEqual(inputs[0], inputs[1]) || !bytes.Equal(before, after) {
		t.Fatal("retry replaced native request identity or input")
	}
	if strings.Contains(string(before), "clusterID") || strings.Contains(string(before), "thresholdDays") || strings.Contains(out.String(), "PRIVATE") {
		t.Fatal("native input escaped into recovery or output")
	}
}

func TestNVKENativePendingRecoveryRefusesChangedTuple(t *testing.T) {
	for _, change := range []string{"actor", "site-version", "authority-version", "source-version", "source-operation", "operation-timeout", "acknowledge-lost-node-teardown", "input", "malformed", "incomplete-receipt"} {
		t.Run(change, func(t *testing.T) {
			cmd, _, file := nativeTestCommand(t)
			mutations := 0
			changed := false
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nativeTestData()
				if changed && change == "actor" {
					data["astroliftMyProfile"] = map[string]any{"userId": 43}
				}
				if changed && change == "incomplete-receipt" {
					delete(data, "astroliftNvkeRequest")
				}
				if strings.HasPrefix(request.Query, "mutation") {
					mutations++
					w.WriteHeader(503)
					return
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			c := nvkeTestClient(server)
			if runNVKENativeRequest(cmd, context.Background(), c, "RenewK8sCertsV2") == nil || mutations != 1 {
				t.Fatal("first lost reply did not retain a pending request")
			}
			changed = true
			switch change {
			case "actor", "incomplete-receipt":
			case "input":
				nvkeTestInput(t, cmd, `{"clusterID":"PRIVATE_CHANGED"}`)
			case "malformed":
				if err := os.WriteFile(file, []byte(`{"PRIVATE":"incomplete"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "source-operation":
				_ = cmd.Flags().Set(change, nvkeTestOperation)
			case "acknowledge-lost-node-teardown":
				_ = cmd.Flags().Set(change, "true")
			default:
				_ = cmd.Flags().Set(change, "60")
			}
			if err := runNVKENativeRequest(cmd, context.Background(), c, "RenewK8sCertsV2"); err == nil || mutations != 1 || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("pending native review changed: %v", err)
			}
		})
	}
}

func TestNVKENativeReceiptRecoveryNeverReadsInputOrResubmits(t *testing.T) {
	cmd, out, file := nativeTestCommand(t)
	var receipt map[string]any
	mutations := 0
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := nativeTestData()
		data["astroliftNvkeRequest"] = receipt
		if strings.HasPrefix(request.Query, "mutation") {
			mutations++
			receipt = nativeTestReceipt(request.Variables["input"].(map[string]interface{}))
			w.WriteHeader(503)
			return
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	c := nvkeTestClient(server)
	if runNVKENativeRequest(cmd, context.Background(), c, "RenewK8sCertsV2") == nil {
		t.Fatal("lost reply expected")
	}
	_ = cmd.Flags().Set("file", filepath.Join(t.TempDir(), "missing-private-input.json"))
	if err := runNVKENativeRequest(cmd, context.Background(), c, "RenewK8sCertsV2"); err != nil {
		t.Fatal(err)
	}
	recover, _, err := newNVKENativeWorkflowCommand().Find([]string{"recover"})
	if err != nil {
		t.Fatal(err)
	}
	recover.SetOut(out)
	recover.SetContext(context.Background())
	recover.SetErr(out)
	_ = recover.Flags().Set("request-file", file)
	if err = runNVKENativeRecover(recover, context.Background(), c, nil); err != nil {
		t.Fatal(err)
	}
	if mutations != 1 {
		t.Fatal("receipt lookup resubmitted native work")
	}
}

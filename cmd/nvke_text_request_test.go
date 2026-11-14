package cmd

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestNVKETextAdmissionPreservesExactUTF8AndRecoversReceiptBeforeReadingInput(t *testing.T) {
	cmd, out, diagnostics, file := nvkeTestCommand(t)
	raw := "{\n  \"document\": {\"count\":9007199254740993,\"private\":\"PRIVATE_INPUT\",\"label\":\"café\"}\n}\n"
	inputPath := nvkeTestInput(t, cmd, raw)
	mutations := 0
	var accepted map[string]any
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := nvkeBaseData()
		if strings.Contains(request.Query, "requestNvkeOperationText(") {
			mutations++
			input := request.Variables["input"].(map[string]interface{})
			if input["parametersText"] != raw || input["expectedActorId"] != float64(42) {
				t.Error("reviewed text/actor changed")
			}
			saved, err := readReviewedRequest(file)
			if err != nil || saved.Kind != nvkeTextRequestKind || saved.RequestID != input["requestId"] {
				t.Error("write preceded durable identity")
			}
			accepted = nvkeTestResult(input, "QUEUED")
			accepted["isInventory"] = false
			data["requestNvkeOperationText"] = map[string]any{"ok": true, "data": nil}
		}
		if strings.Contains(request.Query, "astroliftNvkeRequest(") {
			data["astroliftNvkeRequest"] = accepted
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	c := nvkeTestClient(server)
	if err := runNVKERequest(cmd, context.Background(), c, "datastore.createMachineImage", false); err == nil {
		t.Fatal("lost admission reply must fail")
	}
	saved, _ := readReviewedRequest(file)
	if saved.ExpectedSource != nvkeParameterDigest([]byte(raw)) {
		t.Fatal("digest does not bind exact text")
	}
	if err := os.Remove(inputPath); err != nil {
		t.Fatal(err)
	}
	if err := runNVKERequest(cmd, context.Background(), c, "datastore.createMachineImage", false); err != nil {
		t.Fatal(err)
	}
	if mutations != 1 || strings.Contains(out.String()+diagnostics.String(), "PRIVATE_INPUT") {
		t.Fatal("recovery replayed or leaked native input")
	}
	data, _ := os.ReadFile(file)
	if strings.Contains(string(data), "PRIVATE_INPUT") {
		t.Fatal("native input stored in recovery")
	}
}
func TestNVKEPendingTextCannotChangeAndReusesOriginalNonce(t *testing.T) {
	cmd, _, _, file := nvkeTestCommand(t)
	inputPath := nvkeTestInput(t, cmd, "{ \"body\": {} }\n")
	var requests []string
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := nvkeBaseData()
		if strings.Contains(request.Query, "astroliftNvkeRequest(") {
			data["astroliftNvkeRequest"] = nil
		}
		if strings.Contains(request.Query, "requestNvkeOperationText(") {
			input := request.Variables["input"].(map[string]interface{})
			requests = append(requests, input["requestId"].(string))
			data["requestNvkeOperationText"] = map[string]any{"ok": true, "data": nil}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	c := nvkeTestClient(server)
	_ = runNVKERequest(cmd, context.Background(), c, "listClusters", false)
	original, _ := readReviewedRequest(file)
	if err := os.WriteFile(inputPath, []byte(`{"body":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runNVKERequest(cmd, context.Background(), c, "listClusters", false); err == nil || len(requests) != 1 {
		t.Fatal("changed text replaced pending request")
	}
	if err := os.WriteFile(inputPath, []byte("{ \"body\": {} }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = runNVKERequest(cmd, context.Background(), c, "listClusters", false)
	if len(requests) != 2 || requests[0] != original.RequestID || requests[1] != requests[0] {
		t.Fatal("original nonce was not retained")
	}
}
func TestNVKEInventoryAndResourceModesHaveDistinctBoundReceipts(t *testing.T) {
	for _, mode := range []string{"inventory", "resource"} {
		t.Run(mode, func(t *testing.T) {
			cmd, _, _, file := nvkeTestCommand(t)
			if mode == "inventory" {
				_ = cmd.Flags().Set("inventory", "true")
			} else {
				_ = cmd.Flags().Set("source-operation", nvkeTestOperation)
				_ = cmd.Flags().Set("source-version", "7")
				_ = cmd.Flags().Set("resource-key", "signed-key")
			}
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nvkeBaseData()
				field := "requestNvkeInventory"
				if mode == "resource" {
					field = "requestNvkeResourceOperation"
				}
				if strings.Contains(request.Query, field+"(") {
					input := request.Variables["input"].(map[string]interface{})
					row := nvkeTestResult(input, "QUEUED")
					row["isInventory"] = mode == "inventory"
					if mode == "resource" {
						if input["sourceOperationId"] != nvkeTestOperation || input["expectedSourceVersion"] != float64(7) || input["sourceResourceKey"] != "signed-key" {
							t.Error("source binding changed")
						}
						row["resourceSource"] = map[string]any{"operationId": nvkeTestOperation, "version": 7, "resourceKey": "signed-key"}
					}
					data[field] = map[string]any{"ok": true, "data": row}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			if err := runNVKERequest(cmd, context.Background(), nvkeTestClient(server), "listClusters", false); err != nil {
				t.Fatal(err)
			}
			saved, _ := readReviewedRequest(file)
			if (saved.Kind == nvkeInventoryRequestKind) != (mode == "inventory") {
				t.Fatal("mode lost")
			}
			if mode == "resource" && (saved.NVKESource == nil || saved.NVKESource.Version != 7) {
				t.Fatal("source not persisted")
			}
		})
	}
}
func TestNVKEWrongSourceReceiptCannotReplacePendingRecovery(t *testing.T) {
	cmd, _, _, file := nvkeTestCommand(t)
	_ = cmd.Flags().Set("source-operation", nvkeTestOperation)
	_ = cmd.Flags().Set("source-version", "7")
	var row map[string]any
	mutations := 0
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := nvkeBaseData()
		if strings.Contains(request.Query, "requestNvkeResourceOperation(") {
			mutations++
			input := request.Variables["input"].(map[string]interface{})
			row = nvkeTestResult(input, "QUEUED")
			row["resourceSource"] = map[string]any{"operationId": nvkeTestOperation, "version": 8, "resourceKey": nil}
			data["requestNvkeResourceOperation"] = map[string]any{"ok": true, "data": nil}
		}
		if strings.Contains(request.Query, "astroliftNvkeRequest(") {
			data["astroliftNvkeRequest"] = row
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	c := nvkeTestClient(server)
	_ = runNVKERequest(cmd, context.Background(), c, "listClusters", false)
	original, _ := os.ReadFile(file)
	if err := runNVKERequest(cmd, context.Background(), c, "listClusters", false); err == nil {
		t.Fatal("wrong source receipt adopted")
	}
	current, _ := os.ReadFile(file)
	if string(original) != string(current) || mutations != 1 {
		t.Fatal("pending identity changed")
	}
}

func TestNVKEEmptySourceFlagsCannotDowngradeToOrdinaryAdmission(t *testing.T) {
	for _, flag := range []string{"source-operation", "source-version", "resource-key"} {
		t.Run(flag, func(t *testing.T) {
			cmd, _, _, _ := nvkeTestCommand(t)
			value := ""
			if flag == "source-version" {
				value = "0"
			}
			_ = cmd.Flags().Set(flag, value)
			calls := 0
			server := nvkeTestServer(t, func(_ gqlRequest, w http.ResponseWriter) { calls++; writePipelineTestResponse(t, w, nvkeBaseData()) })
			defer server.Close()
			if err := runNVKERequest(cmd, context.Background(), nvkeTestClient(server), "listClusters", false); err == nil || calls != 0 {
				t.Fatal("explicit invalid source flags downgraded to an unbound request")
			}
		})
	}
}
func TestNVKETextRejectsInvalidUTF8AndTrailingDocumentsBeforeAdmission(t *testing.T) {
	for _, raw := range []string{string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'}), "{} {}", "[]"} {
		cmd, _, _, file := nvkeTestCommand(t)
		nvkeTestInput(t, cmd, raw)
		writes := 0
		server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
			if strings.Contains(request.Query, "mutation") {
				writes++
			}
			writePipelineTestResponse(t, w, nvkeBaseData())
		})
		err := runNVKERequest(cmd, context.Background(), nvkeTestClient(server), "listClusters", false)
		server.Close()
		if err == nil || writes != 0 {
			t.Fatal("invalid text admitted")
		}
		if _, statErr := os.Stat(file); !os.IsNotExist(statErr) {
			t.Fatal("invalid text persisted a new identity")
		}
	}
}

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func nvkeMaintenanceTestCommand(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := newNVKEMaintenanceCommand()
	cmd.SetContext(context.Background())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	for flag, value := range map[string]string{"site": nvkeTestSite, "version": "9"} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	return cmd, out
}

func maintenanceTestReply() map[string]any {
	return map[string]any{
		"siteId": nvkeTestSite, "operationId": nvkeTestOperation, "operationVersion": 9,
		"clusterId": nvkeTestOrg, "renewalScheduleOverdueAtObservation": nil,
		"mutationLockReported": true,
		"renewal":              map[string]any{"workflowId": "operator-更新-cycle", "state": "failed", "forced": false, "outcome": nil, "retainedMutationLock": true},
		"etcdKeyRotation":      nil, "privateUnknownField": "PRIVATE_NATIVE_KEY",
	}
}

func TestNVKEMaintenancePreservesUnknownAndFalseWithoutNativeWork(t *testing.T) {
	cmd, out := nvkeMaintenanceTestCommand(t)
	calls := 0
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		calls++
		data := nvkeBaseData()
		if strings.Contains(request.Query, "astroliftNvkeMaintenanceObservation(") {
			if request.Variables["site"] != nvkeTestSite || request.Variables["id"] != nvkeTestOperation || request.Variables["actor"] != float64(42) || request.Variables["version"] != float64(9) {
				t.Error("reviewed maintenance scope changed")
			}
			data["astroliftNvkeMaintenanceObservation"] = maintenanceTestReply()
		} else if !strings.Contains(request.Query, "astroliftMyProfile") {
			t.Error("maintenance attempted work outside identity and saved observation reads")
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	if err := runNVKEMaintenance(cmd, context.Background(), nvkeTestClient(server), []string{nvkeTestOperation}); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	renewal := result["renewal"].(map[string]any)
	if calls != 2 || result["renewalScheduleOverdueAtObservation"] != nil || result["etcdKeyRotation"] != nil || renewal["forced"] != false || renewal["outcome"] != nil || renewal["retainedMutationLock"] != true || renewal["workflowId"] != "operator-更新-cycle" || strings.Contains(out.String(), "PRIVATE") {
		t.Fatalf("maintenance values changed or leaked: %s", out.String())
	}
}

func TestNVKEMaintenanceInvalidInputDoesNotRead(t *testing.T) {
	for _, changed := range []string{"site", "operation", "version"} {
		t.Run(changed, func(t *testing.T) {
			cmd, _ := nvkeMaintenanceTestCommand(t)
			id := nvkeTestOperation
			switch changed {
			case "operation":
				id = "invalid"
			case "site":
				_ = cmd.Flags().Set("site", "invalid")
			case "version":
				_ = cmd.Flags().Set("version", "0")
			}
			calls := 0
			server := nvkeTestServer(t, func(_ gqlRequest, _ http.ResponseWriter) { calls++ })
			defer server.Close()
			if err := runNVKEMaintenance(cmd, context.Background(), nvkeTestClient(server), []string{id}); err == nil || calls != 0 {
				t.Fatal("invalid review reached network")
			}
		})
	}
}

func TestNVKEMaintenanceRejectsMismatchedAndFailedReplies(t *testing.T) {
	for _, changed := range []string{"siteId", "operationId", "operationVersion", "clusterId", "transport"} {
		t.Run(changed, func(t *testing.T) {
			cmd, out := nvkeMaintenanceTestCommand(t)
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nvkeBaseData()
				if strings.Contains(request.Query, "astroliftNvkeMaintenanceObservation(") {
					if changed == "transport" {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = w.Write([]byte("PRIVATE_NATIVE_ERROR"))
						return
					}
					reply := maintenanceTestReply()
					reply[changed] = "invalid"
					if changed == "operationVersion" {
						reply[changed] = 10
					}
					data["astroliftNvkeMaintenanceObservation"] = reply
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			err := runNVKEMaintenance(cmd, context.Background(), nvkeTestClient(server), []string{nvkeTestOperation})
			if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("unsafe maintenance reply: %v %s", err, out.String())
			}
		})
	}
}

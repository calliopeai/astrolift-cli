package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/privatefile"
	"github.com/spf13/cobra"
)

const nativeResetTestReview = `{"workflowId":"rotation-更新","baseRunId":"123e4567-e89b-12d3-a456-426614174005","workflowTaskEventId":"9007199254740993","activityEventId":"9007199254740994","activityType":"StartEtcdEncryptionRewrite","phase":"rewrite","mode":"restart_rewrite","rationale":"PRIVATE_RATIONALE","quiescenceEvidence":"PRIVATE_EVIDENCE","acknowledgeBoundaryReviewed":true,"acknowledgeConflictingWorkStopped":true}`

func resetTestCommand(t *testing.T) (*cobra.Command, *bytes.Buffer, string) {
	t.Helper()
	cmd := newNVKENativeResetCommand()
	cmd.SetContext(context.Background())
	cmd.Flags().Bool("json", true, "")
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	file := filepath.Join(t.TempDir(), "reset-recovery.json")
	for flag, value := range map[string]string{"site": nvkeTestSite, "site-version": "7", "authority-version": "3", "source-operation": nvkeNativeTestSource, "source-version": "9", "request-file": file} {
		if err := cmd.Flags().Set(flag, value); err != nil {
			t.Fatal(err)
		}
	}
	nvkeTestInput(t, cmd, nativeResetTestReview)
	return cmd, out, file
}

func TestNVKENativeResetLostReplyRetainsExactReviewAndIdentity(t *testing.T) {
	cmd, out, file := resetTestCommand(t)
	var inputs []map[string]interface{}
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := nativeTestData()
		if strings.Contains(request.Query, "requestNvkeNativeReset(") {
			input := request.Variables["input"].(map[string]interface{})
			inputs = append(inputs, input)
			saved, err := readNVKENativeRecoveryKind(file, nvkeNativeResetKind)
			if err != nil || saved.RequestID != input["requestId"] || saved.ExpectedSource != nvkeParameterDigest([]byte(nativeResetTestReview)) {
				t.Error("reset admission preceded durable exact review")
			}
			if input["workflowTaskEventId"] != "9007199254740993" || input["activityEventId"] != "9007199254740994" || input["expectedActorId"] != float64(42) || input["rationale"] != "PRIVATE_RATIONALE" {
				t.Error("reset review or actor changed")
			}
			if len(inputs) == 1 {
				w.WriteHeader(503)
				_, _ = w.Write([]byte("PRIVATE_REPLY"))
				return
			}
			row := nativeTestReceipt(input)
			row["operationKey"] = nvkeNativeResetKey
			data["requestNvkeNativeReset"] = map[string]any{"ok": true, "data": row}
		}
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	c := nvkeTestClient(server)
	if err := runNVKENativeReset(cmd, context.Background(), c, nil); err == nil || !strings.Contains(err.Error(), "preserve") || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("unsafe reset loss: %v", err)
	}
	before, err := privatefile.ReadFile(file, 16384)
	if err != nil {
		t.Fatal(err)
	}
	if err = runNVKENativeReset(cmd, context.Background(), c, nil); err != nil {
		t.Fatal(err)
	}
	after, _ := privatefile.ReadFile(file, 16384)
	if len(inputs) != 2 || !reflect.DeepEqual(inputs[0], inputs[1]) || !bytes.Equal(before, after) {
		t.Fatal("reset retry changed the original tuple")
	}
	if strings.Contains(string(before)+out.String(), "PRIVATE") || strings.Contains(string(before), "rotation-更新") {
		t.Fatal("private review escaped recovery/output")
	}
}

func TestNVKENativeResetReceiptRecoveryDoesNotReadInputOrAdmit(t *testing.T) {
	cmd, out, file := resetTestCommand(t)
	var receipt map[string]any
	mutations := 0
	server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
		data := nativeTestData()
		if strings.Contains(request.Query, "requestNvkeNativeReset(") {
			mutations++
			receipt = nativeTestReceipt(request.Variables["input"].(map[string]interface{}))
			receipt["operationKey"] = nvkeNativeResetKey
			data["requestNvkeNativeReset"] = map[string]any{"ok": true, "data": nil}
		}
		data["astroliftNvkeRequest"] = receipt
		writePipelineTestResponse(t, w, data)
	})
	defer server.Close()
	c := nvkeTestClient(server)
	if runNVKENativeReset(cmd, context.Background(), c, nil) == nil {
		t.Fatal("lost reply expected")
	}
	path := resourceStringFlag(cmd, "file")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := runNVKENativeReset(cmd, context.Background(), c, nil); err != nil {
		t.Fatal(err)
	}
	recoverCmd := cmd.Commands()[0]
	recoverCmd.SetContext(context.Background())
	recoverCmd.SetOut(out)
	recoverCmd.SetErr(out)
	_ = recoverCmd.Flags().Set("request-file", file)
	if err := runNVKENativeResetRecover(recoverCmd, context.Background(), c, nil); err != nil {
		t.Fatal(err)
	}
	if mutations != 1 || strings.Contains(out.String(), "PRIVATE") {
		t.Fatal("recovery repeated admission or leaked review")
	}
	if _, err := readNVKENativeRecovery(file); err == nil {
		t.Fatal("reset recovery was accepted as a workflow start")
	}
}

func TestNVKENativeResetPendingReviewCannotChange(t *testing.T) {
	for _, change := range []string{"actor", "site-version", "authority-version", "source-version", "source-operation", "operation-timeout", "input", "mode", "missing-receipt", "wrong-receipt", "malformed"} {
		t.Run(change, func(t *testing.T) {
			cmd, _, file := resetTestCommand(t)
			mutations := 0
			changed := false
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nativeTestData()
				if changed && change == "actor" {
					data["astroliftMyProfile"] = map[string]any{"userId": 43}
				}
				if changed && change == "missing-receipt" {
					delete(data, "astroliftNvkeRequest")
				}
				if changed && change == "wrong-receipt" {
					data["astroliftNvkeRequest"] = map[string]any{"id": nvkeTestOperation, "version": 1, "siteId": nvkeTestSite, "requestId": "other", "operationKey": nvkeNativeResetKey}
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
			if runNVKENativeReset(cmd, context.Background(), c, nil) == nil || mutations != 1 {
				t.Fatal("pending reset not recorded")
			}
			changed = true
			switch change {
			case "actor", "missing-receipt", "wrong-receipt":
			case "malformed":
				if err := os.WriteFile(file, []byte(`{"PRIVATE":"bad"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "input":
				nvkeTestInput(t, cmd, strings.ReplaceAll(nativeResetTestReview, "PRIVATE_RATIONALE", "PRIVATE_CHANGED"))
			case "mode":
				nvkeTestInput(t, cmd, strings.ReplaceAll(nativeResetTestReview, "restart_rewrite", "resume_rewrite"))
			case "source-operation":
				_ = cmd.Flags().Set(change, nvkeTestOperation)
			default:
				_ = cmd.Flags().Set(change, "60")
			}
			if err := runNVKENativeReset(cmd, context.Background(), c, nil); err == nil || mutations != 1 || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("changed reset admitted: %v", err)
			}
		})
	}
}

func TestNVKENativeResetReviewRejectsAmbiguousOrUnreviewedInput(t *testing.T) {
	for _, input := range []string{
		strings.ReplaceAll(nativeResetTestReview, `"acknowledgeBoundaryReviewed":true`, `"acknowledgeBoundaryReviewed":false`),
		strings.ReplaceAll(nativeResetTestReview, `"9007199254740993"`, `9007199254740993`),
		strings.ReplaceAll(nativeResetTestReview, `"9007199254740993"`, `"09223372036854775807"`),
		strings.ReplaceAll(nativeResetTestReview, `"9007199254740993"`, `"9223372036854775808"`),
		strings.ReplaceAll(nativeResetTestReview, `"phase":"rewrite"`, `"phase":"prepare|add_secondary"`),
		strings.ReplaceAll(nativeResetTestReview, `"mode":"restart_rewrite"`, `"mode":"restart_rewrite|resume_rewrite"`),
		strings.ReplaceAll(nativeResetTestReview, `"phase":"rewrite"`, `"phase":"rewrite","phase":"prepare"`),
		strings.ReplaceAll(nativeResetTestReview, `"phase":"rewrite"`, `"Phase":"rewrite"`),
		strings.ReplaceAll(nativeResetTestReview, `"rationale":"PRIVATE_RATIONALE"`, `"rationale":null`),
		strings.ReplaceAll(nativeResetTestReview, `"rationale":"PRIVATE_RATIONALE"`, `"rationale":"`+strings.Repeat("é", 2001)+`"`),
		nativeResetTestReview + ` {"PRIVATE":"extra"}`,
		strings.ReplaceAll(nativeResetTestReview, `"mode":"restart_rewrite"`, `"mode":"refresh_survivors"`),
	} {
		if _, err := decodeNVKENativeReset([]byte(input)); err == nil || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatalf("unsafe reset decode: %v", err)
		}
	}
	if review, err := decodeNVKENativeReset([]byte(nativeResetTestReview)); err != nil || review.WorkflowTaskEventID != "9007199254740993" {
		t.Fatal("exact valid review refused")
	}
}

func TestNVKENativeHistoryKeepsExactEventStringsAndRejectsMismatchedPages(t *testing.T) {
	for _, mismatch := range []string{"", "site", "run", "source", "workflow", "event", "link"} {
		t.Run(mismatch, func(t *testing.T) {
			cmd := newNVKENativeHistoryCommand()
			cmd.SetContext(context.Background())
			out := &bytes.Buffer{}
			cmd.SetOut(out)
			for flag, value := range map[string]string{"site": nvkeTestSite, "site-version": "7", "authority-version": "3", "source-operation": nvkeNativeTestSource, "source-version": "9", "after": "OPAQUE_CURSOR", "limit": "2"} {
				_ = cmd.Flags().Set(flag, value)
			}
			server := nvkeTestServer(t, func(request gqlRequest, w http.ResponseWriter) {
				data := nativeTestData()
				if strings.Contains(request.Query, "astroliftNvkeNativeHistory(") {
					input := request.Variables["input"].(map[string]interface{})
					if input["after"] != "OPAQUE_CURSOR" || input["limit"] != float64(2) || input["expectedActorId"] != float64(42) {
						t.Error("history review changed")
					}
					row := map[string]any{"siteId": nvkeTestSite, "siteVersion": 7, "authorityVersion": 3, "sourceOperationId": nvkeNativeTestSource, "sourceVersion": 9, "clusterId": nvkeTestOperation, "workflow": "RotateEtcdEncryptionKey", "workflowId": "owner-更新", "runId": nvkeTestOperation, "observedAt": "2026-11-08T01:00:00Z", "status": "FAILED", "items": []map[string]any{{"eventId": "9007199254740993", "eventType": "ACTIVITY_TASK_SCHEDULED", "workflowTaskCompletedEventId": "9007199254740992"}}, "nextCursor": "NEXT_CURSOR"}
					switch mismatch {
					case "site":
						row["siteId"] = nvkeTestOperation
					case "run":
						row["runId"] = "not-an-exact-run"
					case "source":
						row["sourceVersion"] = 10
					case "workflow":
						row["workflow"] = "RenewK8sCertsV2"
					case "event":
						row["items"].([]map[string]any)[0]["eventId"] = "09007199254740993"
					case "link":
						row["items"].([]map[string]any)[0]["workflowTaskCompletedEventId"] = "9223372036854775808"
					}
					data["astroliftNvkeNativeHistory"] = map[string]any{"ok": true, "data": row}
				}
				writePipelineTestResponse(t, w, data)
			})
			defer server.Close()
			err := runNVKENativeHistory(cmd, context.Background(), nvkeTestClient(server), []string{"RotateEtcdEncryptionKey"})
			if mismatch != "" {
				if err == nil || out.Len() != 0 {
					t.Fatal("mismatched history published")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var page api.NVKENativeHistoryPage
			if json.Unmarshal(out.Bytes(), &page) != nil || page.Items[0].EventID != "9007199254740993" || *page.NextCursor != "NEXT_CURSOR" {
				t.Fatal("history lost precision or cursor")
			}
		})
	}
}

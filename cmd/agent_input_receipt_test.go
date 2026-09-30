package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

const receiptRequestID = "91314b8f-ab29-49b7-9a3a-c54d01ae3651"

func TestAgentInputRecoversLostSendReplyWithOriginalIdentity(t *testing.T) {
	resetAgentSendFlags()
	defer resetAgentSendFlags()
	agentSendRequestID = receiptRequestID
	agentSendJSON = true
	var mu sync.Mutex
	sends, reads := 0, 0
	var stored map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var req gqlRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Variables["taskId"] != "task-aaa" || req.Variables["requestId"] != receiptRequestID {
			t.Errorf("wrong identity: %+v", req.Variables)
		}
		var data map[string]interface{}
		if strings.Contains(req.Query, "mutation") {
			sends++
			if !strings.Contains(req.Query, "clientRequestId: $requestId") {
				t.Error("missing request binding")
			}
			if stored == nil {
				stored = queuedMessage(map[string]interface{}{"clientRequestId": receiptRequestID})
			}
			if sends == 1 {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				return
			}
			data = map[string]interface{}{"sendAgentTaskInput": map[string]interface{}{"ok": true, "data": stored}}
		} else {
			reads++
			data = map[string]interface{}{"agentTaskInputMessage": stored}
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": data})
	}))
	defer srv.Close()
	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	err := runAgentSend(cmd, context.Background(), client, &config.Config{}, "task-aaa", "also check the migration")
	if err == nil || !strings.Contains(err.Error(), receiptRequestID) {
		t.Fatalf("uncertain send should preserve key: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("uncertain send printed success: %s", out.String())
	}
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err = runAgentInputReceipt(cmd, context.Background(), client, "task-aaa", receiptRequestID); err != nil {
		t.Fatal(err)
	}
	var recovered agentTaskInputMessage
	if err = json.Unmarshal(out.Bytes(), &recovered); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err = runAgentSend(cmd, context.Background(), client, &config.Config{}, "task-aaa", "also check the migration"); err != nil {
		t.Fatal(err)
	}
	var retry agentTaskInputMessage
	if err = json.Unmarshal(out.Bytes(), &retry); err != nil {
		t.Fatal(err)
	}
	if retry.ID != recovered.ID || recovered.ClientRequestID == nil || *recovered.ClientRequestID != receiptRequestID {
		t.Fatalf("receipt changed: %+v / %+v", recovered, retry)
	}
	mu.Lock()
	defer mu.Unlock()
	if sends != 2 || reads != 1 {
		t.Fatalf("unexpected retries: sends=%d reads=%d", sends, reads)
	}
}

func TestAgentInputReceiptMissingIsNullWithoutSending(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"agentTaskInputMessage": nil}, &captured)
	defer srv.Close()
	cmd, out := agentTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := runAgentInputReceipt(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "task-aaa", receiptRequestID); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "null" || strings.Contains(captured.Query, "mutation") {
		t.Fatalf("receipt lookup mutated or invented receipt: %s / %s", captured.Query, out.String())
	}
}

func TestAgentSendRejectsMissingOrMismatchedKeyedReceipts(t *testing.T) {
	for _, row := range []interface{}{nil, queuedMessage(nil), queuedMessage(map[string]interface{}{"clientRequestId": "different"}), queuedMessage(map[string]interface{}{"clientRequestId": receiptRequestID, "message": "different"})} {
		resetAgentSendFlags()
		agentSendRequestID = receiptRequestID
		srv := gqlServer(t, map[string]interface{}{"sendAgentTaskInput": map[string]interface{}{"ok": true, "data": row}}, nil)
		cmd, out := agentTestCmd()
		err := runAgentSend(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-aaa", "also check the migration")
		srv.Close()
		if err == nil || out.Len() != 0 {
			t.Fatalf("invalid receipt accepted: %v / %s", err, out.String())
		}
	}
	resetAgentSendFlags()
}

func TestAgentKeyedSendNeverFallsBackOnOldServer(t *testing.T) {
	resetAgentSendFlags()
	defer resetAgentSendFlags()
	agentSendRequestID = receiptRequestID
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"errors":[{"message":"Unknown argument clientRequestId"}]}`))
	}))
	defer srv.Close()
	cmd, out := agentTestCmd()
	err := runAgentSend(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-aaa", "nudge")
	if err == nil || calls != 1 || out.Len() != 0 {
		t.Fatalf("unsupported keyed send fell back: %v calls=%d", err, calls)
	}
}

func TestAgentSendUnkeyedKeepsLegacySchema(t *testing.T) {
	resetAgentSendFlags()
	defer resetAgentSendFlags()
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"sendAgentTaskInput": map[string]interface{}{"ok": true, "data": queuedMessage(nil)}}, &captured)
	defer srv.Close()
	cmd, _ := agentTestCmd()
	if err := runAgentSend(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-aaa", "nudge"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(captured.Query, "clientRequestId") || captured.Variables["requestId"] != nil {
		t.Fatal("unkeyed send requires new server")
	}
}

func TestAgentRecoveredDeliveryDoesNotClaimNewQueueOrExecution(t *testing.T) {
	resetAgentSendFlags()
	defer resetAgentSendFlags()
	agentSendRequestID = receiptRequestID
	srv := gqlServer(t, map[string]interface{}{"sendAgentTaskInput": map[string]interface{}{"ok": true, "data": queuedMessage(map[string]interface{}{"clientRequestId": receiptRequestID, "deliveredAt": "2026-09-21T12:00:00Z"})}}, nil)
	defer srv.Close()
	cmd, out := agentTestCmd()
	if err := runAgentSend(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-aaa", "also check the migration"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "not delivered yet") || strings.Contains(out.String(), "Queued for") || !strings.Contains(out.String(), "does not confirm execution") {
		t.Fatalf("misleading receipt: %s", out.String())
	}
}

func TestAgentSendRejectsExplicitInvalidRequestIDBeforeClientLoad(t *testing.T) {
	resetAgentSendFlags()
	defer resetAgentSendFlags()
	for _, key := range []string{"", "not-a-uuid"} {
		cmd, _ := agentTestCmd()
		cmd.Flags().StringVar(&agentSendRequestID, "request-id", "", "")
		if err := cmd.Flags().Set("request-id", key); err != nil {
			t.Fatal(err)
		}
		if err := agentSendCmd.Args(cmd, []string{"task", "message"}); err == nil {
			t.Fatalf("invalid request key accepted: %q", key)
		}
	}
}

func TestAgentInputReceiptRejectsWrongIdentity(t *testing.T) {
	for _, row := range []interface{}{queuedMessage(nil), queuedMessage(map[string]interface{}{"clientRequestId": "wrong"}), queuedMessage(map[string]interface{}{"clientRequestId": receiptRequestID, "id": ""})} {
		srv := gqlServer(t, map[string]interface{}{"agentTaskInputMessage": row}, nil)
		cmd, out := agentTestCmd()
		err := runAgentInputReceipt(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "task", receiptRequestID)
		srv.Close()
		if err == nil || out.Len() != 0 {
			t.Fatalf("bad receipt accepted: %v / %s", err, out.String())
		}
	}
}

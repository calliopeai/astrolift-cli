package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

func sessionTestCommand() (*cobra.Command, *bytes.Buffer) {
	cmd, out := agentTestCmd()
	addAgentSessionAttachFlags(cmd.Flags())
	return cmd, out
}

func TestSessionAttachRejectsAmbiguousOrEmptyIntent(t *testing.T) {
	for _, flags := range []map[string]string{
		{"steer": "review", "approve": "call"}, {"steer": " "}, {"approve": ""},
		{"deny": ""}, {"action-file": ""}, {"reason": "policy"},
		{"interactive": "true"}, {"box": "true", "approve": "call"},
		{"interactive": "true", "box": "true", "json": "true"},
		{"interactive": "true", "box": "true", "snapshot": "true"},
		{"snapshot": "true", "steer": "review"},
	} {
		cmd, _ := sessionTestCommand()
		for name, value := range flags {
			if err := cmd.Flags().Set(name, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := validateAgentSessionFlags(cmd); err == nil {
			t.Errorf("accepted flags %v", flags)
		}
	}
}

func TestSessionActionsKeepControllerIntentAndObservedTurn(t *testing.T) {
	for _, flag := range []string{"steer", "approve", "deny"} {
		cmd, _ := sessionTestCommand()
		_ = cmd.Flags().Set(flag, "observed-tool")
		if flag == "deny" {
			_ = cmd.Flags().Set("reason", "Outside brief")
		}
		raw, err := agentSessionAction(cmd, json.RawMessage(`{"activeTurn":{"id":"observed-turn"}}`))
		if err != nil {
			t.Fatal(err)
		}
		var action map[string]interface{}
		_ = json.Unmarshal(raw, &action)
		if flag == "steer" {
			if action["kind"] != "steering" || action["message"].(map[string]interface{})["text"] != "observed-tool" {
				t.Fatal(string(raw))
			}
		} else if action["turnId"] != "observed-turn" || action["toolCallId"] != "observed-tool" || action["approved"] != (flag == "approve") {
			t.Fatal(string(raw))
		}
		if flag == "deny" && action["reasonMessage"] != "Outside brief" {
			t.Fatal(string(raw))
		}
	}
	cmd, _ := sessionTestCommand()
	_ = cmd.Flags().Set("approve", "tool")
	if _, err := agentSessionAction(cmd, json.RawMessage(`{"turns":[]}`)); err == nil {
		t.Fatal("approved without an observed turn")
	}
}

func TestSessionActionFileRequiresAnActionObject(t *testing.T) {
	for _, input := range []string{`null`, `[]`, `"value"`, `{}`, strings.Repeat(" ", 256*1024+1)} {
		cmd, _ := sessionTestCommand()
		_ = cmd.Flags().Set("action-file", "-")
		cmd.SetIn(strings.NewReader(input))
		if _, err := agentSessionAction(cmd, nil); err == nil {
			t.Fatalf("accepted invalid action: %.20s", input)
		}
	}
}

func TestSessionWatchDrainsTerminalHistoryBeforeResult(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer selected-token" || r.Header.Get("X-Astrolift-Organization") != "selected-org" {
			t.Error("lost selected identity")
		}
		var query gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&query)
		after := int(query.Variables["after"].(float64))
		if query.Variables["org"] != "selected-org" || query.Variables["task"] != "task" {
			t.Error("lost selected task scope")
		}
		events := []map[string]interface{}{}
		for i := after + 1; i <= 101 && i <= after+100; i++ {
			events = append(events, map[string]interface{}{"sequence": i, "turnId": "turn", "messageId": fmt.Sprint(i), "kind": "input_required", "text": "", "request": map[string]string{"kind": "question"}, "data": map[string]string{"call_id": "tool"}})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"agentTask": map[string]interface{}{"status": "completed", "eventSequence": 101, "result": map[string]string{"output": "Finished"}}, "agentTaskEvents": events}})
	}))
	defer server.Close()
	cmd, out := sessionTestCommand()
	_ = cmd.Flags().Set("json", "true")
	client := api.NewClient(server.URL, "selected-token", false)
	client.SetOrg("selected-org")
	if err := watchAgentTaskEvents(cmd, context.Background(), client, "task"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if requests != 2 || len(lines) != 102 || !strings.Contains(lines[100], `"sequence":101`) || !strings.Contains(lines[101], `"output":"Finished"`) || !strings.Contains(lines[0], `"request":{"kind":"question"}`) {
		t.Fatalf("incomplete watch: requests=%d rows=%d first=%s last=%s", requests, len(lines), lines[0], lines[len(lines)-1])
	}
}

func TestSessionWatchRefusesGapsWithoutInventingCompletion(t *testing.T) {
	server := gqlServer(t, map[string]interface{}{"agentTask": map[string]interface{}{"status": "completed", "eventSequence": 2}, "agentTaskEvents": []map[string]interface{}{{"sequence": 2, "kind": "turn_completed"}}}, nil)
	defer server.Close()
	cmd, out := sessionTestCommand()
	err := watchAgentTaskEvents(cmd, context.Background(), api.NewClient(server.URL, "token", false), "task")
	if err == nil || !strings.Contains(err.Error(), "expected sequence 1") || out.Len() != 0 {
		t.Fatalf("gap accepted: %v %s", err, out)
	}
}

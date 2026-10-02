package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func TestDispatchCallbackContract(t *testing.T) {
	resetDispatchFlags()
	defer resetDispatchFlags()
	agentDispatchCallbackURL = "https://hooks.internal.example.org/tasks"
	agentDispatchCallbackSecretRef = "completion-key"
	agentDispatchCorrelationID = strings.Repeat("界", 128)
	agentDispatchCallbackMode = "notify"
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"runAstroliftAgent": map[string]interface{}{"ok": true, "data": map[string]interface{}{"id": "task-1", "status": "queued", "callbackStatus": "pending", "callbackAttempts": 0}}}, &captured)
	defer srv.Close()
	cmd, _ := agentTestCmd()
	if err := runAgentDispatch(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "report"); err != nil {
		t.Fatal(err)
	}
	input := captured.Variables["input"].(map[string]interface{})
	for key, want := range map[string]string{"callbackUrl": agentDispatchCallbackURL, "callbackSecretRef": "completion-key", "correlationId": agentDispatchCorrelationID, "callbackMode": "NOTIFY"} {
		if input[key] != want {
			t.Errorf("%s = %v", key, input[key])
		}
	}
	if !strings.Contains(captured.Query, "callbackStatus callbackAttempts callbackLastError") {
		t.Fatal("missing delivery state")
	}
}
func TestDispatchCallbackValidation(t *testing.T) {
	for _, tc := range []struct{ name, url, secret, correlation, mode string }{
		{name: "missing URL", secret: "key"},
		{name: "missing key", url: "https://example.org/tasks"},
		{name: "HTTP", url: "http://example.org/tasks", secret: "key"},
		{name: "credentials", url: "https://user:private@example.org/tasks", secret: "key"},
		{name: "fragment", url: "https://example.org/tasks#fragment", secret: "key"},
		{name: "long ID", url: "https://example.org/tasks", secret: "key", correlation: strings.Repeat("界", 129)},
		{name: "bad mode", url: "https://example.org/tasks", secret: "key", mode: "progress"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetDispatchFlags()
			defer resetDispatchFlags()
			agentDispatchCallbackURL, agentDispatchCallbackSecretRef, agentDispatchCorrelationID, agentDispatchCallbackMode = tc.url, tc.secret, tc.correlation, tc.mode
			if err := addAgentCallbackInput(map[string]interface{}{}); err == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
	resetDispatchFlags()
	defer resetDispatchFlags()
	input := map[string]interface{}{}
	if err := addAgentCallbackInput(input); err != nil {
		t.Fatal(err)
	}
	if len(input) != 0 {
		t.Fatal("callback-free dispatch adds fields")
	}
	agentDispatchCallbackURL, agentDispatchCallbackSecretRef = "https://example.org/tasks", "key"
	if err := addAgentCallbackInput(input); err != nil {
		t.Fatal(err)
	}
	if input["callbackMode"] != "FULL" {
		t.Fatal("default must be FULL")
	}
}
func TestReadCallbackSecret(t *testing.T) {
	cmd, _ := agentTestCmd()
	cmd.SetIn(strings.NewReader(strings.Repeat("k", 32) + "\n"))
	value, err := readCallbackSecret(cmd, true, "")
	if err != nil || value != strings.Repeat("k", 32) {
		t.Fatalf("stdin read: %q %v", value, err)
	}
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte(" "+strings.Repeat("k", 32)+" \r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	value, err = readCallbackSecret(cmd, false, file)
	if err != nil || value != " "+strings.Repeat("k", 32)+" " {
		t.Fatalf("file read: %q %v", value, err)
	}
	for _, tc := range []struct {
		stdin bool
		file  string
		body  string
	}{
		{}, {stdin: true, file: file}, {stdin: true, body: "\n"},
		{stdin: true, body: "short-key"},
		{stdin: true, body: strings.Repeat("k", 32) + string([]byte{0xff})}, {stdin: true, body: strings.Repeat("s", 4097)}, {file: file + "missing"},
	} {
		cmd.SetIn(strings.NewReader(tc.body))
		if _, err := readCallbackSecret(cmd, tc.stdin, tc.file); err == nil {
			t.Fatal("invalid secret source accepted")
		}
	}
	cmd.SetIn(strings.NewReader(strings.Repeat("k", 4096) + "\r\n"))
	if value, err := readCallbackSecret(cmd, true, ""); err != nil || len(value) != 4096 {
		t.Fatalf("maximum key boundary rejected: %v", err)
	}
	if agentCallbacksSecretCmd.Flags().Lookup("value") != nil {
		t.Fatal("secret values must not be supported in argv")
	}
}
func TestCallbackSecretNeverEchoesServerErrors(t *testing.T) {
	marker := "sensitive-key-marker"
	for _, ok := range []bool{true, false} {
		var captured gqlRequest
		srv := gqlServer(t, map[string]interface{}{"setAgentTaskCallbackSecret": map[string]interface{}{"ok": ok, "errors": []interface{}{map[string]interface{}{"message": marker}}, "data": map[string]interface{}{"name": "completion-key"}}}, &captured)
		cmd, out := agentTestCmd()
		err := runAgentCallbackSecretSet(cmd, context.Background(), api.NewClient(srv.URL, "tok", true), "completion-key", marker)
		srv.Close()
		if ok && err != nil || !ok && err == nil {
			t.Fatalf("ok=%v err=%v", ok, err)
		}
		if strings.Contains(out.String(), marker) || err != nil && strings.Contains(err.Error(), marker) {
			t.Fatal("key leaked")
		}
		if captured.Variables["value"] != marker {
			t.Fatal("key not sent to secret setter")
		}
	}
}
func TestCallbackConfigurationAndReplayContract(t *testing.T) {
	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"configureAgentTaskCallbacks": map[string]interface{}{"ok": true, "data": map[string]interface{}{"allowedHosts": []string{"*.internal.example.org"}}},
		"redeliverAgentTaskCallback":  map[string]interface{}{"ok": true, "data": map[string]interface{}{"id": "task-1", "status": "completed", "callbackStatus": "pending", "callbackAttempts": 0}},
	}, &captured)
	defer srv.Close()
	cmd, out := agentTestCmd()
	client := api.NewClient(srv.URL, "tok", false)
	if err := runAgentCallbackConfigure(cmd, context.Background(), client, []string{"*.internal.example.org"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "*.internal.example.org") {
		t.Fatal("policy not shown")
	}
	if err := runAgentCallbackRedeliver(cmd, context.Background(), client, "task-1"); err != nil {
		t.Fatal(err)
	}
	if captured.Variables["taskId"] != "task-1" || !strings.Contains(captured.Query, "$taskId: GUID!") {
		t.Fatal("invalid replay contract")
	}
	if !strings.Contains(out.String(), "replay queued") {
		t.Fatal("replay not reported")
	}
}

func TestCallbackTaskReadCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		errors []string
		calls  int
	}{
		{"older server", []string{`Cannot query field "callbackStatus" on type "AstroliftAgentTask".`}, 2},
		{"permission", []string{"permission denied"}, 1},
		{"other schema", []string{`Cannot query field "agentTask" on type "Query".`}, 1},
		{"mixed errors", []string{`Cannot query field "callbackStatus" on type "AstroliftAgentTask".`, "permission denied"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request gqlRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if request.Variables["id"] != "task-1" || r.Header.Get("Authorization") != "Bearer token" {
					t.Error("scope changed")
				}
				if calls == 1 {
					messages := make([]map[string]string, len(tc.errors))
					for i, message := range tc.errors {
						messages[i] = map[string]string{"message": message}
					}
					if err := json.NewEncoder(w).Encode(map[string]interface{}{"errors": messages}); err != nil {
						t.Error(err)
					}
					return
				}
				if strings.Contains(request.Query, "callbackStatus") || strings.Contains(request.Query, "callbackAttempts") || strings.Contains(request.Query, "callbackLastError") {
					t.Error("callback fields retained in legacy fallback")
				}
				if err := json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"agentTask": map[string]interface{}{"id": "task-1", "status": "completed"}}}); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			var target struct {
				Task *agentTask `json:"agentTask"`
			}
			err := queryAgentCallbackState(context.Background(), api.NewClient(srv.URL, "token", false), agentTaskQuery, map[string]interface{}{"id": "task-1"}, &target)
			if calls != tc.calls {
				t.Fatalf("calls=%d want %d", calls, tc.calls)
			}
			if tc.calls == 2 {
				if err != nil || target.Task == nil || target.Task.CallbackStatus != nil {
					t.Fatalf("legacy read failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("real failure hidden")
			}
		})
	}
}

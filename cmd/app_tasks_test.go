package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func resetAppTasksFlags() {
	appTasksWorkload = ""
	appTasksEnvironment = ""
	appTasksRequestID = ""
	appTasksWait = false
	appTasksTimeout = 30 * time.Minute
	appTasksPollInterval = time.Millisecond
}

func taskRunRow(status string, exitCode interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id": "run-guid", "registeredAppSlug": "reports", "workloadSlug": "export",
		"environmentName": "staging", "triggerKind": "api", "status": status,
		"exitCode": exitCode, "failureReason": "", "k8sJobName": "export-run-abc",
		"namespace": "acme-reports-staging", "createdAt": "2026-10-10T12:00:00Z",
		"startedAt": nil, "endedAt": nil, "durationSeconds": nil,
	}
}

// taskServer answers runTask with run-guid and serves statuses in order on
// each astroliftTaskRun read, repeating the last.
func taskServer(t *testing.T, statuses []string, sent *[]gqlRequest) *api.Client {
	t.Helper()
	reads := 0
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		*sent = append(*sent, req)
		if strings.Contains(req.Query, "runTask") {
			return map[string]interface{}{"runTask": map[string]interface{}{
				"ok": true, "errors": []interface{}{}, "data": map[string]interface{}{"id": "run-guid"},
			}}
		}
		status := statuses[min(reads, len(statuses)-1)]
		reads++
		var exit interface{}
		switch status {
		case "succeeded":
			exit = 0
		case "failed":
			exit = 3
		}
		return map[string]interface{}{"astroliftTaskRun": taskRunRow(status, exit)}
	})
	t.Cleanup(srv.Close)
	return api.NewClient(srv.URL, "tok", false)
}

func TestAppTasksRunSendsTheInputAndPrintsTheRun(t *testing.T) {
	resetAppTasksFlags()
	t.Cleanup(resetAppTasksFlags)
	appTasksWorkload, appTasksEnvironment, appTasksRequestID = "export", "staging", "ci-42"
	var sent []gqlRequest
	client := taskServer(t, []string{"pending"}, &sent)
	cmd, out := appTestCmd()

	if err := runAppTasksRun(cmd, context.Background(), client, "reports", []string{"python", "export.py"}); err != nil {
		t.Fatal(err)
	}
	input := sent[0].Variables["input"].(map[string]interface{})
	if input["appSlug"] != "reports" || input["workloadSlug"] != "export" ||
		input["environmentName"] != "staging" || input["requestId"] != "ci-42" {
		t.Fatalf("runTask input: %#v", input)
	}
	if got := input["command"].([]interface{}); len(got) != 2 || got[0] != "python" {
		t.Fatalf("command: %#v", got)
	}
	if len(sent) != 2 || sent[1].Variables["id"] != "run-guid" {
		t.Fatalf("expected one read of the started run, got %d requests", len(sent))
	}
	for _, want := range []string{"run-guid", "reports/export", "staging", "pending"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestAppTasksRunOmitsUnsetOptionsAndSendsAnEmptyCommand(t *testing.T) {
	resetAppTasksFlags()
	t.Cleanup(resetAppTasksFlags)
	appTasksWorkload = "export"
	var sent []gqlRequest
	client := taskServer(t, []string{"pending"}, &sent)
	cmd, _ := appTestCmd()

	if err := runAppTasksRun(cmd, context.Background(), client, "reports", nil); err != nil {
		t.Fatal(err)
	}
	input := sent[0].Variables["input"].(map[string]interface{})
	if _, ok := input["environmentName"]; ok {
		t.Error("environmentName must be omitted so the server can infer it")
	}
	if _, ok := input["requestId"]; ok {
		t.Error("requestId must be omitted when not given")
	}
	if got, ok := input["command"].([]interface{}); !ok || len(got) != 0 {
		t.Fatalf("command must be an empty list, got %#v", input["command"])
	}
}

func TestAppTasksRunRequiresAWorkload(t *testing.T) {
	resetAppTasksFlags()
	t.Cleanup(resetAppTasksFlags)
	cmd, _ := appTestCmd()
	client := api.NewClient("http://127.0.0.1:1", "tok", false)
	if err := runAppTasksRun(cmd, context.Background(), client, "reports", nil); err == nil || !strings.Contains(err.Error(), "--workload") {
		t.Fatalf("expected a --workload refusal, got %v", err)
	}
}

func TestAppTasksRunSurfacesTheServerRefusal(t *testing.T) {
	resetAppTasksFlags()
	t.Cleanup(resetAppTasksFlags)
	appTasksWorkload = "export"
	srv := gqlServerFunc(t, func(gqlRequest) map[string]interface{} {
		return map[string]interface{}{"runTask": map[string]interface{}{
			"ok": false, "data": nil,
			"errors": []map[string]string{{"code": "PERMISSION_DENIED", "message": "app.run_task is required"}},
		}}
	})
	defer srv.Close()
	cmd, _ := appTestCmd()
	err := runAppTasksRun(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "reports", nil)
	if err == nil || !strings.Contains(err.Error(), "app.run_task is required") {
		t.Fatalf("expected the server's message, got %v", err)
	}
}

func TestAppTasksWaitPollsToSuccessAndEmitsJSON(t *testing.T) {
	resetAppTasksFlags()
	t.Cleanup(resetAppTasksFlags)
	appTasksWorkload, appTasksWait = "export", true
	var sent []gqlRequest
	client := taskServer(t, []string{"pending", "running", "succeeded"}, &sent)
	cmd, out := appTestCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}

	if err := runAppTasksRun(cmd, context.Background(), client, "reports", nil); err != nil {
		t.Fatal(err)
	}
	var run map[string]interface{}
	if err := json.Unmarshal(out.Bytes(), &run); err != nil {
		t.Fatalf("output is not one JSON object: %v\n%s", err, out.String())
	}
	if run["id"] != "run-guid" || run["status"] != "succeeded" || run["exitCode"] != float64(0) ||
		run["environmentName"] != "staging" || run["workloadSlug"] != "export" {
		t.Fatalf("json run: %#v", run)
	}
	if _, ok := run["command"]; ok {
		t.Error("the run's command must never be printed")
	}
	if len(sent) != 4 {
		t.Fatalf("expected runTask plus three reads, got %d requests", len(sent))
	}
}

func TestAppTasksTerminalFailureAndTimeoutExitNonZero(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []string
		timeout  time.Duration
		want     string
	}{
		{"failed", []string{"running", "failed"}, time.Minute, "failed"},
		{"cancelled", []string{"cancelled"}, time.Minute, "cancelled"},
		{"timeout", []string{"running"}, 5 * time.Millisecond, "still running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetAppTasksFlags()
			t.Cleanup(resetAppTasksFlags)
			appTasksWait, appTasksTimeout = true, tc.timeout
			var sent []gqlRequest
			client := taskServer(t, tc.statuses, &sent)
			cmd, _ := appTestCmd()
			err := runAppTasksStatus(cmd, context.Background(), client, "run-guid")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestAppTasksStatusWithoutWaitReadsOnceAndReportsFailure(t *testing.T) {
	resetAppTasksFlags()
	t.Cleanup(resetAppTasksFlags)
	var sent []gqlRequest
	client := taskServer(t, []string{"running"}, &sent)
	cmd, out := appTestCmd()
	if err := runAppTasksStatus(cmd, context.Background(), client, "run-guid"); err != nil {
		t.Fatalf("a running run is not an error without --wait: %v", err)
	}
	if len(sent) != 1 || !strings.Contains(out.String(), "running") {
		t.Fatalf("requests=%d output=%s", len(sent), out.String())
	}

	sent = nil
	client = taskServer(t, []string{"failed"}, &sent)
	cmd, out = appTestCmd()
	if err := runAppTasksStatus(cmd, context.Background(), client, "run-guid"); err == nil {
		t.Fatal("a failed run must exit non-zero")
	}
	if !strings.Contains(out.String(), "Exit code:   3") {
		t.Fatalf("output lacks the exit code:\n%s", out.String())
	}
}

func TestAppTasksStatusUnknownRun(t *testing.T) {
	resetAppTasksFlags()
	t.Cleanup(resetAppTasksFlags)
	srv := gqlServerFunc(t, func(gqlRequest) map[string]interface{} {
		return map[string]interface{}{"astroliftTaskRun": nil}
	})
	defer srv.Close()
	cmd, _ := appTestCmd()
	err := runAppTasksStatus(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "nope")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found, got %v", err)
	}
}

package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

func resetAgentVNCFlags() {
	agentVNCURLOnly = false
	agentVNCJSON = false
	agentVNCForce = false
}

// taskRow builds an agentTask response row, defaulting to the state a VNC
// session actually requires (running + vnc enabled).
func taskRow(id string, over map[string]interface{}) map[string]interface{} {
	row := map[string]interface{}{
		"id": id, "status": "running", "callbackUrl": "",
		"result": nil, "createdAt": "2026-06-17T09:59:00+00:00",
		"startedAt": nil, "finishedAt": nil,
		"vncEnabled": true, "vncUrl": "/app/vnc/" + id, "snapshotUrl": nil,
	}
	for k, v := range over {
		row[k] = v
	}
	return row
}

func TestAgentVNCPrintsAbsoluteConsoleURL(t *testing.T) {
	resetAgentVNCFlags()
	defer resetAgentVNCFlags()

	srv := gqlServer(t, map[string]interface{}{
		"agentTask": taskRow("task-aaa", nil),
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAgentVNC(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-aaa"); err != nil {
		t.Fatalf("runAgentVNC: %v", err)
	}

	got := out.String()
	want := srv.URL + "/agents/runs/task-aaa/vnc"
	if !strings.Contains(got, want) {
		t.Errorf("expected console URL %q in output:\n%s", want, got)
	}
	// The raw relay path is exactly what the caller could not use; it must
	// not be presented as the thing to open.
	if strings.Contains(got, "Console: /app/vnc/") {
		t.Errorf("relay path leaked as the console URL:\n%s", got)
	}
}

// --url is the scripting surface: bare URL, nothing else on stdout.
func TestAgentVNCURLOnlyPrintsBareURL(t *testing.T) {
	resetAgentVNCFlags()
	agentVNCURLOnly = true
	defer resetAgentVNCFlags()

	srv := gqlServer(t, map[string]interface{}{
		"agentTask": taskRow("task-aaa", nil),
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAgentVNC(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-aaa"); err != nil {
		t.Fatalf("runAgentVNC: %v", err)
	}

	got := strings.TrimSpace(out.String())
	if got != srv.URL+"/agents/runs/task-aaa/vnc" {
		t.Errorf("--url should print only the URL, got %q", got)
	}
}

// The relay closes 4410 for a non-running task; failing here gives a reason
// instead of a socket that closes the moment the page loads.
func TestAgentVNCRefusesNonRunningTask(t *testing.T) {
	resetAgentVNCFlags()
	defer resetAgentVNCFlags()

	srv := gqlServer(t, map[string]interface{}{
		"agentTask": taskRow("task-done", map[string]interface{}{"status": "completed"}),
	}, nil)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	err := runAgentVNC(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-done")
	if err == nil {
		t.Fatal("expected an error for a completed task")
	}
	if !strings.Contains(err.Error(), "completed") {
		t.Errorf("error should name the task's actual status, got: %v", err)
	}
}

func TestAgentVNCRefusesTaskWithoutVNC(t *testing.T) {
	resetAgentVNCFlags()
	defer resetAgentVNCFlags()

	srv := gqlServer(t, map[string]interface{}{
		"agentTask": taskRow("task-novnc", map[string]interface{}{"vncEnabled": false, "vncUrl": ""}),
	}, nil)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	err := runAgentVNC(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-novnc")
	if err == nil || !strings.Contains(err.Error(), "VNC enabled") {
		t.Fatalf("expected a VNC-not-enabled error, got: %v", err)
	}
}

// --force is for a task that is about to start; it must bypass both gates.
func TestAgentVNCForceBypassesPreflight(t *testing.T) {
	resetAgentVNCFlags()
	agentVNCForce = true
	agentVNCURLOnly = true
	defer resetAgentVNCFlags()

	srv := gqlServer(t, map[string]interface{}{
		"agentTask": taskRow("task-pending", map[string]interface{}{"status": "queued", "vncEnabled": false}),
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAgentVNC(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-pending"); err != nil {
		t.Fatalf("--force should still print a URL: %v", err)
	}
	if !strings.Contains(out.String(), "/agents/runs/task-pending/vnc") {
		t.Errorf("expected URL under --force, got %q", out.String())
	}
}

func TestAgentVNCJSONCarriesBothURLs(t *testing.T) {
	resetAgentVNCFlags()
	agentVNCJSON = true
	defer resetAgentVNCFlags()

	srv := gqlServer(t, map[string]interface{}{
		"agentTask": taskRow("task-aaa", nil),
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAgentVNC(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "task-aaa"); err != nil {
		t.Fatalf("runAgentVNC: %v", err)
	}

	var info agentVNCInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("decoding JSON: %v\n%s", err, out.String())
	}
	if info.ConsoleURL != srv.URL+"/agents/runs/task-aaa/vnc" {
		t.Errorf("consoleUrl = %q", info.ConsoleURL)
	}
	// The raw relay path stays available for a caller driving its own noVNC.
	if info.RelayPath != "/app/vnc/task-aaa" {
		t.Errorf("relayPath = %q, want the platform's stored path", info.RelayPath)
	}
	if !info.VNCEnabled || info.Status != "running" {
		t.Errorf("preflight fields not carried: %+v", info)
	}
}

func TestAgentVNCMissingTaskErrors(t *testing.T) {
	resetAgentVNCFlags()
	defer resetAgentVNCFlags()

	srv := gqlServer(t, map[string]interface{}{"agentTask": nil}, nil)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	err := runAgentVNC(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "nope")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a not-found error, got: %v", err)
	}
}

// A reverse-proxied install serves the console under a sub-path; naive
// concatenation would drop it and produce a 404 URL.
func TestVNCConsoleURLPreservesBaseSubPath(t *testing.T) {
	got, err := vncConsoleURL("https://example.test/astrolift", "task-aaa")
	if err != nil {
		t.Fatalf("vncConsoleURL: %v", err)
	}
	if got != "https://example.test/astrolift/agents/runs/task-aaa/vnc" {
		t.Errorf("sub-path base not preserved: %q", got)
	}
}

func TestVNCConsoleURLEscapesTaskID(t *testing.T) {
	got, err := vncConsoleURL("https://example.test", "a/b")
	if err != nil {
		t.Fatalf("vncConsoleURL: %v", err)
	}
	if !strings.Contains(got, "a%2Fb") {
		t.Errorf("task id should be path-escaped, got %q", got)
	}
}

func TestVNCConsoleURLRequiresBase(t *testing.T) {
	if _, err := vncConsoleURL("", "task-aaa"); err == nil {
		t.Fatal("expected an error when no API URL is configured")
	}
}

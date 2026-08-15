package cmd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func resetAppPodsFlags() {
	appPodsApp = ""
	appPodsEnv = ""
	appPodsWorkload = ""
	appPodsReady = false
	appPodsJSON = false
}

// podRow builds an astroliftAppPods response row defaulting to a healthy,
// exec-able pod.
func podRow(name, workload string, over map[string]interface{}) map[string]interface{} {
	row := map[string]interface{}{
		"name": name, "workload": workload,
		"status": "Running", "phase": "Running", "ready": true,
		"restarts": 0, "age": nil, "node": "ip-10-0-1-5",
		"containerStatuses": []map[string]interface{}{{"name": "app"}},
	}
	for k, v := range over {
		row[k] = v
	}
	return row
}

func TestAppPodsRendersTargetsAndContainers(t *testing.T) {
	resetAppPodsFlags()
	defer resetAppPodsFlags()

	srv := gqlServer(t, map[string]interface{}{
		"astroliftAppPods": []map[string]interface{}{
			podRow("web-abc", "web", map[string]interface{}{
				"containerStatuses": []map[string]interface{}{{"name": "app"}, {"name": "sidecar"}},
			}),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAppPods(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPods: %v", err)
	}

	got := out.String()
	// The pod name feeds `exec --pod`; the container names feed `exec -c`.
	for _, want := range []string{"web-abc", "web", "ip-10-0-1-5", "app,sidecar"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\n%s", want, got)
		}
	}
}

// --workload is the narrowing an exec picker needs on a multi-workload app.
func TestAppPodsFiltersByWorkload(t *testing.T) {
	resetAppPodsFlags()
	appPodsWorkload = "worker"
	defer resetAppPodsFlags()

	srv := gqlServer(t, map[string]interface{}{
		"astroliftAppPods": []map[string]interface{}{
			podRow("web-abc", "web", nil),
			podRow("worker-xyz", "worker", nil),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAppPods(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "app"); err != nil {
		t.Fatalf("runAppPods: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "worker-xyz") {
		t.Errorf("expected the worker pod, got:\n%s", got)
	}
	if strings.Contains(got, "web-abc") {
		t.Errorf("--workload should have excluded the web pod, got:\n%s", got)
	}
}

// --ready exists because a non-ready pod is not a usable exec target.
func TestAppPodsReadyFilterExcludesUnreadyPods(t *testing.T) {
	resetAppPodsFlags()
	appPodsReady = true
	defer resetAppPodsFlags()

	srv := gqlServer(t, map[string]interface{}{
		"astroliftAppPods": []map[string]interface{}{
			podRow("web-good", "web", nil),
			podRow("web-crash", "web", map[string]interface{}{
				"ready": false, "status": "CrashLoopBackOff", "restarts": 7,
			}),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAppPods(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPods: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "web-crash") {
		t.Errorf("--ready should have excluded the crash-looping pod:\n%s", got)
	}
	if !strings.Contains(got, "1 pod(s) shown.") {
		t.Errorf("count should reflect the filter, got:\n%s", got)
	}
}

// Without --ready, an unhealthy pod must still be visible AND legible —
// that is the pod an operator most wants to find.
func TestAppPodsShowsUnhealthyPodStatus(t *testing.T) {
	resetAppPodsFlags()
	defer resetAppPodsFlags()

	srv := gqlServer(t, map[string]interface{}{
		"astroliftAppPods": []map[string]interface{}{
			podRow("web-crash", "web", map[string]interface{}{
				"ready": false, "status": "CrashLoopBackOff", "phase": "Running", "restarts": 7,
			}),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAppPods(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPods: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "CrashLoopBackOff") {
		t.Errorf("status should surface CrashLoopBackOff, not the coarse phase:\n%s", got)
	}
	if !strings.Contains(got, "7") {
		t.Errorf("restart count should be shown:\n%s", got)
	}
}

func TestAppPodsSendsEnvironmentOnlyWhenSet(t *testing.T) {
	resetAppPodsFlags()
	defer resetAppPodsFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{"astroliftAppPods": []map[string]interface{}{}}, &captured)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	if err := runAppPods(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPods: %v", err)
	}
	if _, present := captured.Variables["environmentName"]; present {
		t.Errorf("environmentName should be omitted when --env is unset: %#v", captured.Variables)
	}

	appPodsEnv = "staging"
	cmd2, _ := agentTestCmd()
	if err := runAppPods(cmd2, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPods: %v", err)
	}
	if got := captured.Variables["environmentName"]; got != "staging" {
		t.Errorf("environmentName = %v, want staging", got)
	}
}

func TestAppPodsJSONCarriesExecTargets(t *testing.T) {
	resetAppPodsFlags()
	appPodsJSON = true
	defer resetAppPodsFlags()

	srv := gqlServer(t, map[string]interface{}{
		"astroliftAppPods": []map[string]interface{}{
			podRow("web-abc", "web", map[string]interface{}{
				"containerStatuses": []map[string]interface{}{{"name": "app"}, {"name": "sidecar"}},
			}),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAppPods(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPods: %v", err)
	}

	var pods []appPod
	if err := json.Unmarshal(out.Bytes(), &pods); err != nil {
		t.Fatalf("decoding JSON: %v\n%s", err, out.String())
	}
	if len(pods) != 1 || pods[0].Name != "web-abc" {
		t.Fatalf("unexpected pods: %+v", pods)
	}
	if names := pods[0].containerNames(); len(names) != 2 || names[1] != "sidecar" {
		t.Errorf("container names not carried: %v", names)
	}
}

// An empty result should say which filters produced it, so a picker showing
// nothing is distinguishable from an app with no pods at all.
func TestAppPodsEmptyMessageNamesFilters(t *testing.T) {
	resetAppPodsFlags()
	appPodsWorkload = "worker"
	appPodsReady = true
	defer resetAppPodsFlags()

	srv := gqlServer(t, map[string]interface{}{"astroliftAppPods": []map[string]interface{}{}}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAppPods(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "web"); err != nil {
		t.Fatalf("runAppPods: %v", err)
	}
	got := out.String()
	for _, want := range []string{`"web"`, `"worker"`, "--ready"} {
		if !strings.Contains(got, want) {
			t.Errorf("empty message should mention %s, got: %s", want, got)
		}
	}
}

func TestPodStatusLabelFallsBackToPhase(t *testing.T) {
	if got := podStatusLabel(appPod{Status: "CrashLoopBackOff", Phase: "Running"}); got != "CrashLoopBackOff" {
		t.Errorf("status should win over phase, got %q", got)
	}
	if got := podStatusLabel(appPod{Phase: "Pending"}); got != "Pending" {
		t.Errorf("phase fallback = %q", got)
	}
	if got := podStatusLabel(appPod{}); got != "-" {
		t.Errorf("empty status/phase should render %q, got %q", "-", got)
	}
}

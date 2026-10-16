package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/gorilla/websocket"
)

// Target resolution for `astro exec` without --pod (SteadyMD DV-9250).
//
// Reported on 0.11.2: `astro exec --app abbott-cgm -- <cmd>` printed nothing
// and exited 1 against a pod that was running and ready, while naming the pod
// and `--container web` worked. The lookup found the right pod and then took
// its container from the first entry of containerStatuses. The control plane
// lists init containers first and the kubelet orders the rest by name, so the
// first entry is an init container, or a sidecar whose name sorts before the
// app's, whenever the pod has one.
// A session opened in a container that is not running fails inside the
// cluster after the WebSocket upgrade, and the relay passes that on as a bare
// exit code 1 with no output.

// execLookupServer answers the control plane's GraphQL lookups and, on a
// WebSocket upgrade, plays the exec relay. It records every query and every
// relay dial, so a test can see which pod and container were chosen, or that
// a refused lookup never dialed at all.
type execLookupServer struct {
	URL string

	mu      sync.Mutex
	queries []gqlRequest
	dials   []string

	// relayDone closes when a relay script returns: the edge that makes what
	// the script captured safe to read, as fakeRelay.wait does.
	relayDone chan struct{}
	closeOnce sync.Once
}

func (s *execLookupServer) wait(t *testing.T) {
	t.Helper()
	select {
	case <-s.relayDone:
	case <-time.After(5 * time.Second):
		t.Fatal("relay script did not finish")
	}
}

func newExecLookupServer(t *testing.T, respond func(gqlRequest) map[string]interface{}, script func(*relaySession)) *execLookupServer {
	t.Helper()
	s := &execLookupServer{relayDone: make(chan struct{})}
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			var req gqlRequest
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("decoding GraphQL request: %v", err)
			}
			s.mu.Lock()
			s.queries = append(s.queries, req)
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": respond(req)})
			return
		}
		s.mu.Lock()
		s.dials = append(s.dials, r.URL.Path)
		s.mu.Unlock()
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()
		in := make(chan execFrame, 64)
		go func() {
			defer close(in)
			for {
				_, data, rerr := conn.ReadMessage()
				if rerr != nil {
					return
				}
				var f execFrame
				if json.Unmarshal(data, &f) == nil {
					in <- f
				}
			}
		}()
		script(&relaySession{t: t, conn: conn, in: in})
		s.closeOnce.Do(func() { close(s.relayDone) })
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

func (s *execLookupServer) recorded() ([]gqlRequest, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]gqlRequest(nil), s.queries...), append([]string(nil), s.dials...)
}

// withExecLookup is withExecSession for a session whose target exec has to
// resolve itself: no --pod, no --container, no --environment.
func withExecLookup(t *testing.T, app, workload string, exit func(int)) {
	t.Helper()
	withExecSession(t, newBlockingReader(t), notATerm{}, exit)
	prevWorkload, prevEnvironment := execWorkload, execEnvironment
	t.Cleanup(func() { execWorkload, execEnvironment = prevWorkload, prevEnvironment })
	execApp, execPod, execContainer, execWorkload, execEnvironment = app, "", "", workload, ""
}

// execPodRow is one astroliftAppPods row as the control plane sends it.
func execPodRow(name, workload, phase string, ready bool, containers ...map[string]interface{}) map[string]interface{} {
	if containers == nil {
		containers = []map[string]interface{}{{"name": workload, "kind": "primary"}}
	}
	return map[string]interface{}{
		"name": name, "workload": workload, "phase": phase, "ready": ready,
		"containerStatuses": containers,
	}
}

// initFirstContainers is containerStatuses in the order the control plane
// sends it: init containers first, then the rest sorted by name, which puts
// a sidecar called anything before "web" ahead of the app's own container.
func initFirstContainers() []map[string]interface{} {
	return []map[string]interface{}{
		{"name": "copy-instrumentation", "kind": "init"},
		{"name": "log-router", "kind": "sidecar"},
		{"name": "web", "kind": "primary"},
	}
}

func TestExecAutoResolveOpensThePrimaryContainerNotTheFirstListed(t *testing.T) {
	var open execFrame
	srv := newExecLookupServer(t, func(req gqlRequest) map[string]interface{} {
		return map[string]interface{}{"astroliftAppPods": []interface{}{
			execPodRow("web-6c78bc9f68-tbv7c", "web", "Running", true, initFirstContainers()...),
		}}
	}, func(s *relaySession) {
		open = s.recvUntil("open")
		s.send(execFrame{Type: "exit", Code: 0})
	})

	withExecLookup(t, "abbott-cgm", "", func(code int) {
		t.Errorf("exit 0 forwarded a status %d", code)
	})
	cmd, _, _ := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"echo", "hi"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)

	queries, dials := srv.recorded()
	if len(dials) != 1 || dials[0] != "/app/exec/abbott-cgm/web-6c78bc9f68-tbv7c" {
		t.Fatalf("relay dials = %v, want the one ready pod", dials)
	}
	if open.Container != "web" {
		t.Errorf("open frame container = %q, want the pod's primary container %q", open.Container, "web")
	}
	// The fixture answers with kind whatever the client asked for, so only
	// the query text shows the CLI actually requests it.
	if len(queries) == 0 || !strings.Contains(queries[0].Query, "kind") {
		t.Errorf("pod lookup does not select containerStatuses.kind:\n%v", queries)
	}
}

func TestExecRefusesToPickAPodThatIsNotReady(t *testing.T) {
	srv := newExecLookupServer(t, func(req gqlRequest) map[string]interface{} {
		return map[string]interface{}{"astroliftAppPods": []interface{}{
			execPodRow("web-a", "web", "Pending", false),
			execPodRow("web-b", "web", "Running", false),
		}}
	}, func(s *relaySession) {
		t.Error("dialed the relay for a pod that is not ready")
	})

	withExecLookup(t, "abbott-cgm", "", func(int) { t.Error("forwarded an exit status") })
	cmd, _, _ := execTestCmd()

	err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"echo", "hi"})
	if err == nil {
		t.Fatal("expected an error when no pod is ready")
	}
	for _, want := range []string{`"abbott-cgm"`, "default environment", "2 pods", "web-a (Pending, not ready)", "web-b (Running, not ready)", "--pod"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
	if _, dials := srv.recorded(); len(dials) != 0 {
		t.Errorf("relay dialed %v", dials)
	}
}

func TestExecSaysWhereItLookedWhenNoPodsAreListed(t *testing.T) {
	srv := newExecLookupServer(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "agentBox(") {
			return map[string]interface{}{"agentBox": nil}
		}
		return map[string]interface{}{"astroliftAppPods": []interface{}{}}
	}, func(s *relaySession) {
		t.Error("dialed the relay with no pod")
	})

	withExecLookup(t, "abbott-cgm", "", func(int) { t.Error("forwarded an exit status") })
	cmd, _, _ := execTestCmd()

	err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), nil)
	if err == nil {
		t.Fatal("expected an error when the app has no pods")
	}
	for _, want := range []string{`"abbott-cgm"`, "default environment", "no pods"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestExecNamesTheWorkloadsThatDoHavePods(t *testing.T) {
	srv := newExecLookupServer(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "agentBox(") {
			return map[string]interface{}{"agentBox": nil}
		}
		return map[string]interface{}{"astroliftAppPods": []interface{}{
			execPodRow("web-a", "web", "Running", true),
			execPodRow("worker-b", "worker", "Running", true),
		}}
	}, func(s *relaySession) {
		t.Error("dialed the relay for a workload with no pods")
	})

	withExecLookup(t, "abbott-cgm", "api", func(int) { t.Error("forwarded an exit status") })
	cmd, _, _ := execTestCmd()

	err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), nil)
	if err == nil {
		t.Fatal("expected an error when the workload has no pods")
	}
	// A typo in --workload is the likely cause, so name what does exist.
	for _, want := range []string{`workload "api"`, `"abbott-cgm"`, "default environment", "2 pods", "web", "worker"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

func TestExecNonZeroExitWithNoOutputNamesTheTarget(t *testing.T) {
	// What the report saw: a session that ends with code 1 and nothing else.
	// The relay sends exactly this when the command cannot start in the
	// container, because the reason travels on a Kubernetes channel the relay
	// reads for the code and then drops.
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		s.send(execFrame{Type: "ready"})
		s.send(execFrame{Type: "exit", Code: 1})
	})

	forwarded := -1
	withExecSession(t, newBlockingReader(t), notATerm{}, func(code int) { forwarded = code })
	cmd, out, errOut := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"echo", "hi"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	if forwarded != 1 {
		t.Errorf("exit status forwarded = %d, want 1", forwarded)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", out.String())
	}
	for _, want := range []string{"exit code 1", "no output", "pod web-abc", "container app", "--container"} {
		if !strings.Contains(errOut.String(), want) {
			t.Errorf("stderr should mention %s: %q", want, errOut.String())
		}
	}
}

func TestExecNonZeroExitAfterOutputNamesTheTarget(t *testing.T) {
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
		s.send(execFrame{Type: "ready"})
		s.send(execFrame{Type: "stderr", Data: "migrate: relation exists\n"})
		s.send(execFrame{Type: "exit", Code: 3})
	})

	withExecSession(t, newBlockingReader(t), notATerm{}, func(int) {})
	cmd, _, errOut := execTestCmd()

	if err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"migrate"}); err != nil {
		t.Fatalf("runExec: %v", err)
	}
	srv.wait(t)
	got := errOut.String()
	if !strings.HasPrefix(got, "migrate: relation exists\n") {
		t.Errorf("the remote's own stderr must come first, unchanged: %q", got)
	}
	for _, want := range []string{"exited with code 3", "pod web-abc", "container app"} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr should mention %s: %q", want, got)
		}
	}
	if strings.Contains(got, "no output") {
		t.Errorf("the command printed something; the no-output hint does not apply: %q", got)
	}
}

func TestExecSessionEndingWithoutAnExitStatusIsAnError(t *testing.T) {
	// The relay closes without an exit frame when it refuses the open after
	// accepting the socket (a permission recheck, an expired session) or when
	// opening fails on its side. That used to return nil: exit 0, nothing said.
	srv := newFakeRelay(t, func(s *relaySession) {
		s.recvUntil("open")
	})

	withExecSession(t, newBlockingReader(t), notATerm{}, func(code int) {
		t.Errorf("forwarded exit status %d for a session that reported none", code)
	})
	cmd, _, _ := execTestCmd()

	err := runExec(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), []string{"sh"})
	srv.wait(t)
	if err == nil {
		t.Fatal("a session that ended without an exit status must be an error")
	}
	for _, want := range []string{"pod web-abc", "exit status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

// ---- --environment <GUID> (reviewed target) ---------------------------------

const execTestEnvironmentID = "ae3f9c69-c478-440d-a975-09fcf379f4c0"

// withReviewedExecLookup sets the exec inputs for a reviewed lookup in one
// environment and restores them after.
func withReviewedExecLookup(t *testing.T) {
	t.Helper()
	prevApp, prevWorkload, prevEnvironment, prevPod, prevContainer := execApp, execWorkload, execEnvironment, execPod, execContainer
	t.Cleanup(func() {
		execApp, execWorkload, execEnvironment, execPod, execContainer = prevApp, prevWorkload, prevEnvironment, prevPod, prevContainer
	})
	execApp, execWorkload, execEnvironment, execPod, execContainer = "abbott-cgm", "", execTestEnvironmentID, "", ""
}

// reviewedExecResponder answers the reviewed lookup: the environment list,
// the environment's pods, and a review that echoes the requested target.
func reviewedExecResponder(t *testing.T, pods []interface{}) func(gqlRequest) map[string]interface{} {
	return func(req gqlRequest) map[string]interface{} {
		switch {
		case strings.Contains(req.Query, "astroliftEnvironments("):
			return map[string]interface{}{"astroliftEnvironments": []interface{}{
				map[string]interface{}{"id": execTestEnvironmentID, "name": "production"},
			}}
		case strings.Contains(req.Query, "astroliftAppPods("):
			if req.Variables["environment"] != "production" {
				t.Errorf("pods listed for environment %v, want production", req.Variables["environment"])
			}
			return map[string]interface{}{"astroliftAppPods": pods}
		case strings.Contains(req.Query, "astroliftAppExecTarget("):
			f := false
			target := testEnvironmentReview().Target
			target.EnvironmentID, target.EnvironmentName = execTestEnvironmentID, "production"
			return map[string]interface{}{"astroliftAppExecTarget": execEnvironmentTarget{
				environmentActionTarget: target,
				PodName:                 req.Variables["pod"].(string),
				Container:               req.Variables["container"].(string),
				PodUID:                  "physical-incarnation",
				PodBinding:              "PREFLIGHT_ONLY",
				Resumable:               &f,
			}}
		}
		t.Errorf("unexpected query: %s", req.Query)
		return map[string]interface{}{}
	}
}

func TestExecEnvironmentDefaultsToThePrimaryContainer(t *testing.T) {
	withReviewedExecLookup(t)
	srv := newExecLookupServer(t, reviewedExecResponder(t, []interface{}{
		execPodRow("web-6c78bc9f68-tbv7c", "web", "Running", true, initFirstContainers()...),
	}), func(s *relaySession) { t.Error("a review must not dial the relay") })

	target, err := reviewExecEnvironment(context.Background(), api.NewClient(srv.URL, "tok", false))
	if err != nil {
		t.Fatalf("reviewExecEnvironment: %v", err)
	}
	if target.Container != "web" {
		t.Errorf("reviewed container = %q, want the pod's primary container %q", target.Container, "web")
	}
	queries, _ := srv.recorded()
	for _, q := range queries {
		if strings.Contains(q.Query, "astroliftAppPods(") && !strings.Contains(q.Query, "kind") {
			t.Errorf("environment pod lookup does not select containerStatuses.kind:\n%s", q.Query)
		}
	}
}

func TestExecEnvironmentSaysWhereItLookedWhenNoPodQualifies(t *testing.T) {
	withReviewedExecLookup(t)
	srv := newExecLookupServer(t, reviewedExecResponder(t, []interface{}{
		execPodRow("web-a", "web", "Pending", false),
	}), func(s *relaySession) { t.Error("a review must not dial the relay") })

	_, err := reviewExecEnvironment(context.Background(), api.NewClient(srv.URL, "tok", false))
	if err == nil {
		t.Fatal("expected an error when no pod in the environment is ready")
	}
	for _, want := range []string{`"abbott-cgm"`, `environment "production"`, execTestEnvironmentID, "1 pod", "web-a (Pending, not ready)", "no primary fallback"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %s: %v", want, err)
		}
	}
}

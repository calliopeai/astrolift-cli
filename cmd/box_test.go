package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

// resetBoxFlags clears the package-level box flags between tests.
func resetBoxFlags() {
	boxEnsureAgent = ""
	boxEnsureSpec = ""
	boxEnsureName = ""
	boxEnsureIdle = ""
	boxEnsureWait = false
	boxEnsureJSON = false
	boxListAll = false
	boxListJSON = false
	boxRmYes = false
	boxAttachAgent = ""
	boxAttachSpec = ""
	boxAttachIdle = ""
}

// boxPayload is the server-side shape of one AstroliftAgentBox.
func boxPayload(slug, status string) map[string]interface{} {
	return map[string]interface{}{
		"slug":                slug,
		"name":                "A Box",
		"status":              status,
		"agentSlug":           "",
		"environmentSpecSlug": "claude-dev",
		"image":               "agent-claude:1",
		"idleTimeoutSeconds":  3600,
		"sessionName":         "astrolift",
		"attachCommand":       []interface{}{"tmux", "new-session", "-A", "-s", "astrolift"},
		"namespace":           "astrolift-agents-acme",
		"podName":             "agent-box-abc-xyz",
		"lastError":           "",
		"createdAt":           "2026-08-18T12:00:00Z",
		"startedAt":           "2026-08-18T12:00:05Z",
	}
}

// orgsPayload is what resolveOrg reads. A single org means no prompt.
func orgsPayload() interface{} {
	return []interface{}{
		map[string]interface{}{"id": "org-1", "slug": "acme", "name": "Acme"},
	}
}

// gqlServerFunc is a GraphQL stub whose response depends on the query, for the
// multi-step flows (ensure, then poll the same box until it comes up) that a
// single fixed payload can't express.
func gqlServerFunc(t *testing.T, respond func(gqlRequest) map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": respond(req)})
	}))
}

// errFromString builds a plain error for the annotation tests, which are about
// how a message is rewritten rather than about where it came from.
func errFromString(msg string) error { return errors.New(msg) }

func TestBoxEnsureSendsMutationAndPrintsAttachHint(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()
	boxEnsureSpec = "claude-dev"

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": orgsPayload(),
		"ensureAgentBox": map[string]interface{}{
			"ok":     true,
			"errors": []interface{}{},
			"data":   boxPayload("box-claude-dev", "provisioning"),
		},
	}, &captured)
	defer srv.Close()

	client := api.NewClient(srv.URL, "tok", false)
	cmd, out := agentTestCmd()
	if err := runBoxEnsure(cmd, context.Background(), client, &config.Config{}); err != nil {
		t.Fatalf("runBoxEnsure: %v", err)
	}

	if !strings.Contains(captured.Query, "ensureAgentBox(input: $input, orgId: $orgId)") {
		t.Errorf("did not call ensureAgentBox:\n%s", captured.Query)
	}
	input, ok := captured.Variables["input"].(map[string]interface{})
	if !ok {
		t.Fatalf("input var not an object: %v", captured.Variables["input"])
	}
	if input["environmentSpecSlug"] != "claude-dev" {
		t.Errorf("environmentSpecSlug = %v, want claude-dev", input["environmentSpecSlug"])
	}
	got := out.String()
	if !strings.Contains(got, "box-claude-dev") {
		t.Errorf("output missing box slug:\n%s", got)
	}
	// The point of the verb is that the caller learns how to get inside.
	if !strings.Contains(got, "astro box attach box-claude-dev") {
		t.Errorf("output missing the attach hint:\n%s", got)
	}
}

// An unset --idle-timeout must not be sent. Sending a zero we invented would
// read server-side as the explicit "never reap" sentinel and quietly pin a
// node forever — the exact failure the timeout exists to prevent.
func TestBoxEnsureOmitsIdleTimeoutWhenUnset(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()
	boxEnsureSpec = "claude-dev"

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": orgsPayload(),
		"ensureAgentBox": map[string]interface{}{
			"ok": true, "errors": []interface{}{}, "data": boxPayload("box-claude-dev", "provisioning"),
		},
	}, &captured)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	if err := runBoxEnsure(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runBoxEnsure: %v", err)
	}

	input := captured.Variables["input"].(map[string]interface{})
	if _, present := input["idleTimeoutSeconds"]; present {
		t.Errorf("idleTimeoutSeconds sent when the flag was unset: %v", input["idleTimeoutSeconds"])
	}
}

func TestBoxEnsureSendsParsedIdleTimeout(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()
	boxEnsureSpec = "claude-dev"
	boxEnsureIdle = "90m"

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": orgsPayload(),
		"ensureAgentBox": map[string]interface{}{
			"ok": true, "errors": []interface{}{}, "data": boxPayload("box-claude-dev", "provisioning"),
		},
	}, &captured)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	if err := runBoxEnsure(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runBoxEnsure: %v", err)
	}

	input := captured.Variables["input"].(map[string]interface{})
	if input["idleTimeoutSeconds"].(float64) != 5400 {
		t.Errorf("idleTimeoutSeconds = %v, want 5400", input["idleTimeoutSeconds"])
	}
}

// The verb has to refuse a request that names nothing to run, locally, rather
// than spending a round trip to be told the same thing.
func TestBoxEnsureRefusesWithNothingToRun(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()

	srv := gqlServer(t, map[string]interface{}{}, nil)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	err := runBoxEnsure(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{})
	if err == nil {
		t.Fatal("expected an error when neither --agent nor --env-spec is given")
	}
	if !strings.Contains(err.Error(), "--agent") || !strings.Contains(err.Error(), "--env-spec") {
		t.Errorf("error should name both flags, got: %v", err)
	}
}

func TestBoxEnsureSurfacesServerFailure(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()
	boxEnsureSpec = "claude-dev"

	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": orgsPayload(),
		"ensureAgentBox": map[string]interface{}{
			"ok": false,
			"errors": []interface{}{
				map[string]interface{}{"code": "VALIDATION", "message": "agent batch-bot is run mode once; only a persistent agent can back a box"},
			},
			"data": nil,
		},
	}, nil)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	err := runBoxEnsure(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{})
	if err == nil {
		t.Fatal("expected the server-side rejection to surface")
	}
	if !strings.Contains(err.Error(), "persistent") {
		t.Errorf("server message lost: %v", err)
	}
}

func TestBoxEnsureJSONEmitsTheRecord(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()
	boxEnsureSpec = "claude-dev"
	boxEnsureJSON = true

	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": orgsPayload(),
		"ensureAgentBox": map[string]interface{}{
			"ok": true, "errors": []interface{}{}, "data": boxPayload("box-claude-dev", "running"),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runBoxEnsure(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runBoxEnsure: %v", err)
	}

	// The IDE consumes this; it has to be a parseable record carrying the two
	// things a caller needs — where to go and what to run.
	var parsed agentBox
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n%s", err, out.String())
	}
	if parsed.Slug != "box-claude-dev" {
		t.Errorf("slug = %q", parsed.Slug)
	}
	if strings.Join(parsed.AttachCommand, " ") != "tmux new-session -A -s astrolift" {
		t.Errorf("attachCommand = %v", parsed.AttachCommand)
	}
}

// --wait must not report success while the box is still provisioning; the
// caller's next move is to attach, which fails against a pod that isn't up.
func TestBoxEnsureWaitPollsUntilRunning(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()
	boxEnsureSpec = "claude-dev"
	boxEnsureWait = true

	prev := boxWaitPollInterval
	boxWaitPollInterval = time.Millisecond
	defer func() { boxWaitPollInterval = prev }()

	polls := 0
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "ensureAgentBox") {
			return map[string]interface{}{
				"ensureAgentBox": map[string]interface{}{
					"ok": true, "errors": []interface{}{}, "data": boxPayload("box-claude-dev", "provisioning"),
				},
			}
		}
		if strings.Contains(req.Query, "agentBox(slug:") {
			polls++
			status := "provisioning"
			if polls >= 2 {
				status = "running"
			}
			return map[string]interface{}{"agentBox": boxPayload("box-claude-dev", status)}
		}
		return map[string]interface{}{"astroliftOrganizations": orgsPayload()}
	})
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runBoxEnsure(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runBoxEnsure: %v", err)
	}
	if polls < 2 {
		t.Errorf("polled %d time(s); --wait should have kept polling past provisioning", polls)
	}
	if !strings.Contains(out.String(), "running") {
		t.Errorf("final status not reported:\n%s", out.String())
	}
}

// A box that dies before it is attachable must fail the wait with the reason,
// not spin until the timeout.
func TestBoxEnsureWaitFailsFastOnASettledBox(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()
	boxEnsureSpec = "claude-dev"
	boxEnsureWait = true

	prev := boxWaitPollInterval
	boxWaitPollInterval = time.Millisecond
	defer func() { boxWaitPollInterval = prev }()

	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		if strings.Contains(req.Query, "ensureAgentBox") {
			return map[string]interface{}{
				"ensureAgentBox": map[string]interface{}{
					"ok": true, "errors": []interface{}{}, "data": boxPayload("box-claude-dev", "pending"),
				},
			}
		}
		if strings.Contains(req.Query, "agentBox(slug:") {
			failed := boxPayload("box-claude-dev", "failed")
			failed["lastError"] = "ANTHROPIC_API_KEY could not be resolved"
			return map[string]interface{}{"agentBox": failed}
		}
		return map[string]interface{}{"astroliftOrganizations": orgsPayload()}
	})
	defer srv.Close()

	cmd, _ := agentTestCmd()
	err := runBoxEnsure(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{})
	if err == nil {
		t.Fatal("expected --wait to fail on a box that ended before becoming attachable")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Errorf("the reason the box failed was dropped: %v", err)
	}
}

func TestBoxListDefaultsToWarmBoxesOnly(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": orgsPayload(),
		"agentBoxes":             []interface{}{boxPayload("box-claude-dev", "running")},
	}, &captured)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runBoxList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runBoxList: %v", err)
	}

	if captured.Variables["includeEnded"] != false {
		t.Errorf("includeEnded = %v, want false by default", captured.Variables["includeEnded"])
	}
	if !strings.Contains(out.String(), "box-claude-dev") {
		t.Errorf("box missing from the table:\n%s", out.String())
	}
}

func TestBoxListAllAsksForSettledBoxes(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()
	boxListAll = true

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"astroliftOrganizations": orgsPayload(),
		"agentBoxes":             []interface{}{},
	}, &captured)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	if err := runBoxList(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}); err != nil {
		t.Fatalf("runBoxList: %v", err)
	}
	if captured.Variables["includeEnded"] != true {
		t.Errorf("includeEnded = %v, want true under --all", captured.Variables["includeEnded"])
	}
}

// "0" is the never-reap sentinel, and rendering it as a bare 0s reads like
// "reaps immediately" — the opposite of what it means.
func TestFormatIdleTimeoutNamesTheNeverCase(t *testing.T) {
	if got := formatIdleTimeout(0); got != "never" {
		t.Errorf("formatIdleTimeout(0) = %q, want never", got)
	}
	if got := formatIdleTimeout(5400); got != "1h30m0s" {
		t.Errorf("formatIdleTimeout(5400) = %q", got)
	}
}

func TestParseIdleTimeoutAcceptsDurationsSecondsAndNever(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"never", 0},
		{"NEVER", 0},
		{"0", 0},
		{"3600", 3600},
		{"90m", 5400},
		{"4h", 14400},
	}
	for _, c := range cases {
		got, err := parseIdleTimeout(c.in)
		if err != nil {
			t.Errorf("parseIdleTimeout(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseIdleTimeout(%q) = %d, want %d", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"soon", "-5", "-1h", "1 hour"} {
		if _, err := parseIdleTimeout(bad); err == nil {
			t.Errorf("parseIdleTimeout(%q) should have failed", bad)
		}
	}
}

func TestBoxRmSendsDestroy(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()
	boxRmYes = true

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"destroyAgentBox": map[string]interface{}{"ok": true, "errors": []interface{}{}},
	}, &captured)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runBoxRm(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), "box-claude-dev"); err != nil {
		t.Fatalf("runBoxRm: %v", err)
	}
	if !strings.Contains(captured.Query, "destroyAgentBox(slug: $slug)") {
		t.Errorf("did not call destroyAgentBox:\n%s", captured.Query)
	}
	if captured.Variables["slug"] != "box-claude-dev" {
		t.Errorf("slug = %v", captured.Variables["slug"])
	}
	if !strings.Contains(out.String(), "destroyed") {
		t.Errorf("no confirmation printed:\n%s", out.String())
	}
}

func TestBoxAttachRejectsAnUnknownSlug(t *testing.T) {
	resetBoxFlags()
	defer resetBoxFlags()

	srv := gqlServer(t, map[string]interface{}{"agentBox": nil}, nil)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	err := runBoxAttach(cmd, context.Background(), api.NewClient(srv.URL, "tok", false), &config.Config{}, "nope")
	if err == nil {
		t.Fatal("expected an error for a slug that does not resolve")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should say the box was not found: %v", err)
	}
}

// The exec relay admits registered apps only, and a box is not one, so a
// healthy box surfaces as a permission error. Left bare it sends the reader
// auditing their own grants for a gap that is the platform's.
func TestBoxAttachExplainsTheExecRelayGap(t *testing.T) {
	box := &agentBox{
		Slug:          "box-claude-dev",
		Status:        "running",
		Namespace:     "astrolift-agents-acme",
		PodName:       "agent-box-abc-xyz",
		AttachCommand: []string{"tmux", "new-session", "-A", "-s", "astrolift"},
	}
	annotated := annotateBoxExecError(box, errFromString("exec denied — you need the app.exec_pod permission"))

	msg := annotated.Error()
	if !strings.Contains(msg, "astrolift#128") {
		t.Errorf("annotation should point at the platform gap:\n%s", msg)
	}
	// And it must leave the reader a way in right now.
	if !strings.Contains(msg, "kubectl -n astrolift-agents-acme exec -it agent-box-abc-xyz") {
		t.Errorf("annotation should offer the direct route:\n%s", msg)
	}
}

// An unrelated exec failure must pass through untouched rather than acquire a
// misleading explanation.
func TestBoxAttachLeavesUnrelatedExecErrorsAlone(t *testing.T) {
	box := &agentBox{Slug: "box-claude-dev", Status: "running"}
	original := errFromString("connecting exec socket: dial tcp: i/o timeout")
	if got := annotateBoxExecError(box, original); got != original {
		t.Errorf("unrelated error was rewritten: %v", got)
	}
}

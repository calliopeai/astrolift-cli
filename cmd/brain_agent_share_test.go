package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// astro brain share/unshare --agent and agent shares in show (astrolift-app#2449).

func brainWithAgentShares(slug string) map[string]interface{} {
	b := brainPayload(slug)
	b["agentShares"] = []interface{}{map[string]interface{}{"agent": "triage", "app": "support-agents", "grantedAt": "2026-10-17T10:00:00+00:00"}}
	return b
}

// olderBrainServer answers like a server before #2449: agentShares and
// agentSlug are unknown, everything else works. It records what it was sent.
func olderBrainServer(t *testing.T, sent *[]gqlRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		*sent = append(*sent, req)
		if strings.Contains(req.Query, "agentShares") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"errors": []interface{}{map[string]interface{}{"message": `Cannot query field "agentShares" on type "AstroliftBrain".`}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": map[string]interface{}{"astroliftBrain": brainPayload("acme-brain")}})
	}))
}

func TestBrainShowListsAgentShares(t *testing.T) {
	resetBrainFlags()
	srv := gqlServerFunc(t, func(gqlRequest) map[string]interface{} {
		return map[string]interface{}{"astroliftBrain": brainWithAgentShares("acme-brain")}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)

	if err := runBrainShow(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Shared with agent:") || !strings.Contains(out.String(), "triage (viewer, app support-agents)") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBrainShowOnAnOlderServerFallsBackWithoutAgentShares(t *testing.T) {
	resetBrainFlags()
	var sent []gqlRequest
	srv := olderBrainServer(t, &sent)
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)
	_ = cmd.Flags().Set("json", "true")

	if err := runBrainShow(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || strings.Contains(sent[1].Query, "agentShares") {
		t.Fatalf("sent = %+v", sent)
	}
	if strings.Contains(out.String(), "agentShares") || !strings.Contains(out.String(), `"slug": "acme-brain"`) {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBrainShareWithAnAgentSendsAgentSlugAsViewer(t *testing.T) {
	resetBrainFlags()
	brainAgent, brainYes = "triage", true
	var sent []gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		sent = append(sent, req)
		if strings.Contains(req.Query, "shareBrain") {
			return map[string]interface{}{"shareBrain": brainOK(brainWithAgentShares("acme-brain"))}
		}
		probe := brainPayload("acme-brain")
		probe["agentShares"] = []interface{}{}
		return map[string]interface{}{"astroliftBrain": probe}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)

	if err := runBrainShare(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || !strings.Contains(sent[0].Query, "astroliftBrain") {
		t.Fatalf("sent = %+v", sent)
	}
	want := map[string]interface{}{"slug": "acme-brain", "agentSlug": "triage", "access": "VIEWER"}
	if !reflect.DeepEqual(sent[1].Variables["input"], want) {
		t.Fatalf("input = %v", sent[1].Variables["input"])
	}
	if !strings.Contains(out.String(), "triage (viewer, app support-agents)") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBrainUnshareWithAnAgentSendsAgentSlug(t *testing.T) {
	resetBrainFlags()
	brainAgent = "triage"
	var sent []gqlRequest
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		sent = append(sent, req)
		if strings.Contains(req.Query, "unshareBrain") {
			b := brainPayload("acme-brain")
			b["agentShares"] = []interface{}{}
			return map[string]interface{}{"unshareBrain": brainOK(b)}
		}
		return map[string]interface{}{"astroliftBrain": brainWithAgentShares("acme-brain")}
	})
	defer srv.Close()
	cmd, out, _ := brainTestCmd(false)

	if err := runBrainUnshare(cmd, context.Background(), brainClient(srv), "acme-brain"); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || !reflect.DeepEqual(sent[1].Variables["input"], map[string]interface{}{"slug": "acme-brain", "agentSlug": "triage"}) {
		t.Fatalf("sent = %+v", sent)
	}
	if !strings.Contains(out.String(), "Agent triage no longer reads acme-brain") {
		t.Fatalf("output = %s", out.String())
	}
}

func TestBrainShareWithAnAgentOnAnOlderServerWritesNothing(t *testing.T) {
	for _, unshare := range []bool{false, true} {
		resetBrainFlags()
		brainAgent, brainYes = "triage", true
		var sent []gqlRequest
		srv := olderBrainServer(t, &sent)
		cmd, _, _ := brainTestCmd(false)

		run := runBrainShare
		if unshare {
			run = runBrainUnshare
		}
		err := run(cmd, context.Background(), brainClient(srv), "acme-brain")
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "#2449") {
			t.Fatalf("unshare=%v err = %v", unshare, err)
		}
		for _, req := range sent {
			if strings.Contains(req.Query, "mutation") {
				t.Fatalf("unshare=%v wrote on an older server: %s", unshare, req.Query)
			}
		}
	}
}

func TestBrainShareTargetAndAgentAccessRefusedBeforeAnyCall(t *testing.T) {
	srv := gqlServerFunc(t, func(req gqlRequest) map[string]interface{} {
		t.Fatalf("sent: %s", req.Query)
		return nil
	})
	defer srv.Close()

	resetBrainFlags()
	cmd, _, _ := brainTestCmd(false)
	if err := runBrainShare(cmd, context.Background(), brainClient(srv), "acme-brain"); err == nil || !strings.Contains(err.Error(), "--agent") {
		t.Fatalf("neither: %v", err)
	}
	brainAgent = "triage"
	cmd, _, _ = brainTestCmd(false)
	_ = cmd.Flags().Set("team", "sales")
	if err := runBrainUnshare(cmd, context.Background(), brainClient(srv), "acme-brain"); err == nil || !strings.Contains(err.Error(), "--team") {
		t.Fatalf("both: %v", err)
	}
	brainAccess = "editor"
	cmd, _, _ = brainTestCmd(false)
	if err := runBrainShare(cmd, context.Background(), brainClient(srv), "acme-brain"); err == nil || !strings.Contains(err.Error(), "viewer") {
		t.Fatalf("editor: %v", err)
	}
	resetBrainFlags()
}

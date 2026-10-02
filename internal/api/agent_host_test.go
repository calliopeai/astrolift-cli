package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const hostTaskID = "00000000-0000-0000-0000-000000000001"

func hostFixture(t *testing.T, token, org string, terminal, deny bool) (*httptest.Server, <-chan []string) {
	t.Helper()
	methods := make(chan []string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/ahp" || r.Header.Get("Authorization") != "Bearer "+token || r.Header.Get("X-Astrolift-Organization") != org {
			t.Error("wrong server, credentials or organization")
			w.WriteHeader(403)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		var seen []string
		defer func() { methods <- seen }()
		channel := "ahp-session:/" + hostTaskID + "/chat"
		if terminal {
			channel = "ahp-terminal:/" + hostTaskID + "/attachment"
		}
		for {
			var request struct {
				ID     interface{}            `json:"id"`
				Method string                 `json:"method"`
				Params map[string]interface{} `json:"params"`
			}
			if err := conn.ReadJSON(&request); err != nil {
				return
			}
			seen = append(seen, request.Method)
			var result interface{}
			switch request.Method {
			case "initialize":
				versions := request.Params["protocolVersions"].([]interface{})
				if len(versions) != 1 || versions[0] != "1.0.0" {
					t.Error("did not negotiate the pinned protocol")
				}
				root := map[string]interface{}{"agents": []interface{}{}, "_meta": map[string]interface{}{"astrolift.nextClientSeq": 9}}
				if terminal {
					root["terminals"] = []map[string]interface{}{{"resource": channel, "title": "Box", "claim": map[string]string{"kind": "client", "clientId": "client"}, "lifecycle": map[string]string{"status": "running"}}}
				}
				result = map[string]interface{}{"protocolVersion": "1.0.0", "serverSeq": 10, "snapshots": []map[string]interface{}{{"resource": "ahp-root://", "fromSeq": 10, "state": root}}}
			case "subscribe":
				if request.Params["channel"] != channel {
					t.Error("subscribed to the wrong resource")
				}
				state := map[string]interface{}{"resource": channel, "title": "Task", "status": 8, "modifiedAt": "2026-09-29T00:00:00Z", "turns": []interface{}{}}
				if terminal {
					state = map[string]interface{}{"title": "Box", "content": []map[string]string{{"type": "unclassified", "value": "PTY history"}}, "claim": map[string]string{"kind": "client", "clientId": "client"}, "lifecycle": map[string]string{"status": "running"}, "isPty": true}
				}
				result = map[string]interface{}{"snapshot": map[string]interface{}{"resource": channel, "fromSeq": 10, "state": state}}
			case "dispatchAction":
				if request.Params["clientSeq"] != float64(9) {
					t.Errorf("wrong client sequence: %v", request.Params["clientSeq"])
				}
				params := map[string]interface{}{"channel": channel, "action": request.Params["action"], "serverSeq": 11, "origin": map[string]interface{}{"clientId": "client", "clientSeq": 9}}
				if deny {
					params["rejectionReason"] = "Viewer lacks controller permission"
				}
				_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "method": "action", "params": params})
				continue
			default:
				t.Errorf("unexpected method %s", request.Method)
				return
			}
			_ = conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}))
	return server, methods
}

func TestAgentHostScopingConcurrentServersAndDetach(t *testing.T) {
	var wg sync.WaitGroup
	for _, name := range []string{"one", "two"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			server, methods := hostFixture(t, name+"-token", name+"-org", false, false)
			defer server.Close()
			client := NewClient(server.URL, name+"-token", false)
			client.SetOrg(name + "-org")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			session, err := client.AttachAgentSession(ctx, hostTaskID, "", "client")
			if err != nil {
				t.Error(err)
				return
			}
			if !strings.Contains(string(session.Snapshot.State), `"turns"`) {
				t.Error("chat history missing")
			}
			receipt, err := session.Dispatch(ctx, json.RawMessage(`{"type":"chat/pendingMessageSet","kind":"steering","id":"00000000-0000-0000-0000-000000000002","message":{"text":"Review","origin":{"kind":"user"}}}`), nil)
			if err != nil || receipt == nil {
				t.Errorf("dispatch failed: %v", err)
			}
			_ = session.Close(ctx)
			select {
			case seen := <-methods:
				if strings.Join(seen, ",") != "initialize,subscribe,dispatchAction" {
					t.Errorf("detach emitted extra methods: %v", seen)
				}
			case <-ctx.Done():
				t.Error("detach did not close transport")
			}
		}(name)
	}
	wg.Wait()
}

func TestAgentHostViewerRejectionIsAnError(t *testing.T) {
	server, _ := hostFixture(t, "token", "org", false, true)
	defer server.Close()
	client := NewClient(server.URL, "token", false)
	client.SetOrg("org")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.AttachAgentSession(ctx, hostTaskID, "", "client")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close(ctx) }()
	_, err = session.Dispatch(ctx, json.RawMessage(`{"type":"chat/pendingMessageSet","kind":"steering","id":"00000000-0000-0000-0000-000000000002","message":{"text":"Review","origin":{"kind":"user"}}}`), nil)
	if err == nil || !strings.Contains(err.Error(), "Viewer lacks") {
		t.Fatalf("viewer denial was lost: %v", err)
	}
}

func TestAgentHostTerminalSnapshotRetainsLifecycleAndPTYContent(t *testing.T) {
	server, _ := hostFixture(t, "token", "org", true, false)
	defer server.Close()
	client := NewClient(server.URL, "token", false)
	client.SetOrg("org")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := client.AttachAgentSession(ctx, "", hostTaskID, "client")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close(ctx) }()
	if !strings.Contains(string(session.Snapshot.State), "PTY history") || !strings.Contains(string(session.Snapshot.State), `"isPty":true`) {
		t.Fatalf("terminal state was decoded as a session: %s", session.Snapshot.State)
	}
}

func TestAgentHostExplicitUnavailableSupportsWatchFallback(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(closed)
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var request map[string]interface{}
		if err := conn.ReadJSON(&request); err != nil {
			t.Error(err)
			return
		}
		if err := conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": request["id"], "error": map[string]interface{}{"code": -32003, "message": "Attach unavailable", "data": map[string]interface{}{"ahp_available": false, "reason": "agent_live_attach disabled", "fallback": "agent_task.watch"}}}); err != nil {
			t.Error(err)
			return
		}
		// Keep this RPC-error fixture open until the caller detaches. Closing
		// immediately races the typed response against transport shutdown.
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()
	client := NewClient(server.URL, "token", false)
	client.SetOrg("org")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := client.AttachAgentSession(ctx, hostTaskID, "", "client")
	var unavailable *AgentHostUnavailable
	if !errors.As(err, &unavailable) || !strings.Contains(unavailable.Reason, "disabled") {
		t.Fatalf("missing fallback reason: %v", err)
	}
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("unavailable attach did not detach its transport")
	}
}

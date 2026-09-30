package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// These actions were checked against the pinned 1.0 reducer. The SDK supplies
// RPC and WebSocket lifecycle; notification bodies must retain the 1.0 wire.
func TestPinnedHostActionsSurviveSDKTransport(t *testing.T) {
	raw, err := os.ReadFile("testdata/agent-host-actions.json")
	if err != nil {
		t.Fatal(err)
	}
	var actions []json.RawMessage
	if err := json.Unmarshal(raw, &actions); err != nil {
		t.Fatal(err)
	}
	// Exceed the SDK's subscription buffer to prove we do not silently drop.
	for len(actions) < 600 {
		actions = append(actions, actions...)
	}
	channel := "ahp-session:/" + hostTaskID + "/chat"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		for {
			var request struct {
				ID     interface{} `json:"id"`
				Method string      `json:"method"`
			}
			if err := conn.ReadJSON(&request); err != nil {
				return
			}
			var result interface{}
			if request.Method == "initialize" {
				result = map[string]interface{}{"protocolVersion": "1.0.0", "serverSeq": 0, "snapshots": []map[string]interface{}{{"resource": "ahp-root://", "fromSeq": 0, "state": map[string]interface{}{"agents": []interface{}{}}}}}
			} else {
				result = map[string]interface{}{"snapshot": map[string]interface{}{"resource": channel, "fromSeq": 0, "state": map[string]interface{}{"turns": []interface{}{}}}}
			}
			if err := conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
				return
			}
			if request.Method == "subscribe" {
				for i, action := range actions {
					if err := conn.WriteJSON(map[string]interface{}{"jsonrpc": "2.0", "method": "action", "params": map[string]interface{}{"channel": channel, "action": action, "serverSeq": i + 1}}); err != nil {
						return
					}
				}
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := NewClient(server.URL, "token", false)
	client.SetOrg("org")
	session, err := client.AttachAgentSession(ctx, hostTaskID, "", "client")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close(ctx) }()
	// Let the producer fill the bounded queue before reading it.
	time.Sleep(50 * time.Millisecond)
	for i, original := range actions {
		select {
		case received, ok := <-session.Events():
			if !ok {
				t.Fatalf("disconnected after %d/%d events", i, len(actions))
			}
			var before, after interface{}
			_ = json.Unmarshal(original, &before)
			_ = json.Unmarshal(received.Action, &after)
			if received.ServerSeq != int64(i+1) || !reflect.DeepEqual(before, after) {
				t.Fatalf("action %d changed or lost: %s", i, received.Action)
			}
		case <-ctx.Done():
			t.Fatalf("event %d dropped: %v", i, ctx.Err())
		}
	}
}

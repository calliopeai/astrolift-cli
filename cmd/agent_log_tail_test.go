package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
)

func TestAgentLogFollowersContinueWhenTailRolls(t *testing.T) {
	for _, dispatch := range []bool{false, true} {
		name := "logs-follow"
		if dispatch {
			name = "dispatch-tail"
		}
		t.Run(name, func(t *testing.T) {
			priorFollow, priorTail, priorInterval := agentLogsFollow, agentLogsTail, agentLogsPollIntervalForTest
			agentLogsFollow, agentLogsTail, agentLogsPollIntervalForTest = true, 3, time.Millisecond
			defer func() {
				agentLogsFollow, agentLogsTail, agentLogsPollIntervalForTest = priorFollow, priorTail, priorInterval
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			snapshots := [][]string{{"a", "b", "c"}, {"b", "c", "d"}, {"c", "d", "e"}}
			var mu sync.Mutex
			polls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Query string `json:"query"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if strings.Contains(req.Query, "agentTaskLogs") {
					if polls == len(snapshots) {
						cancel()
						return
					}
					lines := snapshots[polls]
					polls++
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"agentTaskLogs": lines}})
				} else {
					status := "running"
					if polls == len(snapshots) {
						status = "completed"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"agentTask": map[string]any{"status": status}}})
				}
			}))
			defer srv.Close()
			client := api.NewClient(srv.URL, "fixture-token", false)
			command, out := agentTestCmd()
			var err error
			if dispatch {
				err = tailAgentTask(command, ctx, client, "fixture-task")
			} else {
				err = runAgentLogs(command, ctx, client, "fixture-task")
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := "a\nb\nc\nd\ne\n"
			if dispatch {
				expected += "Final status: completed\n"
			}
			if out.String() != expected {
				t.Fatalf("rolling tail output = %q, want %q", out.String(), expected)
			}
		})
	}
}

func TestAgentLogTailSnapshots(t *testing.T) {
	tests := []struct {
		name      string
		snapshots [][]string
		want      []string
	}{
		{"growing", [][]string{{"a"}, {"a", "b"}, {"a", "b"}}, []string{"a", "b"}},
		{"shorter overlapping tail", [][]string{{"a", "b", "c"}, {"c", "d"}}, []string{"a", "b", "c", "d"}},
		{"empty transient", [][]string{{"a", "b"}, nil, {"a", "b", "c"}}, []string{"a", "b", "c"}},
		{"no overlap after rotation", [][]string{{"a", "b"}, {"c", "d"}}, []string{"a", "b", "c", "d"}},
		{"repeated new lines", [][]string{{"a", "b"}, {"b", "c", "c"}}, []string{"a", "b", "c", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tail agentLogTail
			var got []string
			for _, snapshot := range tt.snapshots {
				got = append(got, tail.append(snapshot)...)
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
)

func resetAgentSendFlags() {
	agentSendJSON = false
	agentSendStdin = false
}

// queuedMessage builds a sendAgentTaskInput data row, defaulting to the
// state a fresh send actually returns: queued, not yet delivered.
func queuedMessage(over map[string]interface{}) map[string]interface{} {
	row := map[string]interface{}{
		"id":          "01a00791-6806-7a82-a221-0cb76d192393",
		"message":     "also check the migration",
		"author":      "ada",
		"createdAt":   "2026-08-15T22:36:04+00:00",
		"deliveredAt": nil,
	}
	for k, v := range over {
		row[k] = v
	}
	return row
}

// ---- message resolution (pure helper) --------------------------------------

func TestAgentSendMessageResolution(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		stdin   string
		useIn   bool
		want    string
		wantErr string
	}{
		{
			name: "positional argument",
			args: []string{"task-1", "also check the migration"},
			want: "also check the migration",
		},
		{
			name: "positional argument is trimmed",
			args: []string{"task-1", "  nudge\n"},
			want: "nudge",
		},
		{
			name:  "stdin",
			args:  []string{"task-1"},
			stdin: "read from a pipe\n",
			useIn: true,
			want:  "read from a pipe",
		},
		{
			// The reason --stdin exists: a shell mangles this and a
			// multi-line instruction must survive intact.
			name:  "stdin keeps interior newlines",
			args:  []string{"task-1"},
			stdin: "\n first line\n\n second line \n\n",
			useIn: true,
			want:  "first line\n\n second line",
		},
		{
			name:    "both sources is ambiguous",
			args:    []string{"task-1", "inline"},
			stdin:   "piped",
			useIn:   true,
			wantErr: "not both",
		},
		{
			name:    "no input at all",
			args:    []string{"task-1"},
			wantErr: "input is required",
		},
		{
			// A blank positional must not be mistaken for "message given";
			// it would post an empty string the server then rejects.
			name:    "blank positional is not input",
			args:    []string{"task-1", "   "},
			wantErr: "input is required",
		},
		{
			name:    "empty stdin",
			args:    []string{"task-1"},
			stdin:   "  \n\t ",
			useIn:   true,
			wantErr: "no input read from stdin",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _ := agentTestCmd()
			cmd.SetIn(bytes.NewBufferString(tc.stdin))

			got, err := agentSendMessage(cmd, tc.args, tc.useIn)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got message %q", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("agentSendMessage: %v", err)
			}
			if got != tc.want {
				t.Errorf("message = %q, want %q", got, tc.want)
			}
		})
	}
}

// ---- the mutation call -----------------------------------------------------

func TestAgentSendPostsTaskIDAndMessage(t *testing.T) {
	resetAgentSendFlags()
	defer resetAgentSendFlags()

	var captured gqlRequest
	srv := gqlServer(t, map[string]interface{}{
		"sendAgentTaskInput": map[string]interface{}{
			"ok": true, "errors": []interface{}{}, "data": queuedMessage(nil),
		},
	}, &captured)
	defer srv.Close()

	cmd, out := agentTestCmd()
	err := runAgentSend(cmd, context.Background(), api.NewClient(srv.URL, "tok", false),
		&config.Config{}, "task-aaa", "also check the migration")
	if err != nil {
		t.Fatalf("runAgentSend: %v", err)
	}

	if !strings.Contains(captured.Query, "sendAgentTaskInput") {
		t.Errorf("expected the sendAgentTaskInput mutation, got:\n%s", captured.Query)
	}
	if captured.Variables["taskId"] != "task-aaa" {
		t.Errorf("taskId = %v, want task-aaa", captured.Variables["taskId"])
	}
	if captured.Variables["message"] != "also check the migration" {
		t.Errorf("message = %v", captured.Variables["message"])
	}

	// The human output must not imply the agent has already acted on it —
	// delivery happens later, at the agent's next turn boundary.
	got := out.String()
	if !strings.Contains(got, "task-aaa") {
		t.Errorf("expected the task id in output:\n%s", got)
	}
	if !strings.Contains(got, "not delivered yet") {
		t.Errorf("output should say the message is not delivered yet:\n%s", got)
	}
}

func TestAgentSendJSONEmitsTheQueuedMessage(t *testing.T) {
	resetAgentSendFlags()
	defer resetAgentSendFlags()
	agentSendJSON = true

	srv := gqlServer(t, map[string]interface{}{
		"sendAgentTaskInput": map[string]interface{}{
			"ok": true, "errors": []interface{}{}, "data": queuedMessage(nil),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAgentSend(cmd, context.Background(), api.NewClient(srv.URL, "tok", false),
		&config.Config{}, "task-aaa", "also check the migration"); err != nil {
		t.Fatalf("runAgentSend: %v", err)
	}

	var got agentTaskInputMessage
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding --json output %q: %v", out.String(), err)
	}
	if got.ID == "" || got.Message != "also check the migration" || got.Author != "ada" {
		t.Errorf("unexpected message record: %+v", got)
	}
	// deliveredAt null is the whole signal a caller polls on — it must
	// survive as null rather than being flattened to "".
	if got.DeliveredAt != nil {
		t.Errorf("deliveredAt = %v, want null on a freshly queued message", *got.DeliveredAt)
	}
}

func TestAgentSendJSONKeepsDeliveredAtWhenSet(t *testing.T) {
	resetAgentSendFlags()
	defer resetAgentSendFlags()
	agentSendJSON = true

	srv := gqlServer(t, map[string]interface{}{
		"sendAgentTaskInput": map[string]interface{}{
			"ok":     true,
			"errors": []interface{}{},
			"data": queuedMessage(map[string]interface{}{
				"deliveredAt": "2026-08-15T22:40:00+00:00",
			}),
		},
	}, nil)
	defer srv.Close()

	cmd, out := agentTestCmd()
	if err := runAgentSend(cmd, context.Background(), api.NewClient(srv.URL, "tok", false),
		&config.Config{}, "task-aaa", "nudge"); err != nil {
		t.Fatalf("runAgentSend: %v", err)
	}

	var got agentTaskInputMessage
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decoding --json output: %v", err)
	}
	if got.DeliveredAt == nil || *got.DeliveredAt != "2026-08-15T22:40:00+00:00" {
		t.Errorf("deliveredAt not surfaced: %+v", got)
	}
}

// ---- the failure envelope --------------------------------------------------

func TestAgentSendSurfacesTheMutationError(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		message string
	}{
		// The task is not running, so there is no turn boundary left.
		{"precondition", "PRECONDITION", "task is not accepting input (completed)"},
		// A foreign or unknown task id reads the same — the server does not
		// leak which.
		{"not found", "NOT_FOUND", "task not found"},
		{"denied", "PERMISSION_DENIED", "agent_task.send_input required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetAgentSendFlags()
			defer resetAgentSendFlags()

			srv := gqlServer(t, map[string]interface{}{
				"sendAgentTaskInput": map[string]interface{}{
					"ok": false,
					"errors": []interface{}{
						map[string]interface{}{
							"code": tc.code, "message": tc.message, "field": nil,
						},
					},
					"data": nil,
				},
			}, nil)
			defer srv.Close()

			cmd, out := agentTestCmd()
			err := runAgentSend(cmd, context.Background(), api.NewClient(srv.URL, "tok", false),
				&config.Config{}, "task-aaa", "nudge")

			if err == nil {
				t.Fatal("expected an error for a failed envelope")
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("error %q does not carry the server message %q", err.Error(), tc.message)
			}
			// A failed send must not print a success line.
			if strings.Contains(out.String(), "Queued") {
				t.Errorf("printed a success line on failure:\n%s", out.String())
			}
		})
	}
}

func TestAgentSendFailsWithoutAServerMessage(t *testing.T) {
	resetAgentSendFlags()
	defer resetAgentSendFlags()

	srv := gqlServer(t, map[string]interface{}{
		"sendAgentTaskInput": map[string]interface{}{
			"ok": false, "errors": []interface{}{}, "data": nil,
		},
	}, nil)
	defer srv.Close()

	cmd, _ := agentTestCmd()
	err := runAgentSend(cmd, context.Background(), api.NewClient(srv.URL, "tok", false),
		&config.Config{}, "task-aaa", "nudge")

	if err == nil || !strings.Contains(err.Error(), "unknown error") {
		t.Fatalf("expected a generic failure, got %v", err)
	}
}

// ---- registration ----------------------------------------------------------

func TestAgentSendIsRegisteredUnderAgent(t *testing.T) {
	var found bool
	for _, c := range agentCmd.Commands() {
		if c.Name() == "send" {
			found = true
			if !c.Flags().HasAvailableFlags() {
				t.Error("agent send has no flags; --json and --stdin should be registered")
			}
			for _, flag := range []string{"json", "stdin"} {
				if c.Flags().Lookup(flag) == nil {
					t.Errorf("agent send is missing the --%s flag", flag)
				}
			}
			// Task id is required; the input may come from stdin instead.
			if err := c.Args(c, []string{}); err == nil {
				t.Error("agent send should require a task id")
			}
			if err := c.Args(c, []string{"t", "msg", "extra"}); err == nil {
				t.Error("agent send should reject a third argument")
			}
		}
	}
	if !found {
		t.Fatal("agent send is not registered under 'astro agent'")
	}
}

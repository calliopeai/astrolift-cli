package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

var agentSessionCmd = &cobra.Command{Use: "session", Short: "Attach to an existing agent session"}

var agentSessionAttachCmd = &cobra.Command{
	Use: "attach <task-or-box-id>", Short: "Follow agent turns and tool calls without restarting the agent",
	Long: `Join an existing task using the control plane's Agent Host Protocol.
The initial snapshot contains history, followed by live actions. Ctrl-C detaches.
Use --steer, --approve, --deny, or --action-file to send one controller action.

With --box, the id selects a box terminal. --interactive enables keyboard input;
Ctrl-] detaches and leaves the box's tmux session running. Missing gVisor, network
fence or gateway controls refuse box attach. An unavailable task host falls back
to its durable event watch; controller actions require AHP to be available.`,
	Args: cobra.ExactArgs(1), RunE: runAgentSessionAttach,
}

func init() {
	addAgentSessionAttachFlags(agentSessionAttachCmd.Flags())
	agentSessionCmd.AddCommand(agentSessionAttachCmd)
	agentCmd.AddCommand(agentSessionCmd)
}

func addAgentSessionAttachFlags(f *pflag.FlagSet) {
	f.Bool("box", false, "Attach to a box terminal instead of a task")
	f.Bool("interactive", false, "Enable box keyboard input (Ctrl-] detaches)")
	f.Bool("snapshot", false, "Print history and detach immediately")
	f.String("steer", "", "Queue an explicit steering message")
	f.String("approve", "", "Approve a pending tool call by its displayed id")
	f.String("deny", "", "Deny a pending tool call by its displayed id")
	f.String("reason", "", "Reason for denying the tool")
	f.String("action-file", "", "Read one AHP controller action from a JSON file, or - for stdin")
	f.String("client-id", "", "Stable attachment identity (defaults to a fresh UUID)")
}

func validateAgentSessionFlags(cmd *cobra.Command) error {
	box, interactive := boolFlag(cmd, "box"), boolFlag(cmd, "interactive")
	if interactive && (!box || boolFlag(cmd, "json") || boolFlag(cmd, "snapshot")) {
		return fmt.Errorf("--interactive requires --box, text output, and a live attachment")
	}
	count := 0
	for _, name := range []string{"steer", "approve", "deny", "action-file"} {
		if cmd.Flags().Changed(name) {
			count++
			value, _ := cmd.Flags().GetString(name)
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("--%s cannot be empty", name)
			}
			if box && name != "action-file" {
				return fmt.Errorf("--%s requires a task session", name)
			}
		}
	}
	if count > 1 || count > 0 && (interactive || boolFlag(cmd, "snapshot")) {
		return fmt.Errorf("choose one controller action, a snapshot, or interactive input")
	}
	if cmd.Flags().Changed("reason") && !cmd.Flags().Changed("deny") {
		return fmt.Errorf("--reason requires --deny")
	}
	return nil
}

func runAgentSessionAttach(cmd *cobra.Command, args []string) error {
	id, err := uuid.Parse(args[0])
	if err != nil {
		return fmt.Errorf("session id must be a task or box UUID")
	}
	if err := validateAgentSessionFlags(cmd); err != nil {
		return err
	}
	box, interactive := boolFlag(cmd, "box"), boolFlag(cmd, "interactive")
	client, _, err := loadScopedAgentClient(cmd)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	clientID, _ := cmd.Flags().GetString("client-id")
	if clientID == "" {
		clientID = "astro-" + uuid.NewString()
	}
	taskID, boxID := id.String(), ""
	if box {
		taskID, boxID = "", taskID
	}
	session, err := client.AttachAgentSession(ctx, taskID, boxID, clientID)
	if err != nil {
		var unavailable *api.AgentHostUnavailable
		if !box && errors.As(err, &unavailable) && !cmd.Flags().Changed("steer") && !cmd.Flags().Changed("approve") && !cmd.Flags().Changed("deny") && !cmd.Flags().Changed("action-file") {
			fmt.Fprintln(cmd.ErrOrStderr(), unavailable.Reason+"; following durable task events")
			return watchAgentTaskEvents(cmd, ctx, client, taskID)
		}
		return err
	}
	defer func() { _ = session.Close(context.Background()) }()
	emit := func(value interface{}) {
		if boolFlag(cmd, "json") {
			_ = json.NewEncoder(cmd.OutOrStdout()).Encode(value)
			return
		}
		encoded, _ := json.Marshal(value)
		var envelope struct {
			Action map[string]interface{} `json:"action"`
		}
		if json.Unmarshal(encoded, &envelope) == nil && envelope.Action != nil {
			printAgentHostAction(cmd.OutOrStdout(), envelope.Action)
			return
		}
		printAgentHostHistory(cmd.OutOrStdout(), session.Snapshot.State)
	}
	emit(session.Snapshot)
	other := func(event api.AgentHostAction) { emit(event) }
	action, err := agentSessionAction(cmd, session.Snapshot.State)
	if err != nil {
		return err
	}
	if len(action) > 0 {
		receipt, err := session.Dispatch(ctx, action, other)
		if receipt != nil {
			emit(receipt)
		}
		return err
	}
	if boolFlag(cmd, "snapshot") {
		return nil
	}
	var input <-chan []byte
	var sizes <-chan time.Time
	if interactive {
		file, ok := cmd.InOrStdin().(*os.File)
		if !ok || !term.IsTerminal(int(file.Fd())) {
			return fmt.Errorf("interactive box attach requires a terminal on stdin")
		}
		previous, err := term.MakeRaw(int(file.Fd()))
		if err != nil {
			return err
		}
		defer func() { _ = term.Restore(int(file.Fd()), previous) }()
		incoming := make(chan []byte)
		input = incoming
		go func() {
			defer close(incoming)
			for {
				buffer := make([]byte, 4096)
				n, err := file.Read(buffer)
				if n > 0 {
					select {
					case incoming <- buffer[:n]:
					case <-ctx.Done():
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		sizes = ticker.C
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case data, ok := <-input:
			if !ok {
				return nil
			}
			if strings.ContainsRune(string(data), '\x1d') {
				return nil
			}
			raw, _ := json.Marshal(map[string]interface{}{"type": "terminal/input", "data": string(data)})
			if _, err := session.Dispatch(ctx, raw, other); err != nil {
				return err
			}
		case <-sizes:
			file := cmd.InOrStdin().(*os.File)
			cols, rows, err := term.GetSize(int(file.Fd()))
			if err != nil {
				continue
			}
			raw, _ := json.Marshal(map[string]interface{}{"type": "terminal/resized", "cols": cols, "rows": rows})
			if _, err := session.Dispatch(ctx, raw, other); err != nil {
				return err
			}
		case event, ok := <-session.Events():
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("agent host disconnected; attach again to recover history")
			}
			other(event)
		}
	}
}

func agentSessionAction(cmd *cobra.Command, snapshot json.RawMessage) (json.RawMessage, error) {
	count := 0
	for _, flag := range []string{"steer", "approve", "deny", "action-file"} {
		if cmd.Flags().Changed(flag) {
			count++
		}
	}
	if count > 1 {
		return nil, fmt.Errorf("choose one controller action")
	}
	file, _ := cmd.Flags().GetString("action-file")
	if file != "" {
		var reader io.Reader
		if file == "-" {
			reader = cmd.InOrStdin()
		} else {
			handle, err := os.Open(file)
			if err != nil {
				return nil, err
			}
			defer handle.Close()
			reader = handle
		}
		raw, err := io.ReadAll(io.LimitReader(reader, 256*1024+1))
		if err != nil {
			return nil, err
		}
		var action map[string]json.RawMessage
		if len(raw) > 256*1024 || json.Unmarshal(raw, &action) != nil || len(action["type"]) == 0 {
			return nil, fmt.Errorf("action file must contain at most 256 KiB of a JSON action object with a type")
		}
		return raw, nil
	}
	steer, _ := cmd.Flags().GetString("steer")
	if cmd.Flags().Changed("steer") {
		if strings.TrimSpace(steer) == "" {
			return nil, fmt.Errorf("steering message is empty")
		}
		return json.Marshal(map[string]interface{}{"type": "chat/pendingMessageSet", "kind": "steering", "id": uuid.NewString(), "message": map[string]interface{}{"text": steer, "origin": map[string]string{"kind": "user"}}})
	}
	approve, _ := cmd.Flags().GetString("approve")
	deny, _ := cmd.Flags().GetString("deny")
	if approve != "" || deny != "" {
		var state struct {
			ActiveTurn *struct {
				ID string `json:"id"`
			} `json:"activeTurn"`
		}
		if json.Unmarshal(snapshot, &state) != nil || state.ActiveTurn == nil {
			return nil, fmt.Errorf("the session has no active turn to approve")
		}
		result := map[string]interface{}{"type": "chat/toolCallConfirmed", "turnId": state.ActiveTurn.ID, "toolCallId": approve, "approved": true, "confirmed": "user-action"}
		if deny != "" {
			delete(result, "confirmed")
			result["toolCallId"], result["approved"], result["reason"] = deny, false, "denied"
			if reason, _ := cmd.Flags().GetString("reason"); reason != "" {
				result["reasonMessage"] = reason
			}
		}
		return json.Marshal(result)
	}
	return nil, nil
}

func printAgentHostAction(out io.Writer, action map[string]interface{}) {
	switch action["type"] {
	case "chat/delta":
		fmt.Fprint(out, action["content"])
	case "terminal/data":
		fmt.Fprint(out, action["data"])
	default:
		raw, _ := json.Marshal(action)
		fmt.Fprintln(out, string(raw))
	}
}

func printAgentHostHistory(out io.Writer, raw json.RawMessage) {
	var state struct {
		Turns      []json.RawMessage `json:"turns"`
		ActiveTurn json.RawMessage   `json:"activeTurn"`
		Content    []struct {
			Value string `json:"value"`
		} `json:"content"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return
	}
	for _, part := range state.Content {
		fmt.Fprint(out, part.Value)
	}
	turns := append(state.Turns, state.ActiveTurn)
	for _, rawTurn := range turns {
		var turn struct {
			ResponseParts []map[string]interface{} `json:"responseParts"`
		}
		if json.Unmarshal(rawTurn, &turn) != nil {
			continue
		}
		for _, part := range turn.ResponseParts {
			if part["kind"] == "markdown" {
				fmt.Fprint(out, part["content"])
			} else {
				encoded, _ := json.Marshal(part)
				fmt.Fprintln(out, string(encoded))
			}
		}
	}
}

func watchAgentTaskEvents(cmd *cobra.Command, ctx context.Context, client *api.Client, taskID string) error {
	const query = `query($org: ID!, $task: ID!, $after: Int!) {
      agentTask(id: $task) { id status eventSequence result failureMessage }
      agentTaskEvents(orgId: $org, taskId: $task, after: $after, limit: 100) { sequence turnId messageId kind text request data }
    }`
	cursor := 0
	for {
		var response struct {
			Task *struct {
				Status         string          `json:"status"`
				Sequence       int             `json:"eventSequence"`
				Result         json.RawMessage `json:"result"`
				FailureMessage string          `json:"failureMessage"`
			} `json:"agentTask"`
			Events []struct {
				Sequence  int             `json:"sequence"`
				Kind      string          `json:"kind"`
				Text      string          `json:"text"`
				Data      json.RawMessage `json:"data"`
				Request   json.RawMessage `json:"request"`
				TurnID    string          `json:"turnId"`
				MessageID string          `json:"messageId"`
			} `json:"agentTaskEvents"`
		}
		if err := client.GraphQL(ctx, query, map[string]interface{}{"org": client.Org(), "task": taskID, "after": cursor}, &response); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if response.Task == nil {
			return fmt.Errorf("task unavailable or permission denied")
		}
		for _, event := range response.Events {
			if event.Sequence != cursor+1 {
				return fmt.Errorf("task event history is incomplete; expected sequence %d", cursor+1)
			}
			cursor = event.Sequence
			if boolFlag(cmd, "json") {
				_ = json.NewEncoder(cmd.OutOrStdout()).Encode(event)
			} else if event.Text != "" {
				fmt.Fprint(cmd.OutOrStdout(), event.Text)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\n", event.Kind, event.Data)
			}
		}
		if cursor >= response.Task.Sequence && response.Task.Status != "running" && response.Task.Status != "provisioning" && response.Task.Status != "queued" && response.Task.Status != "pending" {
			result := map[string]interface{}{"type": "task/result", "status": response.Task.Status, "eventSequence": response.Task.Sequence, "result": response.Task.Result, "failureMessage": response.Task.FailureMessage}
			if boolFlag(cmd, "json") {
				_ = json.NewEncoder(cmd.OutOrStdout()).Encode(result)
			} else if response.Task.FailureMessage != "" {
				fmt.Fprintln(cmd.OutOrStdout(), response.Task.FailureMessage)
			} else if len(response.Task.Result) > 0 && string(response.Task.Result) != "null" {
				fmt.Fprintln(cmd.OutOrStdout(), string(response.Task.Result))
			}
			return nil
		}
		if len(response.Events) == 100 {
			continue
		}
		if boolFlag(cmd, "snapshot") {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
}

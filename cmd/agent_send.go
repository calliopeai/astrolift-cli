// Package cmd — `astro agent send`.
//
// Queues a follow-up prompt for a task that is already running. Dispatch is
// otherwise fire-and-observe: `--tail` streams logs out and nothing goes
// back in, so a local IDE stream can steer an agent mid-run and a remote
// one cannot.
//
// This is the *queued* steering channel, not an interactive one. The
// message is persisted against the task and the agent picks it up at its
// next turn boundary — the point one harness invocation finishes. So a
// successful send means "queued", not "the agent has read it"; the
// deliveredAt timestamp in the result is what answers that, and it is null
// until the runner takes the message. Not every runtime can accept a
// follow-up prompt (see the astrolift-agents support matrix); one that
// cannot leaves the message queued rather than dropping it, so a message
// that never gets a deliveredAt is a real signal, not a lost write.
//
// GraphQL operation (field names per backend/schema.graphql):
//   - sendAgentTaskInput(taskId, message) → AstroliftAgentTaskInputMessage
//
// The resolver is org-scoped server-side and gated on
// `agent_task.send_input`, so the only inputs are the task id and the text.
//
// Issues: calliopeai/astrolift-cli#59, calliopeai/astrolift-app#1390
package cmd

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/calliopeai/astrolift-cli/internal/config"
	"github.com/spf13/cobra"
)

// ---- flags -----------------------------------------------------------------

var (
	agentSendJSON  bool
	agentSendStdin bool
)

// ---- GraphQL operation -----------------------------------------------------

const sendAgentTaskInputMutation = `mutation($taskId: ID!, $message: String!) {
  sendAgentTaskInput(taskId: $taskId, message: $message) {
    ok
    errors { code message field }
    data { id message author createdAt deliveredAt }
  }
}`

// agentTaskInputMessage mirrors the AstroliftAgentTaskInputMessage type.
// `deliveredAt` is null while the message is still queued.
type agentTaskInputMessage struct {
	ID          string  `json:"id"`
	Message     string  `json:"message"`
	Author      string  `json:"author"`
	CreatedAt   string  `json:"createdAt"`
	DeliveredAt *string `json:"deliveredAt"`
}

// agentSendResult is the AstroliftAgentTaskInputMessageMutationResult
// envelope. Errors carry the standard code/message/field triple.
type agentSendResult struct {
	Ok     bool `json:"ok"`
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Field   string `json:"field"`
	} `json:"errors"`
	Data *agentTaskInputMessage `json:"data"`
}

// ---- astro agent send ------------------------------------------------------

var agentSendCmd = &cobra.Command{
	Use:   "send <task-id> [input]",
	Short: "Queue a follow-up prompt for a running agent task",
	Long: `Sends steering input to an AgentTask that is already running.

The message is queued against the task and applied at the agent's next
turn boundary, so this returns as soon as the message is durable — it
does not wait for the agent to read it. Use 'astro agent inspect' or the
console to follow what the agent does next.

Pass the input as the second argument, or use --stdin to read it from
standard input (better for multi-line instructions and for text a shell
would otherwise mangle).

The task must be running: a queued or finished task has no turn boundary
left to apply the message at, and the send is refused rather than
silently stranded.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		message, err := agentSendMessage(cmd, args, agentSendStdin)
		if err != nil {
			return err
		}
		client, cfg, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentSend(cmd, cmd.Context(), client, cfg, args[0], message)
	},
}

// agentSendMessage resolves the message text from the positional argument
// or stdin, rejecting the ambiguous and empty cases up front so a caller
// never spends a round trip to learn it sent nothing.
func agentSendMessage(cmd *cobra.Command, args []string, useStdin bool) (string, error) {
	hasArg := len(args) > 1 && strings.TrimSpace(args[1]) != ""
	switch {
	case useStdin && hasArg:
		return "", fmt.Errorf("pass the input as an argument or with --stdin, not both")
	case useStdin:
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("reading input from stdin: %w", err)
		}
		// Trim the edges only: interior newlines are meaningful in a
		// multi-line instruction, which is the reason --stdin exists.
		message := strings.TrimSpace(string(data))
		if message == "" {
			return "", fmt.Errorf("no input read from stdin")
		}
		return message, nil
	case hasArg:
		return strings.TrimSpace(args[1]), nil
	default:
		return "", fmt.Errorf("input is required: pass it as an argument or use --stdin")
	}
}

func runAgentSend(cmd *cobra.Command, ctx context.Context, client *api.Client, cfg *config.Config, taskID, message string) error {
	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var resp struct {
		Result agentSendResult `json:"sendAgentTaskInput"`
	}
	if err := client.GraphQL(sendCtx, sendAgentTaskInputMutation,
		map[string]interface{}{"taskId": taskID, "message": message}, &resp); err != nil {
		return fmt.Errorf("sending input: %w", err)
	}
	if !resp.Result.Ok {
		return fmt.Errorf("send failed: %s", firstMutationError(resp.Result.Errors))
	}

	if agentSendJSON {
		return renderJSON(cmd, resp.Result.Data)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Queued for task %s.\n", taskID)
	fmt.Fprintln(out, "The agent applies it at its next turn boundary; it is not delivered yet.")
	return nil
}

// ---- init ------------------------------------------------------------------

func init() {
	agentSendCmd.Flags().BoolVar(&agentSendJSON, "json", false, "Output the queued message as JSON")
	agentSendCmd.Flags().BoolVar(&agentSendStdin, "stdin", false, "Read the input from standard input")

	agentCmd.AddCommand(agentSendCmd)
}

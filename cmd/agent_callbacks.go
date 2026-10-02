package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

const agentCallbackPolicyQuery = `query { agentTaskCallbackPolicy { allowedHosts } }`
const configureAgentCallbacksMutation = `mutation($allowedHosts: [String!]!) {
 configureAgentTaskCallbacks(allowedHosts: $allowedHosts) { ok errors { message } data { allowedHosts } }
}`
const setAgentCallbackSecretMutation = `mutation($name: String!, $value: String!) {
 setAgentTaskCallbackSecret(name: $name, value: $value) { ok errors { message } data { name } }
}`
const redeliverAgentCallbackMutation = `mutation($taskId: GUID!) {
 redeliverAgentTaskCallback(taskId: $taskId) { ok errors { message } data { id status callbackStatus callbackAttempts callbackLastError } }
}`

type agentCallbackPolicy struct {
	AllowedHosts []string `json:"allowedHosts"`
}
type agentCallbackResult[T any] struct {
	OK     bool `json:"ok"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Data *T `json:"data"`
}

var callbackAllowedHosts []string
var callbackClearHosts bool
var callbackSecretStdin bool
var callbackSecretFile string

func callbackRun(action func(*cobra.Command, context.Context, *api.Client, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		client, _, err := loadScopedAgentClient(cmd)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		return action(cmd, ctx, client, args)
	}
}

var agentCallbacksCmd = &cobra.Command{Use: "callbacks", Short: "Configure signed completion notifications and replay final events"}
var agentCallbacksShowCmd = &cobra.Command{
	Use: "show", Short: "Show the working organization's callback host allow-list", Args: cobra.NoArgs,
	RunE: callbackRun(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
		var response struct {
			Policy *agentCallbackPolicy `json:"agentTaskCallbackPolicy"`
		}
		if err := client.GraphQL(ctx, agentCallbackPolicyQuery, nil, &response); err != nil {
			return fmt.Errorf("reading callback policy: %w", err)
		}
		if response.Policy == nil {
			return fmt.Errorf("callback policy was not returned")
		}
		return renderCallbackPolicy(cmd, response.Policy)
	}),
}
var agentCallbacksConfigureCmd = &cobra.Command{
	Use: "configure", Short: "Replace the organization's allowed callback hosts", Args: cobra.NoArgs,
	RunE: callbackRun(func(cmd *cobra.Command, ctx context.Context, client *api.Client, _ []string) error {
		if callbackClearHosts == (len(callbackAllowedHosts) > 0) {
			return fmt.Errorf("provide --allow-host or --clear, exclusively")
		}
		hosts := callbackAllowedHosts
		if callbackClearHosts {
			hosts = []string{}
		}
		return runAgentCallbackConfigure(cmd, ctx, client, hosts)
	}),
}
var agentCallbacksSecretCmd = &cobra.Command{
	Use: "secret-set <name>", Short: "Set or rotate an organization signing key from stdin or file", Args: cobra.ExactArgs(1),
	Long: `Set or rotate an organization signing key. Use --stdin or --file; a literal
key argument is deliberately unavailable. The key is never printed. During
rotation, configure the receiver to accept the previous and new keys before
replacing this named secret.`,
	RunE: callbackRun(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		value, err := readCallbackSecret(cmd, callbackSecretStdin, callbackSecretFile)
		if err != nil {
			return err
		}
		return runAgentCallbackSecretSet(cmd, ctx, client, args[0], value)
	}),
}
var agentCallbacksRedeliverCmd = &cobra.Command{
	Use: "redeliver <task-guid>", Short: "Replay a task's final notification without rerunning its agent", Args: cobra.ExactArgs(1),
	RunE: callbackRun(func(cmd *cobra.Command, ctx context.Context, client *api.Client, args []string) error {
		return runAgentCallbackRedeliver(cmd, ctx, client, args[0])
	}),
}

func renderCallbackPolicy(cmd *cobra.Command, policy *agentCallbackPolicy) error {
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, policy)
	}
	if len(policy.AllowedHosts) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No callback hosts allowed.")
		return nil
	}
	for _, host := range policy.AllowedHosts {
		fmt.Fprintln(cmd.OutOrStdout(), host)
	}
	return nil
}
func runAgentCallbackConfigure(cmd *cobra.Command, ctx context.Context, client *api.Client, hosts []string) error {
	var response struct {
		Result agentCallbackResult[agentCallbackPolicy] `json:"configureAgentTaskCallbacks"`
	}
	if err := client.GraphQL(ctx, configureAgentCallbacksMutation, map[string]interface{}{"allowedHosts": hosts}, &response); err != nil {
		return fmt.Errorf("configuring callbacks: %w", err)
	}
	if !response.Result.OK {
		return fmt.Errorf("configuring callbacks failed: %s", firstMessageError(response.Result.Errors))
	}
	if response.Result.Data == nil {
		return fmt.Errorf("callback policy was not returned")
	}
	return renderCallbackPolicy(cmd, response.Result.Data)
}
func readCallbackSecret(cmd *cobra.Command, stdin bool, file string) (string, error) {
	if stdin == (file != "") {
		return "", fmt.Errorf("provide exactly one of --stdin or --file")
	}
	reader := cmd.InOrStdin()
	if file != "" {
		source, err := os.Open(file)
		if err != nil {
			return "", fmt.Errorf("cannot open signing-key file")
		}
		defer func() { _ = source.Close() }()
		reader = source
	}
	body, err := io.ReadAll(io.LimitReader(reader, 4099))
	if err != nil {
		return "", fmt.Errorf("cannot read signing key")
	}
	// Match the secret command's newline convention, preserving all other bytes.
	value := strings.TrimRight(string(body), "\r\n")
	if len(body) > 4098 || len(value) > 4096 {
		return "", fmt.Errorf("signing key exceeds 4096 UTF-8 bytes")
	}
	if !utf8.ValidString(value) || len(value) < 32 {
		return "", fmt.Errorf("signing key must contain at least 32 valid UTF-8 bytes")
	}
	return value, nil
}
func runAgentCallbackSecretSet(cmd *cobra.Command, ctx context.Context, client *api.Client, name, value string) error {
	var response struct {
		Result agentCallbackResult[struct {
			Name string `json:"name"`
		}] `json:"setAgentTaskCallbackSecret"`
	}
	if err := client.GraphQL(ctx, setAgentCallbackSecretMutation, map[string]interface{}{"name": name, "value": value}, &response); err != nil {
		// A proxy or server error may reflect input; never echo it on this operation.
		return fmt.Errorf("setting callback signing key failed")
	}
	if !response.Result.OK || response.Result.Data == nil || response.Result.Data.Name != name {
		return fmt.Errorf("setting callback signing key failed")
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, response.Result.Data)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Callback signing key stored.")
	return nil
}
func runAgentCallbackRedeliver(cmd *cobra.Command, ctx context.Context, client *api.Client, taskID string) error {
	var response struct {
		Result agentCallbackResult[struct {
			ID                string  `json:"id"`
			Status            string  `json:"status"`
			CallbackStatus    *string `json:"callbackStatus"`
			CallbackAttempts  *int    `json:"callbackAttempts"`
			CallbackLastError *string `json:"callbackLastError"`
		}] `json:"redeliverAgentTaskCallback"`
	}
	if err := client.GraphQL(ctx, redeliverAgentCallbackMutation, map[string]interface{}{"taskId": taskID}, &response); err != nil {
		return fmt.Errorf("replaying callback: %w", err)
	}
	if !response.Result.OK {
		return fmt.Errorf("replaying callback failed: %s", firstMessageError(response.Result.Errors))
	}
	if response.Result.Data == nil {
		return fmt.Errorf("callback replay returned no task")
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, response.Result.Data)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Callback replay queued for task %s.\n", response.Result.Data.ID)
	return nil
}

// Older servers can still serve task reads without delivery-state fields.
// Only read-only schema mismatches trigger this fallback; mutations never replay.
func queryAgentCallbackState(ctx context.Context, client *api.Client, query string, vars map[string]interface{}, target interface{}) error {
	err := queryStartupDiagnostic(ctx, client, query, vars, target)
	if err == nil || !errors.Is(err, api.ErrSchemaMismatch) {
		return err
	}
	lower := strings.ToLower(err.Error())
	if !strings.Contains(lower, "callbackstatus") && !strings.Contains(lower, "callbackattempts") && !strings.Contains(lower, "callbacklasterror") {
		return err
	}
	legacy := query
	for _, field := range []string{"callbackStatus", "callbackAttempts", "callbackLastError"} {
		legacy = strings.ReplaceAll(legacy, "    "+field+"\n", "")
	}
	return queryStartupDiagnostic(ctx, client, legacy, vars, target)
}

func init() {
	agentCallbacksConfigureCmd.Flags().StringSliceVar(&callbackAllowedHosts, "allow-host", nil, "Exact host or *.domain (repeatable); replaces the current list")
	agentCallbacksConfigureCmd.Flags().BoolVar(&callbackClearHosts, "clear", false, "Clear all allowed hosts")
	agentCallbacksSecretCmd.Flags().BoolVar(&callbackSecretStdin, "stdin", false, "Read signing key from standard input")
	agentCallbacksSecretCmd.Flags().StringVar(&callbackSecretFile, "file", "", "Read signing key from a file")
	agentCallbacksCmd.AddCommand(agentCallbacksShowCmd, agentCallbacksConfigureCmd, agentCallbacksSecretCmd, agentCallbacksRedeliverCmd)
	agentCmd.AddCommand(agentCallbacksCmd)
}

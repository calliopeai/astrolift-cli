// Package cmd — `astro agent secret ...`: write-through VALUE management for
// the secret refs an agent environment spec declares (#1173).
//
// The env-spec row carries secret *references* only; these commands
// set/rotate/delete the referenced VALUES through to the install's secret
// store via the control-plane API, which never persists the plaintext.
// `set` prefers --stdin or a hidden prompt so values stay out of shell
// history, and never echoes the value. `ls` shows per-ref presence
// (metadata only), `rm` removes a stored value.
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const setAgentSecretMutation = `mutation($slug: String!, $envVar: String!, $value: String!) {
  setAgentSecretValue(envSpecSlug: $slug, envVar: $envVar, value: $value) {
    ok errors { message } data { envVar uri exists }
  }
}`

const deleteAgentSecretMutation = `mutation($slug: String!, $envVar: String!) {
  deleteAgentSecretValue(envSpecSlug: $slug, envVar: $envVar) {
    ok errors { message } data { envVar uri exists }
  }
}`

const agentSecretStatusQuery = `query($slug: String!) {
  agentEnvironmentSpecSecretStatus(slug: $slug) { envVar uri exists error }
}`

type agentSecretStatus struct {
	EnvVar string `json:"envVar"`
	URI    string `json:"uri"`
	Exists bool   `json:"exists"`
	Error  string `json:"error"`
}

type agentSecretMutationResult struct {
	Ok     bool `json:"ok"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func agentSecretMutErr(r agentSecretMutationResult, op string) error {
	if r.Ok {
		return nil
	}
	if len(r.Errors) > 0 {
		return fmt.Errorf("%s failed: %s", op, r.Errors[0].Message)
	}
	return fmt.Errorf("%s failed", op)
}

var (
	agentSecretValue string
	agentSecretStdin bool
)

var agentSecretCmd = &cobra.Command{
	Use:   "secret",
	Short: "Manage the VALUES behind an env spec's secret refs (set/rotate, ls, rm)",
	Long: `Provision, rotate, inspect, and delete the secret VALUES an agent
environment spec's refs point at. Values are written straight through to the
install's secret store; the control plane never stores or echoes them.`,
}

var agentSecretSetCmd = &cobra.Command{
	Use:   "set <env-spec-slug> <ENV_VAR>",
	Short: "Set or rotate the value for a spec's secret ref (write-only)",
	Long: `Sets (or rotates) the value bound to <ENV_VAR> on the given env spec.

The value is read from --value, or --stdin, or an interactive hidden prompt
(preferred — --value lands in shell history). It is never echoed or stored
in the control plane.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		value, err := readAgentSecretValue(cmd)
		if err != nil {
			return err
		}
		return runAgentSecretSet(cmd, cmd.Context(), client, args[0], args[1], value)
	},
}

var agentSecretLsCmd = &cobra.Command{
	Use:     "ls <env-spec-slug>",
	Aliases: []string{"list", "status"},
	Short:   "Show per-ref secret status (set / missing) for a spec",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentSecretLs(cmd, cmd.Context(), client, args[0])
	},
}

var agentSecretRmCmd = &cobra.Command{
	Use:   "rm <env-spec-slug> <ENV_VAR>",
	Short: "Delete the stored value for a spec's secret ref",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAgentSecretRm(cmd, cmd.Context(), client, args[0], args[1])
	},
}

// readAgentSecretValue resolves the value to write from --value, --stdin, or
// an interactive hidden prompt. It never writes the value to stdout.
func readAgentSecretValue(cmd *cobra.Command) (string, error) {
	if agentSecretValue != "" {
		return agentSecretValue, nil
	}
	if agentSecretStdin {
		data, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", fmt.Errorf("reading value from stdin: %w", err)
		}
		v := strings.TrimRight(string(data), "\r\n")
		if v == "" {
			return "", fmt.Errorf("empty value read from stdin")
		}
		return v, nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", fmt.Errorf("no value provided: pass --value, --stdin, or run in a terminal")
	}
	fmt.Fprint(cmd.OutOrStdout(), "Value (hidden): ")
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(cmd.OutOrStdout())
	if err != nil {
		return "", fmt.Errorf("reading value: %w", err)
	}
	v := strings.TrimRight(string(b), "\r\n")
	if v == "" {
		return "", fmt.Errorf("empty value")
	}
	return v, nil
}

func runAgentSecretSet(cmd *cobra.Command, ctx context.Context, client *api.Client, slug, envVar, value string) error {
	var resp struct {
		Result agentSecretMutationResult `json:"setAgentSecretValue"`
	}
	if err := client.GraphQL(ctx, setAgentSecretMutation,
		map[string]interface{}{"slug": slug, "envVar": envVar, "value": value}, &resp); err != nil {
		return fmt.Errorf("setting secret: %w", err)
	}
	if err := agentSecretMutErr(resp.Result, "set"); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Set %s on env-spec %s\n", envVar, slug)
	return nil
}

func runAgentSecretRm(cmd *cobra.Command, ctx context.Context, client *api.Client, slug, envVar string) error {
	var resp struct {
		Result agentSecretMutationResult `json:"deleteAgentSecretValue"`
	}
	if err := client.GraphQL(ctx, deleteAgentSecretMutation,
		map[string]interface{}{"slug": slug, "envVar": envVar}, &resp); err != nil {
		return fmt.Errorf("deleting secret: %w", err)
	}
	if err := agentSecretMutErr(resp.Result, "delete"); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s on env-spec %s\n", envVar, slug)
	return nil
}

func runAgentSecretLs(cmd *cobra.Command, ctx context.Context, client *api.Client, slug string) error {
	var resp struct {
		Rows []agentSecretStatus `json:"agentEnvironmentSpecSecretStatus"`
	}
	if err := client.GraphQL(ctx, agentSecretStatusQuery,
		map[string]interface{}{"slug": slug}, &resp); err != nil {
		return fmt.Errorf("fetching secret status: %w", err)
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Rows)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ENV_VAR\tURI\tSTATUS")
	for _, r := range resp.Rows {
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.EnvVar, r.URI, agentSecretStatusLabel(r))
	}
	return w.Flush()
}

// agentSecretStatusLabel renders a ref's presence as a compact status word.
func agentSecretStatusLabel(r agentSecretStatus) string {
	if r.Error != "" {
		return "error: " + r.Error
	}
	if r.Exists {
		return "set"
	}
	return "missing"
}

func init() {
	f := agentSecretSetCmd.Flags()
	f.StringVar(&agentSecretValue, "value", "", "Secret value (avoid — lands in shell history; prefer --stdin/prompt)")
	f.BoolVar(&agentSecretStdin, "stdin", false, "Read the value from stdin")

	agentSecretCmd.AddCommand(agentSecretSetCmd, agentSecretLsCmd, agentSecretRmCmd)
	agentCmd.AddCommand(agentSecretCmd)
}

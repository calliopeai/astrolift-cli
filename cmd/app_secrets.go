// Package cmd -- `astro app secrets ...`: list, create (set), and delete the
// literal secret values an app's manifest [env] block references.
//
// The group existed as an empty placeholder (calliopeai/astrolift-cli#99):
// `astro app secrets create/list` and `astro app events` printed the
// generic sub-resource blurb and every subcommand no-opped, so the
// documented ci_pushed flow ("register, set secrets, deploy") had no
// terminal path -- an operator had to hand-roll the setAppSecret mutation
// with curl (see the issue for the exact call that proved this out).
//
// GraphQL operations (field names per backend/schema.graphql):
//   - list   -> astroliftAppSecrets(appSlug, environmentName)
//   - create -> setAppSecret(input: SetAppSecretInput!) -- an upsert; the
//     platform has no separate update mutation, so create also corrects an
//     existing key
//   - delete -> deleteAppSecret(input: DeleteAppSecretInput!)
//
// Values are never printed. astroliftAppSecrets returns metadata only (no
// value field exists on the type at all); create reads the value from
// --value, --stdin, or a hidden prompt and never echoes it back.
//
// Both write mutations can return a non-null pendingProposalId instead of
// applying immediately -- installs that require secret-change approval
// (#488) queue the edit instead of writing it. The CLI reports that state
// rather than claiming the write already took effect.
//
// ifMatchVersion (optimistic concurrency) and expiresAt exist on
// SetAppSecretInput but aren't wired to a flag here: neither is part of
// what #99 asked for, and the platform's own proven workaround
// (setAppSecret with appSlug/key/value/scope/setVia) didn't use them
// either.
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

// ---- flags -----------------------------------------------------------------

var (
	appSecretsListEnv string

	appSecretsCreateValue  string
	appSecretsCreateStdin  bool
	appSecretsCreateScope  string
	appSecretsCreateSetVia string

	appSecretsDeleteYes bool
)

// ---- GraphQL operations -----------------------------------------------------

const appSecretsListQuery = `query($appSlug: String!, $environmentName: String) {
  astroliftAppSecrets(appSlug: $appSlug, environmentName: $environmentName) {
    id
    key
    environmentName
    source
    bundleSlug
    managedServiceKind
    isMasked
    lastEditedAt
    lastEditedBy { id username displayName }
    expiresAt
    setVia
    scope
  }
}`

const setAppSecretMutation = `mutation($input: SetAppSecretInput!) {
  setAppSecret(input: $input) {
    ok
    errors { code message field }
    data { appSlug key rawManifestStaged pendingProposalId }
  }
}`

const deleteAppSecretMutation = `mutation($input: DeleteAppSecretInput!) {
  deleteAppSecret(input: $input) {
    ok
    errors { code message field }
    data { appSlug key rawManifestStaged pendingProposalId }
  }
}`

// ---- response shapes (GraphQL camelCase) ------------------------------------

// appSecretEditor mirrors AstroliftSecretEditor.
type appSecretEditor struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
}

// appSecret mirrors the AstroliftAppSecret GraphQL type. No value field
// exists on the wire -- the query is metadata-only by construction.
type appSecret struct {
	ID                 string           `json:"id"`
	Key                string           `json:"key"`
	EnvironmentName    string           `json:"environmentName"`
	Source             string           `json:"source"`
	BundleSlug         string           `json:"bundleSlug"`
	ManagedServiceKind string           `json:"managedServiceKind"`
	IsMasked           bool             `json:"isMasked"`
	LastEditedAt       *string          `json:"lastEditedAt"`
	LastEditedBy       *appSecretEditor `json:"lastEditedBy"`
	ExpiresAt          *string          `json:"expiresAt"`
	SetVia             string           `json:"setVia"`
	Scope              string           `json:"scope"`
}

// appSecretWritePayload mirrors Appsecretwritepayload, the data{} shape
// shared by setAppSecret and deleteAppSecret.
type appSecretWritePayload struct {
	AppSlug           string  `json:"appSlug"`
	Key               string  `json:"key"`
	RawManifestStaged string  `json:"rawManifestStaged"`
	PendingProposalID *string `json:"pendingProposalId"`
}

type appSecretWriteResult struct {
	OK     bool                   `json:"ok"`
	Errors []mutationError        `json:"errors"`
	Data   *appSecretWritePayload `json:"data"`
}

// ---- astro app secrets -------------------------------------------------------

var appSecretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "List, create, and delete an app's secret values",
	Long: `Manage the literal env-var secret values an app's manifest [env] block
references.

"create" is an upsert over setAppSecret: it writes a new key or corrects an
existing one identically, since the platform has no separate update
mutation. The value is read from --value, --stdin, or a hidden prompt, and
is never echoed back or shown by "list" -- the list query returns metadata
only (key, environment, source, who last touched it), never the value.

On an install that requires secret-change approval, create/delete queue a
proposal instead of applying immediately; the CLI reports the proposal id
rather than claiming the write already landed.`,
}

var appSecretsListCmd = &cobra.Command{
	Use:   "list [app]",
	Short: "List an app's secret values (metadata only, never the value)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		slug, err := resolveAppSlug(cmd, firstArg(args))
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppSecretsList(cmd, cmd.Context(), client, slug)
	},
}

func runAppSecretsList(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug string) error {
	vars := map[string]interface{}{"appSlug": appSlug}
	if env := strings.TrimSpace(appSecretsListEnv); env != "" {
		vars["environmentName"] = env
	}

	var resp struct {
		Secrets []appSecret `json:"astroliftAppSecrets"`
	}
	if err := client.GraphQL(ctx, appSecretsListQuery, vars, &resp); err != nil {
		return fmt.Errorf("listing secrets for %s: %w", appSlug, err)
	}

	if boolFlag(cmd, "json") {
		return renderJSON(cmd, resp.Secrets)
	}

	out := cmd.OutOrStdout()
	if len(resp.Secrets) == 0 {
		fmt.Fprintf(out, "No secrets found for app %q.\n", appSlug)
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tENVIRONMENT\tSOURCE\tSCOPE\tSET VIA\tLAST EDITED\tEXPIRES")
	for _, s := range resp.Secrets {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			s.Key, dashIfEmpty(s.EnvironmentName), dashIfEmpty(s.Source), dashIfEmpty(s.Scope),
			dashIfEmpty(s.SetVia), shortTime(s.LastEditedAt), shortTime(s.ExpiresAt))
	}
	if err := w.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n%d secret(s) shown.\n", len(resp.Secrets))
	return nil
}

var appSecretsCreateCmd = &cobra.Command{
	Use:   "create <key> [app]",
	Short: "Create or update an app secret value",
	Long: `Writes <key> onto the app's [env] block via setAppSecret. This is an
upsert: an existing key is overwritten, since the platform has no separate
update mutation.

The value is read from --value, --stdin, or a hidden prompt (preferred --
--value lands in shell history). It is never echoed or logged.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := strings.TrimSpace(args[0])
		if key == "" {
			return fmt.Errorf("key must not be empty")
		}
		appArg := ""
		if len(args) > 1 {
			appArg = args[1]
		}
		slug, err := resolveAppSlug(cmd, appArg)
		if err != nil {
			return err
		}
		value, err := readAppSecretValue(cmd)
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppSecretsCreate(cmd, cmd.Context(), client, slug, key, value)
	},
}

func runAppSecretsCreate(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug, key, value string) error {
	input := map[string]interface{}{
		"appSlug": appSlug,
		"key":     key,
		"value":   value,
		"scope":   appSecretsCreateScope,
	}
	if setVia := strings.TrimSpace(appSecretsCreateSetVia); setVia != "" {
		input["setVia"] = setVia
	}

	var resp struct {
		Result appSecretWriteResult `json:"setAppSecret"`
	}
	if err := client.GraphQL(ctx, setAppSecretMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("setting secret %s on %s: %w", key, appSlug, err)
	}
	if !resp.Result.OK {
		return fmt.Errorf("setting secret %s on %s failed: %s", key, appSlug, firstDeployError(resp.Result.Errors))
	}
	return reportAppSecretWrite(cmd, appSlug, key, "Set", resp.Result.Data)
}

var appSecretsDeleteCmd = &cobra.Command{
	Use:   "delete <key> [app]",
	Short: "Delete an app secret value",
	Args:  cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := strings.TrimSpace(args[0])
		if key == "" {
			return fmt.Errorf("key must not be empty")
		}
		appArg := ""
		if len(args) > 1 {
			appArg = args[1]
		}
		slug, err := resolveAppSlug(cmd, appArg)
		if err != nil {
			return err
		}
		client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
		if err != nil {
			return err
		}
		return runAppSecretsDelete(cmd, cmd.Context(), client, slug, key)
	},
}

func runAppSecretsDelete(cmd *cobra.Command, ctx context.Context, client *api.Client, appSlug, key string) error {
	out := cmd.OutOrStdout()
	if !appSecretsDeleteYes {
		noPrompt, _ := cmd.Root().PersistentFlags().GetBool("no-prompt")
		if noPrompt {
			return fmt.Errorf("delete needs confirmation: pass --yes (--no-prompt is set)")
		}
		fmt.Fprintf(out, "Delete secret %s on app %s? [y/N] ", key, appSlug)
		var answer string
		_, _ = fmt.Fscan(cmd.InOrStdin(), &answer)
		if !strings.EqualFold(strings.TrimSpace(answer), "y") {
			fmt.Fprintln(out, "Aborted.")
			return nil
		}
	}

	input := map[string]interface{}{"appSlug": appSlug, "key": key}
	var resp struct {
		Result appSecretWriteResult `json:"deleteAppSecret"`
	}
	if err := client.GraphQL(ctx, deleteAppSecretMutation, map[string]interface{}{"input": input}, &resp); err != nil {
		return fmt.Errorf("deleting secret %s on %s: %w", key, appSlug, err)
	}
	if !resp.Result.OK {
		return fmt.Errorf("deleting secret %s on %s failed: %s", key, appSlug, firstDeployError(resp.Result.Errors))
	}
	return reportAppSecretWrite(cmd, appSlug, key, "Deleted", resp.Result.Data)
}

// reportAppSecretWrite renders the shared write-payload shape for both
// create and delete. A non-empty pendingProposalId means the install
// requires secret-change approval and the edit is queued, not applied.
func reportAppSecretWrite(cmd *cobra.Command, appSlug, key, verb string, data *appSecretWritePayload) error {
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, data)
	}
	out := cmd.OutOrStdout()
	if data != nil && data.PendingProposalID != nil && *data.PendingProposalID != "" {
		fmt.Fprintf(out, "%s %s on %s requires approval; queued as proposal %s.\n", verb, key, appSlug, *data.PendingProposalID)
		fmt.Fprintln(out, "It applies once an approver reviews the change in the console.")
		return nil
	}
	fmt.Fprintf(out, "%s secret %s on app %s.\n", verb, key, appSlug)
	return nil
}

// readAppSecretValue resolves the value to write from --value, --stdin, or
// an interactive hidden prompt. It never writes the value to stdout.
//
// Mirrors readAgentSecretValue (cmd/agent_secret.go) for the analogous
// agent-secret flow; kept independent rather than shared since the two
// commands own unrelated flag sets and that file's own helper is
// unexported-by-convention to its command group, matching this codebase's
// existing pattern of per-file self-contained resource commands.
func readAppSecretValue(cmd *cobra.Command) (string, error) {
	if appSecretsCreateValue != "" {
		return appSecretsCreateValue, nil
	}
	if appSecretsCreateStdin {
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

func init() {
	appSecretsListCmd.Flags().StringVar(&appSecretsListEnv, "environment", "", "Filter to one environment (default: union across all environments)")

	appSecretsCreateCmd.Flags().StringVar(&appSecretsCreateValue, "value", "", "Secret value (avoid -- lands in shell history; prefer --stdin/prompt)")
	appSecretsCreateCmd.Flags().BoolVar(&appSecretsCreateStdin, "stdin", false, "Read the value from stdin")
	appSecretsCreateCmd.Flags().StringVar(&appSecretsCreateScope, "scope", "all", `Visibility scope: "all", an environment name, or "preview:<branch>"`)
	appSecretsCreateCmd.Flags().StringVar(&appSecretsCreateSetVia, "set-via", "cli", "Recorded source of this write, shown on the secret's metadata")

	appSecretsDeleteCmd.Flags().BoolVarP(&appSecretsDeleteYes, "yes", "y", false, "Skip the confirmation prompt")

	appSecretsCmd.AddCommand(appSecretsListCmd, appSecretsCreateCmd, appSecretsDeleteCmd)
}
